package main

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	authMagic        = "S5D1"
	roleSocks        = 1
	roleDNS          = 2
	roleMuxSocks     = 3
	kindConnect      = 1
	kindDNS          = 2
	kindStatus       = 3
	maxFrame         = 1 << 20
	maxDNSMessage    = 4096
	ioTimeout        = 10 * time.Second
	dialTimeout      = 5 * time.Second
	maxTokenLength   = 4096
	maxMuxPayload    = 256 << 10
	maxMuxStreams    = 1024
	muxOpenTimeout   = 5 * time.Second
	defaultTCPBuffer = 1 << 20
	copyBufferSize   = 256 << 10
)

var errProtocol = errors.New("protocol error")

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "server":
		if err := runServer(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "client":
		if err := runClient(os.Args[2:]); err != nil {
			log.Fatal(err)
		}
	case "version":
		fmt.Println("s5dns 0.3.0")
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: s5dns server|client [flags]")
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	listen := fs.String("listen", "0.0.0.0:8443", "TLS listen address")
	certFile := fs.String("cert", "/etc/s5dns/server.crt", "server certificate PEM")
	keyFile := fs.String("key", "/etc/s5dns/server.key", "server private key PEM")
	tokenFile := fs.String("token-file", "/etc/s5dns/server.token", "shared token file")
	dnsUpstream := fs.String("dns-upstream", "1.1.1.1:53", "upstream DNS UDP address")
	tcpBuffer := fs.Int("tcp-buffer", defaultTCPBuffer, "TCP read/write buffer size in bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}

	token, err := readToken(*tokenFile)
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		return fmt.Errorf("load certificate and key: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
	ln, err := tls.Listen("tcp", *listen, cfg)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()
	log.Printf("server listening on %s; DNS upstream %s", *listen, *dnsUpstream)

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			log.Printf("accept: %v", err)
			continue
		}
		go handleServerConn(conn, token, *dnsUpstream, *tcpBuffer)
	}
}

func handleServerConn(raw net.Conn, token, dnsUpstream string, tcpBuffer int) {
	defer raw.Close()
	conn, ok := raw.(*tls.Conn)
	if !ok {
		return
	}
	tuneTCP(conn.NetConn(), tcpBuffer)
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))
	if err := conn.Handshake(); err != nil {
		log.Printf("TLS handshake from %s: %v", conn.RemoteAddr(), err)
		return
	}
	role, err := serverAuthenticate(conn, token)
	if err != nil {
		log.Printf("authentication from %s: %v", conn.RemoteAddr(), err)
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
	default:
		log.Printf("unsupported role %d from %s", role, conn.RemoteAddr())
	}
}

func serverAuthenticate(conn net.Conn, token string) (byte, error) {
	magic := make([]byte, len(authMagic))
	if _, err := io.ReadFull(conn, magic); err != nil {
		return 0, err
	}
	if string(magic) != authMagic {
		return 0, errProtocol
	}
	var header [3]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return 0, err
	}
	role := header[0]
	tokenLen := int(binary.BigEndian.Uint16(header[1:]))
	if tokenLen == 0 || tokenLen > maxTokenLength {
		return 0, errProtocol
	}
	presented := make([]byte, tokenLen)
	if _, err := io.ReadFull(conn, presented); err != nil {
		return 0, err
	}
	if subtle.ConstantTimeCompare([]byte(token), presented) != 1 {
		return 0, errProtocol
	}
	if role != roleSocks && role != roleDNS && role != roleMuxSocks {
		return 0, errProtocol
	}
	if err := writeAll(conn, []byte{0}); err != nil {
		return 0, err
	}
	return role, nil
}

func handleRemoteSocks(conn net.Conn, tcpBuffer int) {
	kind, payload, err := readFrame(conn, 65535)
	if err != nil || kind != kindConnect {
		return
	}
	target, err := decodeTarget(payload)
	if err != nil {
		_ = writeFrame(conn, kindStatus, []byte{1})
		return
	}
	remote, err := dialTCP(target, tcpBuffer)
	if err != nil {
		_ = writeFrame(conn, kindStatus, []byte{mapDialError(err)})
		return
	}
	defer remote.Close()
	if err := writeFrame(conn, kindStatus, []byte{0}); err != nil {
		return
	}
	proxyBidirectional(conn, remote)
}

func handleRemoteDNS(conn net.Conn, upstream string) {
	kind, query, err := readFrame(conn, maxDNSMessage)
	if err != nil || kind != kindDNS || len(query) == 0 {
		return
	}
	addr, err := net.ResolveUDPAddr("udp", upstream)
	if err != nil {
		_ = writeFrame(conn, kindStatus, []byte{1})
		return
	}
	udp, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		_ = writeFrame(conn, kindStatus, []byte{1})
		return
	}
	defer udp.Close()
	_ = udp.SetDeadline(time.Now().Add(dialTimeout))
	if _, err := udp.Write(query); err != nil {
		_ = writeFrame(conn, kindStatus, []byte{1})
		return
	}
	response := make([]byte, maxDNSMessage)
	n, err := udp.Read(response)
	if err != nil {
		_ = writeFrame(conn, kindStatus, []byte{1})
		return
	}
	payload := append([]byte{0}, response[:n]...)
	_ = writeFrame(conn, kindStatus, payload)
}

func runClient(args []string) error {
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	serverAddr := fs.String("server", "127.0.0.1:8443", "remote TLS server address")
	serverName := fs.String("server-name", "localhost", "TLS server name")
	caFile := fs.String("ca", "/etc/s5dns/ca.crt", "trusted CA certificate PEM")
	tokenFile := fs.String("token-file", "/etc/s5dns/client.token", "shared token file")
	socksListen := fs.String("socks-listen", "127.0.0.1:1080", "local SOCKS5 TCP address")
	dnsListen := fs.String("dns-listen", "127.0.0.1:5353", "local DNS UDP address")
	mux := fs.Bool("mux", false, "reuse one authenticated TLS session for multiple SOCKS streams")
	tcpBuffer := fs.Int("tcp-buffer", defaultTCPBuffer, "TCP read/write buffer size in bytes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		return fmt.Errorf("read token: %w", err)
	}
	ca, err := os.ReadFile(*caFile)
	if err != nil {
		return fmt.Errorf("read CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return fmt.Errorf("parse CA: no certificates found")
	}
	tlsConfig := &tls.Config{
		RootCAs:            pool,
		ServerName:         *serverName,
		MinVersion:         tls.VersionTLS13,
		ClientSessionCache: tls.NewLRUClientSessionCache(128),
	}
	socks, err := net.Listen("tcp", *socksListen)
	if err != nil {
		return fmt.Errorf("listen SOCKS5: %w", err)
	}
	defer socks.Close()
	dnsAddr, err := net.ResolveUDPAddr("udp", *dnsListen)
	if err != nil {
		return fmt.Errorf("resolve DNS listen: %w", err)
	}
	dns, err := net.ListenUDP("udp", dnsAddr)
	if err != nil {
		return fmt.Errorf("listen DNS: %w", err)
	}
	defer dns.Close()
	log.Printf("client SOCKS5 listening on %s; DNS listening on %s", *socksListen, *dnsListen)

	if *mux {
		go acceptMuxSocks(socks, *serverAddr, token, tlsConfig, *tcpBuffer)
	} else {
		go acceptSocks(socks, *serverAddr, token, tlsConfig, *tcpBuffer)
	}
	go serveDNS(dns, *serverAddr, token, tlsConfig, *tcpBuffer)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	return nil
}

func acceptSocks(ln net.Listener, serverAddr, token string, tlsConfig *tls.Config, tcpBuffer int) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("accept SOCKS5: %v", err)
			continue
		}
		go handleSocksClient(conn, serverAddr, token, tlsConfig, tcpBuffer)
	}
}

func handleSocksClient(local net.Conn, serverAddr, token string, tlsConfig *tls.Config, tcpBuffer int) {
	defer local.Close()
	tuneTCP(local, tcpBuffer)
	_ = local.SetDeadline(time.Now().Add(ioTimeout))
	if err := socksHandshake(local); err != nil {
		return
	}
	target, payload, err := readSocksRequest(local)
	if err != nil {
		_ = sendSocksReply(local, 1)
		return
	}
	transport, err := dialTransport(serverAddr, token, tlsConfig, roleSocks, tcpBuffer)
	if err != nil {
		_ = sendSocksReply(local, 1)
		return
	}
	defer transport.Close()
	if err := writeFrame(transport, kindConnect, payload); err != nil {
		_ = sendSocksReply(local, 1)
		return
	}
	kind, status, err := readFrame(transport, 1)
	if err != nil || kind != kindStatus || len(status) != 1 || status[0] != 0 {
		if err == nil && len(status) == 1 {
			_ = sendSocksReply(local, status[0])
		} else {
			_ = sendSocksReply(local, 1)
		}
		return
	}
	if err := sendSocksReply(local, 0); err != nil {
		return
	}
	_ = local.SetDeadline(time.Time{})
	proxyBidirectional(local, transport)
	_ = target
}

func socksHandshake(conn net.Conn) error {
	var header [2]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil || header[0] != 5 {
		return errProtocol
	}
	if header[1] == 0 {
		return errProtocol
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}
	for _, method := range methods {
		if method == 0 {
			return writeAll(conn, []byte{5, 0})
		}
	}
	_ = writeAll(conn, []byte{5, 0xff})
	return errProtocol
}

func readSocksRequest(conn net.Conn) (string, []byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return "", nil, err
	}
	if header[0] != 5 || header[2] != 0 {
		return "", nil, errProtocol
	}
	if header[1] != 1 {
		_ = sendSocksReply(conn, 7)
		return "", nil, errProtocol
	}
	var host string
	switch header[3] {
	case 1:
		ip := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", nil, err
		}
		host = net.IP(ip).String()
	case 3:
		var length [1]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil || length[0] == 0 {
			return "", nil, errProtocol
		}
		name := make([]byte, int(length[0]))
		if _, err := io.ReadFull(conn, name); err != nil {
			return "", nil, err
		}
		host = string(name)
	case 4:
		ip := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", nil, err
		}
		host = net.IP(ip).String()
	default:
		return "", nil, errProtocol
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(conn, portBytes[:]); err != nil {
		return "", nil, err
	}
	port := binary.BigEndian.Uint16(portBytes[:])
	return net.JoinHostPort(host, strconv.Itoa(int(port))), append([]byte{header[3]}, appendAddressPayload(host, header[3], port)...), nil
}

func appendAddressPayload(host string, atyp byte, port uint16) []byte {
	var out []byte
	switch atyp {
	case 1:
		out = append(out, net.ParseIP(host).To4()...)
	case 3:
		out = append(out, byte(len(host)))
		out = append(out, []byte(host)...)
	case 4:
		out = append(out, net.ParseIP(host).To16()...)
	}
	var p [2]byte
	binary.BigEndian.PutUint16(p[:], port)
	return append(out, p[:]...)
}

func decodeTarget(payload []byte) (string, error) {
	if len(payload) < 1 {
		return "", errProtocol
	}
	atyp := payload[0]
	rest := payload[1:]
	var host string
	switch atyp {
	case 1:
		if len(rest) != 6 {
			return "", errProtocol
		}
		host = net.IP(rest[:4]).String()
		rest = rest[4:]
	case 3:
		if len(rest) < 3 {
			return "", errProtocol
		}
		length := int(rest[0])
		if length == 0 || len(rest) != 1+length+2 {
			return "", errProtocol
		}
		host = string(rest[1 : 1+length])
		rest = rest[1+length:]
	case 4:
		if len(rest) != 18 {
			return "", errProtocol
		}
		host = net.IP(rest[:16]).String()
		rest = rest[16:]
	default:
		return "", errProtocol
	}
	port := binary.BigEndian.Uint16(rest[:2])
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func sendSocksReply(conn net.Conn, rep byte) error {
	return writeAll(conn, []byte{5, rep, 0, 1, 0, 0, 0, 0, 0, 0})
}

func serveDNS(conn *net.UDPConn, serverAddr, token string, tlsConfig *tls.Config, tcpBuffer int) {
	buffer := make([]byte, 65535)
	for {
		n, peer, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("read DNS: %v", err)
			continue
		}
		if n == 0 || n > maxDNSMessage {
			continue
		}
		query := append([]byte(nil), buffer[:n]...)
		go func() {
			response, err := forwardDNS(serverAddr, token, tlsConfig, query, tcpBuffer)
			if err != nil {
				log.Printf("DNS request from %s: %v", peer, err)
				return
			}
			if _, err := conn.WriteToUDP(response, peer); err != nil {
				log.Printf("DNS response to %s: %v", peer, err)
			}
		}()
	}
}

func forwardDNS(serverAddr, token string, tlsConfig *tls.Config, query []byte, tcpBuffer int) ([]byte, error) {
	transport, err := dialTransport(serverAddr, token, tlsConfig, roleDNS, tcpBuffer)
	if err != nil {
		return nil, err
	}
	defer transport.Close()
	_ = transport.SetDeadline(time.Now().Add(dialTimeout))
	if err := writeFrame(transport, kindDNS, query); err != nil {
		return nil, err
	}
	kind, payload, err := readFrame(transport, maxDNSMessage+1)
	if err != nil || kind != kindStatus || len(payload) < 1 || payload[0] != 0 {
		return nil, errProtocol
	}
	return payload[1:], nil
}

func dialTransport(serverAddr, token string, tlsConfig *tls.Config, role byte, tcpBuffer int) (net.Conn, error) {
	base, err := dialTCP(serverAddr, tcpBuffer)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(base, tlsConfig.Clone())
	_ = conn.SetDeadline(time.Now().Add(ioTimeout))
	if err := conn.Handshake(); err != nil {
		base.Close()
		return nil, err
	}
	if len(token) == 0 || len(token) > maxTokenLength {
		conn.Close()
		return nil, errProtocol
	}
	var header [3]byte
	header[0] = role
	binary.BigEndian.PutUint16(header[1:], uint16(len(token)))
	if err := writeAll(conn, []byte(authMagic)); err != nil || writeAll(conn, header[:]) != nil || writeAll(conn, []byte(token)) != nil {
		conn.Close()
		return nil, errProtocol
	}
	var status [1]byte
	if _, err := io.ReadFull(conn, status[:]); err != nil || status[0] != 0 {
		conn.Close()
		return nil, errProtocol
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

var copyPool = sync.Pool{
	New: func() any { return make([]byte, copyBufferSize) },
}

func proxyBidirectional(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	go copyStream(a, b, done)
	go copyStream(b, a, done)
	<-done
	_ = a.Close()
	_ = b.Close()
	<-done
}

func copyStream(dst io.Writer, src io.Reader, done chan<- struct{}) {
	buf := copyPool.Get().([]byte)
	_, _ = io.CopyBuffer(dst, src, buf)
	copyPool.Put(buf)
	done <- struct{}{}
}

func dialTCP(address string, tcpBuffer int) (net.Conn, error) {
	dialer := net.Dialer{Timeout: dialTimeout}
	conn, err := dialer.Dial("tcp", address)
	if err == nil {
		tuneTCP(conn, tcpBuffer)
	}
	return conn, err
}

func tuneTCP(conn net.Conn, tcpBuffer int) {
	if tcpBuffer <= 0 {
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetReadBuffer(tcpBuffer)
		_ = tcp.SetWriteBuffer(tcpBuffer)
	}
}

func writeFrame(w io.Writer, kind byte, payload []byte) error {
	if len(payload)+1 > maxFrame {
		return errProtocol
	}
	var header [5]byte
	binary.BigEndian.PutUint32(header[:4], uint32(len(payload)+1))
	header[4] = kind
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func readFrame(r io.Reader, maxPayload int) (byte, []byte, error) {
	var lengthBytes [4]byte
	if _, err := io.ReadFull(r, lengthBytes[:]); err != nil {
		return 0, nil, err
	}
	length := int(binary.BigEndian.Uint32(lengthBytes[:]))
	if length < 1 || length-1 > maxPayload || length > maxFrame {
		return 0, nil, errProtocol
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(r, frame); err != nil {
		return 0, nil, err
	}
	return frame[0], frame[1:], nil
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readToken(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("token file is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" || len(token) > maxTokenLength {
		return "", fmt.Errorf("token must be 1..%d bytes", maxTokenLength)
	}
	return token, nil
}

func mapDialError(err error) byte {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return 5
	}
	if errors.Is(err, syscall.ENETUNREACH) {
		return 3
	}
	if errors.Is(err, syscall.EHOSTUNREACH) {
		return 4
	}
	return 1
}
