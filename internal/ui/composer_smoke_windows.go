//go:build windows

package ui

import (
	"strings"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// Exercise actual typing and the native formatting rectangle, including resize.
// WM_SETTEXT alone does not reproduce the edit control's typing/wrapping path.
func (c *composer) smokeWrapping() map[string]any {
	report := map[string]any{}
	text := c.Text()
	start, end := c.Selection()
	bounds := c.BoundsPixels()
	defer func() {
		_ = c.SetBoundsPixels(bounds)
		c.SetText(text)
		c.Select(start, end)
	}()
	for _, sample := range []struct {
		name  string
		width int
	}{{"narrow", 360}, {"wide", 720}, {"restored", bounds.Width}} {
		next := bounds
		next.Width = sample.width
		_ = c.SetBoundsPixels(next)
		c.SetText("")
		for i := 0; i < 40; i++ {
			c.SendMessage(win.WM_CHAR, uintptr('i'), 0)
		}
		var rect win.RECT
		c.SendMessage(win.EM_GETRECT, 0, uintptr(unsafe.Pointer(&rect)))
		report["composer_"+sample.name+"_format_width"] = rect.Right - rect.Left
		report["composer_"+sample.name+"_client_width"] = c.ClientBoundsPixels().Width
		report["composer_"+sample.name+"_typed_lines"] = c.SendMessage(win.EM_GETLINECOUNT, 0, 0)
	}
	c.SetText("")
	compact := c.height
	report["composer_compact"] = compact == walk.IntFrom96DPI(32, c.DPI())
	for i := 0; i < 3; i++ {
		c.SendMessage(win.WM_CHAR, uintptr('a'), 0)
		// Shift+Enter inserts a newline; WM_CHAR is otherwise intercepted to send.
		c.Replace("\r\n")
	}
	report["composer_grows_for_newlines"] = c.height > compact
	c.SetText(strings.Repeat("line\r\n", 50))
	report["composer_growth_capped"] = c.height == walk.IntFrom96DPI(160, c.DPI())
	c.SetText("")
	report["composer_shrinks_after_clear"] = c.height == compact
	c.SetText(strings.Repeat("word ", 120))
	narrow := bounds
	narrow.Width = 360
	_ = c.SetBoundsPixels(narrow)
	narrowHeight := c.height
	wide := bounds
	wide.Width = 1000
	_ = c.SetBoundsPixels(wide)
	report["composer_reflows_on_resize"] = c.height < narrowHeight && c.height > compact
	return report
}
