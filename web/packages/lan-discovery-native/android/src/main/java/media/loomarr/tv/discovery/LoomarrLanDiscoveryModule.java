package media.loomarr.tv.discovery;

import android.content.Context;
import android.net.nsd.NsdManager;
import android.net.nsd.NsdServiceInfo;
import com.facebook.react.bridge.Arguments;
import com.facebook.react.bridge.ReactApplicationContext;
import com.facebook.react.bridge.ReactContextBaseJavaModule;
import com.facebook.react.bridge.ReactMethod;
import com.facebook.react.bridge.WritableMap;
import com.facebook.react.modules.core.DeviceEventManagerModule;
import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.net.InterfaceAddress;
import java.net.NetworkInterface;
import java.net.SocketTimeoutException;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Enumeration;
import java.util.HashSet;
import java.util.Map;
import java.util.Set;
import org.json.JSONObject;

public final class LoomarrLanDiscoveryModule extends ReactContextBaseJavaModule {
  private static final String SERVICE_TYPE = "_loomarr._tcp.";
  private static final String BROADCAST_REQUEST = "LOOMARR_DISCOVER/1";
  private static final int BROADCAST_PORT = 51029;
  private final NsdManager manager;
  private NsdManager.DiscoveryListener listener;
  private volatile DatagramSocket broadcastSocket;
  private volatile int generation;

  LoomarrLanDiscoveryModule(ReactApplicationContext context) {
    super(context);
    manager = (NsdManager) context.getSystemService(Context.NSD_SERVICE);
  }

  @Override
  public String getName() {
    return "LoomarrLanDiscovery";
  }

  @ReactMethod
  public void start() {
    stop();
    final int activeGeneration = ++generation;
    startBroadcast(activeGeneration);
    listener = new NsdManager.DiscoveryListener() {
      @Override public void onDiscoveryStarted(String type) {}
      @Override public void onDiscoveryStopped(String type) {}

      @Override
      public void onStartDiscoveryFailed(String type, int code) {
        listener = null;
      }

      @Override
      public void onStopDiscoveryFailed(String type, int code) {
        listener = null;
      }

      @Override
      public void onServiceFound(NsdServiceInfo service) {
        if (activeGeneration != generation) return;
        if (!SERVICE_TYPE.equals(service.getServiceType())) return;
        manager.resolveService(service, new NsdManager.ResolveListener() {
          @Override public void onResolveFailed(NsdServiceInfo ignored, int code) {}
          @Override public void onServiceResolved(NsdServiceInfo resolved) {
            if (activeGeneration == generation) emitFound(resolved);
          }
        });
      }

      @Override
      public void onServiceLost(NsdServiceInfo service) {
        WritableMap payload = Arguments.createMap();
        payload.putString("id", service.getServiceName());
        emit("loomarrDiscoveryLost", payload);
      }
    };
    try {
      manager.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, listener);
    } catch (RuntimeException error) {
      listener = null;
    }
  }

  @ReactMethod
  public void stop() {
    generation += 1;
    DatagramSocket socket = broadcastSocket;
    broadcastSocket = null;
    if (socket != null) socket.close();
    NsdManager.DiscoveryListener active = listener;
    listener = null;
    if (active != null) {
      try {
        manager.stopServiceDiscovery(active);
      } catch (IllegalArgumentException ignored) {
        // Android throws when discovery failed before registration completed; stopped is still true.
      }
    }
  }

  @ReactMethod
  public void addListener(String eventName) {}

  @ReactMethod
  public void removeListeners(double count) {}

  private void startBroadcast(final int activeGeneration) {
    Thread worker = new Thread(() -> browseBroadcast(activeGeneration), "loomarr-lan-discovery");
    worker.setDaemon(true);
    worker.start();
  }

  private void browseBroadcast(int activeGeneration) {
    try (DatagramSocket socket = new DatagramSocket()) {
      socket.setBroadcast(true);
      socket.setSoTimeout(1000);
      if (activeGeneration != generation) return;
      broadcastSocket = socket;
      byte[] request = BROADCAST_REQUEST.getBytes(StandardCharsets.UTF_8);
      long nextSend = 0;
      byte[] response = new byte[1025];
      while (activeGeneration == generation && !socket.isClosed()) {
        long now = System.currentTimeMillis();
        if (now >= nextSend) {
          for (InetAddress target : broadcastTargets()) {
            socket.send(new DatagramPacket(request, request.length, target, BROADCAST_PORT));
          }
          nextSend = now + 3000;
        }
        try {
          DatagramPacket packet = new DatagramPacket(response, response.length);
          socket.receive(packet);
          if (packet.getLength() <= 1024) emitBroadcast(response, packet.getLength());
        } catch (SocketTimeoutException ignored) {
          // The one-second timeout keeps stop and the three-second retry bounded.
        }
      }
    } catch (Exception ignored) {
      // DNS-SD and UDP are independent transports. One transport failing must not stop the other.
    } finally {
      if (activeGeneration == generation) broadcastSocket = null;
    }
  }

  private static Set<InetAddress> broadcastTargets() throws Exception {
    Set<InetAddress> targets = new HashSet<>();
    targets.add(InetAddress.getByName("255.255.255.255"));
    Enumeration<NetworkInterface> interfaces = NetworkInterface.getNetworkInterfaces();
    if (interfaces == null) return targets;
    while (interfaces.hasMoreElements()) {
      NetworkInterface network = interfaces.nextElement();
      try {
        if (!network.isUp() || network.isLoopback()) continue;
      } catch (Exception ignored) {
        continue;
      }
      for (InterfaceAddress address : network.getInterfaceAddresses()) {
        if (address.getBroadcast() != null) targets.add(address.getBroadcast());
      }
    }
    return targets;
  }

  private void emitBroadcast(byte[] bytes, int length) {
    try {
      JSONObject response = new JSONObject(new String(bytes, 0, length, StandardCharsets.UTF_8));
      if (response.optInt("protocol") != 1) return;
      String id = response.optString("id");
      String name = response.optString("name");
      String url = response.optString("url");
      URL parsed = new URL(url);
      if (id.isEmpty() || name.isEmpty() || parsed.getHost().isEmpty()) return;
      if (!"http".equals(parsed.getProtocol()) && !"https".equals(parsed.getProtocol())) return;
      WritableMap payload = Arguments.createMap();
      payload.putString("id", id);
      payload.putString("name", name);
      payload.putString("url", url);
      payload.putString("protocol", "1");
      emit("loomarrDiscoveryFound", payload);
    } catch (Exception ignored) {
      // Untrusted LAN datagrams that do not match the bounded protocol are ignored.
    }
  }

  private void emitFound(NsdServiceInfo service) {
    InetAddress host = service.getHost();
    if (host == null || service.getPort() < 1) return;
    String scheme = attribute(service, "scheme", "http");
    if (!"http".equals(scheme) && !"https".equals(scheme)) return;
    String address = host.getHostAddress();
    if (address == null || address.isEmpty()) return;
    int zone = address.indexOf('%');
    if (zone >= 0) address = address.substring(0, zone);
    if (address.indexOf(':') >= 0) address = "[" + address + "]";

    WritableMap payload = Arguments.createMap();
    payload.putString("id", service.getServiceName());
    payload.putString("name", service.getServiceName());
    payload.putString("url", scheme + "://" + address + ":" + service.getPort());
    payload.putString("protocol", attribute(service, "protocol", ""));
    emit("loomarrDiscoveryFound", payload);
  }

  private static String attribute(NsdServiceInfo service, String name, String fallback) {
    Map<String, byte[]> attributes = service.getAttributes();
    byte[] value = attributes.get(name);
    return value == null ? fallback : new String(value, StandardCharsets.UTF_8);
  }

  private void emit(String event, WritableMap payload) {
    ReactApplicationContext context = getReactApplicationContext();
    if (!context.hasActiveReactInstance()) return;
    context.getJSModule(DeviceEventManagerModule.RCTDeviceEventEmitter.class).emit(event, payload);
  }
}
