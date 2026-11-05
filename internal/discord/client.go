package discord

import (
	"bytes"

	"context"
	"crypto/rand"
	"discrud/internal/netdebug"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const APIBase = "https://discord.com/api/v9"
const UserAgent = "Discrud/" + ClientVersion + " (personal unofficial Windows chat client)"
const MaxUploadBytes = 10 * 1024 * 1024 // Conservative MVP cap, not an entitlement claim.
const maxResponse = 16 * 1024 * 1024

type Client struct {
	token          string
	base           string
	http           *http.Client
	rateMu         sync.Mutex
	blockedUntil   time.Time
	subMu          sync.Mutex
	guild, channel string
	subChanged     chan struct{}
}

func NewClient(token string) *Client {
	return &Client{token: token, base: APIBase, http: &http.Client{Transport: netdebug.Transport(nil), Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func NewNonce() string { var b [12]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }
func (c *Client) request(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return errors.New("invalid API path")
	}
	c.rateMu.Lock()
	delay := time.Until(c.blockedUntil)
	c.rateMu.Unlock()
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Origin", "https://discord.com")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("X-Discord-Locale", "en-US")
	req.Header.Set("X-Super-Properties", superProperties())
	if c.token == "" {
		req.Header.Set("Referer", "https://discord.com/login")
	} else {
		req.Header.Set("Referer", "https://discord.com/channels/@me")
	}
	if c.token != "" {
		req.Header.Set("Authorization", c.token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("Discord could not be reached. Check your connection; a send may have succeeded before the connection failed.")
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil {
		return errors.New("Discord response interrupted; check the conversation before retrying a send")
	}
	if len(b) > maxResponse {
		return errors.New("Discord response exceeded the safety limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		e := classifyAPIError(resp.StatusCode, b)
		if resp.StatusCode == 429 {
			c.rateMu.Lock()
			c.blockedUntil = time.Now().Add(e.RetryAfter)
			c.rateMu.Unlock()
		}
		return e
	}
	if out != nil && len(b) > 0 {
		if err = json.Unmarshal(b, out); err != nil {
			return errors.New("Discord returned an unexpected response format")
		}
	}
	return nil
}
func (c *Client) json(ctx context.Context, method, path string, value, out any) error {
	var body io.Reader
	if value != nil {
		b, err := json.Marshal(value)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	return c.request(ctx, method, path, body, "application/json", out)
}
func (c *Client) Me(ctx context.Context) (u User, err error) {
	err = c.json(ctx, "GET", "/users/@me", nil, &u)
	return
}
func (c *Client) Guilds(ctx context.Context) (all []Guild, err error) {
	after := ""
	for {
		var page []Guild
		path := "/users/@me/guilds?limit=200"
		if after != "" {
			path += "&after=" + url.QueryEscape(after)
		}
		if err = c.json(ctx, "GET", path, nil, &page); err != nil {
			return
		}
		all = append(all, page...)
		if len(page) < 200 {
			return
		}
		after = page[len(page)-1].ID
		if len(all) > 5000 {
			return nil, errors.New("server list exceeded safety limit")
		}
	}
}
func (c *Client) Channels(ctx context.Context, g string) (v []Channel, err error) {
	err = c.json(ctx, "GET", "/guilds/"+url.PathEscape(g)+"/channels", nil, &v)
	for i := range v {
		v[i].GuildID = g
	}
	return
}
func (c *Client) DMs(ctx context.Context) (v []Channel, err error) {
	err = c.json(ctx, "GET", "/users/@me/channels", nil, &v)
	return
}
func (c *Client) Roles(ctx context.Context, g string) (v []Role, err error) {
	err = c.json(ctx, "GET", "/guilds/"+url.PathEscape(g)+"/roles", nil, &v)
	return
}
func (c *Client) MyRoles(ctx context.Context, g string) (v []string, err error) {
	var m struct {
		Roles []string `json:"roles"`
	}
	err = c.json(ctx, "GET", "/users/@me/guilds/"+url.PathEscape(g)+"/member", nil, &m)
	return m.Roles, err
}
func (c *Client) Messages(ctx context.Context, ch, before string) (v []Message, err error) {
	path := "/channels/" + url.PathEscape(ch) + "/messages?limit=50"
	if before != "" {
		path += "&before=" + url.QueryEscape(before)
	}
	err = c.json(ctx, "GET", path, nil, &v)
	return
}
func mentionPolicy(replyPing bool) any {
	return map[string]any{"parse": []string{"users", "roles"}, "replied_user": replyPing}
}
func (c *Client) Send(ctx context.Context, ch string, s Send) (m Message, err error) {
	if strings.TrimSpace(s.Content) == "" && len(s.Files) == 0 {
		return m, errors.New("message is empty")
	}
	if s.Nonce == "" {
		s.Nonce = NewNonce()
	}
	payload := map[string]any{"content": s.Content, "allowed_mentions": mentionPolicy(s.ReplyTo != nil && s.ReplyPing), "nonce": s.Nonce, "enforce_nonce": true}
	if s.ReplyTo != nil {
		payload["message_reference"] = s.ReplyTo
	}
	path := "/channels/" + url.PathEscape(ch) + "/messages"
	if len(s.Files) == 0 {
		err = c.json(ctx, "POST", path, payload, &m)
		return
	}
	var total int
	for _, f := range s.Files {
		total += len(f.Data)
	}
	if total > MaxUploadBytes {
		return m, fmt.Errorf("MVP uploads are limited to %d MiB total", MaxUploadBytes/1024/1024)
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	attachments := make([]map[string]any, 0, len(s.Files))
	for i, f := range s.Files {
		attachments = append(attachments, map[string]any{"id": i, "filename": f.Name})
	}
	payload["attachments"] = attachments
	p, _ := json.Marshal(payload)
	_ = w.WriteField("payload_json", string(p))
	for i, f := range s.Files {
		part, e := w.CreateFormFile(fmt.Sprintf("files[%d]", i), f.Name)
		if e != nil {
			return m, e
		}
		if _, e = part.Write(f.Data); e != nil {
			return m, e
		}
	}
	if err = w.Close(); err != nil {
		return
	}
	err = c.request(ctx, "POST", path, &b, w.FormDataContentType(), &m)
	return
}
func (c *Client) Edit(ctx context.Context, ch, id, text string) (m Message, err error) {
	err = c.json(ctx, "PATCH", "/channels/"+url.PathEscape(ch)+"/messages/"+url.PathEscape(id), map[string]any{"content": text, "allowed_mentions": mentionPolicy(false)}, &m)
	return
}
func (c *Client) Delete(ctx context.Context, ch, id string) error {
	return c.json(ctx, "DELETE", "/channels/"+url.PathEscape(ch)+"/messages/"+url.PathEscape(id), nil, nil)
}
func (c *Client) Subscribe(guild, channel string) {
	c.subMu.Lock()
	c.guild = guild
	if c.subChanged == nil {
		c.subChanged = make(chan struct{}, 1)
	}
	changed := c.subChanged
	c.channel = channel
	c.subMu.Unlock()
	select {
	case changed <- struct{}{}:
	default:
	}
}

func (c *Client) subscriptionChanges() <-chan struct{} {
	c.subMu.Lock()
	defer c.subMu.Unlock()
	if c.subChanged == nil {
		c.subChanged = make(chan struct{}, 1)
	}
	return c.subChanged
}

// AcceptEvent also runs at the UI boundary to reject events queued before a switch.
// Account metadata and connection lifecycle events are not channel subscriptions.
func AcceptEvent(e Event, channel string) bool {
	switch e.Type {
	case "MESSAGE_CREATE", "MESSAGE_UPDATE", "MESSAGE_DELETE", "MESSAGE_DELETE_BULK":
		var keys struct {
			ChannelID string `json:"channel_id"`
		}
		return channel != "" && json.Unmarshal(e.Data, &keys) == nil && keys.ChannelID == channel
	case "", "READY", "RESUMED", "AUTH_FAILED", "GATEWAY_FAILED", "CHANNEL_CREATE", "CHANNEL_UPDATE", "CHANNEL_DELETE":
		return true
	default:
		return false
	}
}

func (c *Client) acceptEvent(e Event) bool {
	c.subMu.Lock()
	channel := c.channel
	c.subMu.Unlock()
	return AcceptEvent(e, channel)
}
