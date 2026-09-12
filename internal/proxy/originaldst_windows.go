//go:build windows

package proxy

import (
	"fmt"
	"net"
)

func originalDestination(net.Conn) (net.Addr, error) {
	return nil, fmt.Errorf("transparent original destination is unavailable on windows")
}

func OriginalDestination(conn net.Conn) (net.Addr, error) { return originalDestination(conn) }

func RelayOriginal(net.Conn, net.Addr) error {
	return fmt.Errorf("transparent relay is unavailable on windows")
}
