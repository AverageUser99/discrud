//go:build windows

package ui

import (
	"bytes"
	"context"
	"errors"
	"image"
	"math"
	"net/url"
	"sync"

	"discrud/internal/discord"
	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Download the original, not the thumbnail; keep the same host restrictions and
// preflight the decoder before allocating a full-sized image.
func fullImage(ctx context.Context, att discord.Attachment, demo bool) (image.Image, error) {
	if demo && att.URL == "demo://sample.png" {
		return thumbnail(ctx, att, true)
	}
	raw := att.URL
	if !allowedMediaURL(raw) {
		raw = att.ProxyURL
	}
	if !allowedMediaURL(raw) {
		return nil, errors.New("image host is not an approved Discord media host")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Del("width")
	q.Del("height")
	u.RawQuery = q.Encode()
	data, err := downloadMedia(ctx, u.String(), 20*1024*1024)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 16*1024*1024 {
		return nil, errors.New("image exceeds the 16 megapixel viewer limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

type imageViewer struct {
	dlg               *walk.Dialog
	canvas            *walk.CustomWidget
	bitmap            *walk.Bitmap
	brush             *walk.SolidColorBrush
	width, height     int
	scale, panX, panY float64
	dragging          bool
	lastX, lastY      int
	message           string
}

func (v *imageViewer) rect() walk.Rectangle {
	b := v.canvas.ClientBoundsPixels()
	w, h := int(math.Round(float64(v.width)*v.scale)), int(math.Round(float64(v.height)*v.scale))
	return walk.Rectangle{X: (b.Width-w)/2 + int(v.panX), Y: (b.Height-h)/2 + int(v.panY), Width: w, Height: h}
}

var imageViewerWindows sync.Map // HWND -> *imageViewer; no Go pointers in native refdata
var imageViewerCallback = windows.NewCallback(func(hwnd, msg, w, l, id, ref uintptr) uintptr {
	if value, ok := imageViewerWindows.Load(hwnd); ok {
		v := value.(*imageViewer)
		switch uint32(msg) {
		case win.WM_GETDLGCODE:
			if w == win.VK_ESCAPE {
				return win.DLGC_WANTALLKEYS
			}
		case win.WM_KEYDOWN:
			if w == win.VK_ESCAPE {
				win.PostMessage(v.dlg.Handle(), win.WM_CLOSE, 0, 0)
				return 0
			}
		case win.WM_MOUSEWHEEL:
			if v.bitmap == nil {
				return 0
			}
			point := win.POINT{X: int32(int16(win.LOWORD(uint32(l)))), Y: int32(int16(win.HIWORD(uint32(l))))}
			win.ScreenToClient(win.HWND(hwnd), &point)
			old := v.rect()
			factor := math.Pow(1.2, float64(int16(win.HIWORD(uint32(w))))/120)
			next := math.Max(0.02, math.Min(8, v.scale*factor))
			factor = next / v.scale
			v.scale = next
			b := v.canvas.ClientBoundsPixels()
			newW, newH := float64(v.width)*next, float64(v.height)*next
			v.panX = float64(point.X) - (float64(point.X)-float64(old.X))*factor - (float64(b.Width)-newW)/2
			v.panY = float64(point.Y) - (float64(point.Y)-float64(old.Y))*factor - (float64(b.Height)-newH)/2
			v.canvas.Invalidate()
			return 0
		case win.WM_LBUTTONDOWN:
			x, y := int(int16(win.LOWORD(uint32(l)))), int(int16(win.HIWORD(uint32(l))))
			r := v.rect()
			if v.bitmap == nil || x < r.X || y < r.Y || x >= r.X+r.Width || y >= r.Y+r.Height {
				win.PostMessage(v.dlg.Handle(), win.WM_CLOSE, 0, 0)
				return 0
			}
			v.dragging, v.lastX, v.lastY = true, x, y
			win.SetCapture(win.HWND(hwnd))
			return 0
		case win.WM_MOUSEMOVE:
			if v.dragging {
				x, y := int(int16(win.LOWORD(uint32(l)))), int(int16(win.HIWORD(uint32(l))))
				v.panX += float64(x - v.lastX)
				v.panY += float64(y - v.lastY)
				v.lastX, v.lastY = x, y
				v.canvas.Invalidate()
				return 0
			}
		case win.WM_LBUTTONUP:
			if v.dragging {
				v.dragging = false
				win.ReleaseCapture()
				return 0
			}
		case win.WM_CAPTURECHANGED:
			v.dragging = false
		case win.WM_NCDESTROY:
			imageViewerWindows.Delete(hwnd)
		}
	}
	result, _, _ := defHistorySubclass.Call(hwnd, msg, w, l)
	return result
})

func (a *App) openImageViewer(att discord.Attachment) {
	if a.session == nil {
		return
	}
	ctx, cancel := context.WithCancel(a.session)
	dlg, err := walk.NewDialog(a.window)
	if err != nil {
		cancel()
		a.showError(err)
		return
	}
	v := &imageViewer{dlg: dlg, message: "Loading image…", scale: 1}
	brush, err := walk.NewSolidColorBrush(walk.RGB(30, 30, 30))
	if err != nil {
		cancel()
		dlg.Dispose()
		a.showError(err)
		return
	}
	v.brush = brush
	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{})
	layout.SetSpacing(0)
	dlg.SetLayout(layout)
	canvas, err := walk.NewCustomWidgetPixels(dlg, win.WS_TABSTOP, func(c *walk.Canvas, _ walk.Rectangle) error {
		bounds := v.canvas.ClientBoundsPixels()
		if err := c.FillRectanglePixels(v.brush, bounds); err != nil {
			return err
		}
		if v.bitmap == nil {
			return c.DrawTextPixels(v.message, v.canvas.Font(), walk.RGB(240, 240, 240), bounds, walk.TextCenter|walk.TextVCenter|walk.TextSingleLine)
		}
		return c.DrawImageStretchedPixels(v.bitmap, v.rect())
	})
	if err != nil {
		cancel()
		brush.Dispose()
		dlg.Dispose()
		a.showError(err)
		return
	}
	v.canvas = canvas
	canvas.SetPaintMode(walk.PaintNoErase)
	canvas.SetInvalidatesOnResize(true)
	canvas.SetToolTipText("Mouse wheel: zoom · drag: pan · Esc or click outside: close")
	hwnd := uintptr(canvas.Handle())
	imageViewerWindows.Store(hwnd, v)
	if ok, _, _ := setHistorySubclass.Call(hwnd, imageViewerCallback, 0xD161, 0); ok == 0 {
		imageViewerWindows.Delete(hwnd)
		cancel()
		brush.Dispose()
		dlg.Dispose()
		a.showError(errors.New("could not initialize image viewer"))
		return
	}
	dlg.Disposing().Attach(func() {
		cancel()
		imageViewerWindows.Delete(hwnd)
		if v.bitmap != nil {
			v.bitmap.Dispose()
			v.bitmap = nil
		}
		brush.Dispose()
	})
	// Keep the modeless viewer open when the main window refreshes or gains focus.
	owner := a.window.BoundsPixels()
	dlg.SetSizePixels(walk.Size{Width: max(400, owner.Width-80), Height: max(300, owner.Height-80)})
	dlg.SetTitle("Image viewer · Esc to close")
	dlg.Show()
	canvas.SetFocus()
	go func() {
		select {
		case a.mediaSem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-a.mediaSem }()
		img, err := fullImage(ctx, att, a.opt.Demo)
		a.post(func() {
			if ctx.Err() != nil || dlg.IsDisposed() || canvas.IsDisposed() {
				return
			}
			if err != nil {
				v.message = "Image unavailable: " + err.Error()
				canvas.Invalidate()
				return
			}
			bitmap, err := walk.NewBitmapFromImage(img)
			if err != nil {
				v.message = "Could not display image"
				canvas.Invalidate()
				return
			}
			v.bitmap = bitmap
			size := img.Bounds().Size()
			v.width, v.height = size.X, size.Y
			b := canvas.ClientBoundsPixels()
			if b.Width > 0 && b.Height > 0 {
				v.scale = math.Min(1, math.Min(float64(b.Width)/float64(v.width), float64(b.Height)/float64(v.height)))
			}
			canvas.Invalidate()
		})
	}()
}
