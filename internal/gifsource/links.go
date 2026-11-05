// Package gifsource resolves supported sharing links to real GIF or MP4 media.
// It never executes HTML/JavaScript or treats a provider webpage as an image.
package gifsource

import (
	"net/url"
	"regexp"
	"strings"
)

const MaxLinks = 4

var slugPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,180}$`)
var webLinks = regexp.MustCompile("(?i)https?://[^\\s<>\"`]+")

func secureURL(raw string) (*url.URL, bool) {
	if len(raw) > 4096 {
		return nil, false
	}
	u, err := url.Parse(raw)
	return u, err == nil && u.Scheme == "https" && u.User == nil && u.Hostname() != "" && (u.Port() == "" || u.Port() == "443") && u.Opaque == ""
}
func CanonicalPage(raw string) (string, bool) {
	u, ok := secureURL(raw)
	if !ok {
		return "", false
	}
	h := strings.ToLower(u.Hostname())
	if h != "klipy.com" && h != "www.klipy.com" {
		return "", false
	}
	path := strings.TrimSuffix(u.Path, "/")
	if !strings.HasPrefix(path, "/gifs/") || !slugPattern.MatchString(strings.TrimPrefix(path, "/gifs/")) {
		return "", false
	}
	return "https://klipy.com" + path, true
}
func Links(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range webLinks.FindAllString(text, 32) {
		raw = strings.TrimRight(raw, ".,!?:;)]}")
		if page, ok := CanonicalPage(raw); ok && !seen[page] {
			seen[page] = true
			out = append(out, page)
			if len(out) == MaxLinks {
				break
			}
		}
	}
	return out
}
func AllowedMedia(raw string) bool {
	u, ok := secureURL(raw)
	if !ok {
		return false
	}
	h := strings.ToLower(u.Hostname())
	switch h {
	case "cdn.discordapp.com", "media.discordapp.net", "images-ext-1.discordapp.net", "images-ext-2.discordapp.net", "media.tenor.com", "c.tenor.com":
		return true
	}
	return h == "klipy.com" || strings.HasSuffix(h, ".klipy.com")
}
func gifURL(raw string) bool {
	u, ok := secureURL(raw)
	return ok && strings.HasSuffix(strings.ToLower(u.Path), ".gif")
}
func VideoURL(raw string) bool {
	u, ok := secureURL(raw)
	if !ok {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.HasSuffix(p, ".mp4") || strings.HasSuffix(p, ".m4v")
}

type Hint struct {
	URL, ProxyURL string
	GIF, Video    bool
}

func knownOtherFormat(path string) bool {
	path = strings.ToLower(path)
	for _, suffix := range []string{".mp4", ".m4v", ".webm", ".png", ".jpg", ".jpeg", ".webp", ".avif"} {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// Real video sources win over posters. Never fabricate .gif filenames from an
// MP4/poster URL: providers can return a valid "content unavailable" GIF there.
func Candidates(hints []Hint) []string {
	var out []string
	seen := map[string]bool{}
	add := func(raw string) {
		if AllowedMedia(raw) && !seen[raw] && len(out) < 8 {
			seen[raw] = true
			out = append(out, raw)
		}
	}
	for _, h := range hints {
		if h.Video || VideoURL(h.URL) || VideoURL(h.ProxyURL) {
			for _, raw := range []string{h.ProxyURL, h.URL} {
				u, ok := secureURL(raw)
				if ok && (VideoURL(raw) || !knownOtherFormat(u.Path)) {
					add(raw)
				}
			}
		}
	}
	for _, h := range hints {
		if h.GIF || gifURL(h.URL) || gifURL(h.ProxyURL) {
			for _, raw := range []string{h.ProxyURL, h.URL} {
				if !AllowedMedia(raw) {
					continue
				}
				u, _ := url.Parse(raw)
				if !knownOtherFormat(u.Path) {
					q := u.Query()
					if q.Has("format") {
						q.Del("format")
						u.RawQuery = q.Encode()
					}
					add(u.String())
				}
			}
		}
	}
	return out
}
