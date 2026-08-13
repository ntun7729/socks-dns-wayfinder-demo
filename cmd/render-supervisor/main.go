package main

import (
	"log"
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
	listen := envOr("S5DNS_LISTEN", "127.0.0.1:8443")
	wsListen := envOr("S5DNS_WS_LISTEN", "127.0.0.1:9443")
	dnsUpstream := envOr("S5DNS_DNS_UPSTREAM", "1.1.1.1:53")
	cloudflaredPath := envOr("CLOUDFLARED_BINARY", "/usr/local/bin/cloudflared")

	children := []*child{{
		name: "s5dns",
		cmd: exec.Command(s5dnsPath,
			"server",
			"-listen", listen,
			"-cert", certPath,
			"-key", keyPath,
			"-password-env", "S5DNS_PASSWORD",
			"-dns-upstream", dnsUpstream,
			"-ws-listen", wsListen,
		),
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
		stopChildren(children)
		for range children {
			<-results
		}
		return
	case result := <-results:
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
