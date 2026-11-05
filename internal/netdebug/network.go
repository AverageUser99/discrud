// Package netdebug provides opt-in, payload-free network diagnostics.
package netdebug

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type Monitor struct {
	logger                                             *log.Logger
	http, handshakes, received, sent, rxBytes, txBytes atomic.Uint64
	thumbnailHits, thumbnailMisses                     atomic.Uint64
}

var active atomic.Pointer[Monitor]

// Enable installs process-wide diagnostics. Call the returned function before
// closing out. Counters describe application operations, not TCP/TLS wire bytes.
func Enable(out io.Writer) func() {
	m := &Monitor{logger: log.New(out, "network ", log.Ldate|log.Ltime|log.Lmicroseconds)}
	active.Store(m)
	m.logger.Print("enabled; payloads, headers, identifiers and URL queries are never logged")
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				m.summary()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { active.CompareAndSwap(m, nil); close(done); <-stopped; m.summary() }) }
}
func (m *Monitor) summary() {
	h, ws := m.http.Load(), m.handshakes.Load()
	m.logger.Printf("totals web_requests=%d http_attempts=%d ws_handshakes=%d ws_rx_messages=%d ws_tx_messages=%d ws_rx_payload_bytes=%d ws_tx_payload_bytes=%d thumbnail_hits=%d thumbnail_misses=%d", h+ws, h, ws, m.received.Load(), m.sent.Load(), m.rxBytes.Load(), m.txBytes.Load(), m.thumbnailHits.Load(), m.thumbnailMisses.Load())
}

// category never returns arbitrary URL components (CDN paths and query strings
// can contain signatures, identifiers or other sensitive data).
func category(u *url.URL) string {
	switch strings.ToLower(u.Hostname()) {
	case "cdn.discordapp.com", "media.discordapp.net":
		return "media"
	case "gateway.discord.gg":
		return "gateway"
	case "remote-auth-gateway.discord.gg":
		return "qr-auth"
	case "discord.com":
		p := strings.TrimPrefix(u.Path, "/api/v9")
		switch {
		case p == "/users/@me/remote-auth/login":
			return "qr-exchange"
		case strings.HasPrefix(p, "/channels/") && strings.Contains(p, "/messages"):
			return "messages"
		case p == "/users/@me":
			return "account"
		case p == "/users/@me/guilds":
			return "guild-list"
		case p == "/users/@me/channels":
			return "dm-list"
		case strings.HasPrefix(p, "/guilds/") && strings.HasSuffix(p, "/channels"):
			return "channel-list"
		case strings.HasPrefix(p, "/guilds/") && strings.HasSuffix(p, "/roles"):
			return "roles"
		case strings.HasPrefix(p, "/users/@me/guilds/") && strings.HasSuffix(p, "/member"):
			return "membership"
		}
	}
	if strings.HasSuffix(strings.ToLower(u.Hostname()), ".discord.gg") {
		return "gateway"
	}
	return "other"
}

type transport struct{ base http.RoundTripper }

// Transport counts every RoundTrip attempt, including failed/cancelled ones.
// It does not alter requests or consume response bodies.
func Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return transport{base}
}
func (t transport) RoundTrip(req *http.Request) (*http.Response, error) {
	m := active.Load()
	if m == nil {
		return t.base.RoundTrip(req)
	}
	n := m.http.Add(1)
	start := time.Now()
	method := req.Method
	switch method {
	case "GET", "POST", "PATCH", "DELETE", "PUT", "HEAD", "OPTIONS":
	default:
		method = "other"
	}
	m.logger.Printf("http #%d start method=%s category=%s", n, method, category(req.URL))
	resp, err := t.base.RoundTrip(req)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	m.logger.Printf("http #%d end status=%d failed=%t elapsed_ms=%d transport_failed=%t http_error=%t", n, status, err != nil || status >= 400, time.Since(start).Milliseconds(), err != nil, status >= 400)
	return resp, err
}

// packetKind logs only fixed protocol labels, never arbitrary remote strings.
func packetKind(data []byte) string {
	var p struct {
		Op   json.RawMessage `json:"op"`
		Type string          `json:"t"`
	}
	if json.Unmarshal(data, &p) != nil {
		return "other"
	}
	var op int
	if json.Unmarshal(p.Op, &op) == nil {
		switch op {
		case 0:
			switch p.Type {
			case "MESSAGE_CREATE":
				return "message-create"
			case "MESSAGE_UPDATE":
				return "message-update"
			case "MESSAGE_DELETE", "MESSAGE_DELETE_BULK":
				return "message-delete"
			case "READY":
				return "ready"
			case "RESUMED":
				return "resumed"
			default:
				return "dispatch"
			}
		case 1:
			return "heartbeat"
		case 2:
			return "identify"
		case 6:
			return "resume"
		case 7:
			return "reconnect"
		case 9:
			return "invalid-session"
		case 10:
			return "hello"
		case 11:
			return "heartbeat-ack"
		case 14, 37:
			return "subscription"
		}
	}
	var qr string
	if json.Unmarshal(p.Op, &qr) == nil {
		switch qr {
		case "hello", "init", "heartbeat", "heartbeat_ack", "nonce_proof", "pending_remote_init", "pending_ticket", "pending_login", "cancel":
			return qr
		}
	}
	return "other"
}

// Socket counts JSON application messages, not WebSocket control frames.
// Calls still use Gorilla's framing and normal read/write deadlines.
type Socket struct {
	*websocket.Conn
	monitor  *Monitor
	category string
	ctx      context.Context
}

func Dial(ctx context.Context, d *websocket.Dialer, endpoint string, headers http.Header) (*Socket, *http.Response, error) {
	m := active.Load()
	u, _ := url.Parse(endpoint)
	label := "other"
	if u != nil {
		label = category(u)
	}
	var n uint64
	start := time.Now()
	if m != nil {
		n = m.handshakes.Add(1)
		m.logger.Printf("ws #%d connecting category=%s", n, label)
	}
	conn, resp, err := d.DialContext(ctx, endpoint, headers)
	if m != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		m.logger.Printf("ws #%d handshake status=%d failed=%t elapsed_ms=%d", n, status, err != nil, time.Since(start).Milliseconds())
	}
	if err != nil {
		return nil, resp, err
	}
	return &Socket{Conn: conn, monitor: m, category: label, ctx: ctx}, resp, nil
}
func (s *Socket) ReadJSON(v any) error {
	if s.monitor == nil {
		return s.Conn.ReadJSON(v)
	}
	_, data, err := s.Conn.ReadMessage()
	if err != nil {
		kind, code := failureKind(err)
		if s.ctx != nil && s.ctx.Err() != nil {
			kind, code = failureKind(s.ctx.Err())
		}
		s.monitor.logger.Printf("ws read-ended category=%s cause=%s close_code=%d", s.category, kind, code)
		return err
	}
	n := s.monitor.received.Add(1)
	s.monitor.rxBytes.Add(uint64(len(data)))
	s.monitor.logger.Printf("ws rx #%d category=%s kind=%s payload_bytes=%d", n, s.category, packetKind(data), len(data))
	return json.NewDecoder(bytes.NewReader(data)).Decode(v)
}
func (s *Socket) WriteJSON(v any) error {
	if s.monitor == nil {
		return s.Conn.WriteJSON(v)
	}
	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(v); err != nil {
		return err
	}
	data := buffer.Bytes()
	err := s.Conn.WriteMessage(websocket.TextMessage, data)
	if err == nil {
		n := s.monitor.sent.Add(1)
		s.monitor.txBytes.Add(uint64(len(data)))
		s.monitor.logger.Printf("ws tx #%d category=%s kind=%s payload_bytes=%d", n, s.category, packetKind(data), len(data))
	} else {
		kind, code := failureKind(err)
		s.monitor.logger.Printf("ws write-failed category=%s cause=%s close_code=%d", s.category, kind, code)
	}
	return err
}
