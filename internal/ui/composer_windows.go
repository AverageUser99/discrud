//go:build windows

package ui

import (
	"runtime"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var nextTabItem = windows.NewLazySystemDLL("user32.dll").NewProc("GetNextDlgTabItem")

type composer struct {
	walk.WidgetBase
	changed   walk.EventPublisher
	send      func()
	paste     func() bool
	complete  func() bool
	skipPaste bool
	height    int // desired outer height in native pixels
	ready     bool
}

func newComposer(parent walk.Container) (*composer, error) {
	c := &composer{}
	if err := walk.InitWidget(c, parent, "EDIT", win.WS_VISIBLE|win.WS_TABSTOP|win.ES_MULTILINE|win.ES_WANTRETURN|win.ES_AUTOVSCROLL|win.WS_VSCROLL, win.WS_EX_CLIENTEDGE); err != nil {
		return nil, err
	}
	c.SendMessage(win.EM_SETLIMITTEXT, 10000, 0)
	// Keep the text inset small so it doesn't waste the narrowest usable line.
	c.SendMessage(win.EM_SETMARGINS, 3, uintptr(win.MAKELONG(4, 4)))
	brush, err := walk.NewSolidColorBrush(walk.RGB(255, 255, 255))
	if err != nil {
		c.Dispose()
		return nil, err
	}
	c.SetBackground(brush)
	c.Disposing().Attach(brush.Dispose)
	_ = c.SetMinMaxSize(walk.Size{Width: 320, Height: 32}, walk.Size{Width: 0, Height: 160})
	c.ready = true
	c.updateHeight()
	return c, nil
}

// Walk suppresses WM_WINDOWPOSCHANGED's default handling unless a custom
// widget opts in. EDIT needs that handling to receive WM_SIZE and update its
// formatting rectangle; otherwise it wraps at its initial creation width.
func (c *composer) NeedsWmSize() bool { return true }

func (c *composer) Text() string {
	n := int(c.SendMessage(win.WM_GETTEXTLENGTH, 0, 0))
	b := make([]uint16, n+1)
	c.SendMessage(win.WM_GETTEXT, uintptr(len(b)), uintptr(unsafe.Pointer(&b[0])))
	return windows.UTF16ToString(b)
}
func (c *composer) SetText(text string) {
	p, _ := windows.UTF16PtrFromString(text)
	c.SendMessage(win.WM_SETTEXT, 0, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
	c.updateHeight()
}
func (c *composer) Selection() (int, int) {
	var start, end uint32
	c.SendMessage(win.EM_GETSEL, uintptr(unsafe.Pointer(&start)), uintptr(unsafe.Pointer(&end)))
	return int(start), int(end)
}
func (c *composer) Select(start, end int) { c.SendMessage(win.EM_SETSEL, uintptr(start), uintptr(end)) }
func (c *composer) Replace(text string) {
	p, _ := windows.UTF16PtrFromString(text)
	c.SendMessage(win.EM_REPLACESEL, 1, uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}
func (c *composer) WndProc(hwnd win.HWND, msg uint32, w, l uintptr) uintptr {
	switch msg {
	case win.WM_SIZE, win.WM_SETFONT:
		result := c.WidgetBase.WndProc(hwnd, msg, w, l)
		// EDIT must first update its wrapping rectangle/font before counting lines.
		c.updateHeight()
		return result
	case win.WM_GETDLGCODE:
		if w == win.VK_RETURN || w == win.VK_TAB {
			return win.DLGC_WANTALLKEYS
		}
		return win.DLGC_HASSETSEL | win.DLGC_WANTARROWS | win.DLGC_WANTCHARS
	case win.WM_COMMAND:
		if win.HIWORD(uint32(w)) == win.EN_CHANGE {
			c.updateHeight()
			c.changed.Publish()
		}
	case win.WM_KEYDOWN:
		c.skipPaste = false
		if w == win.VK_TAB {
			if c.complete != nil && c.complete() {
				return 0
			}
			root := win.GetAncestor(hwnd, win.GA_ROOT)
			next, _, _ := nextTabItem.Call(uintptr(root), uintptr(hwnd), uintptr(win.BoolToBOOL(walk.ShiftDown())))
			if next != 0 {
				win.SetFocus(win.HWND(next))
			}
			return 0
		}
		if w == uintptr(walk.KeyV) && walk.ControlDown() && c.paste != nil && c.paste() {
			c.skipPaste = true
			return 0
		}
		if w == uintptr(walk.KeyA) && walk.ControlDown() {
			c.Select(0, -1)
			return 0
		}
	case win.WM_CHAR:
		if w == 22 && c.skipPaste {
			c.skipPaste = false
			return 0
		}
		if w == 13 && !walk.ShiftDown() {
			if c.send != nil {
				c.send()
			}
			return 0
		}
		if w == 9 {
			return 0
		}
	}
	return c.WidgetBase.WndProc(hwnd, msg, w, l)
}

// Use the native wrapped line count so typing, pasting and window resizes all
// agree with the EDIT control. Stop growing at 160 DIP; longer drafts scroll.
func (c *composer) updateHeight() {
	if !c.ready {
		return
	}
	dpi := c.DPI()
	lineHeight := walk.IntFrom96DPI(18, dpi)
	if dc := win.GetDC(c.Handle()); dc != 0 {
		font := win.HGDIOBJ(c.SendMessage(win.WM_GETFONT, 0, 0))
		old := win.SelectObject(dc, font)
		var metrics win.TEXTMETRIC
		if win.GetTextMetrics(dc, &metrics) {
			lineHeight = int(metrics.TmHeight)
		}
		win.SelectObject(dc, old)
		win.ReleaseDC(c.Handle(), dc)
	}
	lines := max(1, int(c.SendMessage(win.EM_GETLINECOUNT, 0, 0)))
	height := min(walk.IntFrom96DPI(160, dpi), max(walk.IntFrom96DPI(32, dpi), lines*lineHeight+walk.IntFrom96DPI(8, dpi)))
	if height != c.height {
		c.height = height
		c.RequestLayout()
	}
}

func (c *composer) CreateLayoutItem(*walk.LayoutContext) walk.LayoutItem {
	return &composerLayoutItem{size: walk.Size{Width: walk.IntFrom96DPI(320, c.DPI()), Height: c.height}}
}

type composerLayoutItem struct {
	walk.LayoutItemBase
	size walk.Size
}

func (*composerLayoutItem) LayoutFlags() walk.LayoutFlags {
	return walk.ShrinkableHorz | walk.GrowableHorz | walk.GreedyHorz
}
func (li *composerLayoutItem) IdealSize() walk.Size { return li.size }
func (li *composerLayoutItem) MinSize() walk.Size   { return li.size }
