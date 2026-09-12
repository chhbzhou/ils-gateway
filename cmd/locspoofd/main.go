package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/chhbzhou/ils-gateway/internal/config"
	"github.com/chhbzhou/ils-gateway/internal/control"
	"github.com/chhbzhou/ils-gateway/internal/health"
	"github.com/chhbzhou/ils-gateway/internal/proxy"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	cfgPath := flag.String("config", "/etc/config/ios-location-spoofer", "path")
	activityFile := flag.String("activity-file", "/var/lib/locspoofd-activity/activity.json", "persistent activity file")
	upstream := flag.String("upstream-socks5", "", "optional upstream SOCKS5 endpoint")
	caCert := flag.String("ca-cert", "/etc/locspoof/pki/ca-cert.pem", "persistent CA certificate")
	leafDir := flag.String("leaf-dir", "/etc/locspoof/pki", "pre-signed leaf certificate directory")
	flag.Parse()
	cfg, e := config.Load(*cfgPath)
	if e != nil {
		log.Fatalf("invalid configuration: %v", e)
	}
	if cfg.Enabled && !cfg.ProfileSet {
		log.Fatal("service enabled without a complete location profile")
	}
	h := health.NewWithActivityFile(*activityFile)
	h.SetMACResolver(resolveMACFromARP)
	h.SetEnabled(cfg.Enabled)
	stateDir := os.Getenv("LOCSPOOF_STATE_DIR")
	if stateDir == "" {
		stateDir = "/var/lib/locspoofd"
	}
	s := control.NewWithState(cfg.SocketPath, stateDir, h)
	policy := newRuntimePolicy(cfg)
	s.SetReload(func() error {
		next, err := config.Load(*cfgPath + ".reload")
		if err != nil {
			return fmt.Errorf("invalid reload configuration: %w", err)
		}
		if !next.Enabled || !next.ProfileSet {
			return fmt.Errorf("reload requires an enabled service and complete profile")
		}
		if !hotReloadCompatible(cfg, next) {
			return fmt.Errorf("configuration requires a service restart")
		}
		policy.Store(next)
		h.SetEnabled(next.Enabled)
		return nil
	})
	if e = s.Listen(); e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	allowSNI := compiledAllowlist()
	if len(allowSNI) == 0 {
		log.Fatal("no WLoc host allowlist configured")
	}
	rewrite := buildRewrite(cfg)
	listen := net.JoinHostPort(cfg.ListenAddr, fmt.Sprint(cfg.ListenPort))
	dataPlane, e := proxy.NewServerWithLeaves(listen, allowSNI, rewrite, *leafDir)
	if e != nil {
		log.Fatal(e)
	}
	dataPlane.SetLimits(cfg.MaxConnections, cfg.MaxBodyBytes, cfg.MaxBufferedBytes)
	dataPlane.SetObserver(h)
	dataPlane.SetDetailedRewrite(policy.DetailedRewrite(h))
	upstreamEndpoint := *upstream
	if upstreamEndpoint == "" {
		upstreamEndpoint = cfg.UpstreamSocks5
	}
	if upstreamEndpoint != "" {
		if err := dataPlane.SetUpstreamSocks5(upstreamEndpoint); err != nil {
			log.Fatalf("invalid upstream SOCKS5: %v", err)
		}
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- dataPlane.ListenAndServe() }()
	if err := <-dataPlane.Ready(); err != nil {
		log.Fatal(err)
	}
	readyPath := "/var/run/locspoofd/ready"
	enrollment, enrollLn, e := startEnrollment("0.0.0.0:"+fmt.Sprint(cfg.ProxyPort), *caCert)
	if e != nil {
		_ = os.Remove(readyPath)
		log.Fatal(e)
	}
	if err := os.WriteFile(readyPath, []byte("ready\n"), 0640); err != nil {
		_ = enrollLn.Close()
		log.Fatal(err)
	}
	h.SetHealthy(true)
	defer os.Remove(readyPath)
	log.Printf("data plane listening on %s, enrollment on %d, upstream=%s", listen, cfg.ProxyPort, upstreamEndpoint)
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	failed := false
	select {
	case err := <-serveErr:
		if err != nil && err != http.ErrServerClosed {
			log.Printf("data plane stopped: %v", err)
			failed = true
		}
	case <-ch:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = dataPlane.Shutdown(ctx)
	_ = enrollment.Shutdown(ctx)
	_ = enrollLn.Close()
	if failed {
		_ = s.Close()
		_ = os.Remove(readyPath)
		os.Exit(1)
	}
}

func startEnrollment(address, caCert string) (*http.Server, net.Listener, error) {
	server := &http.Server{Addr: address, Handler: proxy.EnrollmentHandler(caCert), ReadHeaderTimeout: 5 * time.Second}
	listener, err := net.Listen("tcp4", address)
	if err != nil {
		return nil, nil, err
	}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("enrollment listener stopped: %v", err)
		}
	}()
	return server, listener, nil
}
