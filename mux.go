package main

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	muxFrameOpen byte = iota + 1
	muxFrameOpenOK
	muxFrameOpenErr
	muxFrameData
	muxFrameFIN
	muxFrameReset
	muxFramePing
	muxFramePong
	muxFrameGoAway
	muxDataChunk = maxMuxPayload
	muxRecvSlots = 32
)

type muxFrame struct {
	typ      byte
	streamID uint32
	payload  []byte
}

type muxSession struct {
	conn      net.Conn
	server    bool
	tcpBuffer int
	writeMu   sync.Mutex
	streamsMu sync.Mutex
	streams   map[uint32]*muxStream
	closed    chan struct{}
	closeOnce sync.Once
	onOpen    func(*muxStream, []byte)
	onClose   func(*muxSession)
}

type muxStream struct {
	session     *muxSession
	id          uint32
	recvCh      chan []byte
	openCh      chan error
	opened      bool
	done        chan struct{}
	stateMu     sync.Mutex
	pending     []byte
	recvClosed  bool
	recvErr     error
	writeClosed bool
	closeOnce   sync.Once
}

func acceptMuxSocks(ln net.Listener, serverAddr, websocketURL, token string, tlsConfig *tls.Config, tcpBuffer int) {
	manager := &muxManager{
		serverAddr:   serverAddr,
		websocketURL: websocketURL,
		token:        token,
		tlsConfig:    tlsConfig,
		tcpBuffer:    tcpBuffer,
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("accept multiplexed SOCKS5: %v", err)
			continue
		}
		go handleMuxSocksClient(conn, manager)
	}
}

func handleMuxSocksClient(local net.Conn, manager *muxManager) {
	defer local.Close()
	tuneTCP(local, manager.tcpBuffer)
	_ = local.SetDeadline(time.Now().Add(ioTimeout))
	if err := socksHandshake(local); err != nil {
		return
	}
	_, payload, err := readSocksRequest(local)
	if err != nil {
		_ = sendSocksReply(local, 1)
		return
	}
	stream, err := manager.openStream(payload)
	if err != nil {
		_ = sendSocksReply(local, 1)
		return
	}
	defer stream.Close()
	if err := sendSocksReply(local, 0); err != nil {
		return
	}
	_ = local.SetDeadline(time.Time{})
	proxyBidirectional(local, stream)
}

func handleRemoteMux(conn net.Conn, tcpBuffer int) {
	session := newMuxSession(conn, true, tcpBuffer)
	session.onOpen = func(stream *muxStream, payload []byte) {
		target, err := decodeTarget(payload)
		if err != nil {
			_ = session.writeFrame(muxFrameOpenErr, stream.id, []byte{1})
			stream.finishRead(errProtocol)
			return
		}
		remote, err := dialTCP(target, tcpBuffer)
		if err != nil {
			_ = session.writeFrame(muxFrameOpenErr, stream.id, []byte{mapDialError(err)})
			stream.finishRead(err)
			return
		}
		if err := session.writeFrame(muxFrameOpenOK, stream.id, []byte{0}); err != nil {
			_ = remote.Close()
			return
		}
		go proxyBidirectional(remote, stream)
	}
	session.readLoop()
}

func newMuxSession(conn net.Conn, server bool, tcpBuffer int) *muxSession {
	tuneTCP(conn, tcpBuffer)
	return &muxSession{
		conn:      conn,
		server:    server,
		tcpBuffer: tcpBuffer,
		streams:   make(map[uint32]*muxStream),
		closed:    make(chan struct{}),
	}
}

func (s *muxSession) readLoop() {
	for {
		frame, err := readMuxFrame(s.conn)
		if err != nil {
			s.closeWithError(err)
			return
		}
		switch frame.typ {
		case muxFrameOpen:
			if !s.server || frame.streamID == 0 || frame.streamID%2 == 0 {
				s.closeWithError(errProtocol)
				return
			}
			if s.streamCount() >= maxMuxStreams {
				_ = s.writeFrame(muxFrameOpenErr, frame.streamID, []byte{1})
				continue
			}
			stream := newMuxStream(s, frame.streamID, false)
			if !s.addStream(stream) {
				s.closeWithError(errProtocol)
				return
			}
			if s.onOpen == nil {
				_ = s.writeFrame(muxFrameOpenErr, frame.streamID, []byte{1})
				stream.finishRead(errProtocol)
				continue
			}
			go s.onOpen(stream, frame.payload)
		case muxFrameOpenOK, muxFrameOpenErr:
			stream := s.getStream(frame.streamID)
			if stream == nil || stream.openCh == nil || len(frame.payload) != 1 {
				s.closeWithError(errProtocol)
				return
			}
			var err error
			if frame.typ == muxFrameOpenErr || frame.payload[0] != 0 {
				err = fmt.Errorf("remote connect rejected: %d", frame.payload[0])
			}
			select {
			case stream.openCh <- err:
			default:
			}
		case muxFrameData:
			stream := s.getStream(frame.streamID)
			if stream == nil || stream.openCh != nil && !stream.isOpen() {
				s.closeWithError(errProtocol)
				return
			}
			if err := stream.deliver(frame.payload); err != nil {
				_ = s.writeFrame(muxFrameReset, frame.streamID, []byte{1})
			}
		case muxFrameFIN:
			stream := s.getStream(frame.streamID)
			if stream == nil {
				continue
			}
			stream.finishRead(nil)
		case muxFrameReset:
			stream := s.getStream(frame.streamID)
			if stream != nil {
				stream.finishRead(io.ErrClosedPipe)
				s.removeStream(frame.streamID)
			}
		case muxFramePing:
			if err := s.writeFrame(muxFramePong, 0, frame.payload); err != nil {
				s.closeWithError(err)
				return
			}
		case muxFramePong:
		case muxFrameGoAway:
			s.closeWithError(io.EOF)
			return
		default:
			s.closeWithError(errProtocol)
			return
		}
	}
}

func (s *muxSession) openStream(payload []byte) (*muxStream, error) {
	if len(payload) == 0 || len(payload) > maxMuxPayload {
		return nil, errProtocol
	}
	stream := newMuxStream(s, s.nextStreamID(), true)
	if s.streamCount() >= maxMuxStreams || !s.addStream(stream) {
		return nil, fmt.Errorf("multiplexed stream limit reached")
	}
	if err := s.writeFrame(muxFrameOpen, stream.id, payload); err != nil {
		s.removeStream(stream.id)
		return nil, err
	}
	select {
	case err := <-stream.openCh:
		stream.markOpen()
		if err != nil {
			stream.Close()
			return nil, err
		}
		return stream, nil
	case <-time.After(muxOpenTimeout):
		stream.Close()
		return nil, fmt.Errorf("multiplexed stream open timeout")
	case <-s.closed:
		stream.Close()
		return nil, io.ErrClosedPipe
	}
}

func (s *muxSession) nextStreamID() uint32 {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	var next uint32 = 1
	for id := range s.streams {
		if id >= next {
			next = id + 2
		}
	}
	return next
}

func (s *muxSession) addStream(stream *muxStream) bool {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	select {
	case <-s.closed:
		return false
	default:
	}
	if len(s.streams) >= maxMuxStreams {
		return false
	}
	if _, exists := s.streams[stream.id]; exists {
		return false
	}
	s.streams[stream.id] = stream
	return true
}

func (s *muxSession) getStream(id uint32) *muxStream {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return s.streams[id]
}

func (s *muxSession) removeStream(id uint32) {
	s.streamsMu.Lock()
	delete(s.streams, id)
	s.streamsMu.Unlock()
}

func (s *muxSession) streamCount() int {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	return len(s.streams)
}

func (s *muxSession) writeFrame(typ byte, streamID uint32, payload []byte) error {
	if len(payload) > maxMuxPayload {
		return errProtocol
	}
	frame := make([]byte, 9+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	frame[4] = typ
	binary.BigEndian.PutUint32(frame[5:9], streamID)
	copy(frame[9:], payload)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return writeAll(s.conn, frame)
}

func (s *muxSession) closeWithError(err error) {
	s.closeOnce.Do(func() {
		close(s.closed)
		_ = s.conn.Close()
		s.streamsMu.Lock()
		streams := make([]*muxStream, 0, len(s.streams))
		for _, stream := range s.streams {
			streams = append(streams, stream)
		}
		s.streams = make(map[uint32]*muxStream)
		s.streamsMu.Unlock()
		for _, stream := range streams {
			stream.finishRead(err)
		}
		if s.onClose != nil {
			s.onClose(s)
		}
	})
}

func (s *muxSession) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func readMuxFrame(r io.Reader) (muxFrame, error) {
	var header [9]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return muxFrame{}, err
	}
	length := int(binary.BigEndian.Uint32(header[0:4]))
	if length < 0 || length > maxMuxPayload {
		return muxFrame{}, errProtocol
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return muxFrame{}, err
	}
	return muxFrame{
		typ:      header[4],
		streamID: binary.BigEndian.Uint32(header[5:9]),
		payload:  payload,
	}, nil
}

func newMuxStream(session *muxSession, id uint32, awaitingOpen bool) *muxStream {
	stream := &muxStream{
		session: session,
		id:      id,
		recvCh:  make(chan []byte, muxRecvSlots),
		done:    make(chan struct{}),
	}
	if awaitingOpen {
		stream.openCh = make(chan error, 1)
	} else {
		stream.opened = true
	}
	return stream
}

func (s *muxStream) markOpen() {
	s.stateMu.Lock()
	s.opened = true
	s.stateMu.Unlock()
}

func (s *muxStream) isOpen() bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.opened
}

func (s *muxStream) deliver(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.recvClosed {
		return io.ErrClosedPipe
	}
	select {
	case s.recvCh <- data:
		return nil
	case <-s.done:
		return io.ErrClosedPipe
	}
}

func (s *muxStream) Read(p []byte) (int, error) {
	for {
		s.stateMu.Lock()
		if len(s.pending) > 0 {
			n := copy(p, s.pending)
			s.pending = s.pending[n:]
			s.stateMu.Unlock()
			return n, nil
		}
		closed := s.recvClosed
		err := s.recvErr
		s.stateMu.Unlock()
		if closed {
			if err != nil {
				return 0, err
			}
			return 0, io.EOF
		}
		data, ok := <-s.recvCh
		if !ok {
			continue
		}
		s.stateMu.Lock()
		s.pending = data
		s.stateMu.Unlock()
	}
}

func (s *muxStream) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.stateMu.Lock()
	closed := s.writeClosed
	s.stateMu.Unlock()
	if closed || s.session.isClosed() {
		return 0, io.ErrClosedPipe
	}
	written := 0
	for written < len(p) {
		end := written + muxDataChunk
		if end > len(p) {
			end = len(p)
		}
		if err := s.session.writeFrame(muxFrameData, s.id, p[written:end]); err != nil {
			return written, err
		}
		written = end
	}
	return written, nil
}

func (s *muxStream) CloseWrite() error {
	s.stateMu.Lock()
	if s.writeClosed {
		s.stateMu.Unlock()
		return nil
	}
	s.writeClosed = true
	s.stateMu.Unlock()
	return s.session.writeFrame(muxFrameFIN, s.id, nil)
}

func (s *muxStream) Close() error {
	var err error
	s.closeOnce.Do(func() {
		err = s.session.writeFrame(muxFrameReset, s.id, []byte{0})
		s.finishRead(io.ErrClosedPipe)
		s.session.removeStream(s.id)
	})
	return err
}

func (s *muxStream) finishRead(err error) {
	s.stateMu.Lock()
	if !s.recvClosed {
		s.recvClosed = true
		s.recvErr = err
		close(s.recvCh)
		close(s.done)
	}
	if s.openCh != nil {
		select {
		case s.openCh <- err:
		default:
		}
	}
	s.stateMu.Unlock()
}

type muxManager struct {
	mu           sync.Mutex
	current      *muxSession
	serverAddr   string
	websocketURL string
	token        string
	tlsConfig    *tls.Config
	tcpBuffer    int
}

func (m *muxManager) openStream(payload []byte) (*muxStream, error) {
	for attempt := 0; attempt < 2; attempt++ {
		session, err := m.getSession()
		if err != nil {
			return nil, err
		}
		stream, err := session.openStream(payload)
		if err == nil {
			return stream, nil
		}
		m.invalidate(session)
	}
	return nil, io.ErrClosedPipe
}

func (m *muxManager) getSession() (*muxSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil && !m.current.isClosed() {
		return m.current, nil
	}
	conn, err := dialClientTransport(m.websocketURL, m.serverAddr, m.token, m.tlsConfig, roleMuxSocks, m.tcpBuffer)
	if err != nil {
		return nil, err
	}
	session := newMuxSession(conn, false, m.tcpBuffer)
	session.onClose = func(closed *muxSession) {
		m.invalidate(closed)
	}
	m.current = session
	go session.readLoop()
	return session, nil
}

func (m *muxManager) invalidate(session *muxSession) {
	m.mu.Lock()
	if m.current == session {
		m.current = nil
	}
	m.mu.Unlock()
}
