package main

import (
	"bufio"
	"bytes"
	"errors"
	"github.com/chhbzhou/ils-gateway/internal/config"
	"github.com/chhbzhou/ils-gateway/internal/provider/applewloc"
	"github.com/chhbzhou/ils-gateway/internal/proxy"
	"io"
	"net"
	"os"
	"strings"
	"sync/atomic"
)

var compiledWLocHosts = []string{
	"gsp-ssl.ls.apple.com",
	"gspe1-ssl.ls.apple.com",
	"gs-loc.apple.com",
	"gs-loc-cn.apple.com",
	"bluedot.is.autonavi.com",
	"bluedot.is.autonavi.com.gds.alibabadns.com",
}

type runtimePolicy struct {
	config atomic.Pointer[config.Config]
}

func newRuntimePolicy(cfg config.Config) *runtimePolicy {
	policy := &runtimePolicy{}
	policy.Store(cfg)
	return policy
}

func (p *runtimePolicy) Store(cfg config.Config) {
	copy := cfg
	p.config.Store(&copy)
}

func (p *runtimePolicy) DetailedRewrite(h interface{ SetLastError(string) }) func([]byte) ([]byte, proxy.RewriteMetadata, error) {
	return func(body []byte) ([]byte, proxy.RewriteMetadata, error) {
		cfg := p.config.Load()
		if cfg == nil || !cfg.Enabled || !cfg.ProfileSet {
			return body, proxy.RewriteMetadata{Unsupported: true}, applewloc.ErrUnsupported
		}
		out, result, err := applewloc.RewriteBody(body, cfg.Profile)
		if err != nil {
			h.SetLastError(err.Error())
			return nil, proxy.RewriteMetadata{Unsupported: errors.Is(err, applewloc.ErrUnsupported)}, err
		}
		return out, proxy.RewriteMetadata{Envelope: result.Envelope, Locations: result.Locations}, nil
	}
}

func compiledAllowlist() map[string]bool {
	out := make(map[string]bool, len(compiledWLocHosts))
	for _, host := range compiledWLocHosts {
		out[proxy.NormalizeSNI(host)] = true
	}
	return out
}

func hotReloadCompatible(current, next config.Config) bool {
	return current.ListenAddr == next.ListenAddr &&
		current.ListenPort == next.ListenPort &&
		current.ProxyPort == next.ProxyPort &&
		current.SocketPath == next.SocketPath &&
		current.UpstreamSocks5 == next.UpstreamSocks5 &&
		current.MaxConnections == next.MaxConnections &&
		current.MaxBodyBytes == next.MaxBodyBytes &&
		current.MaxBufferedBytes == next.MaxBufferedBytes
}

func resolveMACFromARP(clientIP string) string {
	file, err := os.Open("/proc/net/arp")
	if err != nil {
		return ""
	}
	defer file.Close()
	return resolveMACFromARPReader(file, clientIP)
}

func resolveMACFromARPReader(reader io.Reader, clientIP string) string {
	if ip := net.ParseIP(clientIP); ip == nil || ip.To4() == nil {
		return ""
	}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || fields[0] != clientIP {
			continue
		}
		mac, err := net.ParseMAC(fields[3])
		if err == nil && len(mac) == 6 {
			return mac.String()
		}
	}
	return ""
}

func buildAllowlist(cfg config.Config) map[string]bool {
	out := make(map[string]bool, len(cfg.WLocHosts))
	for _, host := range cfg.WLocHosts {
		out[proxy.NormalizeSNI(host)] = true
	}
	return out
}

func buildRewrite(cfg config.Config) func([]byte) ([]byte, bool) {
	if !cfg.Enabled || !cfg.ProfileSet {
		return nil
	}
	profile := cfg.Profile
	return func(body []byte) ([]byte, bool) {
		out, _, err := applewloc.RewriteBody(body, profile)
		return out, err == nil
	}
}

func buildDetailedRewrite(cfg config.Config, h interface {
	SetLastError(string)
}) func([]byte) ([]byte, proxy.RewriteMetadata, error) {
	if !cfg.Enabled || !cfg.ProfileSet {
		return nil
	}
	profile := cfg.Profile
	return func(body []byte) ([]byte, proxy.RewriteMetadata, error) {
		out, result, err := applewloc.RewriteBody(body, profile)
		if err != nil {
			h.SetLastError(err.Error())
			return nil, proxy.RewriteMetadata{Unsupported: errors.Is(err, applewloc.ErrUnsupported)}, err
		}
		if bytes.Equal(body, out) {
			return out, proxy.RewriteMetadata{Envelope: result.Envelope, Locations: result.Locations}, nil
		}
		return out, proxy.RewriteMetadata{Envelope: result.Envelope, Locations: result.Locations}, nil
	}
}
