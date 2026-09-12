//go:build linux

package proxy

import (
	"fmt"
	"io"
	"net"
	"syscall"
	"time"
	"unsafe"
)

// originalDestination returns the address before a transparent NAT redirect.
// Linux exposes it through SO_ORIGINAL_DST (IPv4) and IP6T_SO_ORIGINAL_DST.
func originalDestination(conn net.Conn) (net.Addr, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return nil, fmt.Errorf("connection does not expose syscall fd")
	}
	var out net.Addr
	var firstErr error
	raw, err := sc.SyscallConn()
	if err != nil {
		return nil, err
	}
	err = raw.Control(func(fd uintptr) {
		if a, err := getOriginalSockaddr(int(fd), syscall.SOL_IP); err == nil {
			out = a
			return
		} else {
			firstErr = err
		}
		if a, err := getOriginalSockaddr(int(fd), syscall.IPPROTO_IPV6); err == nil {
			out = a
		} else if firstErr == nil {
			firstErr = err
		}
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, firstErr
	}
	return out, nil
}

// OriginalDestination is the public form used by the daemon accept loop.
func OriginalDestination(conn net.Conn) (net.Addr, error) { return originalDestination(conn) }

// RelayOriginal connects to the pre-NAT destination and copies bytes in both
// directions. It is the fail-open path for destinations/SNI values outside
// the MITM policy. The accepted connection is closed when either direction
// terminates.
func RelayOriginal(conn net.Conn, destination net.Addr) error {
	if destination == nil {
		return fmt.Errorf("missing original destination")
	}
	upstream, err := net.DialTimeout(destination.Network(), destination.String(), 10*time.Second)
	if err != nil {
		return err
	}
	defer upstream.Close()
	defer conn.Close()
	result := make(chan error, 2)
	go func() { _, e := io.Copy(upstream, conn); result <- e }()
	go func() { _, e := io.Copy(conn, upstream); result <- e }()
	return <-result
}

func getOriginalSockaddr(fd, level int) (net.Addr, error) {
	// SO_ORIGINAL_DST and IP6T_SO_ORIGINAL_DST are both 80 on Linux.
	const originalDst = 80
	buf := make([]byte, 128)
	length := uint32(len(buf))
	_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, uintptr(fd), uintptr(level), originalDst, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&length)), 0)
	if errno != 0 {
		return nil, errno
	}
	if length < 8 {
		return nil, fmt.Errorf("short original destination sockaddr")
	}
	family := *(*uint16)(unsafe.Pointer(&buf[0]))
	switch family {
	case syscall.AF_INET:
		port := int(buf[2])<<8 | int(buf[3])
		ip := net.IPv4(buf[4], buf[5], buf[6], buf[7])
		return &net.TCPAddr{IP: ip, Port: port}, nil
	case syscall.AF_INET6:
		if length < 28 {
			return nil, fmt.Errorf("short ipv6 sockaddr")
		}
		port := int(buf[2])<<8 | int(buf[3])
		ip := make(net.IP, net.IPv6len)
		copy(ip, buf[8:24])
		return &net.TCPAddr{IP: ip, Port: port, Zone: ""}, nil
	default:
		return nil, fmt.Errorf("unsupported sockaddr family %d", family)
	}
}
