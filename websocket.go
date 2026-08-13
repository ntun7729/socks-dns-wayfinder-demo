package main

import (
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	websocketPath      = "/s5dns"
	websocketMaxFrame  = int64(maxMuxPayload + 16)
	websocketPingEvery = 20 * time.Second
)

var websocketUpgrader = websocket.Upgrader{
	ReadBufferSize:  copyBufferSize,
	WriteBufferSize: copyBufferSize,
	CheckOrigin: func(r *http.Request) bool {
		// The native client does not send Origin. Browser-originated requests
		// are rejected unless a future explicit origin policy is added.
		return r.Header.Get("Origin") == ""
	},
}

type websocketNetConn struct {
	ws        *websocket.Conn
	readMu    sync.Mutex
	writeMu   sync.Mutex
	readBuf   []byte
	closed    chan struct{}
	closeOnce sync.Once
}

func newWebsocketNetConn(ws *websocket.Conn) *websocketNetConn {
	conn := &websocketNetConn{
		ws:     ws,
		closed: make(chan struct{}),
	}
	ws.SetReadLimit(websocketMaxFrame)
	go conn.heartbeat()
	return conn
}

func (c *websocketNetConn) heartbeat() {
	ticker := time.NewTicker(websocketPingEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = c.ws.WriteControl(websocket.PingMessage, []byte("s5dns"), time.Now().Add(5*time.Second))
		case <-c.closed:
			return
		}
	}
}

func (c *websocketNetConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for {
		if len(c.readBuf) > 0 {
			n := copy(p, c.readBuf)
			c.readBuf = c.readBuf[n:]
			return n, nil
		}
		messageType, reader, err := c.ws.NextReader()
		if err != nil {
			return 0, err
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		payload, err := io.ReadAll(io.LimitReader(reader, websocketMaxFrame+1))
		if err != nil {
			return 0, err
		}
		if int64(len(payload)) > websocketMaxFrame {
			return 0, fmt.Errorf("websocket message exceeds limit")
		}
		if len(payload) == 0 {
			continue
		}
		c.readBuf = payload
	}
}

func (c *websocketNetConn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(ioTimeout))
	writer, err := c.ws.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return 0, err
	}
	n, writeErr := writer.Write(p)
	closeErr := writer.Close()
	if writeErr != nil {
		return n, writeErr
	}
	if closeErr != nil {
		return n, closeErr
	}
	return n, nil
}

func (c *websocketNetConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		err = c.ws.Close()
	})
	return err
}

func (c *websocketNetConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *websocketNetConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *websocketNetConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}

func (c *websocketNetConn) SetReadDeadline(t time.Time) error {
	return c.ws.SetReadDeadline(t)
}

func (c *websocketNetConn) SetWriteDeadline(t time.Time) error {
	return c.ws.SetWriteDeadline(t)
}

func serveWebsocket(listen, token, dnsUpstream string, tcpBuffer int) error {
	handler := http.NewServeMux()
	handler.HandleFunc(websocketPath, func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocketUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn := newWebsocketNetConn(ws)
		go handleWebsocketConn(conn, token, dnsUpstream, tcpBuffer)
	})
	handler.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen WebSocket: %w", err)
	}
	log.Printf("WebSocket origin listening on http://%s%s", listen, websocketPath)
	return server.Serve(ln)
}

func handleWebsocketConn(conn net.Conn, token, dnsUpstream string, tcpBuffer int) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))
	role, err := serverAuthenticate(conn, token)
	if err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	switch role {
	case roleSocks:
		handleRemoteSocks(conn, tcpBuffer)
	case roleMuxSocks:
		handleRemoteMux(conn, tcpBuffer)
	case roleDNS:
		handleRemoteDNS(conn, dnsUpstream)
	}
}

func dialWebsocketTransport(rawURL, token string, tlsConfig *tls.Config, role byte, tcpBuffer int) (net.Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
		return nil, fmt.Errorf("invalid WebSocket URL")
	}
	if u.Path == "" {
		u.Path = websocketPath
	}
	if u.Path != websocketPath {
		return nil, fmt.Errorf("WebSocket URL path must be %s", websocketPath)
	}
	cfg := tlsConfig.Clone()
	var dialer websocket.Dialer
	if u.Scheme == "wss" {
		// Cloudflare terminates the public WSS certificate at the edge. Use
		// system roots for that outer certificate; the s5dns token remains
		// the application-level peer authentication inside the WebSocket.
		cfg.RootCAs = nil
		cfg.ServerName = u.Hostname()
		cfg.NextProtos = []string{"http/1.1"}
		dialer.TLSClientConfig = cfg
	} else {
		dialer.TLSClientConfig = nil
	}
	dialer.NetDial = (&net.Dialer{Timeout: dialTimeout}).Dial
	ws, _, err := dialer.Dial(u.String(), nil)
	if err != nil {
		return nil, err
	}
	tuneTCP(ws.UnderlyingConn(), tcpBuffer)
	conn := newWebsocketNetConn(ws)
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))
	if len(token) == 0 || len(token) > maxTokenLength {
		_ = conn.Close()
		return nil, errProtocol
	}
	var header [3]byte
	header[0] = role
	binary.BigEndian.PutUint16(header[1:], uint16(len(token)))
	if err := writeAll(conn, []byte(authMagic)); err != nil || writeAll(conn, header[:]) != nil || writeAll(conn, []byte(token)) != nil {
		_ = conn.Close()
		return nil, errProtocol
	}
	var status [1]byte
	if _, err := io.ReadFull(conn, status[:]); err != nil || status[0] != 0 {
		_ = conn.Close()
		return nil, errProtocol
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func validateWebsocketURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") {
		return "", fmt.Errorf("WebSocket URL must use ws:// or wss://")
	}
	if u.Path == "" {
		u.Path = websocketPath
	}
	if u.Path != websocketPath {
		return "", fmt.Errorf("WebSocket URL path must be %s", websocketPath)
	}
	return u.String(), nil
}

func websocketSchemeIsSecure(rawURL string) bool {
	u, _ := url.Parse(rawURL)
	return strings.EqualFold(u.Scheme, "wss")
}
