package proxy

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/dreamcatchered/swagcore/internal/store"
	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

var (
	ErrNodeUnavailable = errors.New("proxy node unavailable")
	ErrInvalidTarget   = errors.New("invalid proxy target")
)

type Target struct {
	Address string `json:"address"`
	Network string `json:"network,omitempty"`
}

type Manager struct {
	store    *store.Store
	mu       sync.RWMutex
	sessions map[int64]*yamux.Session
	upgrader websocket.Upgrader
}

func NewManager(st *store.Store) *Manager {
	return &Manager{store: st, sessions: make(map[int64]*yamux.Session), upgrader: websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(*http.Request) bool { return true }}}
}

func (m *Manager) ServeAgentWS(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("X-Node-Token")
	if token == "" {
		http.Error(w, "unauthorized", 401)
		return
	}
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	node, err := m.store.NodeByTokenHash(hash)
	if err != nil || node == nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	ws, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	stream := NewWSStream(ws)
	cfg := yamux.DefaultConfig()
	cfg.EnableKeepAlive = true
	cfg.KeepAliveInterval = 30 * time.Second
	cfg.ConnectionWriteTimeout = 10 * time.Second
	cfg.MaxStreamWindowSize = 256 * 1024
	session, err := yamux.Server(stream, cfg)
	if err != nil {
		_ = stream.Close()
		return
	}
	m.mu.Lock()
	old := m.sessions[node.ID]
	m.sessions[node.ID] = session
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	defer func() {
		m.mu.Lock()
		if m.sessions[node.ID] == session {
			delete(m.sessions, node.ID)
		}
		m.mu.Unlock()
		_ = session.Close()
	}()
	<-session.CloseChan()
}

func (m *Manager) OpenStream(nodeID int64, address string) (net.Conn, error) {
	if !validTarget(address) {
		return nil, ErrInvalidTarget
	}
	m.mu.RLock()
	session := m.sessions[nodeID]
	m.mu.RUnlock()
	if session == nil || session.IsClosed() {
		return nil, ErrNodeUnavailable
	}
	st, err := session.Open()
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(Target{Address: address})
	if len(raw) > 4096 {
		_ = st.Close()
		return nil, ErrInvalidTarget
	}
	_ = st.SetDeadline(time.Now().Add(15 * time.Second))
	if err = binary.Write(st, binary.BigEndian, uint16(len(raw))); err == nil {
		_, err = st.Write(raw)
	}
	var status [1]byte
	if err == nil {
		_, err = io.ReadFull(st, status[:])
	}
	_ = st.SetDeadline(time.Time{})
	if err != nil || status[0] != 1 {
		_ = st.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("node dial failed")
	}
	return st, nil
}

func (m *Manager) ExchangeUDP(nodeID int64, address string, payload []byte) ([]byte, error) {
	if !validTarget(address) || len(payload) == 0 || len(payload) > 65507 {
		return nil, ErrInvalidTarget
	}
	m.mu.RLock()
	session := m.sessions[nodeID]
	m.mu.RUnlock()
	if session == nil || session.IsClosed() {
		return nil, ErrNodeUnavailable
	}
	st, err := session.Open()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	raw, _ := json.Marshal(Target{Address: address, Network: "udp"})
	if len(raw) > 4096 {
		return nil, ErrInvalidTarget
	}
	_ = st.SetDeadline(time.Now().Add(10 * time.Second))
	if err = binary.Write(st, binary.BigEndian, uint16(len(raw))); err != nil {
		return nil, err
	}
	if _, err = st.Write(raw); err != nil {
		return nil, err
	}
	var status [1]byte
	if _, err = io.ReadFull(st, status[:]); err != nil {
		return nil, err
	}
	if status[0] != 1 {
		return nil, fmt.Errorf("node UDP dial failed")
	}
	if err = binary.Write(st, binary.BigEndian, uint16(len(payload))); err != nil {
		return nil, err
	}
	if _, err = st.Write(payload); err != nil {
		return nil, err
	}
	var n uint16
	if err = binary.Read(st, binary.BigEndian, &n); err != nil {
		return nil, err
	}
	out := make([]byte, int(n))
	_, err = io.ReadFull(st, out)
	return out, err
}

func (m *Manager) Online(nodeID int64) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.sessions[nodeID]
	return s != nil && !s.IsClosed()
}

func validTarget(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return false
	}
	p, err := strconv.Atoi(port)
	return err == nil && p >= 1 && p <= 65535
}

func relay(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(b, a)
		if x, ok := b.(interface{ CloseWrite() error }); ok {
			_ = x.CloseWrite()
		}
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(a, b)
		if x, ok := a.(interface{ CloseWrite() error }); ok {
			_ = x.CloseWrite()
		}
		done <- struct{}{}
	}()
	<-done
}