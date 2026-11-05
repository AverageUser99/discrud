package gifsource

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"discrud/internal/gifplayer"
	"discrud/internal/nativevideo"
)

var ErrUnavailable = errors.New("no accessible GIF/MP4 source; click to retry or open the original link")
var ErrBlocked = errors.New("media provider blocked the preview request; click to retry or open the original link")
var ErrUnsafe = errors.New("unsupported or unsafe GIF source")

type entry struct {
	url   string
	err   error
	until time.Time
}
type flight struct {
	done chan struct{}
	url  string
	err  error
}
type Resolver struct {
	client    *http.Client
	userAgent string
	mu        sync.Mutex
	cache     map[string]entry
	flights   map[string]*flight
}

func New(client *http.Client, userAgent ...string) *Resolver {
	if client == nil {
		client = &http.Client{Transport: NewTransport(), Timeout: 20 * time.Second}
	}
	copy := *client
	copy.Jar = nil
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	agent := "Discrud GIF preview"
	if len(userAgent) > 0 && userAgent[0] != "" {
		agent = userAgent[0]
	}
	return &Resolver{client: &copy, userAgent: agent, cache: map[string]entry{}, flights: map[string]*flight{}}
}

// NewTransport pins each connection to a validated public IP. Redirects are
// separately validated, TLS still checks the original hostname, and environment
// proxies cannot bypass the DNS/private-network boundary.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrUnsafe
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, ErrUnavailable
		}
		for _, ip := range ips {
			if !publicIP(ip.IP) {
				return nil, ErrUnsafe
			}
		}
		d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		for _, ip := range ips {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		return nil, ErrUnavailable
	}
	return t
}
func publicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	// CGNAT and documentation/benchmark ranges are not public media destinations.
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
func (r *Resolver) Invalidate(page string) {
	page, ok := CanonicalPage(page)
	if !ok {
		return
	}
	r.mu.Lock()
	delete(r.cache, page)
	r.mu.Unlock()
}
func (r *Resolver) remember(page, raw string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// A slow failed webpage request must not overwrite a successful late embed.
	if old, ok := r.cache[page]; err != nil && ok && old.err == nil && time.Now().Before(old.until) {
		return
	}
	if len(r.cache) >= 64 {
		oldest := ""
		for key, v := range r.cache {
			if oldest == "" || v.until.Before(r.cache[oldest].until) {
				oldest = key
			}
		}
		delete(r.cache, oldest)
	}
	ttl := 10 * time.Minute
	if err != nil {
		ttl = 15 * time.Second
	}
	r.cache[page] = entry{url: raw, err: err, until: time.Now().Add(ttl)}
}

// Load returns bounded GIF or self-contained MP4 bytes, not HTML/static posters. Hints
// from a late Discord embed are always tried before a negative metadata cache.
// Only URLs/short failure results are cached, never an unbounded media byte cache.
func (r *Resolver) Load(ctx context.Context, page string, hints []Hint) ([]byte, error) {
	page, ok := CanonicalPage(page)
	if !ok {
		return nil, ErrUnsafe
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	job := &requestJob{r: r, page: page, seen: map[string]bool{}}
	if data, raw := job.tryMedia(ctx, Candidates(hints)); data != nil {
		r.remember(page, raw, nil)
		return data, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.mu.Lock()
	cached, hit := r.cache[page]
	r.mu.Unlock()
	if hit && time.Now().Before(cached.until) {
		if cached.err != nil {
			return nil, cached.err
		}
		if data, _ := job.tryMedia(ctx, []string{cached.url}); data != nil {
			return data, nil
		} else {
			r.Invalidate(page)
		}
	}
	r.mu.Lock()
	if pending := r.flights[page]; pending != nil {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-pending.done:
		}
		if pending.err != nil {
			return nil, pending.err
		}
		if data, _ := job.tryMedia(ctx, []string{pending.url}); data != nil {
			return data, nil
		}
		return nil, ErrUnavailable
	}
	pending := &flight{done: make(chan struct{})}
	r.flights[page] = pending
	r.mu.Unlock()
	data, raw, err := job.resolvePage(ctx)
	if ctx.Err() != nil {
		err = ctx.Err()
	} else {
		r.remember(page, raw, err)
	}
	r.mu.Lock()
	pending.url, pending.err = raw, err
	delete(r.flights, page)
	close(pending.done)
	r.mu.Unlock()
	return data, err
}

type requestJob struct {
	r        *Resolver
	page     string
	seen     map[string]bool
	requests int
}

func (j *requestJob) tryMedia(ctx context.Context, candidates []string) ([]byte, string) {
	for _, raw := range candidates {
		if j.seen[raw] || !AllowedMedia(raw) {
			continue
		}
		j.seen[raw] = true
		data, _, err := j.fetch(ctx, raw, "media", gifplayer.MaxBytes)
		if err == nil && validMedia(data) {
			return data, raw
		}
		if ctx.Err() != nil || j.requests >= 12 {
			break
		}
	}
	return nil, ""
}

// LoadMedia loads only supplied media URLs (e.g. a Discord GIFV video proxy).
func (r *Resolver) LoadMedia(ctx context.Context, hints []Hint) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	job := &requestJob{r: r, seen: map[string]bool{}}
	if b, _ := job.tryMedia(ctx, Candidates(hints)); b != nil {
		return b, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, ErrUnavailable
}
func validMedia(b []byte) bool {
	if isGIFData(b) {
		_, err := gifplayer.Inspect(b)
		return err == nil
	}
	if nativevideo.IsMP4(b) {
		_, err := nativevideo.Inspect(b)
		return err == nil
	}
	return false
}
func isGIFData(b []byte) bool {
	return len(b) >= 6 && (string(b[:6]) == "GIF89a" || string(b[:6]) == "GIF87a")
}
func (j *requestJob) resolvePage(ctx context.Context) ([]byte, string, error) {
	data, base, err := j.fetch(ctx, j.page, "page", MaxMetadata)
	if err != nil {
		return nil, "", err
	}
	if isGIFData(data) {
		if _, err := gifplayer.Inspect(data); err == nil {
			return data, base, nil
		}
	}
	meta := pageMetadata(data, j.page)
	if b, raw := j.tryMedia(ctx, Candidates(meta.hints)); b != nil {
		return b, raw, nil
	}
	if meta.oembed != "" && j.allowed(meta.oembed, "oembed") {
		data, _, err = j.fetch(ctx, meta.oembed, "oembed", MaxMetadata)
		if err == nil {
			hints := jsonHints(data, j.page, true)
			// oEmbed may carry declarative image/video HTML rather than a GIF field.
			var embedded struct {
				HTML string `json:"html"`
			}
			var embedMeta metadata
			if json.Unmarshal(data, &embedded) == nil {
				embedMeta = parseMetadata([]byte(embedded.HTML), j.page, true)
				hints = append(hints, embedMeta.hints...)
			}
			if b, raw := j.tryMedia(ctx, Candidates(hints)); b != nil {
				return b, raw, nil
			}
			// One provider-advertised iframe may contain declarative media tags.
			// Fetch its metadata only; never run a webview, script or nested frames.
			if j.allowed(embedMeta.iframe, "embed") {
				if frame, base, err := j.fetch(ctx, embedMeta.iframe, "embed", MaxMetadata); err == nil {
					if b, raw := j.tryMedia(ctx, Candidates(metadataAt(frame, j.page, base, true).hints)); b != nil {
						return b, raw, nil
					}
				}
			}
		}
	}
	lower := strings.ToLower(string(data))
	if strings.Contains(lower, "cf-chl-") || strings.Contains(lower, "challenges.cloudflare.com") {
		return nil, "", ErrBlocked
	}
	return nil, "", ErrUnavailable
}
func (j *requestJob) allowed(raw, kind string) bool {
	switch kind {
	case "media":
		return AllowedMedia(raw)
	case "page":
		p, ok := CanonicalPage(raw)
		return ok && p == j.page
	case "embed":
		u, ok := secureURL(raw)
		if !ok {
			return false
		}
		h := strings.ToLower(u.Hostname())
		return (h == "klipy.com" || strings.HasSuffix(h, ".klipy.com")) && strings.Contains(strings.ToLower(u.Path), "embed")
	case "oembed":
		u, ok := secureURL(raw)
		if !ok {
			return false
		}
		h := strings.ToLower(u.Hostname())
		if (h != "klipy.com" && !strings.HasSuffix(h, ".klipy.com")) || !strings.Contains(strings.ToLower(u.Path), "oembed") {
			return false
		}
		if target := u.Query().Get("url"); target != "" {
			p, ok := CanonicalPage(target)
			return ok && p == j.page
		}
		return true
	}
	return false
}
func (j *requestJob) fetch(ctx context.Context, raw, kind string, limit int) ([]byte, string, error) {
	for redirects := 0; redirects <= 3; redirects++ {
		if !j.allowed(raw, kind) || j.requests >= 12 {
			return nil, "", ErrUnsafe
		}
		j.requests++
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, "", ErrUnsafe
		}
		req.Header.Set("User-Agent", j.r.userAgent)
		if kind == "media" {
			req.Header.Set("Accept", "video/mp4, image/gif;q=0.9")
		} else {
			req.Header.Set("Accept", "text/html, application/json;q=0.9")
		}
		resp, err := j.r.client.Do(req)
		if err != nil {
			return nil, "", ErrUnavailable
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			location, err := resp.Location()
			resp.Body.Close()
			if err != nil {
				return nil, "", ErrUnavailable
			}
			raw = location.String()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if resp.StatusCode == 403 || resp.StatusCode == 429 || resp.StatusCode == 503 {
				return nil, "", ErrBlocked
			}
			return nil, "", ErrUnavailable
		}
		if resp.ContentLength > int64(limit) {
			resp.Body.Close()
			return nil, "", ErrUnavailable
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
		resp.Body.Close()
		if err != nil || len(body) > limit {
			return nil, "", ErrUnavailable
		}
		return body, raw, nil
	}
	return nil, "", ErrUnavailable
}
