package discord

import (
	"context"

	"discrud/internal/netdebug"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type gatewaySession struct {
	ID    string
	URL   string
	Guild string // Last explicit subscription, retained across resumable reconnects.
	Seq   atomic.Int64
}
type gatewayPacket struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int64          `json:"s"`
	T  string          `json:"t"`
}

// Guild message delivery is gated by the protocol's "typing" subscription.
// The "channels" field requests member-list ranges, NOT message subscriptions.
// Sending a nonempty channels map while typing=false contradicts that gate.
// Keep roster/member/thread lists empty; filter messages to the selection locally.
// Reference: discord.py-self discord/state.py::GuildSubscriptions.
func subscriptionPacket(guild, channel string) map[string]any {
	return map[string]any{"op": 37, "d": map[string]any{
		"subscriptions": map[string]any{guild: map[string]any{
			"typing": channel != "", "activities": false, "member_updates": false,
			"threads": false, "channels": map[string]any{},
			"members": []string{}, "thread_member_lists": []string{},
		}},
	}}
}

func validGateway(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "wss" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return (host == "gateway.discord.gg" || strings.HasSuffix(host, ".discord.gg")) && (u.Port() == "" || u.Port() == "443")
}
func (c *Client) Run(ctx context.Context, emit func(Event)) {
	c.runGateway(ctx, emit, c.gateway)
}

func (c *Client) runGateway(ctx context.Context, emit func(Event), connect func(context.Context, *gatewaySession, func(Event)) (bool, error)) {
	session := &gatewaySession{}
	session.Seq.Store(-1)
	delay := time.Second
	for ctx.Err() == nil {
		emit(Event{Status: "Connecting to Discord..."})
		started := time.Now()
		ready, err := connect(ctx, session, emit)
		lifetime := time.Since(started)
		if ctx.Err() != nil {
			return
		}
		var closeErr *websocket.CloseError
		if errors.As(err, &closeErr) {
			switch closeErr.Code {
			case 4001, 4002, 4005:
				emit(Event{Type: "GATEWAY_FAILED", Status: fmt.Sprintf("Discord rejected a Gateway packet (code %d). Automatic reconnect stopped to avoid repeating invalid traffic. Restart after updating Discrud.", closeErr.Code)})
				return
			case 4015:
				emit(Event{Type: "GATEWAY_FAILED", Status: "Discord's session limit was reached (code 4015). Close other Discrud instances before restarting."})
				return
			case 4004, 4010, 4011, 4012, 4013, 4014:
				emit(Event{Type: "AUTH_FAILED", Status: fmt.Sprintf("Discord closed the session (code %d). Log out and sign in again.", closeErr.Code)})
				return
			case 4006, 4007, 4009:
				session.ID = ""
				session.URL = ""
				session.Seq.Store(-1)
			}
		}
		delay = reconnectDelay(delay, ready, lifetime)
		if closeErr != nil && closeErr.Code == 4008 {
			delay = time.Minute
		}
		netdebug.GatewayRetry(err, ready, lifetime, delay)
		emit(Event{Status: fmt.Sprintf("Disconnected. Reconnecting in %.0f seconds...", delay.Seconds())})
		timer := time.NewTimer(delay + time.Duration(rand.IntN(500))*time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < time.Minute {
			delay *= 2
			if delay > time.Minute {
				delay = time.Minute
			}
		}
	}
}
func (c *Client) gateway(ctx context.Context, s *gatewaySession, emit func(Event)) (bool, error) {
	endpoint := "wss://gateway.discord.gg/?v=9&encoding=json"
	if s.ID != "" && validGateway(s.URL) {
		endpoint = strings.TrimRight(s.URL, "/") + "/?v=9&encoding=json"
	}
	return c.gatewayAt(ctx, s, emit, endpoint)
}

// A READY followed immediately by a disconnect is a failed connection, not a
// reason to reset exponential backoff. Only a sustained session earns a reset.
func reconnectDelay(previous time.Duration, ready bool, lifetime time.Duration) time.Duration {
	if ready && lifetime >= time.Minute {
		return time.Second
	}
	return previous
}

// The endpoint is injectable only within this package for local protocol tests.
func (c *Client) gatewayAt(ctx context.Context, s *gatewaySession, emit func(Event), endpoint string) (bool, error) {
	conn, resp, err := netdebug.Dial(ctx, &websocket.Dialer{HandshakeTimeout: 20 * time.Second}, endpoint, http.Header{"Origin": []string{"https://discord.com"}, "User-Agent": []string{UserAgent}})
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return false, err
	}
	defer conn.Close()
	conn.SetReadLimit(maxResponse)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	writer := &socketWriter{conn: conn}
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	var hello gatewayPacket
	if err = conn.ReadJSON(&hello); err != nil {
		return false, err
	}
	if hello.Op != 10 {
		return false, errors.New("invalid gateway hello")
	}
	var h struct {
		Interval int64 `json:"heartbeat_interval"`
	}
	if err = json.Unmarshal(hello.D, &h); err != nil || h.Interval < 1000 || h.Interval > 300000 {
		return false, errors.New("invalid heartbeat interval")
	}
	_ = conn.SetReadDeadline(time.Time{})
	if s.ID != "" {
		err = writer.write(map[string]any{"op": 6, "d": map[string]any{"token": c.token, "session_id": s.ID, "seq": s.Seq.Load()}})
	} else {
		s.Guild = ""
		err = writer.write(map[string]any{"op": 2, "d": map[string]any{"token": c.token, "capabilities": 0, "properties": clientProperties(), "presence": map[string]any{"status": "online", "since": 0, "activities": []any{}, "afk": false}, "compress": false}})
	}
	if err != nil {
		return false, err
	}
	var ack atomic.Bool
	ack.Store(true)
	done := make(chan struct{})
	workerDone := make(chan struct{})
	defer func() {
		close(done)
		conn.Close()
		<-workerDone
	}()
	sendHeartbeat := func() error {
		var seq any
		if n := s.Seq.Load(); n >= 0 {
			seq = n
		}
		ack.Store(false)
		return writer.write(map[string]any{"op": 1, "d": seq})
	}
	changes := c.subscriptionChanges()
	readySignal := make(chan struct{})
	// Coalesced selection notifications avoid polling and repeated subscriptions.
	go func() {
		defer close(workerDone)
		beats := time.NewTicker(time.Duration(h.Interval) * time.Millisecond)
		defer beats.Stop()
		readyCh := readySignal
		active := false
		lastGuild, lastChannel := s.Guild, ""
		updateSubscription := func() error {
			c.subMu.Lock()
			g, ch := c.guild, c.channel
			c.subMu.Unlock()
			// Channel changes within one guild only change the local message filter.
			// The wire subscription contains no channel-specific message selector.
			if g == lastGuild && (ch == "") == (lastChannel == "") {
				return nil
			}
			// Explicitly clear the old guild, including when switching to a DM.
			if lastGuild != "" && lastGuild != g {
				if err := writer.write(subscriptionPacket(lastGuild, "")); err != nil {
					return err
				}
			}
			if g != "" {
				if err := writer.write(subscriptionPacket(g, ch)); err != nil {
					return err
				}
			}
			lastGuild, lastChannel = g, ch
			s.Guild = g
			return nil
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-beats.C:
				if !ack.Load() {
					netdebug.HeartbeatTimeout()
					conn.Close()
					return
				}
				if sendHeartbeat() != nil {
					conn.Close()
					return
				}
			case <-readyCh:
				readyCh = nil
				active = true
				if updateSubscription() != nil {
					conn.Close()
					return
				}
			case <-changes:
				if !active {
					continue
				}
				if updateSubscription() != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	ready := false
	for {
		var p gatewayPacket
		if err = conn.ReadJSON(&p); err != nil {
			return ready, err
		}
		if p.S != nil {
			s.Seq.Store(*p.S)
		}
		switch p.Op {
		case 0:
			if p.T == "READY" {
				var d struct {
					SessionID string `json:"session_id"`
					ResumeURL string `json:"resume_gateway_url"`
				}
				if json.Unmarshal(p.D, &d) == nil {
					s.ID = d.SessionID
					s.URL = d.ResumeURL
				}
				if !ready {
					close(readySignal)
				}
				ready = true
				emit(Event{Status: "Connected"})
			}
			if p.T == "RESUMED" {
				if !ready {
					close(readySignal)
				}
				ready = true
				emit(Event{Status: "Connected (resumed)"})
			}
			event := Event{Type: p.T, Data: p.D}
			if c.acceptEvent(event) {
				emit(event)
			}
		case 1:
			if err = sendHeartbeat(); err != nil {
				return ready, err
			}
		case 7:
			return ready, errors.New("reconnect requested")
		case 9:
			var resumable bool
			_ = json.Unmarshal(p.D, &resumable)
			netdebug.SessionInvalidated(resumable)
			if !resumable {
				s.ID = ""
				s.URL = ""
				s.Seq.Store(-1)
			}
			return false, errors.New("session invalidated")
		case 11:
			ack.Store(true)
		}
	}
}
