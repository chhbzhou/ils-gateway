package proxy

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

func newSOCKS5Dialer(endpoint string) (func(context.Context, string, string) (net.Conn, error), error) {
	host, port, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" {
		return nil, fmt.Errorf("invalid SOCKS5 endpoint: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, fmt.Errorf("invalid SOCKS5 port")
	}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: upstreamDialTimeout, KeepAlive: 30 * time.Second}
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(host, port))
		if err != nil {
			return nil, err
		}
		deadline := time.Now().Add(upstreamDialTimeout)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetDeadline(deadline)
		cancelDone := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = conn.Close()
			case <-cancelDone:
			}
		}()
		if err := socks5Connect(conn, address); err != nil {
			close(cancelDone)
			_ = conn.Close()
			return nil, err
		}
		close(cancelDone)
		_ = conn.SetDeadline(time.Time{})
		return conn, nil
	}, nil
}

func socks5Connect(conn net.Conn, address string) error {
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return err
	}
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil || response[0] != 5 || response[1] != 0 {
		return fmt.Errorf("SOCKS5 authentication rejected")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("invalid target port")
	}
	request := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil {
		if ip4 := ip.To4(); ip4 != nil {
			request = append(request, 1)
			request = append(request, ip4...)
		} else {
			request = append(request, 4)
			request = append(request, ip.To16()...)
		}
	} else {
		if len(host) > 255 {
			return fmt.Errorf("SOCKS5 host too long")
		}
		request = append(request, 3, byte(len(host)))
		request = append(request, []byte(strings.TrimSuffix(host, "."))...)
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], uint16(portNumber))
	request = append(request, p[:]...)
	if _, err := conn.Write(request); err != nil {
		return err
	}
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[1] != 0 {
		return fmt.Errorf("SOCKS5 connect failed: %d", header[1])
	}
	var skip int
	switch header[3] {
	case 1:
		skip = 4
	case 4:
		skip = 16
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(conn, n[:]); err != nil {
			return err
		}
		skip = int(n[0])
	default:
		return fmt.Errorf("unknown SOCKS5 address type")
	}
	_, err = io.CopyN(io.Discard, conn, int64(skip+2))
	return err
}
