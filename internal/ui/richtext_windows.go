//go:build windows

package ui

import (
	"runtime"
	"unsafe"

	"discrud/internal/presentation"
	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

var richDLL = windows.NewLazySystemDLL("msftedit.dll")

type richText struct {
	walk.WidgetBase
	height int
	open   func(string)
}

func newRichText(parent walk.Container, text string, open func(string)) (*richText, error) {
	if err := richDLL.Load(); err != nil {
		return nil, err
	}
	r := &richText{height: 24, open: open}
	if err := walk.InitWidget(r, parent, "RICHEDIT50W", win.WS_VISIBLE|win.WS_TABSTOP|win.ES_MULTILINE|win.ES_READONLY|win.ES_NOHIDESEL, 0); err != nil {
		return nil, err
	}
	r.SendMessage(win.EM_SETBKGNDCOLOR, 0, uintptr(appBackgroundColor))
	// Avoid RichEdit's relatively wide default inset; the message card already
	// supplies a consistent, compact outer padding.
	r.SendMessage(win.EM_SETMARGINS, 3, uintptr(win.MAKELONG(4, 4)))
	r.SendMessage(win.EM_SETEVENTMASK, 0, win.ENM_LINK|win.ENM_REQUESTRESIZE)
	r.SendMessage(win.EM_AUTOURLDETECT, 1, 0)
	r.SendMessage(win.EM_EXLIMITTEXT, 0, 256*1024)
	config := win.SETTEXTEX{Codepage: 65001}
	b := append([]byte(presentation.RTF(text)), 0)
	r.SendMessage(win.EM_SETTEXTEX, uintptr(unsafe.Pointer(&config)), uintptr(unsafe.Pointer(&b[0])))
	runtime.KeepAlive(b)
	r.SendMessage(win.EM_REQUESTRESIZE, 0, 0)
	return r, nil
}
func (r *richText) NeedsWmSize() bool { return true }
func (r *richText) WndProc(hwnd win.HWND, msg uint32, w, l uintptr) uintptr {
	switch msg {
	case win.WM_MOUSEWHEEL:
		// RichEdit otherwise consumes the wheel even when all its text already fits.
		for parent := r.Parent(); parent != nil; {
			if scroll, ok := parent.(*walk.ScrollView); ok {
				return scroll.SendMessage(msg, w, l)
			}
			widget, ok := parent.(walk.Widget)
			if !ok {
				break
			}
			parent = widget.Parent()
		}
	case win.WM_SIZE:
		result := r.WidgetBase.WndProc(hwnd, msg, w, l)
		r.SendMessage(win.EM_REQUESTRESIZE, 0, 0)
		return result
	case win.WM_NOTIFY:
		hdr := (*win.NMHDR)(unsafe.Pointer(l))
		switch hdr.Code {
		case win.EN_REQUESTRESIZE:
			req := (*win.REQRESIZE)(unsafe.Pointer(l))
			height := int(req.Rc.Bottom-req.Rc.Top) + 4
			if height < 20 {
				height = 20
			}
			if height != r.height {
				r.height = height
				r.RequestLayout()
			}
		case win.EN_LINK:
			link := (*win.ENLINK)(unsafe.Pointer(l))
			if link.Msg == win.WM_LBUTTONUP {
				n := int(link.Chrg.CpMax - link.Chrg.CpMin)
				if n > 0 && n < 8192 {
					buffer := make([]uint16, n+1)
					tr := win.TEXTRANGE{Chrg: link.Chrg, LpstrText: &buffer[0]}
					r.SendMessage(win.EM_GETTEXTRANGE, 0, uintptr(unsafe.Pointer(&tr)))
					if r.open != nil {
						r.open(windows.UTF16ToString(buffer))
					}
				}
			}
		}
	}
	return r.WidgetBase.WndProc(hwnd, msg, w, l)
}
func (r *richText) CreateLayoutItem(*walk.LayoutContext) walk.LayoutItem {
	return &richLayout{height: r.height}
}

type richLayout struct {
	walk.LayoutItemBase
	height int
}

func (l *richLayout) LayoutFlags() walk.LayoutFlags {
	return walk.ShrinkableHorz | walk.GrowableHorz | walk.GreedyHorz
}
func (l *richLayout) IdealSize() walk.Size { return walk.Size{Width: 200, Height: l.height} }
func (l *richLayout) MinSize() walk.Size   { return walk.Size{Width: 180, Height: l.height} }
