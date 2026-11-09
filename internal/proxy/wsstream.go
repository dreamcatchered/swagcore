package proxy

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSStream adapts binary WebSocket messages to net.Conn for yamux.
// WebSocket message boundaries are hidden from the stream consumer.
type WSStream struct {
	conn   *websocket.Conn
	wmu    sync.Mutex
	rmu    sync.Mutex
	reader io.Reader
}

func NewWSStream(c *websocket.Conn) *WSStream { return &WSStream{conn: c} }

func (s *WSStream) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.rmu.Lock()
	defer s.rmu.Unlock()
	for {
		if s.reader != nil {
			n, err := s.reader.Read(p)
			if err == io.EOF {
				s.reader = nil
				if n > 0 {
					return n, nil
				}
				continue
			}
			return n, err
		}
		kind, r, err := s.conn.NextReader()
		if err != nil {
			return 0, err
		}
		if kind != websocket.BinaryMessage {
			_, _ = io.Copy(io.Discard, r)
			continue
		}
		s.reader = r
	}
}

func (s *WSStream) Write(p []byte) (int, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if err := s.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *WSStream) Close() error         { return s.conn.Close() }
func (s *WSStream) LocalAddr() net.Addr  { return wsAddr("websocket-local") }
func (s *WSStream) RemoteAddr() net.Addr { return wsAddr(s.conn.RemoteAddr().String()) }
func (s *WSStream) SetDeadline(t time.Time) error {
	if err := s.conn.SetReadDeadline(t); err != nil {
		return err
	}
	return s.conn.SetWriteDeadline(t)
}
func (s *WSStream) SetReadDeadline(t time.Time) error  { return s.conn.SetReadDeadline(t) }
func (s *WSStream) SetWriteDeadline(t time.Time) error { return s.conn.SetWriteDeadline(t) }

type wsAddr string

func (a wsAddr) Network() string { return "websocket" }
func (a wsAddr) String() string  { return string(a) }

var _ net.Conn = (*WSStream)(nil)