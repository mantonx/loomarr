package landiscovery

import (
	"encoding/json"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

const (
	BroadcastPort    = 51029
	BroadcastRequest = "LOOMARR_DISCOVER/1"
)

type broadcastResponse struct {
	Protocol int    `json:"protocol"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
}

type broadcastRegistration struct {
	conn net.PacketConn
	once sync.Once
	wg   sync.WaitGroup
}

func StartBroadcast(hostname string, publicURL func() string) (Registration, error) {
	registration, _, err := startBroadcastOn(net.JoinHostPort("", strconv.Itoa(BroadcastPort)), hostname, publicURL)
	return registration, err
}

func startBroadcastOn(address, hostname string, publicURL func() string) (Registration, net.Addr, error) {
	conn, err := net.ListenPacket("udp4", address)
	if err != nil {
		return nil, nil, err
	}
	name := instanceName(hostname)
	registration := &broadcastRegistration{conn: conn}
	registration.wg.Add(1)
	go registration.serve(name, "udp:"+name, publicURL)
	return registration, conn.LocalAddr(), nil
}

func (r *broadcastRegistration) serve(name, id string, publicURL func() string) {
	defer r.wg.Done()
	buf := make([]byte, 1025)
	for {
		n, sender, err := r.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if n != len(BroadcastRequest) || string(buf[:n]) != BroadcastRequest || publicURL == nil {
			continue
		}
		public := strings.TrimRight(strings.TrimSpace(publicURL()), "/")
		parsed, parseErr := url.Parse(public)
		if parseErr != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}
		payload, marshalErr := json.Marshal(broadcastResponse{Protocol: 1, ID: id, Name: name, URL: public})
		if marshalErr != nil {
			continue
		}
		_, _ = r.conn.WriteTo(payload, sender)
	}
}

func (r *broadcastRegistration) Shutdown() {
	if r == nil {
		return
	}
	r.once.Do(func() { _ = r.conn.Close() })
	r.wg.Wait()
}

func instanceName(hostname string) string {
	name := strings.TrimSuffix(strings.TrimSpace(hostname), ".local")
	if name == "" {
		name = "server"
	}
	return "Loomarr on " + name
}
