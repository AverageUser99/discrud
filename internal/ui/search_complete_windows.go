//go:build windows

package ui

import (
	"sort"
	"strings"
	"sync"

	"discrud/internal/discord"
	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// Keep Go callbacks outside native refdata and remove them on widget disposal.
var searchCompletionFields sync.Map // HWND -> *searchCompletion

var searchCompletionCallback = windows.NewCallback(func(hwnd, msg, w, l, id, ref uintptr) uintptr {
	value, ok := searchCompletionFields.Load(hwnd)
	if ok {
		c := value.(*searchCompletion)
		switch uint32(msg) {
		case win.WM_GETDLGCODE:
			if w == win.VK_TAB && !walk.ShiftDown() && len(c.choices) > 0 && c.list.Visible() {
				result, _, _ := defHistorySubclass.Call(hwnd, msg, w, l)
				return result | 0x0002 // DLGC_WANTTAB
			}
		case win.WM_KEYDOWN:
			if w == win.VK_TAB && !walk.ShiftDown() && c.complete() {
				c.skipTab = true
				return 0
			}
		case win.WM_CHAR:
			if w == 9 && c.skipTab {
				c.skipTab = false
				return 0
			}
		case win.WM_NCDESTROY:
			searchCompletionFields.Delete(hwnd)
		}
	}
	result, _, _ := defHistorySubclass.Call(hwnd, msg, w, l)
	return result
})

type searchCompletion struct {
	field             *walk.LineEdit
	list              *walk.ListBox
	users             map[string]discord.User
	choices           []discord.User
	updating, skipTab bool
}

func (c *searchCompletion) update() {
	if c.updating || c.field == nil || c.list == nil {
		return
	}
	text := c.field.Text()
	c.choices = nil
	if strings.HasPrefix(text, "@") && !strings.ContainsAny(text, "\r\n\t<>") {
		query := strings.ToLower(strings.TrimPrefix(text, "@"))
		for _, u := range c.users {
			if u.ID != "" && strings.Contains(strings.ToLower(u.Name()+" "+u.Username), query) {
				c.choices = append(c.choices, u)
			}
		}
		sort.Slice(c.choices, func(i, j int) bool {
			a, b := strings.ToLower(c.choices[i].Name()+" "+c.choices[i].Username), strings.ToLower(c.choices[j].Name()+" "+c.choices[j].Username)
			if a == b {
				return c.choices[i].ID < c.choices[j].ID
			}
			return a < b
		})
		if len(c.choices) > 15 {
			c.choices = c.choices[:15]
		}
	}
	labels := make([]string, 0, len(c.choices))
	for _, u := range c.choices {
		labels = append(labels, "@"+u.Name()+" ("+u.Username+") · Tab to insert")
	}
	c.list.SetModel(labels)
	c.list.SetVisible(len(labels) > 0)
	if len(labels) > 0 {
		c.list.SetCurrentIndex(0)
	}
}

func (c *searchCompletion) complete() bool {
	if len(c.choices) == 0 || !c.list.Visible() {
		return false
	}
	i := c.list.CurrentIndex()
	if i < 0 || i >= len(c.choices) {
		i = 0
	}
	c.updating = true
	c.field.SetText("<@" + c.choices[i].ID + ">")
	c.updating = false
	c.choices = nil
	c.list.SetVisible(false)
	c.field.SetFocus()
	return true
}

func (c *searchCompletion) install() bool {
	hwnd := uintptr(c.field.Handle())
	searchCompletionFields.Store(hwnd, c)
	ok, _, _ := setHistorySubclass.Call(hwnd, searchCompletionCallback, 0xD160, 0)
	if ok == 0 {
		searchCompletionFields.Delete(hwnd)
		return false
	}
	c.field.Disposing().Attach(func() { searchCompletionFields.Delete(hwnd) })
	return true
}
