package landiscovery

import (
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRegistration struct{ stopped bool }

func (r *fakeRegistration) Shutdown() { r.stopped = true }

func TestStartPublishesBoundedLoomarrService(t *testing.T) {
	registration := &fakeRegistration{}
	var got registrationRequest
	started, err := start(&net.TCPAddr{Port: 8080}, " living-room.local ", func(request registrationRequest) (Registration, error) {
		got = request
		return registration, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.instance != "Loomarr on living-room" || got.service != ServiceType || got.domain != "local." || got.port != 8080 {
		t.Fatalf("registration = %#v", got)
	}
	if len(got.text) != 2 || got.text[0] != "protocol=1" || got.text[1] != "scheme=http" {
		t.Fatalf("TXT = %#v", got.text)
	}
	started.Shutdown()
	if !registration.stopped {
		t.Fatal("shutdown did not stop the DNS-SD registration")
	}
}

func TestStartRejectsAnUnusableListenerBeforeRegistration(t *testing.T) {
	want := errors.New("must not register")
	for _, address := range []net.Addr{nil, &net.IPAddr{IP: net.IPv4(127, 0, 0, 1)}, &net.TCPAddr{Port: 0}} {
		if _, err := start(address, "host", func(registrationRequest) (Registration, error) { return nil, want }); err == nil || errors.Is(err, want) {
			t.Fatalf("start(%v) = %v, want local validation error", address, err)
		}
	}
}

func TestStartPropagatesRegistrationFailure(t *testing.T) {
	want := errors.New("multicast unavailable")
	if _, err := start(&net.TCPAddr{Port: 8080}, "host", func(registrationRequest) (Registration, error) {
		return nil, want
	}); !errors.Is(err, want) {
		t.Fatalf("start error = %v, want %v", err, want)
	}
}

func TestBroadcastResponderReturnsCurrentPublicURL(t *testing.T) {
	var publicURL atomic.Value
	publicURL.Store("http://192.168.1.10:8080")
	registration, address, err := startBroadcastOn("127.0.0.1:0", " living-room.local ", func() string {
		return publicURL.Load().(string)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Shutdown)

	requester, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = requester.Close() })
	buf := make([]byte, 1024)
	request := func() broadcastResponse {
		t.Helper()
		if deadlineErr := requester.SetDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
			t.Fatal(deadlineErr)
		}
		if _, writeErr := requester.WriteTo([]byte(BroadcastRequest), address); writeErr != nil {
			t.Fatal(writeErr)
		}
		n, _, readErr := requester.ReadFrom(buf)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var response broadcastResponse
		if decodeErr := json.Unmarshal(buf[:n], &response); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		return response
	}

	response := request()
	if response.Protocol != 1 || response.ID != "udp:Loomarr on living-room" || response.Name != "Loomarr on living-room" || response.URL != publicURL.Load().(string) {
		t.Fatalf("response = %#v", response)
	}
	publicURL.Store("https://loomarr.example.test")
	if response = request(); response.URL != publicURL.Load().(string) {
		t.Fatalf("hot-applied response URL = %q, want %q", response.URL, publicURL.Load().(string))
	}
}

func TestBroadcastResponderIgnoresInvalidRequestsAndUnusableURLs(t *testing.T) {
	var publicURL atomic.Value
	publicURL.Store("")
	registration, address, err := startBroadcastOn("127.0.0.1:0", "host", func() string { return publicURL.Load().(string) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(registration.Shutdown)
	requester, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = requester.Close() })
	buf := make([]byte, 1024)
	assertSilent := func(payload string) {
		t.Helper()
		if deadlineErr := requester.SetReadDeadline(time.Now().Add(75 * time.Millisecond)); deadlineErr != nil {
			t.Fatal(deadlineErr)
		}
		if _, writeErr := requester.WriteTo([]byte(payload), address); writeErr != nil {
			t.Fatal(writeErr)
		}
		if _, _, readErr := requester.ReadFrom(buf); readErr == nil {
			t.Fatalf("payload %q with URL %q unexpectedly received a response", payload, publicURL.Load().(string))
		}
	}

	assertSilent("wrong")
	assertSilent(BroadcastRequest)
	publicURL.Store("ftp://not-http.example")
	assertSilent(BroadcastRequest)
}
