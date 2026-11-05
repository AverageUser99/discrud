//go:build windows

package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"discrud/internal/storage"

	"discrud/internal/discord"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

func (a *App) selectedGuild() string {
	i := a.servers.CurrentIndex()
	if i > 0 && i <= len(a.guilds) {
		return a.guilds[i-1].ID
	}
	return ""
}
func (a *App) serverChanged() {
	if a.backend == nil || a.servers == nil || a.navUpdating {
		return
	}
	g := a.selectedGuild()
	a.updateNav()
	if g == "" {
		return
	}
	if a.guildLoading[g] {
		return
	}
	if a.guildLoaded[g] && storage.Fresh(a.channelsAt[g], a.metadataTTL()) {
		a.loadGuildRoles(g)
		return
	}
	a.guildLoading[g] = true
	backend := a.backend
	a.task(func(ctx context.Context) func() {
		channels, err := backend.Channels(ctx, g)
		return func() {
			delete(a.guildLoading, g)
			if err != nil {
				if a.current.ID == "" {
					a.showError(err)
				}
				return
			}
			for id, c := range a.channels {
				if c.GuildID == g {
					delete(a.channels, id)
				}
			}
			for _, c := range channels {
				c.GuildID = g
				a.rememberChannel(c)
			}
			a.guildLoaded[g] = true
			a.channelsAt[g] = time.Now()
			a.startupDirty = true
			if fresh, ok := a.channels[a.current.ID]; ok {
				a.current = fresh
				a.updateHeader()
			}
			a.updateNav()
			a.updateSuggestions()
			if a.selectedGuild() == g && a.current.ID == "" {
				if a.settings.LastOwner == a.me.ID && a.settings.LastGuild == g {
					if c, ok := a.channels[a.settings.LastChannel]; ok && c.Supported() {
						a.openChannel(c)
					}
				}
				if a.current.ID == "" {
					for _, row := range a.rows {
						if row.channel.Supported() && row.channel.ID != "" {
							a.openChannel(row.channel)
							break
						}
					}
				}
			}
			a.loadGuildRoles(g)
		}
	})
}
func (a *App) updateNav() {
	if a.conversations == nil || a.servers == nil || a.navUpdating {
		return
	}
	a.navUpdating = true
	defer func() { a.navUpdating = false }()
	g := a.selectedGuild()
	query := strings.ToLower(a.filter.Text())
	var list []discord.Channel
	categories := map[string]discord.Channel{}
	for _, c := range a.channels {
		if c.GuildID != g {
			continue
		}
		if c.Type == 4 {
			categories[c.ID] = c
			continue
		}
		if c.Supported() {
			list = append(list, c)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		ca, cb := categories[a.ParentID], categories[b.ParentID]
		if ca.Position != cb.Position {
			return ca.Position < cb.Position
		}
		if a.ParentID != b.ParentID {
			return a.ParentID < b.ParentID
		}
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		return strings.ToLower(a.Label()) < strings.ToLower(b.Label())
	})
	var labels []string
	a.rows = nil
	lastCategory := "!"
	selected := -1
	for _, c := range list {
		if query != "" && !strings.Contains(strings.ToLower(c.Label()), query) {
			continue
		}
		if g != "" && query == "" && c.ParentID != lastCategory {
			lastCategory = c.ParentID
			if cat, ok := categories[c.ParentID]; ok {
				labels = append(labels, "— "+cat.Name+" —")
				a.rows = append(a.rows, navRow{label: cat.Name})
			}
		}
		name := c.Label()
		if g != "" {
			name = "# " + name
		}
		if n := a.mentions[c.ID]; n > 0 {
			name = fmt.Sprintf("%s  (@%d)", name, n)
		}
		if c.ID == a.current.ID {
			selected = len(labels)
		}
		labels = append(labels, name)
		a.rows = append(a.rows, navRow{label: name, channel: c})
	}
	a.conversations.SetModel(labels)
	a.conversations.SetCurrentIndex(selected)
}
func (a *App) openSelected() {
	if a.navUpdating || a.sending {
		return
	}
	i := a.conversations.CurrentIndex()
	if i < 0 || i >= len(a.rows) {
		return
	}
	c := a.rows[i].channel
	if c.ID != "" && c.Supported() {
		a.openChannel(c)
	}
}
func (a *App) restoreLastChannel() {
	if a.settings.LastGuild != "" {
		for i, g := range a.guilds {
			if g.ID == a.settings.LastGuild {
				a.servers.SetCurrentIndex(i + 1)
				return
			}
		}
	}
	if len(a.guilds) > 0 {
		a.servers.SetCurrentIndex(1)
	}
}
func (a *App) quickSwitch() {
	if a.backend == nil || a.sending {
		return
	}
	type target struct {
		label   string
		channel discord.Channel
		server  int
	}
	var all []target
	for i, g := range a.guilds {
		all = append(all, target{label: g.Name + " / browse channels", server: i + 1})
	}
	for _, c := range a.channels {
		if !c.Supported() {
			continue
		}
		name := c.Label()
		if c.GuildID != "" {
			for _, g := range a.guilds {
				if g.ID == c.GuildID {
					name = g.Name + " / #" + name
					break
				}
			}
		} else {
			name = "DM / " + name
		}
		if a.mentions[c.ID] > 0 {
			name += "  (@)"
		}
		all = append(all, target{label: name, channel: c})
	}
	sort.Slice(all, func(i, j int) bool { return strings.ToLower(all[i].label) < strings.ToLower(all[j].label) })
	var dlg *walk.Dialog
	var query *walk.LineEdit
	var list *walk.ListBox
	var visible []target
	var chosen *target
	var open, cancel *walk.PushButton
	refresh := func() {
		if query == nil || list == nil {
			return
		}
		needle := strings.ToLower(query.Text())
		visible = nil
		var labels []string
		for _, t := range all {
			if strings.Contains(strings.ToLower(t.label), needle) {
				visible = append(visible, t)
				labels = append(labels, t.label)
			}
		}
		list.SetModel(labels)
		if len(labels) > 0 {
			list.SetCurrentIndex(0)
		}
	}
	accept := func() {
		i := list.CurrentIndex()
		if i >= 0 && i < len(visible) {
			copy := visible[i]
			chosen = &copy
			dlg.Accept()
		}
	}
	err := (Dialog{AssignTo: &dlg, Title: "Quick switcher", Size: Size{Width: 580, Height: 440}, MinSize: Size{Width: 400, Height: 300}, DefaultButton: &open, CancelButton: &cancel, Layout: VBox{}, Children: []Widget{
		LineEdit{AssignTo: &query, CueBanner: "Find a server, loaded channel, or DM", OnTextChanged: refresh},
		TextLabel{Text: "Choose a server to load its channels. Visited channels are searchable directly."},
		ListBox{AssignTo: &list, StretchFactor: 1, OnItemActivated: accept}, Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{HSpacer{}, PushButton{AssignTo: &open, Text: "Open", OnClicked: accept}, PushButton{AssignTo: &cancel, Text: "Cancel", OnClicked: func() { dlg.Cancel() }}}},
	}}).Create(a.window)
	if err != nil {
		a.showError(err)
		return
	}
	refresh()
	dlg.Run()
	dlg.Dispose()
	if chosen != nil {
		if chosen.server > 0 {
			a.drawer.SetVisible(true)
			a.servers.SetCurrentIndex(chosen.server)
			a.filter.SetText("")
			a.updateNav()
		} else {
			a.openChannel(chosen.channel)
			a.drawer.SetVisible(false)
		}
	}
}
