//go:build windows

package ui

import (
	"context"
	"errors"
	"image"
	"sync"
	"time"
	"unsafe"

	"discrud/internal/discord"
	"discrud/internal/gifplayer"
	"discrud/internal/gifsource"
	"discrud/internal/nativevideo"
	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// lease is shared only with the decoder worker. It makes disposal/cancellation
// race-safe even if a card disappears before a queued UI completion is delivered.
type gifLease struct {
	mu       sync.Mutex
	budget   *gifplayer.Budget
	n        int
	closed   bool
	decoding bool
}

func (l *gifLease) reserve(n int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.n != 0 || !l.budget.Reserve(n) {
		return false
	}
	l.n = n
	l.decoding = true
	return true
}
func (l *gifLease) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	l.closed = true
	if !l.decoding {
		l.release()
	}
}
func (l *gifLease) release() {
	if l.n != 0 {
		l.budget.Release(l.n)
		l.n = 0
	}
}
func (l *gifLease) decoded() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.decoding = false
	if l.closed {
		l.release()
		return true
	}
	return false
}

// Observe native enter/leave without a timer polling the cursor. Keep Go
// references in a map, never in Win32 subclass refdata.
var gifHoverViews sync.Map // HWND -> *gifHoverTarget; removed on disposal

type gifHoverTarget struct {
	app  *App
	view *animatedGIF
}

var gifHoverCallback = windows.NewCallback(func(hwnd, msg, w, l, id, ref uintptr) uintptr {
	result, _, _ := defHistorySubclass.Call(hwnd, msg, w, l)
	value, ok := gifHoverViews.Load(hwnd)
	if !ok {
		return result
	}
	target := value.(*gifHoverTarget)
	v := target.view
	switch uint32(msg) {
	case win.WM_MOUSEMOVE:
		size := v.imageSizePixels()
		x, y := int(int16(win.LOWORD(uint32(l)))), int(int16(win.HIWORD(uint32(l))))
		inside := x >= 0 && y >= 0 && x < size.Width && y < size.Height
		if inside != v.hovered {
			if inside {
				track := win.TRACKMOUSEEVENT{CbSize: uint32(unsafe.Sizeof(win.TRACKMOUSEEVENT{})), DwFlags: win.TME_LEAVE, HwndTrack: win.HWND(hwnd)}
				v.hovered = win.TrackMouseEvent(&track)
			} else {
				v.hovered = false
			}
			target.app.refreshAnimations()
		}
	case win.WM_MOUSELEAVE:
		v.hovered = false
		target.app.refreshAnimations()
	case win.WM_NCDESTROY:
		gifHoverViews.Delete(hwnd)
	}
	return result
})

type animatedGIF struct {
	*walk.CustomWidget
	history          *walk.ScrollView
	player           *gifplayer.Player
	video            *nativevideo.Stream
	videoFrame       nativevideo.Frame
	videoRecycle     *image.RGBA
	videoWaiting     bool
	lease            *gifLease
	dc               win.HDC
	bitmap           win.HBITMAP
	old              win.HGDIOBJ
	pixels           []byte // native DIB memory, valid until bitmap is deleted
	width, height    int
	xmap, ymap       []int
	due              time.Time
	status           string
	hovered          bool
	loading          bool
	cancelLoad       context.CancelFunc
	waitingMemory    int
	retry            func()
	paints, advances int
}

func (a *App) newAnimatedGIF(parent walk.Container, history *walk.ScrollView) (*animatedGIF, error) {
	v := &animatedGIF{history: history, lease: &gifLease{budget: &a.gifBudget}, status: "GIF · Loading…"}
	widget, err := walk.NewCustomWidgetPixels(parent, 0, func(canvas *walk.Canvas, _ walk.Rectangle) error {
		bounds := v.ClientBoundsPixels()
		if err := canvas.FillRectanglePixels(a.background, bounds); err != nil {
			return err
		}
		if v.bitmap == 0 {
			return canvas.DrawTextPixels(v.status, v.Font(), walk.RGB(35, 35, 35), bounds, walk.TextWordbreak)
		}
		size := v.imageSizePixels()
		w, h := size.Width, size.Height
		if w > 0 && h > 0 {
			win.SetStretchBltMode(canvas.HDC(), win.COLORONCOLOR)
			win.StretchBlt(canvas.HDC(), 0, 0, int32(w), int32(h), v.dc, 0, 0, int32(v.width), int32(v.height), win.SRCCOPY)
			v.paints++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	v.CustomWidget = widget
	v.SetPaintMode(walk.PaintNoErase)
	v.SetInvalidatesOnResize(true)
	v.MouseDown().Attach(func(_, _ int, button walk.MouseButton) {
		if button == walk.LeftButton && !v.loading && !v.hasAnimation() && v.retry != nil {
			v.retry()
		}
	})
	v.Disposing().Attach(func() {
		gifHoverViews.Delete(uintptr(v.Handle()))
		delete(a.animations, v)
		if v.cancelLoad != nil {
			v.cancelLoad()
		}
		v.clearMedia()
		v.lease.close()
		a.refreshAnimations()
	})
	hwnd := uintptr(v.Handle())
	gifHoverViews.Store(hwnd, &gifHoverTarget{app: a, view: v})
	if ok, _, _ := setHistorySubclass.Call(hwnd, gifHoverCallback, 0xD15F, 0); ok == 0 {
		gifHoverViews.Delete(hwnd)
		v.Dispose()
		return nil, errors.New("could not observe GIF hover events")
	}
	return v, nil
}

// Use the painted image, not the row's unused space, as the hover target.
func (v *animatedGIF) imageSizePixels() walk.Size {
	bounds := v.ClientBoundsPixels()
	w, h := bounds.Width, bounds.Height
	if v.width > 0 && v.height > 0 {
		if w*v.height > h*v.width {
			w = h * v.width / v.height
		} else {
			h = w * v.height / v.width
		}
	}
	return walk.Size{Width: w, Height: h}
}

func (v *animatedGIF) install(clip *gifplayer.Clip) error {
	v.player = gifplayer.New(clip)
	return v.installBitmap(v.player.Image())
}
func (v *animatedGIF) installBitmap(picture *image.RGBA) error {
	v.clearBitmap()
	size := picture.Bounds().Size()
	v.width, v.height = size.X, size.Y
	if v.width > 360 {
		v.height = max(1, v.height*360/v.width)
		v.width = 360
	}
	if v.height > 240 {
		v.width = max(1, v.width*240/v.height)
		v.height = 240
	}
	info := win.BITMAPINFOHEADER{BiSize: uint32(unsafe.Sizeof(win.BITMAPINFOHEADER{})), BiWidth: int32(v.width), BiHeight: -int32(v.height), BiPlanes: 1, BiBitCount: 32, BiCompression: win.BI_RGB}
	var bits unsafe.Pointer
	v.bitmap = win.CreateDIBSection(0, &info, win.DIB_RGB_COLORS, &bits, 0, 0)
	if v.bitmap == 0 {
		return errors.New("could not allocate GIF display bitmap")
	}
	v.dc = win.CreateCompatibleDC(0)
	if v.dc == 0 {
		win.DeleteObject(win.HGDIOBJ(v.bitmap))
		v.bitmap = 0
		return errors.New("could not allocate GIF display context")
	}
	v.old = win.SelectObject(v.dc, win.HGDIOBJ(v.bitmap))
	v.pixels = unsafe.Slice((*byte)(bits), v.width*v.height*4)
	v.xmap, v.ymap = make([]int, v.width), make([]int, v.height)
	for x := range v.xmap {
		v.xmap[x] = (x * size.X / v.width) * 4
	}
	for y := range v.ymap {
		v.ymap[y] = (y * size.Y / v.height) * size.X * 4
	}
	v.upload()
	return nil
}

// No per-frame bitmap creation, image conversion objects or layout requests.
func (v *animatedGIF) upload() {
	win.GdiFlush() // complete prior batched reads before writing the same DIB
	picture := v.videoFrame.Image
	if picture == nil {
		picture = v.player.Image()
	}
	src := picture.Pix
	bg := [3]uint32{uint32(appBackgroundColor.R()), uint32(appBackgroundColor.G()), uint32(appBackgroundColor.B())}
	for y, sy := range v.ymap {
		row := v.pixels[y*v.width*4:]
		for x, sx := range v.xmap {
			p := src[sy+sx : sy+sx+4]
			inv := uint32(255 - p[3])
			d := row[x*4 : x*4+4]
			d[0] = byte(uint32(p[2]) + bg[2]*inv/255)
			d[1] = byte(uint32(p[1]) + bg[1]*inv/255)
			d[2] = byte(uint32(p[0]) + bg[0]*inv/255)
			d[3] = 255
		}
	}
	v.Invalidate()
}

func (a *App) startGIF(v *animatedGIF, att discord.Attachment, ctx context.Context, gen int) {
	load := func(ctx context.Context) ([]byte, error) {
		if (isMP4(att) || (isGIF(att) && !allowedMediaURL(att.URL) && gifsource.AllowedMedia(att.URL))) && !(a.opt.Demo && att.URL == "demo://sample.mp4") {
			return a.gifResolver.LoadMedia(ctx, []gifsource.Hint{{URL: att.URL, ProxyURL: att.ProxyURL, Video: isMP4(att), GIF: isGIF(att)}})
		}
		return gifBytes(ctx, att, a.opt.Demo)
	}
	if v.retry == nil {
		v.retry = func() { a.startGIFLoader(v, load, ctx, gen) }
	}
	a.startGIFLoader(v, load, ctx, gen)
}

// Attachments and resolved sharing links use exactly the same decode, memory,
// native bitmap and hover-playback path. Retry is explicit, never an idle poll.
func (a *App) startGIFLoader(v *animatedGIF, load func(context.Context) ([]byte, error), ctx context.Context, gen int) {
	if v.loading || v.hasAnimation() || v.IsDisposed() || ctx.Err() != nil {
		return
	}
	v.loading = true
	v.waitingMemory = 0
	v.status = "GIF · Loading…"
	v.Invalidate()
	v.lease.close()
	lease := &gifLease{budget: &a.gifBudget}
	v.lease = lease
	jobCtx, cancel := context.WithCancel(ctx)
	v.cancelLoad = cancel
	go func() {
		select {
		case a.mediaSem <- struct{}{}:
		case <-jobCtx.Done():
			return
		}
		defer func() { <-a.mediaSem }()
		data, err := load(jobCtx)
		var clip *gifplayer.Clip
		var video *nativevideo.Stream
		waitingMemory := 0
		if err == nil {
			var cost int
			isVideo := nativevideo.IsMP4(data)
			if isVideo {
				_, err = nativevideo.Inspect(data)
				cost = nativevideo.MemoryReservation
			} else {
				cost, err = gifplayer.Inspect(data)
			}
			if err == nil && !lease.reserve(cost) {
				if jobCtx.Err() == nil {
					waitingMemory = cost
				}
				err = errors.New("animation memory budget in use")
			}
			if err == nil {
				if isVideo {
					video, err = nativevideo.Open(jobCtx, data)
					if err != nil {
						lease.decoded()
					} else {
						go func(s *nativevideo.Stream) {
							<-s.Done()
							lease.decoded()
							if !a.closed.Load() {
								a.post(a.deferPreviews)
							}
						}(video)
					}
				} else {
					defer func() {
						if lease.decoded() && !a.closed.Load() {
							a.post(a.deferPreviews)
						}
					}()
					clip, err = gifplayer.Decode(data)
				}
			}
		}
		if err != nil {
			lease.close()
		}
		if jobCtx.Err() != nil || a.closed.Load() {
			if video != nil {
				video.Close()
			}
			lease.close()
			return
		}
		a.post(func() {
			if gen != a.generation || jobCtx.Err() != nil || v.IsDisposed() || v.lease != lease {
				if video != nil {
					video.Close()
				}
				lease.close()
				return
			}
			v.loading = false
			v.cancelLoad = nil
			if err == nil {
				if video != nil {
					err = a.installVideo(v, video)
				} else {
					err = v.install(clip)
				}
			}
			if err != nil {
				if video != nil {
					video.Close()
				}
				v.releasePlayback()
				lease.close()
				if waitingMemory > 0 {
					v.waitingMemory = waitingMemory
					v.status = "Waiting for animation memory · scroll or click to retry"
					for _, p := range a.previews {
						if p.animation == v {
							p.started = false
							break
						}
					}
					if a.gifBudget.Available() >= waitingMemory {
						a.deferPreviews()
					}
				} else {
					v.status = "GIF unavailable · Click to retry, or open original link"
				}
				v.SetToolTipText(err.Error())
				v.Invalidate()
				return
			}
			a.animations[v] = true
			if !a.gifVisible(v) {
				for _, p := range a.previews {
					if p.animation == v {
						a.releasePreview(v)
						break
					}
				}
			}
			a.refreshAnimations()
		})
	}()
}

// Drop decoded frames and active streams once a preview leaves the viewport.
// The small native poster remains, and the original preview can be reloaded.
func (a *App) releasePreview(v *animatedGIF) {
	if v == nil || v.IsDisposed() {
		return
	}
	if v.cancelLoad != nil {
		v.cancelLoad()
		v.cancelLoad = nil
	}
	v.loading = false
	v.releasePlayback()
	v.lease.close()
	delete(a.animations, v)
	for _, p := range a.previews {
		if p.animation == v {
			p.started = false
			break
		}
	}
}

func (a *App) gifVisible(v *animatedGIF) bool {
	if a.closed.Load() || v.IsDisposed() || v.history != a.history || !a.window.Visible() || win.IsIconic(a.window.Handle()) || !win.IsWindowVisible(v.Handle()) {
		return false
	}
	var viewport, bounds win.RECT
	return win.GetWindowRect(a.history.Handle(), &viewport) && win.GetWindowRect(v.Handle(), &bounds) && bounds.Bottom > viewport.Top && bounds.Top < viewport.Bottom && bounds.Right > viewport.Left && bounds.Left < viewport.Right
}

// A single one-shot timer services all players. No hidden-window polling and no
// timer per GIF. Hover, scroll, resize, channel switch and show/hide wake it.
// Mouse leave pauses on the current frame; re-enter resumes without decoding.
// Limit active animation to four previews; additional ones keep a still frame.
func (a *App) refreshAnimations() {
	a.animationEpoch++
	if a.animationTimer != nil {
		a.animationTimer.Stop()
		a.animationTimer = nil
	}
	if a.closed.Load() {
		return
	}
	now := time.Now()
	var next time.Time
	for v := range a.animations {
		if !v.hovered || !a.gifVisible(v) {
			v.due = time.Time{}
		}
	}
	active := 0
	// Native preview order is stable, unlike map iteration. This keeps the
	// four-player limit from randomly changing its recipients on every frame.
	for _, preview := range a.previews {
		v := preview.animation
		if v == nil {
			continue
		}
		if !v.hasAnimation() || (v.player != nil && v.player.Done()) || !v.hovered || !a.gifVisible(v) || active >= 4 {
			v.due = time.Time{}
			continue
		}
		active++
		if v.video != nil {
			deadline := a.advanceVideo(v, now)
			if !deadline.IsZero() && (next.IsZero() || deadline.Before(next)) {
				next = deadline
			}
			continue
		}
		if v.due.IsZero() {
			v.due = now.Add(v.player.Delay())
		}
		if !now.Before(v.due) {
			if v.player.Step() {
				v.advances++
				v.upload()
			}
			if v.player.Done() {
				v.due = time.Time{}
				continue
			}
			v.due = now.Add(v.player.Delay())
		}
		if next.IsZero() || v.due.Before(next) {
			next = v.due
		}
	}
	if !next.IsZero() {
		epoch := a.animationEpoch
		a.animationTimer = time.AfterFunc(max(time.Millisecond, time.Until(next)), func() {
			a.post(func() {
				if epoch == a.animationEpoch {
					a.refreshAnimations()
				}
			})
		})
	}
}
