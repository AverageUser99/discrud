package discord

import (
	"context"

	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"discrud/internal/netdebug"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/skip2/go-qrcode"
)

type QREvent struct {
	Image  image.Image
	Status string
}

type socketWriter struct {
	mu   sync.Mutex
	conn *netdebug.Socket
}

func (w *socketWriter) write(v any) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.conn.WriteJSON(v)
}

// QRLogin implements remote-auth v2. Only an explicit mobile confirmation grants a session.
// Protocol reference: https://docs.discord.food/remote-authentication/desktop
func QRLogin(ctx context.Context, notify func(QREvent)) (string, error) {
	notify(QREvent{Status: "Generating an ephemeral login key..."})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", errors.New("could not generate the login key")
	}
	return runQR(ctx, NewClient(""), "wss://remote-auth-gateway.discord.gg/?v=2", key, notify)
}
func runQR(ctx context.Context, client *Client, endpoint string, key *rsa.PrivateKey, notify func(QREvent)) (string, error) {
	conn, resp, err := netdebug.Dial(ctx, &websocket.Dialer{HandshakeTimeout: 20 * time.Second}, endpoint, http.Header{"Origin": []string{"https://discord.com"}, "User-Agent": []string{UserAgent}})
	if err != nil {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return "", errors.New("could not connect to Discord QR authentication")
	}
	defer conn.Close()
	conn.SetReadLimit(64 * 1024)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	writer := &socketWriter{conn: conn}
	var hello struct {
		Op        string `json:"op"`
		Heartbeat int64  `json:"heartbeat_interval"`
		Timeout   int64  `json:"timeout_ms"`
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	if err = conn.ReadJSON(&hello); err != nil || hello.Op != "hello" {
		return "", errors.New("QR authentication did not return a valid hello")
	}
	if hello.Heartbeat < 1000 || hello.Heartbeat > 300000 || hello.Timeout < 1000 || hello.Timeout > 600000 {
		return "", errors.New("QR authentication returned invalid time limits")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Duration(hello.Timeout) * time.Millisecond))
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(public)
	fingerprint := base64.RawURLEncoding.EncodeToString(hash[:])
	if err = writer.write(map[string]any{"op": "init", "encoded_public_key": base64.StdEncoding.EncodeToString(public)}); err != nil {
		return "", errors.New("could not initialize QR authentication")
	}
	done := make(chan struct{})
	defer close(done)
	var acknowledged atomic.Bool
	acknowledged.Store(true)
	go func() {
		ticker := time.NewTicker(time.Duration(hello.Heartbeat) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !acknowledged.Swap(false) || writer.write(map[string]string{"op": "heartbeat"}) != nil {
					conn.Close()
					return
				}
			}
		}
	}()
	decrypt := func(value string) ([]byte, error) {
		b, e := base64.StdEncoding.DecodeString(value)
		if e != nil {
			b, e = base64.RawStdEncoding.DecodeString(value)
		}
		if e != nil {
			return nil, errors.New("invalid encrypted authentication response")
		}
		p, e := rsa.DecryptOAEP(sha256.New(), rand.Reader, key, b, nil)
		if e != nil {
			return nil, errors.New("authentication proof could not be verified")
		}
		return p, nil
	}
	for {
		var packet struct {
			Op             string `json:"op"`
			EncryptedNonce string `json:"encrypted_nonce"`
			Fingerprint    string `json:"fingerprint"`
			EncryptedUser  string `json:"encrypted_user_payload"`
			Ticket         string `json:"ticket"`
		}
		if err = conn.ReadJSON(&packet); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New("QR session expired or disconnected. Choose Scan QR to generate another code.")
		}
		switch packet.Op {
		case "heartbeat_ack":
			acknowledged.Store(true)
		case "nonce_proof":
			nonce, e := decrypt(packet.EncryptedNonce)
			if e != nil {
				return "", e
			}
			if e = writer.write(map[string]string{"op": "nonce_proof", "nonce": base64.RawURLEncoding.EncodeToString(nonce)}); e != nil {
				return "", errors.New("could not submit authentication proof")
			}
		case "pending_remote_init":
			if subtle.ConstantTimeCompare([]byte(packet.Fingerprint), []byte(fingerprint)) != 1 {
				return "", errors.New("QR fingerprint mismatch; authentication stopped")
			}
			qr, e := qrcode.New("https://discord.com/ra/"+fingerprint, qrcode.Medium)
			if e != nil {
				return "", e
			}
			notify(QREvent{Image: qr.Image(280), Status: "Scan using Discord mobile Settings > Scan QR Code. Confirm only your own Discrud login."})
		case "pending_ticket":
			p, e := decrypt(packet.EncryptedUser)
			if e != nil {
				return "", e
			}
			parts := strings.SplitN(string(p), ":", 4)
			if len(parts) != 4 {
				return "", errors.New("invalid QR account response")
			}
			notify(QREvent{Status: fmt.Sprintf("Scanned by %s. Approve this login in the mobile app.", parts[3])})
		case "pending_login":
			notify(QREvent{Status: "Phone approval received. Completing desktop sign-in..."})
			if packet.Ticket == "" {
				return "", errors.New("phone approval arrived without a login ticket; generate and scan a fresh QR code")
			}
			var grant struct {
				EncryptedToken string `json:"encrypted_token"`
			}
			if err = client.json(ctx, "POST", "/users/@me/remote-auth/login", map[string]string{"ticket": packet.Ticket}, &grant); err != nil {
				return "", qrExchangeFailure(err)
			}
			p, e := decrypt(grant.EncryptedToken)
			if e != nil {
				return "", e
			}
			if len(p) == 0 || len(p) > 2048 {
				return "", errors.New("invalid session credential")
			}
			return string(p), nil
		case "cancel":
			return "", errors.New("login was cancelled on the mobile device")
		}
	}
}
