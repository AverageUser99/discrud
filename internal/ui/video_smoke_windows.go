//go:build windows

package ui

import (
	"discrud/internal/discord"
	"encoding/json"
	"github.com/lxn/win"
	"time"
)

// Real native H.264 -> RGB -> reused DIB painting, through the same provider
// resolver and hover scheduler as live GIFV. The HTTP fixture is entirely local.
func (a *App) smokeVideo() map[string]any {
	report := map[string]any{}
	onUI := func(fn func()) {
		done := make(chan struct{})
		a.post(func() { defer close(done); fn() })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			panic("video smoke UI timeout")
		}
	}
	var cursor win.POINT
	var before int
	onUI(func() {
		win.GetCursorPos(&cursor)
		before = a.gifBudget.Used()
		a.atLatest = true
		msg := discord.Message{ID: "999999999999999997", ChannelID: a.current.ID, Author: discord.User{ID: "offline", Username: "Offline MP4 fixture"}, Content: "Silent GIFV proxy fixture", Timestamp: time.Now(), Embeds: []discord.Embed{{Type: "gifv", URL: "https://example.com/native-gifv", Thumbnail: &discord.EmbedMedia{URL: "demo://sample.gif", Width: 64, Height: 48}, Video: &discord.EmbedMedia{URL: "https://example.com/original.mp4", ProxyURL: "https://static.klipy.com/offline/loop.mp4", Width: 64, Height: 48}}}}
		data, _ := json.Marshal(msg)
		a.onEvent(discord.Event{Type: "MESSAGE_CREATE", Data: data})
	})
	var v *animatedGIF
	for i := 0; i < 60; i++ {
		onUI(func() {
			a.loadVisiblePreviews()
			for _, p := range a.previews {
				if p.animation != nil && p.animation.video != nil {
					v = p.animation
					break
				}
			}
		})
		if v != nil {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if v == nil {
		report["mp4_native_loaded"] = false
		return report
	}
	var streamDone <-chan struct{}
	onUI(func() { streamDone = v.video.Done() })
	var bitmap win.HBITMAP
	var advances int
	hover := func() {
		win.SetWindowPos(a.window.Handle(), win.HWND_TOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
		var b, viewport win.RECT
		win.GetWindowRect(v.Handle(), &b)
		win.GetWindowRect(a.history.Handle(), &viewport)
		win.SetCursorPos(max(b.Left, viewport.Left)+8, max(b.Top, viewport.Top)+8)
		v.SendMessage(win.WM_MOUSEMOVE, 0, uintptr(win.MAKELONG(8, 8)))
	}
	onUI(func() {
		report["mp4_native_loaded"] = v.player == nil && v.videoFrame.Image != nil
		bitmap = v.bitmap
		hover()
		advances = v.advances
	})
	time.Sleep(1400 * time.Millisecond)
	onUI(func() {
		report["mp4_hover_loop_and_bitmap_reuse"] = v.advances > advances && v.videoFrame.Loops > 0 && v.bitmap == bitmap && v.paints > 0
		var b win.RECT
		win.GetWindowRect(a.editor.Handle(), &b)
		win.SetCursorPos(b.Left+8, b.Top+8)
		v.SendMessage(win.WM_MOUSELEAVE, 0, 0)
		advances = v.advances
	})
	time.Sleep(220 * time.Millisecond)
	onUI(func() { report["mp4_mouse_leave_paused"] = v.advances == advances && v.due.IsZero(); hover() })
	time.Sleep(240 * time.Millisecond)
	onUI(func() {
		report["mp4_hover_resumes"] = v.advances > advances
		b := v.BoundsPixels()
		b.Y += 10000
		_ = v.SetBoundsPixels(b)
		a.refreshAnimations()
		advances = v.advances
	})
	time.Sleep(180 * time.Millisecond)
	onUI(func() {
		report["mp4_offscreen_paused"] = v.advances == advances && v.due.IsZero()
		b := v.BoundsPixels()
		b.Y -= 10000
		_ = v.SetBoundsPixels(b)
		hover()
		a.window.Hide()
		advances = v.advances
	})
	time.Sleep(180 * time.Millisecond)
	onUI(func() {
		report["mp4_hidden_paused"] = v.advances == advances && v.due.IsZero()
		a.window.Show()
		data, _ := json.Marshal(map[string]string{"id": "999999999999999997", "channel_id": a.current.ID})
		a.onEvent(discord.Event{Type: "MESSAGE_DELETE", Data: data})
		win.SetCursorPos(cursor.X, cursor.Y)
		win.SetWindowPos(a.window.Handle(), win.HWND_NOTOPMOST, 0, 0, 0, 0, win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOACTIVATE)
	})
	select {
	case <-streamDone:
	case <-time.After(3 * time.Second):
		report["mp4_native_cleanup"] = false
		return report
	}
	// The reservation is released after the worker has released COM/native state.
	for i := 0; i < 30; i++ {
		ready := false
		onUI(func() { ready = a.gifBudget.Used() == before })
		if ready {
			report["mp4_native_cleanup"] = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return report
}
