//go:build windows

package ui

import (
	"fmt"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func (a *App) showSettings() {
	var dlg *walk.Dialog
	var minutes *walk.NumberEdit
	var tray *walk.CheckBox
	var save, cancel *walk.PushButton
	cacheInfo := "No message cache is open."
	if a.cache != nil {
		cacheInfo = fmt.Sprintf("%d cached messages · 50 MiB file limit · 2,000 retained messages per conversation · 200 visible", a.cache.Count())
	}
	_, err := (Dialog{AssignTo: &dlg, Title: "Discrud settings", Size: Size{Width: 620, Height: 430}, MinSize: Size{Width: 500, Height: 380}, DefaultButton: &save, CancelButton: &cancel, Layout: VBox{Spacing: 12}, Children: []Widget{
		Label{Text: "Message retention", Font: Font{Family: "ComicShannsMono Nerd Font Mono", PointSize: 11, Bold: true}},
		TextLabel{Text: "Cache retention in minutes (1–43,200). Default: 1,440 minutes / 24 hours. Expiration starts when a message is fetched, not when it was originally sent."},
		NumberEdit{AssignTo: &minutes, MinValue: 1, MaxValue: 43200, Value: a.settings.CacheTTLHours * 60, Decimals: 0, Increment: 60},
		TextLabel{Text: cacheInfo},
		CheckBox{AssignTo: &tray, Text: "Closing the window minimizes to the system tray", Checked: a.settings.CloseToTray, Enabled: a.tray != nil},
		TextLabel{Text: "Messages are cached as plain text/metadata under your Windows profile; small thumbnails and startup metadata are also cached locally. Drafts are not saved. Your session credential is protected separately by Windows Credential Manager. No telemetry or automatic updates."},
		PushButton{Text: "Clear local caches", OnClicked: func() {
			if a.cache == nil {
				return
			}
			if a.thumbnails != nil {
				if err := a.thumbnails.Clear(); err != nil {
					a.showError(err)
					return
				}
			}
			if err := a.startupSaver.Clear(a.dir); err != nil {
				a.showError(err)
				return
			}
			a.clearChannelViews()
			if err := a.cache.Clear(); err != nil {
				a.showError(err)
			} else {
				a.suppressStartupSave = true
				a.startupDirty = false
				a.status.SetText("Local message, startup and thumbnail caches cleared. The current view remains; new network activity may refill the cache.")
			}
		}},
		VSpacer{}, Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &save, Text: "Save", OnClicked: func() {
			a.clearChannelViews()
			a.settings.CacheTTLHours = minutes.Value() / 60
			a.settings.CloseToTray = tray.Checked()
			if a.cache != nil {
				a.cache.SetTTL(time.Duration(a.settings.CacheTTLHours * float64(time.Hour)))
				if err := a.cache.Flush(); err != nil {
					a.showError(err)
					return
				}
			}
			if a.thumbnails != nil {
				a.thumbnails.SetTTL(time.Duration(a.settings.CacheTTLHours * float64(time.Hour)))
			}
			a.saveSettings()
			dlg.Accept()
		}}, PushButton{AssignTo: &cancel, Text: "Cancel", OnClicked: func() { dlg.Cancel() }}}},
	}}).Run(a.window)
	if err != nil {
		a.showError(err)
	}
}
