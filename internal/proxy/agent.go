package proxy

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hashicorp/yamux"
)

// RunAgentTunnel maintains a small binary data channel alongside the JSON control channel.
func RunAgentTunnel(serverURL, token string, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		u, err := proxyURL(serverURL)
		if err != nil {
			log.Printf("[proxy] invalid URL: %v", err)
			return
		}
		h := http.Header{}
		h.Set("X-Node-Token", token)
		ws, _, err := websocket.DefaultDialer.Dial(u, h)
		if err == nil {
			cfg := yamux.DefaultConfig()
			cfg.EnableKeepAlive = true
			cfg.KeepAliveInterval = 30 * time.Second
			cfg.ConnectionWriteTimeout = 10 * time.Second
			cfg.MaxStreamWindowSize = 256 * 1024
			session, e := yamux.Client(NewWSStream(ws), cfg)
			if e == nil {
				log.Printf("[proxy] data tunnel connected")
				for {
					st, e := session.Accept()
					if e != nil {
						break
					}
					go serveTarget(st)
				}
				_ = session.Close()
			} else {
				_ = ws.Close()
			}
		}
		select {
		case <-stop:
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func proxyURL(serverURL string) (string, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "ws":
		u.Scheme = "ws"
	case "wss":
		u.Scheme = "wss"
	default:
		return "", errInvalidURL
	}
	u.Path = "/proxy/agent"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func serveTarget(st net.Conn) {
	defer st.Close()
	_ = st.SetDeadline(time.Now().Add(20 * time.Second))
	var n uint16
	if binary.Read(st, binary.BigEndian, &n) != nil || n == 0 || n > 4096 {
		return
	}
	raw := make([]byte, int(n))
	if _, err := io.ReadFull(st, raw); err != nil {
		return
	}
	var target Target
	if json.Unmarshal(raw, &target) != nil || !validTarget(target.Address) {
		return
	}
	if target.Network == "udp" {
		dst, err := dialPublicTarget(target.Address, "udp")
		if err != nil {
			_, _ = st.Write([]byte{0})
			return
		}
		defer dst.Close()
		_, _ = st.Write([]byte{1})
		var size uint16
		if binary.Read(st, binary.BigEndian, &size) != nil || size == 0 {
			return
		}
		payload := make([]byte, int(size))
		if _, err = io.ReadFull(st, payload); err != nil {
			return
		}
		if _, err = dst.Write(payload); err != nil {
			return
		}
		_ = dst.SetReadDeadline(time.Now().Add(5 * time.Second))
		response := make([]byte, 65507)
		n, err := dst.Read(response)
		if err != nil {
			return
		}
		_ = binary.Write(st, binary.BigEndian, uint16(n))
		_, _ = st.Write(response[:n])
		return
	}
	if target.Network != "" && target.Network != "tcp" {
		return
	}
	dst, err := dialPublicTarget(target.Address, "tcp")
	if err != nil {
		_, _ = st.Write([]byte{0})
		return
	}
	defer dst.Close()
	_, _ = st.Write([]byte{1})
	_ = st.SetDeadline(time.Time{})
	relay(st, dst)
}

func dialPublicTarget(address, network string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, net.ErrClosed
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return nil, ErrInvalidTarget
		}
	}
	return net.DialTimeout(network, net.JoinHostPort(ips[0].String(), port), 10*time.Second)
}

var errInvalidURL = &url.Error{Op: "parse", URL: "server URL", Err: net.InvalidAddrError("expected ws or wss URL")}