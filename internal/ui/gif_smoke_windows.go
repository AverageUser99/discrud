//go:build windows

package ui

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"discrud/internal/demo"
	"discrud/internal/gifplayer"
	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// Called from the smoke coordinator goroutine; every UI observation/mutation is
// marshalled to the native thread. Actual timer wakes, paints and GDI lifetimes
// are exercised, not just manual calls to the compositor.
func (a *App) smokeGIFs() map[string]any {
	report := map[string]any{}
	onUI := func(fn func()) {
		done := make(chan struct{})
		a.post(func() { defer close(done); fn() })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			panic("GIF smoke timed out waiting for native UI")
		}
	}
	var originalCursor win.POINT
	onUI(func() { win.GetCursorPos(&originalCursor) })
	defer onUI(func() {
		win.SetCursorPos(originalCursor.X, originalCursor.Y)
		win.SetWindowPos(a.window.Handle(), win.HWND_NOTOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
	})
	moveAway := func() {
		var bounds win.RECT
		win.GetWindowRect(a.editor.Handle(), &bounds)
		win.SetCursorPos(bounds.Left+8, bounds.Top+8)
	}
	onUI(moveAway)
	var v *animatedGIF
	for i := 0; i < 30; i++ {
		onUI(func() {
			a.loadVisiblePreviews()
			for _, p := range a.previews {
				if p.animation != nil && p.animation.player != nil {
					v = p.animation
					break
				}
			}
		})
		if v != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if v == nil {
		report["gif_native_loaded"] = false
		return report
	}
	hover := func() {
		a.window.Activate()
		win.SetWindowPos(a.window.Handle(), win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
		var bounds, viewport win.RECT
		win.GetWindowRect(v.Handle(), &bounds)
		win.GetWindowRect(a.history.Handle(), &viewport)
		win.SetCursorPos(max(bounds.Left, viewport.Left)+8, max(bounds.Top, viewport.Top)+8)
		v.SendMessage(win.WM_MOUSEMOVE, 0, uintptr(win.MAKELONG(8, 8)))
	}
	onUI(func() {
		for _, m := range a.messages {
			if strings.Contains(m.Content, "https://klipy.com/gifs/scared-63") && len(m.Embeds) == 0 {
				for _, p := range a.previews {
					if p.animation == v && strings.HasPrefix(p.attachmentID, "link-") {
						report["gif_link_without_embed_resolved"] = true
					}
				}
			}
		}
	})
	var bitmap win.HBITMAP
	var advances int
	var bounds walk.Rectangle
	onUI(func() {
		report["gif_native_loaded"] = true
		bitmap, advances, bounds = v.bitmap, v.advances, v.BoundsPixels()
	})
	time.Sleep(180 * time.Millisecond)
	onUI(func() {
		report["gif_idle_until_hover"] = v.advances == advances && a.animationTimer == nil
		hover()
		advances = v.advances
	})
	time.Sleep(300 * time.Millisecond)
	onUI(func() {
		report["gif_native_timer_and_paint"] = v.advances > advances && v.paints > 0
		report["gif_reuses_bitmap_and_layout"] = v.bitmap == bitmap && v.BoundsPixels() == bounds
		moveAway()
		v.SendMessage(win.WM_MOUSELEAVE, 0, 0)
		advances = v.advances
	})
	time.Sleep(180 * time.Millisecond)
	onUI(func() {
		report["gif_mouse_leave_paused"] = v.advances == advances && v.due.IsZero() && a.animationTimer == nil
		hover()
		advances = v.advances
	})
	time.Sleep(200 * time.Millisecond)
	onUI(func() {
		report["gif_hover_reentry_resumes"] = v.advances > advances && v.bitmap == bitmap
		offscreen := v.BoundsPixels()
		offscreen.Y += 10000
		_ = v.SetBoundsPixels(offscreen)
		a.refreshAnimations()
		advances = v.advances
	})
	time.Sleep(200 * time.Millisecond)
	onUI(func() {
		report["gif_offscreen_paused"] = !a.gifVisible(v) && v.advances == advances && v.due.IsZero()
		_ = v.SetBoundsPixels(bounds)
		hover()
		a.refreshAnimations()
		advances = v.advances
	})
	time.Sleep(200 * time.Millisecond)
	onUI(func() {
		report["gif_resumes_when_visible"] = v.advances > advances
		a.window.Hide()
		advances = v.advances
	})
	time.Sleep(180 * time.Millisecond)
	onUI(func() {
		report["gif_hidden_window_paused"] = v.advances == advances && a.animationTimer == nil
		a.window.Show()
		win.ShowWindow(a.window.Handle(), win.SW_MINIMIZE)
		a.refreshAnimations()
		advances = v.advances
	})
	time.Sleep(180 * time.Millisecond)
	onUI(func() {
		report["gif_minimized_paused"] = v.advances == advances && a.animationTimer == nil
		win.ShowWindow(a.window.Handle(), win.SW_RESTORE)
		a.refreshAnimations()
		// Isolated widget validates native cleanup and exact budget reclamation.
		before := a.gifBudget.Used()
		temp, err := a.newAnimatedGIF(a.editHost, a.history)
		if err != nil {
			return
		}
		temp.SetVisible(false)
		clip, err := gifplayer.Decode(demo.GIF())
		if err != nil {
			temp.Dispose()
			return
		}
		if !temp.lease.reserve(clip.Memory()) {
			temp.Dispose()
			return
		}
		temp.lease.decoded()
		if err = temp.install(clip); err != nil {
			temp.Dispose()
			return
		}
		temp.Dispose()
		report["gif_native_cleanup"] = temp.bitmap == 0 && temp.dc == 0 && temp.player == nil && a.gifBudget.Used() == before
	})
	// Retry an actual failed native preview through its click handler, reusing
	// the same widget while replacing the closed allocation lease safely.
	var retryView *animatedGIF
	var beforeRetry int
	var attempts atomic.Int32
	onUI(func() {
		beforeRetry = a.gifBudget.Used()
		retryView, _ = a.newAnimatedGIF(a.editHost, a.history)
		if retryView == nil {
			return
		}
		retryView.SetVisible(false)
		load := func(context.Context) ([]byte, error) {
			if attempts.Add(1) == 1 {
				return nil, errors.New("offline synthetic first attempt failed")
			}
			return demo.GIF(), nil
		}
		retryView.retry = func() { a.startGIFLoader(retryView, load, a.mediaCtx, a.generation) }
		retryView.retry()
	})
	if retryView != nil {
		wait := func(loaded bool) bool {
			for i := 0; i < 30; i++ {
				ready := false
				onUI(func() { ready = !retryView.loading && (retryView.player != nil) == loaded })
				if ready {
					return true
				}
				time.Sleep(20 * time.Millisecond)
			}
			return false
		}
		failed := wait(false)
		onUI(func() {
			retryView.SendMessage(win.WM_LBUTTONDOWN, win.MK_LBUTTON, 0)
			retryView.SendMessage(win.WM_LBUTTONUP, 0, 0)
		})
		succeeded := wait(true)
		onUI(func() {
			retryView.Dispose()
			report["gif_click_retry_recovers"] = failed && succeeded && attempts.Load() == 2 && a.gifBudget.Used() == beforeRetry
		})
	}
	return report
}
