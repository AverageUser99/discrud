//go:build windows

package ui

import (
	"sync"
	"time"

	"discrud/internal/storage"
	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var historySubclassDLL = windows.NewLazySystemDLL("comctl32.dll")
var setHistorySubclass = historySubclassDLL.NewProc("SetWindowSubclass")
var defHistorySubclass = historySubclassDLL.NewProc("DefSubclassProc")
var lockHistoryWindow = windows.NewLazySystemDLL("user32.dll").NewProc("LockWindowUpdate")
var historyApps sync.Map // HWND -> *App, removed at WM_NCDESTROY; no Go pointer in Win32 refdata.
var historyCallback = windows.NewCallback(func(hwnd, msg, wparam, lparam, id, ref uintptr) uintptr {
	// Never hold an in-progress paint transaction across real user input.
	if value, ok := historyApps.Load(hwnd); ok {
		if uint32(msg) == win.WM_MOUSEWHEEL || uint32(msg) == win.WM_KEYDOWN || uint32(msg) == win.WM_VSCROLL && uint16(wparam) != win.SB_THUMBPOSITION {
			value.(*App).endHistoryPaint()
		}
	}
	result, _, _ := defHistorySubclass.Call(hwnd, msg, wparam, lparam)
	if value, ok := historyApps.Load(hwnd); ok {
		a := value.(*App)
		if uint32(msg) == win.WM_NCDESTROY {
			historyApps.Delete(hwnd)
			return result
		}
		if a.history == nil || uintptr(a.history.Handle()) != hwnd {
			return result
		}
		if uint32(msg) == win.WM_MOUSEWHEEL || uint32(msg) == win.WM_KEYDOWN || uint32(msg) == win.WM_VSCROLL && uint16(wparam) != win.SB_THUMBPOSITION {
			a.restorePosition = nil
			a.historyInputID++
		}
		switch uint32(msg) {
		case win.WM_VSCROLL, win.WM_MOUSEWHEEL, win.WM_SIZE, win.WM_KEYDOWN:
			a.deferPreviews()
			a.maybeLoadOlder()
		case win.WM_NCDESTROY:
			historyApps.Delete(hwnd)
		}
	}
	return result
})

// Hide intermediate native layouts while an anchored prepend settles. Unlike
// WM_SETREDRAW, LockWindowUpdate does not change WS_VISIBLE, which Walk uses
// to decide whether children participate in layout. The lock is bounded and
// always released on input, channel switches, disposal, or timeout.
func (a *App) beginHistoryPaint() {
	if a.history == nil || a.history.IsDisposed() || !a.history.Visible() {
		return
	}
	hwnd := a.history.Handle()
	if a.historyPaintLock == hwnd {
		return
	}
	a.endHistoryPaint()
	ok, _, _ := lockHistoryWindow.Call(uintptr(hwnd))
	if ok == 0 {
		return
	}
	a.historyPaintLock = hwnd
	a.historyPaintEpoch++
	epoch := a.historyPaintEpoch
	time.AfterFunc(150*time.Millisecond, func() {
		a.post(func() {
			if a.historyPaintEpoch == epoch {
				a.endHistoryPaint()
			}
		})
	})
}

func (a *App) endHistoryPaint() {
	if a.historyPaintLock == 0 {
		return
	}
	hwnd := a.historyPaintLock
	a.historyPaintLock = 0
	a.historyPaintEpoch++
	lockHistoryWindow.Call(0)
	win.RedrawWindow(hwnd, nil, 0, win.RDW_INVALIDATE|win.RDW_ALLCHILDREN|win.RDW_UPDATENOW)
}

func (a *App) observeHistory() {
	hwnd := uintptr(a.history.Handle())
	historyApps.Store(hwnd, a)
	ok, _, _ := setHistorySubclass.Call(hwnd, historyCallback, 0xD15C, 0)
	if ok == 0 {
		historyApps.Delete(hwnd)
	}
}
func (a *App) captureReadingPosition() storage.ReadingPosition {
	if a.restorePosition != nil && a.restorePosition.Channel == a.current.ID {
		return *a.restorePosition
	}
	p := storage.ReadingPosition{Channel: a.current.ID, FollowLatest: a.atLatest && a.nearBottom()}
	if p.FollowLatest {
		return p
	}
	pos := int(a.scrollInfo().NPos)
	for _, id := range a.renderedIDs {
		card := a.cards[id]
		if card == nil || card.IsDisposed() {
			continue
		}
		b := card.BoundsPixels()
		if b.Y+b.Height > pos {
			p.Anchor = id
			p.Offset = pos - b.Y
			if p.Offset < 0 {
				p.Offset = 0
			}
			break
		}
	}
	return p
}
func (a *App) deferPreviews() {
	a.refreshAnimations()
	if a.closed.Load() {
		return
	}
	if a.previewTimer != nil {
		a.previewTimer.Stop()
	}
	gen, renderID := a.generation, a.renderID
	a.previewTimer = time.AfterFunc(70*time.Millisecond, func() {
		a.post(func() {
			if a.generation == gen && a.renderID == renderID && a.historySettled {
				a.loadVisiblePreviews()
			}
		})
	})
}
func (a *App) latestMessages() {
	if a.loading {
		if a.cancelLoad != nil {
			a.cancelLoad()
		}
		a.loadID++
		a.loading = false
	}
	a.restorePosition = nil
	a.forceLatest = true
	a.loadMessages(false)
}

// The preview's reserved native control stays in the layout while the image is
// loading, so filling it doesn't move the reading position.
type pendingPreview struct {
	view         walk.Widget
	animation    *animatedGIF
	attachmentID string
	start        func()
	started      bool
}

func (a *App) loadVisiblePreviews() {
	defer a.refreshAnimations()
	if a.closed.Load() || a.current.ID == "" || !a.window.Visible() || win.IsIconic(a.window.Handle()) {
		return
	}
	var viewport win.RECT
	if !win.GetWindowRect(a.history.Handle(), &viewport) {
		return
	}
	// Reclaim decoded animations before attempting new visible ones. Static
	// thumbnails and the animation's small poster remain in the cached feed.
	for _, p := range a.previews {
		if v := p.animation; v != nil && !v.IsDisposed() && !a.gifVisible(v) && (v.loading || v.hasAnimation()) {
			a.releasePreview(v)
		}
	}
	for _, p := range a.previews {
		if p.started || p.view.IsDisposed() {
			continue
		}
		var bounds win.RECT
		if !win.GetWindowRect(p.view.Handle(), &bounds) {
			continue
		}
		if bounds.Bottom >= viewport.Top-80 && bounds.Top <= viewport.Bottom+80 {
			if v := p.animation; v != nil {
				if !a.gifVisible(v) || v.waitingMemory > a.gifBudget.Available() {
					continue
				}
			}
			p.started = true
			p.start()
		}
	}
}
