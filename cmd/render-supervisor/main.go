package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

type childResult struct {
	name string
	err  error
}

type child struct {
	name string
	cmd  *exec.Cmd
}

func main() {
	if os.Getenv("S5DNS_PASSWORD") == "" {
		log.Fatal("S5DNS_PASSWORD must be set")
	}

	s5dnsPath := envOr("S5DNS_BINARY", "/usr/local/bin/s5dns")
	certPath := envOr("S5DNS_CERT_PATH", "/etc/secrets/server.crt")
	keyPath := envOr("S5DNS_KEY_PATH", "/etc/secrets/server.key")
	listen := os.Getenv("S5DNS_LISTEN")
	wsListen := envOr("S5DNS_WS_LISTEN", "127.0.0.1:9443")
	dnsUpstream := envOr("S5DNS_DNS_UPSTREAM", "1.1.1.1:53")
	cloudflaredPath := envOr("CLOUDFLARED_BINARY", "/usr/local/bin/cloudflared")
	healthPort := envOr("PORT", "10000")

	serverArgs := []string{
		"server",
		"-listen", listen,
		"-password-env", "S5DNS_PASSWORD",
		"-dns-upstream", dnsUpstream,
		"-ws-listen", wsListen,
	}
	if listen != "" {
		serverArgs = append(serverArgs, "-cert", certPath, "-key", keyPath)
	}
	children := []*child{{
		name: "s5dns",
		cmd:  exec.Command(s5dnsPath, serverArgs...),
	}}

	if token := os.Getenv("CLOUDFLARED_TUNNEL_TOKEN"); token != "" {
		log.Print("CLOUDFLARED_TUNNEL_TOKEN is set; starting Cloudflare Tunnel")
		children = append(children, &child{
			name: "cloudflared",
			cmd: exec.Command(cloudflaredPath,
				"tunnel", "--no-autoupdate", "run", "--token", token,
			),
		})
	} else {
		log.Print("CLOUDFLARED_TUNNEL_TOKEN is not set; running without Cloudflare Tunnel")
	}

	health := &http.Server{
		Addr: ":" + healthPort,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = fmt.Fprintln(w, "ok")
		}),
	}
	go func() {
		log.Printf("Render health endpoint listening on 0.0.0.0:%s", healthPort)
		if err := health.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("health endpoint stopped: %v", err)
		}
	}()

	results := make(chan childResult, len(children))
	for _, c := range children {
		c.cmd.Stdout = os.Stdout
		c.cmd.Stderr = os.Stderr
		c.cmd.Env = os.Environ()
		if err := c.cmd.Start(); err != nil {
			stopChildren(children)
			log.Fatalf("start %s: %v", c.name, err)
		}
		go func(c *child) { results <- childResult{name: c.name, err: c.cmd.Wait()} }(c)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer signal.Stop(signals)

	select {
	case sig := <-signals:
		log.Printf("received %s; stopping children", sig)
		_ = health.Close()
		stopChildren(children)
		for range children {
			<-results
		}
		return
	case result := <-results:
		_ = health.Close()
		if result.err != nil {
			log.Printf("%s exited: %v", result.name, result.err)
		} else {
			log.Printf("%s exited", result.name)
		}
		stopChildren(children)
		for range children {
			<-results
		}
		if result.err != nil {
			os.Exit(1)
		}
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func stopChildren(children []*child) {
	for _, c := range children {
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Signal(syscall.SIGTERM)
		}
	}
}
