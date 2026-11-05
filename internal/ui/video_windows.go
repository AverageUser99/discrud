//go:build windows

package ui

import (
	"discrud/internal/nativevideo"
	"github.com/lxn/win"
	"time"
)

func (v *animatedGIF) hasAnimation() bool { return v.player != nil || v.video != nil }

// Release expensive decoded playback independently of the small poster bitmap.
func (v *animatedGIF) releasePlayback() {
	if v.video != nil {
		v.video.Close()
		v.video = nil
	}
	v.player = nil
	v.videoFrame = nativevideo.Frame{}
	v.videoRecycle = nil
	v.videoWaiting = false
	v.due = time.Time{}
}

func (v *animatedGIF) clearMedia() {
	v.releasePlayback()
	v.clearBitmap()
}

func (v *animatedGIF) clearBitmap() {
	v.pixels = nil
	v.xmap, v.ymap = nil, nil
	if v.dc != 0 {
		win.SelectObject(v.dc, v.old)
		win.DeleteDC(v.dc)
		v.dc = 0
	}
	if v.bitmap != 0 {
		win.DeleteObject(win.HGDIOBJ(v.bitmap))
		v.bitmap = 0
	}
}
func (a *App) installVideo(v *animatedGIF, s *nativevideo.Stream) error {
	v.video = s
	v.videoFrame = s.First
	if err := v.installBitmap(v.videoFrame.Image); err != nil {
		return err
	}
	s.SetNotify(func() {
		a.post(func() {
			if !v.IsDisposed() && v.video == s {
				a.refreshAnimations()
			}
		})
	})
	return nil
}

// A decode is requested only when its frame deadline is due AND the clip is
// hovered/visible. While waiting there is no polling timer; completion wakes the
// existing scheduler. One in-flight result may finish after the pointer leaves.
func (a *App) advanceVideo(v *animatedGIF, now time.Time) time.Time {
	if v.videoWaiting {
		frame, ready := v.video.Take()
		if !ready {
			return time.Time{}
		}
		v.videoWaiting = false
		if frame.Err != nil {
			delete(a.animations, v)
			v.clearMedia()
			v.lease.close()
			v.status = "Video unavailable · Click to retry, or open original link"
			v.SetToolTipText(frame.Err.Error())
			v.Invalidate()
			return time.Time{}
		}
		v.videoRecycle = v.videoFrame.Image
		v.videoFrame = frame
		v.advances++
		v.upload()
		v.due = now.Add(frame.Delay)
	}
	if v.due.IsZero() {
		v.due = now.Add(v.videoFrame.Delay)
	}
	if !now.Before(v.due) {
		if v.video.Request(v.videoRecycle) {
			v.videoRecycle = nil
			v.videoWaiting = true
			v.due = time.Time{}
		}
		return time.Time{}
	}
	return v.due
}
