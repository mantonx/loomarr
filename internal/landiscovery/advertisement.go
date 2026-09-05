// Package landiscovery advertises a running Loomarr HTTP listener to unpaired local TV clients.
package landiscovery

import (
	"fmt"
	"net"

	"github.com/grandcat/zeroconf"
)

const ServiceType = "_loomarr._tcp"

type Registration interface {
	Shutdown()
}

type registrationRequest struct {
	instance string
	service  string
	domain   string
	port     int
	text     []string
}

type registerFunc func(registrationRequest) (Registration, error)

func Start(address net.Addr, hostname string) (Registration, error) {
	return start(address, hostname, func(request registrationRequest) (Registration, error) {
		return zeroconf.Register(
			request.instance,
			request.service,
			request.domain,
			request.port,
			request.text,
			nil,
		)
	})
}

func start(address net.Addr, hostname string, register registerFunc) (Registration, error) {
	if address == nil {
		return nil, fmt.Errorf("LAN discovery: listener address is unavailable")
	}
	_, portText, err := net.SplitHostPort(address.String())
	if err != nil {
		return nil, fmt.Errorf("LAN discovery: parse listener address: %w", err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil || port < 1 {
		return nil, fmt.Errorf("LAN discovery: listener port is unavailable")
	}
	registration, err := register(registrationRequest{
		instance: instanceName(hostname),
		service:  ServiceType,
		domain:   "local.",
		port:     port,
		text:     []string{"protocol=1", "scheme=http"},
	})
	if err != nil {
		return nil, fmt.Errorf("LAN discovery: advertise: %w", err)
	}
	return registration, nil
}
