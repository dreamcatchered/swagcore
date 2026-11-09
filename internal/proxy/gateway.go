package proxy

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Gateway struct {
	Manager  *Manager
	Username string
	Password string
	Listen   string
	next     atomic.Uint64
}

func (g *Gateway) ListenAndServe() error {
	if g.Manager == nil || g.Username == "" || g.Password == "" {
		return fmt.Errorf("proxy gateway requires manager and non-empty credentials")
	}
	ln, err := net.Listen("tcp", g.Listen)
	if err != nil {
		return err
	}
	log.Printf("[proxy] authenticated HTTP/SOCKS5 gateway listening on %s", ln.Addr())
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go g.serve(c)
	}
}

func (g *Gateway) serve(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReaderSize(c, 32*1024)
	b, err := br.Peek(1)
	if err != nil {
		return
	}
	if b[0] == 5 {
		g.serveSOCKS(c, br)
		return
	}
	g.serveHTTP(c, br)
}

func (g *Gateway) pickNode() int64 {
	g.Manager.mu.RLock()
	defer g.Manager.mu.RUnlock()
	var ids []int64
	for id, s := range g.Manager.sessions {
		if s != nil && !s.IsClosed() {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0
	}
	return ids[(g.next.Add(1)-1)%uint64(len(ids))]
}
func (g *Gateway) open(address string) (net.Conn, error) {
	id := g.pickNode()
	if id == 0 {
		return nil, ErrNodeUnavailable
	}
	return g.Manager.OpenStream(id, address)
}
func (g *Gateway) authorized(v string) bool {
	const prefix = "Basic "
	if !strings.HasPrefix(v, prefix) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(v, prefix)))
	if err != nil {
		return false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	return len(parts) == 2 && subtleEqual(parts[0], g.Username) && subtleEqual(parts[1], g.Password)
}

func (g *Gateway) serveHTTP(c net.Conn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if !g.authorized(req.Header.Get("Proxy-Authorization")) {
		_, _ = io.WriteString(c, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"swagCore\"\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
		return
	}
	var address string
	if strings.EqualFold(req.Method, http.MethodConnect) {
		address = req.Host
	} else {
		if req.URL == nil || req.URL.Scheme == "" || req.URL.Host == "" {
			_, _ = io.WriteString(c, "HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n")
			return
		}
		address = req.URL.Host
		if _, _, e := net.SplitHostPort(address); e != nil {
			port := "80"
			if strings.EqualFold(req.URL.Scheme, "https") {
				port = "443"
			}
			address = net.JoinHostPort(address, port)
		}
	}
	up, err := g.open(address)
	if err != nil {
		_, _ = io.WriteString(c, "HTTP/1.1 502 Bad Gateway\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
		return
	}
	defer up.Close()
	_ = c.SetDeadline(time.Time{})
	_ = up.SetDeadline(time.Time{})
	if strings.EqualFold(req.Method, http.MethodConnect) {
		_, _ = io.WriteString(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		relay(c, up)
		return
	}
	req.Header.Del("Proxy-Authorization")
	req.Close = true
	req.Header.Set("Connection", "close")
	req.RequestURI = req.URL.RequestURI()
	req.URL.Scheme = ""
	req.URL.Host = ""
	if err := req.Write(up); err != nil {
		return
	}
	if req.Body != nil {
		_ = req.Body.Close()
	}
	_, _ = io.Copy(c, up)
}

func (g *Gateway) serveSOCKS(c net.Conn, br *bufio.Reader) {
	var head [2]byte
	if _, err := io.ReadFull(br, head[:]); err != nil || head[0] != 5 || head[1] == 0 {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	found := false
	for _, m := range methods {
		if m == 2 {
			found = true
		}
	}
	if !found {
		_, _ = c.Write([]byte{5, 255})
		return
	}
	_, _ = c.Write([]byte{5, 2})
	var auth [2]byte
	if _, err := io.ReadFull(br, auth[:]); err != nil || auth[0] != 1 {
		return
	}
	u := make([]byte, int(auth[1]))
	if _, err := io.ReadFull(br, u); err != nil {
		return
	}
	var plen [1]byte
	if _, err := io.ReadFull(br, plen[:]); err != nil {
		return
	}
	p := make([]byte, int(plen[0]))
	if _, err := io.ReadFull(br, p); err != nil {
		return
	}
	if !subtleEqual(string(u), g.Username) || !subtleEqual(string(p), g.Password) {
		_, _ = c.Write([]byte{1, 1})
		return
	}
	_, _ = c.Write([]byte{1, 0})
	var h [4]byte
	if _, err := io.ReadFull(br, h[:]); err != nil || h[0] != 5 {
		return
	}
	if h[1] != 1 && h[1] != 3 {
		_, _ = c.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	host, err := readSOCKSHost(br, h[3])
	if err != nil {
		return
	}
	var pb [2]byte
	if _, err := io.ReadFull(br, pb[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(pb[:])
	if port == 0 && h[1] == 1 {
		return
	}
	if h[1] == 3 {
		g.serveSOCKSUDP(c, br)
		return
	}
	address := net.JoinHostPort(host, strconv.Itoa(int(port)))
	up, err := g.open(address)
	if err != nil {
		_, _ = c.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer up.Close()
	_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	_ = c.SetDeadline(time.Time{})
	_ = up.SetDeadline(time.Time{})
	relay(c, up)
}

func readSOCKSHost(r io.Reader, atyp byte) (string, error) {
	switch atyp {
	case 1:
		b := make([]byte, 4)
		_, err := io.ReadFull(r, b)
		return net.IP(b).String(), err
	case 4:
		b := make([]byte, 16)
		_, err := io.ReadFull(r, b)
		return net.IP(b).String(), err
	case 3:
		var n [1]byte
		if _, err := io.ReadFull(r, n[:]); err != nil {
			return "", err
		}
		b := make([]byte, int(n[0]))
		_, err := io.ReadFull(r, b)
		return string(b), err
	default:
		return "", fmt.Errorf("unsupported SOCKS address type")
	}
}

func (g *Gateway) serveSOCKSUDP(control net.Conn, br *bufio.Reader) {
	pc, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		_, _ = control.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
		return
	}
	defer pc.Close()
	port := pc.LocalAddr().(*net.UDPAddr).Port
	reply := []byte{5, 0, 0, 1, 0, 0, 0, 0, byte(port >> 8), byte(port)}
	if _, err = control.Write(reply); err != nil {
		return
	}
	peerHost, _, _ := net.SplitHostPort(control.RemoteAddr().String())
	peerIP := net.ParseIP(peerHost)
	var client *net.UDPAddr
	done := make(chan struct{})
	go func() { var b [1]byte; _, _ = br.Read(b[:]); close(done) }()
	buf := make([]byte, 65535)
	for {
		_ = pc.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		select {
		case <-done:
			return
		default:
		}
		udpFrom, ok := from.(*net.UDPAddr)
		if !ok || !udpFrom.IP.Equal(peerIP) {
			continue
		}
		if client == nil {
			client = udpFrom
		} else if !client.IP.Equal(udpFrom.IP) || client.Port != udpFrom.Port {
			continue
		}
		if n < 4 || buf[0] != 0 || buf[1] != 0 || buf[2] != 0 {
			continue
		}
		rd := buf[4:n]
		host, used, err := parseUDPAddress(rd)
		if err != nil {
			continue
		}
		if len(rd) < used+2 {
			continue
		}
		dstPort := binary.BigEndian.Uint16(rd[used : used+2])
		if dstPort == 0 {
			continue
		}
		payload := rd[used+2:]
		if len(payload) == 0 {
			continue
		}
		nodeID := g.pickNode()
		if nodeID == 0 {
			continue
		}
		response, err := g.Manager.ExchangeUDP(nodeID, net.JoinHostPort(host, strconv.Itoa(int(dstPort))), payload)
		if err != nil {
			continue
		}
		out := make([]byte, 0, 10+len(response))
		out = append(out, 0, 0, 0)
		out = appendUDPAddress(out, host)
		out = append(out, byte(dstPort>>8), byte(dstPort))
		out = append(out, response...)
		_, _ = pc.WriteTo(out, client)
	}
}

func parseUDPAddress(b []byte) (string, int, error) {
	if len(b) < 1 {
		return "", 0, io.ErrUnexpectedEOF
	}
	switch b[0] {
	case 1:
		if len(b) < 5 {
			return "", 0, io.ErrUnexpectedEOF
		}
		return net.IP(b[1:5]).String(), 5, nil
	case 4:
		if len(b) < 17 {
			return "", 0, io.ErrUnexpectedEOF
		}
		return net.IP(b[1:17]).String(), 17, nil
	case 3:
		if len(b) < 2+int(b[1]) {
			return "", 0, io.ErrUnexpectedEOF
		}
		return string(b[2 : 2+int(b[1])]), 2 + int(b[1]), nil
	default:
		return "", 0, fmt.Errorf("bad UDP address type")
	}
}
func appendUDPAddress(dst []byte, host string) []byte {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return append(append(dst, 1), v4...)
		}
		return append(append(dst, 4), ip.To16()...)
	}
	if len(host) > 255 {
		host = host[:255]
	}
	dst = append(dst, 3, byte(len(host)))
	return append(dst, host...)
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

var _ = atomic.Uint64{}