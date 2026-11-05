package netdebug

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

// Never include CloseError.Text or err.Error(): peers and HTTP clients may put
// credentials, URLs or other untrusted strings into them.
func failureKind(err error) (string, int) {
	var closeErr *websocket.CloseError
	if errors.As(err, &closeErr) {
		return "websocket-close", closeErr.Code
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled", 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", 0
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return "timeout", 0
	}
	if errors.Is(err, websocket.ErrReadLimit) {
		return "read-limit", 0
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "eof", 0
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return "invalid-json", 0
	}
	return "io-or-protocol-error", 0
}

func GatewayRetry(err error, ready bool, lifetime, delay time.Duration) {
	if m := active.Load(); m != nil {
		kind, code := failureKind(err)
		m.logger.Printf("gateway disconnected cause=%s close_code=%d ready=%t lifetime_ms=%d retry_ms=%d", kind, code, ready, lifetime.Milliseconds(), delay.Milliseconds())
	}
}
func HeartbeatTimeout() {
	if m := active.Load(); m != nil {
		m.logger.Print("gateway heartbeat-ack-timeout")
	}
}

func SessionInvalidated(resumable bool) {
	if m := active.Load(); m != nil {
		m.logger.Printf("gateway session-invalidated resumable=%t", resumable)
	}
}
