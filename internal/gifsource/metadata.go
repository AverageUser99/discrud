package gifsource

import (
	"encoding/json"
	"html"
	"net/url"
	"strings"
)

const MaxMetadata = 1 << 20

type metadata struct {
	hints  []Hint
	oembed string
	iframe string
}

// A small bounded tag/attribute scanner, not a regex pretending to be an HTML
// parser. Only declarative metadata is consumed. Script text is never executed;
// comments, quoted '>' characters, entities and raw script bodies are handled.
func pageMetadata(data []byte, page string) metadata { return parseMetadata(data, page, false) }
func parseMetadata(data []byte, page string, mediaTags bool) metadata {
	return metadataAt(data, page, page, mediaTags)
}
func metadataAt(data []byte, page, base string, mediaTags bool) metadata {
	var out metadata
	if len(data) > MaxMetadata {
		return out
	}
	text := string(data)
	lower := asciiLower(text)
	gifType := false
	add := func(raw string, typed bool) {
		if len(out.hints) < 64 && len(raw) <= 4096 {
			out.hints = append(out.hints, Hint{URL: resolveURL(base, raw), GIF: typed})
		}
	}
	for pos, tags := 0, 0; pos < len(text) && tags < 4096; tags++ {
		n := strings.IndexByte(text[pos:], '<')
		if n < 0 {
			break
		}
		pos += n
		if strings.HasPrefix(text[pos:], "<!--") {
			end := strings.Index(text[pos+4:], "-->")
			if end < 0 {
				break
			}
			pos += 4 + end + 3
			continue
		}
		end := pos + 1
		quote := byte(0)
		for ; end < len(text); end++ {
			b := text[end]
			if quote != 0 {
				if b == quote {
					quote = 0
				}
			} else if b == '\'' || b == '"' {
				quote = b
			} else if b == '>' {
				break
			}
		}
		if end == len(text) {
			break
		}
		tag := text[pos+1 : end]
		pos = end + 1
		name, attrs := tagAttributes(tag)
		switch name {
		case "img", "video", "source":
			if mediaTags {
				add(attrs["src"], strings.EqualFold(attrs["type"], "image/gif"))
			}
		case "iframe":
			if mediaTags && out.iframe == "" {
				out.iframe = resolveURL(base, attrs["src"])
			}
		case "meta":
			key := strings.ToLower(attrs["property"])
			if key == "" {
				key = strings.ToLower(attrs["name"])
			}
			switch key {
			case "og:image:type", "og:video:type", "twitter:player:stream:content_type":
				gifType = gifType || strings.EqualFold(attrs["content"], "image/gif")
			case "og:image", "og:image:url", "og:image:secure_url", "og:video", "og:video:url", "og:video:secure_url", "twitter:image", "twitter:image:src", "twitter:player:stream":
				add(attrs["content"], false)
			}
		case "link":
			if strings.EqualFold(attrs["rel"], "image_src") {
				add(attrs["href"], false)
			}
			if strings.Contains(strings.ToLower(attrs["type"]), "json+oembed") {
				out.oembed = resolveURL(base, attrs["href"])
			}
		case "script":
			close := strings.Index(lower[pos:], "</script")
			if close < 0 {
				pos = len(text)
				break
			}
			body := text[pos : pos+close]
			pos += close
			kind := strings.ToLower(attrs["type"])
			if kind == "application/ld+json" || attrs["id"] == "__NEXT_DATA__" || kind == "application/json" {
				out.hints = append(out.hints, jsonHints([]byte(body), page, kind == "application/ld+json", base)...)
				if len(out.hints) > 64 {
					out.hints = out.hints[:64]
				}
			}
		}
	}
	if gifType {
		for i := range out.hints {
			if !knownOtherFormat(urlPath(out.hints[i].URL)) {
				out.hints[i].GIF = true
			}
		}
	}
	return out
}

// HTML tag names are ASCII. Preserve byte offsets even for Unicode characters
// whose full Unicode case folding changes their UTF-8 length.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}
func urlPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Path
}
func resolveURL(base, raw string) string {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	raw = strings.ReplaceAll(raw, `\/`, `/`)
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		return ""
	}
	b, _ := url.Parse(base)
	return b.ResolveReference(u).String()
}
func tagAttributes(tag string) (string, map[string]string) {
	i := 0
	for i < len(tag) && !space(tag[i]) && tag[i] != '/' {
		i++
	}
	name := strings.ToLower(tag[:i])
	attrs := map[string]string{}
	for i < len(tag) {
		for i < len(tag) && (space(tag[i]) || tag[i] == '/') {
			i++
		}
		start := i
		for i < len(tag) && !space(tag[i]) && tag[i] != '=' && tag[i] != '/' {
			i++
		}
		if start == i {
			i++
			continue
		}
		key := strings.ToLower(tag[start:i])
		for i < len(tag) && space(tag[i]) {
			i++
		}
		if i == len(tag) || tag[i] != '=' {
			continue
		}
		i++
		for i < len(tag) && space(tag[i]) {
			i++
		}
		if i == len(tag) {
			break
		}
		q := byte(0)
		if tag[i] == '\'' || tag[i] == '"' {
			q = tag[i]
			i++
		}
		start = i
		for i < len(tag) && ((q != 0 && tag[i] != q) || (q == 0 && !space(tag[i]))) {
			i++
		}
		attrs[key] = html.UnescapeString(tag[start:i])
		if q != 0 && i < len(tag) {
			i++
		}
	}
	return name, attrs
}
func space(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == '\f' }

func jsonHints(data []byte, page string, selected bool, documentBase ...string) []Hint {
	base := page
	if len(documentBase) > 0 {
		base = documentBase[0]
	}
	var root any
	if len(data) > MaxMetadata || json.Unmarshal(data, &root) != nil {
		return nil
	}
	var out []Hint
	slug := strings.TrimPrefix(urlPath(page), "/gifs/")
	visited := 0
	var walk func(any, bool, int)
	walk = func(value any, trusted bool, depth int) {
		visited++
		if depth > 24 || visited > 4096 || len(out) >= 64 {
			return
		}
		switch v := value.(type) {
		case map[string]any:
			if s, ok := v["slug"].(string); ok {
				if s != slug {
					return
				}
				trusted = true
			}
			for _, key := range []string{"url", "@id", "mainEntityOfPage"} {
				if s, ok := v[key].(string); ok {
					if p, ok := CanonicalPage(s); ok && p == page {
						trusted = true
					}
				}
			}
			typed := false
			videoTyped := false
			for _, key := range []string{"mime", "content_type", "contentType", "encodingFormat", "format"} {
				if s, ok := v[key].(string); ok && (strings.EqualFold(s, "image/gif") || strings.EqualFold(s, "gif")) {
					typed = true
				}
			}
			for _, key := range []string{"mime", "content_type", "contentType", "encodingFormat", "format"} {
				if s, ok := v[key].(string); ok && (strings.EqualFold(s, "video/mp4") || strings.EqualFold(s, "mp4")) {
					videoTyped = true
				}
			}
			// Candidate selection prioritizes actual video over GIF posters.
			for _, key := range []string{"contentUrl", "content_url", "gif_url", "gifUrl", "url", "src", "thumbnailUrl", "thumbnail_url"} {
				if s, ok := v[key].(string); ok && trusted && len(out) < 64 {
					out = append(out, Hint{URL: resolveURL(base, s), GIF: typed, Video: videoTyped})
				}
			}
			for _, key := range []string{"props", "pageProps", "mp4", "m4v", "gif", "item", "data", "content", "media", "media_formats", "formats", "file", "files", "original", "mediumgif", "tinygif", "nanogif", "md", "sm", "xs", "hd", "image", "video", "mainEntity", "encoding", "@graph"} {
				if child, ok := v[key]; ok {
					start := len(out)
					walk(child, trusted || key == "gif" || key == "item" || key == "pageProps", depth+1)
					if key == "video" || key == "mp4" || key == "m4v" {
						for i := start; i < len(out); i++ {
							out[i].Video = true
						}
					}
					if key == "gif" || strings.HasSuffix(key, "gif") {
						for i := start; i < len(out); i++ {
							out[i].GIF = true
						}
					}
				}
			}
		case []any:
			for _, child := range v {
				walk(child, trusted, depth+1)
			}
		case string:
			if trusted {
				out = append(out, Hint{URL: resolveURL(base, v)})
			}
		}
	}
	walk(root, selected, 0)
	return out
}
