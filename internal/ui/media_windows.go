//go:build windows

package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unsafe"

	"discrud/internal/demo"
	"discrud/internal/discord"
	"discrud/internal/gifplayer"
	"discrud/internal/gifsource"
	"discrud/internal/netdebug"
	"discrud/internal/storage"
	"github.com/lxn/win"
	_ "golang.org/x/image/webp"
)

func safeWebURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" && u.User == nil
}
func allowedMediaURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "cdn.discordapp.com" || h == "media.discordapp.net" || h == "images-ext-1.discordapp.net" || h == "images-ext-2.discordapp.net"
}
func cachedThumbnail(ctx context.Context, cache *storage.ThumbnailCache, a discord.Attachment, isDemo bool) (image.Image, error) {
	if cache == nil {
		return thumbnail(ctx, a, isDemo)
	}
	// Attachment IDs are immutable; signed CDN query strings may rotate.
	identity := a.ID
	if identity == "" {
		if u, err := url.Parse(a.URL); err == nil {
			identity = u.Host + u.Path
		}
	}
	// Previous previews requested both dimensions and may already be cropped.
	img, hit, err := cache.Get(ctx, "aspect-fit-v2:"+identity, func(ctx context.Context) (image.Image, error) { return thumbnail(ctx, a, isDemo) })
	result := "ok"
	if err != nil {
		result = "unavailable"
		if errors.Is(err, image.ErrFormat) {
			result = "unsupported-format"
		}
		if ctx.Err() != nil {
			result = "cancelled"
		}
	}
	netdebug.Thumbnail(hit, result)
	return img, err
}

func thumbnail(ctx context.Context, a discord.Attachment, isDemo bool) (image.Image, error) {
	if isDemo && a.URL == "demo://sample.png" {
		img := image.NewRGBA(image.Rect(0, 0, 360, 160))
		for y := 0; y < 160; y++ {
			for x := 0; x < 360; x++ {
				img.SetRGBA(x, y, color.RGBA{uint8(50 + x/4), uint8(90 + y/2), 180, 255})
			}
		}
		return img, nil
	}
	raw := a.ProxyURL
	if !allowedMediaURL(raw) {
		raw = a.URL
	}
	if !allowedMediaURL(raw) {
		return nil, errors.New("preview host is not a Discord attachment host")
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	// Request only one dimension: a fixed width and height can make the
	// media proxy crop to that rectangle before we ever decode the image.
	q.Del("width")
	q.Del("height")
	if a.Width > 0 && a.Height > 0 && int64(a.Height)*360 > int64(a.Width)*240 {
		q.Set("height", "240")
	} else {
		q.Set("width", "360")
	}
	u.RawQuery = q.Encode()
	b, e := downloadMedia(ctx, u.String(), 5*1024*1024)
	if e != nil {
		return nil, e
	}
	cfg, _, e := image.DecodeConfig(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16*1024*1024 {
		return nil, errors.New("unsupported or oversized image preview")
	}
	img, _, e := image.Decode(bytes.NewReader(b))
	if e != nil {
		return nil, e
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w > 360 {
		h = h * 360 / w
		w = 360
	}
	if h > 240 {
		w = w * 240 / h
		h = 240
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	small := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			small.Set(x, y, img.At(bounds.Min.X+x*bounds.Dx()/w, bounds.Min.Y+y*bounds.Dy()/h))
		}
	}
	return small, nil
}

// No cookies, authorization, redirects, arbitrary hosts or HTML fetches.
func downloadMedia(ctx context.Context, raw string, limit int) ([]byte, error) {
	if !allowedMediaURL(raw) {
		return nil, errors.New("preview host is not an approved Discord media host")
	}
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", discord.UserAgent)
	client := &http.Client{Transport: netdebug.Transport(nil), Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("preview unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("preview unavailable")
	}
	if resp.ContentLength > int64(limit) {
		return nil, errors.New("preview download limit exceeded")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("preview download limit exceeded")
	}
	return data, nil
}

func isGIF(a discord.Attachment) bool {
	if a.URL == "demo://sample.gif" {
		return true
	}
	if strings.EqualFold(strings.Split(a.ContentType, ";")[0], "image/gif") {
		return true
	}
	for _, raw := range []string{a.URL, a.ProxyURL, a.Filename} {
		u, err := url.Parse(raw)
		if err == nil && strings.HasSuffix(strings.ToLower(u.Path), ".gif") {
			return true
		}
	}
	return false
}
func isMP4(a discord.Attachment) bool {
	mime := strings.TrimSpace(strings.Split(a.ContentType, ";")[0])
	return strings.EqualFold(mime, "video/mp4") || strings.EqualFold(mime, "video/x-m4v") || gifsource.VideoURL(a.URL) || gifsource.VideoURL(a.ProxyURL) || strings.HasSuffix(strings.ToLower(a.Filename), ".mp4") || strings.HasSuffix(strings.ToLower(a.Filename), ".m4v")
}
func gifBytes(ctx context.Context, a discord.Attachment, isDemo bool) ([]byte, error) {
	if isDemo && a.URL == "demo://sample.mp4" {
		return demo.MP4(), nil
	}
	if isDemo && a.URL == "demo://sample.gif" {
		return demo.GIF(), nil
	}
	raw := a.ProxyURL
	if !allowedMediaURL(raw) {
		raw = a.URL
	}
	return downloadMedia(ctx, raw, gifplayer.MaxBytes)
}

func clipboardImage() (discord.Upload, bool, error) {
	if !win.IsClipboardFormatAvailable(win.CF_BITMAP) {
		return discord.Upload{}, false, nil
	}
	if !win.OpenClipboard(0) {
		return discord.Upload{}, true, errors.New("clipboard is busy; try Paste image again")
	}
	defer win.CloseClipboard()
	h := win.HBITMAP(win.GetClipboardData(win.CF_BITMAP))
	if h == 0 {
		return discord.Upload{}, true, errors.New("could not read clipboard image")
	}
	var bm win.BITMAP
	if win.GetObject(win.HGDIOBJ(h), unsafe.Sizeof(bm), unsafe.Pointer(&bm)) == 0 {
		return discord.Upload{}, true, errors.New("could not inspect clipboard image")
	}
	w, height := int(bm.BmWidth), int(bm.BmHeight)
	if w <= 0 || height <= 0 || int64(w)*int64(height) > 16*1024*1024 {
		return discord.Upload{}, true, errors.New("clipboard image exceeds 16 megapixels")
	}
	pixels := make([]byte, w*height*4)
	info := win.BITMAPINFO{BmiHeader: win.BITMAPINFOHEADER{BiSize: 40, BiWidth: int32(w), BiHeight: -int32(height), BiPlanes: 1, BiBitCount: 32, BiCompression: win.BI_RGB}}
	dc := win.GetDC(0)
	if dc == 0 {
		return discord.Upload{}, true, errors.New("could not access image converter")
	}
	defer win.ReleaseDC(0, dc)
	if win.GetDIBits(dc, h, 0, uint32(height), &pixels[0], &info, win.DIB_RGB_COLORS) == 0 {
		return discord.Upload{}, true, errors.New("could not convert clipboard image")
	}
	for i := 0; i < len(pixels); i += 4 {
		pixels[i], pixels[i+2] = pixels[i+2], pixels[i]
		pixels[i+3] = 255
	}
	img := &image.RGBA{Pix: pixels, Stride: w * 4, Rect: image.Rect(0, 0, w, height)}
	var out bytes.Buffer
	if e := png.Encode(&out, img); e != nil {
		return discord.Upload{}, true, e
	}
	if out.Len() > discord.MaxUploadBytes {
		return discord.Upload{}, true, errors.New("clipboard image exceeds the 10 MiB upload cap")
	}
	return discord.Upload{Name: "clipboard.png", Data: out.Bytes()}, true, nil
}
