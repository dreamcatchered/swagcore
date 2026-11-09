package proxy

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

func TestManagerMultiplexedTCPAndUDP(t *testing.T) {
	m := NewManager(nil)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		session, err := yamux.Server(NewWSStream(ws), nil)
		if err != nil {
			_ = ws.Close()
			return
		}
		m.mu.Lock()
		m.sessions[7] = session
		m.mu.Unlock()
		<-session.CloseChan()
		m.mu.Lock()
		if m.sessions[7] == session {
			delete(m.sessions, 7)
		}
		m.mu.Unlock()
	}))
	defer srv.Close()
	wsURL := "ws" + srv.URL[len("http"):]
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := yamux.Client(NewWSStream(ws), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	go func() {
		for {
			st, err := client.Accept()
			if err != nil {
				return
			}
			go func(st io.ReadWriteCloser) {
				defer st.Close()
				var n uint16
				if binary.Read(st, binary.BigEndian, &n) != nil {
					return
				}
				raw := make([]byte, int(n))
				if _, err := io.ReadFull(st, raw); err != nil {
					return
				}
				var target Target
				if json.Unmarshal(raw, &target) != nil {
					return
				}
				_, _ = st.Write([]byte{1})
				if target.Network == "udp" {
					var size uint16
					if binary.Read(st, binary.BigEndian, &size) != nil {
						return
					}
					payload := make([]byte, int(size))
					if _, err := io.ReadFull(st, payload); err != nil {
						return
					}
					_ = binary.Write(st, binary.BigEndian, uint16(len(payload)))
					_, _ = st.Write(payload)
					return
				}
				_, _ = io.Copy(st, st)
			}(st)
		}
	}()

	deadline := time.Now().Add(time.Second)
	for !m.Online(7) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	tcp, err := m.OpenStream(7, "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("yamux tcp")
	if _, err = tcp.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(tcp, got); err != nil {
		t.Fatal(err)
	}
	_ = tcp.Close()
	if string(got) != string(payload) {
		t.Fatalf("TCP echo=%q", got)
	}

	udpPayload := []byte{0, 1, 2, 3, 4, 5}
	got, err = m.ExchangeUDP(7, "8.8.8.8:53", udpPayload)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(udpPayload) {
		t.Fatalf("UDP echo=%v", got)
	}
}