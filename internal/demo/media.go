package demo

import (
	"bytes"
	_ "embed"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"io"
	"net/http"
)

//go:embed loop.mp4
var mp4Fixture []byte

// MP4 is our generated silent H.264 color-bar fixture, not third-party media.
func MP4() []byte { return mp4Fixture }

// GIFTransport exercises webpage -> metadata -> GIF resolution entirely in
// memory. It cannot access the network or represent Klipy's actual live media.
type GIFTransport struct{}

func (GIFTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	contentType := "text/html"
	switch req.URL.Hostname() + req.URL.Path {
	case "klipy.com/gifs/scared-63":
		body = []byte(`<meta property="og:image" content="https://static.klipy.com/offline/scared-63.gif"><meta property="og:image:type" content="image/gif">`)
	case "klipy.com/gifs/native-video-fixture":
		body = []byte(`<meta property="og:video" content="https://static.klipy.com/offline/loop.mp4">`)
	case "static.klipy.com/offline/loop.mp4":
		body = MP4()
		contentType = "video/mp4"
	case "static.klipy.com/offline/scared-63.gif":
		body = GIF()
		contentType = "image/gif"
	default:
		return nil, errors.New("offline demo refuses external media requests")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: req}, nil
}

// GIF is a tiny generated animation for offline UI tests, with no external assets.
func GIF() []byte {
	palette := color.Palette{color.RGBA{235, 70, 60, 255}, color.RGBA{60, 190, 100, 255}, color.RGBA{65, 110, 230, 255}}
	g := &gif.GIF{Config: image.Config{ColorModel: palette, Width: 120, Height: 60}, LoopCount: 0}
	for n := 0; n < 3; n++ {
		f := image.NewPaletted(image.Rect(0, 0, 120, 60), palette)
		for y := 0; y < 60; y++ {
			for x := 0; x < 120; x++ {
				f.SetColorIndex(x, y, uint8((x/40+n)%3))
			}
		}
		g.Image = append(g.Image, f)
		g.Delay = append(g.Delay, 8)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	var out bytes.Buffer
	if err := gif.EncodeAll(&out, g); err != nil {
		panic(err)
	}
	return out.Bytes()
}
