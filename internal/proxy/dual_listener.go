package proxy

import (
	"fmt"
	"net"
	"strconv"
)

func listenDual(addr string) ([]net.Listener, error) {
	_, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid listen port")
	}
	ln4, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4zero, Port: port})
	if err != nil {
		return nil, err
	}
	actualPort := ln4.Addr().(*net.TCPAddr).Port
	ln6, err := net.ListenTCP("tcp6", &net.TCPAddr{IP: net.IPv6unspecified, Port: actualPort})
	if err != nil {
		_ = ln4.Close()
		return nil, err
	}
	return []net.Listener{ln4, ln6}, nil
}
