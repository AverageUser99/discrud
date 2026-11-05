//go:build windows

package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"discrud/internal/discord"
	"discrud/internal/netdebug"
	"discrud/internal/presentation"
	"github.com/lxn/walk"
	"github.com/lxn/win"
)

func (a *App) openChannel(c discord.Channel) {
	if a.sending || c.ID == "" || !c.Supported() || c.ID == a.current.ID {
		return
	}
	restored, err := a.switchChannelView(c.ID)
	if err != nil {
		a.showError(err)
		return
	}
	if a.restorePosition != nil && a.restorePosition.Channel != c.ID {
		a.restorePosition = nil
	}
	if a.cancelLoad != nil {
		a.cancelLoad()
	}
	a.loadID++
	a.loading = false
	a.current = c
	a.revisions = map[string]int{}
	a.revision = 0
	a.messages = nil
	a.atLatest = true
	a.olderDone = false
	a.historyRetryAt = time.Time{}
	a.forceLatest, a.readyRefreshPending = false, false
	if restored != nil {
		a.messages = restored.messages
		a.atLatest, a.olderDone = restored.atLatest, restored.olderDone
		a.revisions, a.revision = restored.revisions, restored.revision
		position := restored.position
		a.restorePosition = &position
	}
	a.clearComposer()
	a.clearError()
	a.mentions[c.ID] = 0
	a.updateHeader()
	a.updateNav()
	a.drawer.SetVisible(false)
	a.backend.Subscribe(c.GuildID, c.ID)
	a.settings.LastChannel = c.ID
	a.settings.LastGuild = c.GuildID
	a.settings.LastOwner = a.me.ID
	a.startupDirty = true
	anchor := ""
	if a.restorePosition != nil {
		anchor = a.restorePosition.Anchor
	}
	if restored == nil && a.cache != nil {
		cached := a.cache.Get(c.ID)
		contiguous, ok := a.cache.CachedPage(c.ID, anchor)
		if !ok {
			contiguous = cached
		} // Migrate legacy caches on the next REST refresh.
		a.messages = presentation.CachedWindow(contiguous, anchor, 30)
		if len(a.messages) > 0 && len(cached) > 0 {
			a.atLatest = a.messages[len(a.messages)-1].ID == cached[len(cached)-1].ID
		}
	}
	a.showingCached = len(a.messages) > 0
	a.updateHeader()
	a.rememberUsers(a.messages)
	a.renderMessages(a.restorePosition == nil || a.restorePosition.FollowLatest, anchor)
	if len(a.messages) > 0 && !a.cachedShown {
		a.cachedShown = true
		netdebug.Startup("cached_messages", a.startedAt, len(a.messages))
	}
	if a.gatewayEpoch == 0 && len(a.messages) > 0 && !a.opt.Demo {
		// One authoritative refresh after READY closes the startup event gap.
		// If Gateway cannot connect, allow REST-only use after a bounded delay.
		a.status.SetText("Cached messages · Syncing...")
		gen, ch, id := a.generation, c.ID, a.loadID
		time.AfterFunc(3*time.Second, func() {
			a.post(func() {
				if gen == a.generation && ch == a.current.ID && id == a.loadID && a.gatewayEpoch == 0 && !a.loading {
					a.loadMessages(false)
				}
			})
		})
	} else {
		a.loadMessages(false)
	}
	a.saveSettings()
	a.editor.SetFocus()
}
func (a *App) updateHeader() {
	name := a.current.Label()
	if a.current.GuildID != "" {
		name = "# " + name
	}
	if a.showingCached {
		name += " · cached · syncing"
	}
	if !a.atLatest {
		name += " · reading older history"
	}
	a.header.SetText(name)
	if n := lenNonzero(a.mentions); n > 0 {
		a.window.SetTitle(fmt.Sprintf("Discrud · %d conversations with mentions", n))
	} else {
		a.window.SetTitle("Discrud")
	}
}
func (a *App) rememberUsers(messages []discord.Message) {
	for _, m := range messages {
		if m.Author.ID != "" {
			a.users[m.Author.ID] = m.Author
		}
		for _, u := range m.Mentions {
			a.users[u.ID] = u
		}
		if m.ReferencedMessage != nil {
			a.users[m.ReferencedMessage.Author.ID] = m.ReferencedMessage.Author
		}
	}
}
func (a *App) loadMessages(older bool) {
	if a.backend == nil || a.current.ID == "" || a.loading {
		return
	}
	before, anchor := "", ""
	if older && len(a.messages) > 0 {
		if a.olderDone {
			return
		}
		before = a.messages[0].ID
	}
	if older && before == "" {
		return
	}
	if a.cancelLoad != nil {
		a.cancelLoad()
	}
	ctx, cancel := context.WithCancel(a.session)
	a.cancelLoad = cancel
	a.loadID++
	id, gen, ch := a.loadID, a.generation, a.current.ID
	backend, cache, guild := a.backend, a.cache, a.current.GuildID
	startVersions := map[string]int{}
	for k, v := range a.revisions {
		startVersions[k] = v
	}
	requestEpoch := a.gatewayEpoch
	follow := a.restorePosition == nil && a.atLatest && a.nearBottom() || a.restorePosition != nil && a.restorePosition.FollowLatest
	preserve := !older && !a.forceLatest && len(a.messages) > 0
	if preserve {
		if a.restorePosition == nil {
			position := a.captureReadingPosition()
			a.restorePosition = &position
		}
	}
	a.forceLatest = false
	a.loading = true
	if len(a.messages) > 0 {
		a.status.SetText("Cached messages · Syncing...")
	} else {
		a.status.SetText("Loading messages...")
	}
	go func() {
		var messages []discord.Message
		var err error
		hit, end := false, false
		if older && cache != nil {
			messages, hit, end = cache.Before(ch, before)
		}
		if !hit {
			messages, err = backend.Messages(ctx, ch, before)
			end = len(messages) < 50
		}
		a.post(func() {
			if gen != a.generation || id != a.loadID || ch != a.current.ID {
				return
			}
			a.loading = false
			if err != nil {
				if ctx.Err() == nil {
					a.historyRetryAt = time.Now().Add(2 * time.Second)
					var api *discord.APIError
					if errors.As(err, &api) && (api.Status == 403 || api.Status == 404) {
						if cache != nil {
							cache.ReconcileLatest(ch, nil, nil)
						}
						a.messages = nil
						a.showingCached = false
						a.restorePosition = nil
						a.renderMessages(false, "")
						a.updateHeader()
					}
					a.showError(err)
				}
				return
			}
			if a.failed == nil && a.gatewayFailure == nil {
				a.errorText.SetVisible(false)
			}
			var fetched []discord.Message
			for _, m := range messages {
				if m.GuildID == "" {
					m.GuildID = guild
				}
				if a.revisions[m.ID] > startVersions[m.ID] {
					continue
				}
				fetched = append(fetched, m)
			}
			if cache != nil && !hit {
				if !older {
					protected := map[string]bool{}
					for id, version := range a.revisions {
						if version > startVersions[id] {
							protected[id] = true
						}
					}
					cache.ReconcileLatest(ch, messages, protected)
				}
				cache.Put(fetched)
				cache.RememberPage(ch, before, messages)
			}
			// Capture at completion: scrolling is allowed while REST is in flight.
			if older || preserve {
				position := a.captureReadingPosition()
				a.restorePosition = &position
				anchor = position.Anchor
				if !older {
					follow = position.FollowLatest
				}
			}
			if older {
				// Fetch 50 at a time, but reveal at most 20 native cards per insertion.
				// The proven cached prefix supplies the next reveal without another GET.
				visible := presentation.OlderPageWindow(fetched, 20)
				a.messages = presentation.Merge(a.messages, visible, true)
				a.olderDone = end && len(visible) == len(fetched)
				if len(a.messages) > 200 {
					a.messages = a.messages[:200]
					a.atLatest = false
				}
			} else {
				// Keep only the latest page plus events received during the HTTP request.
				var during []discord.Message
				for _, m := range a.messages {
					if a.revisions[m.ID] > startVersions[m.ID] {
						during = append(during, m)
					}
				}
				if preserve && !follow {
					a.messages = presentation.ReconcileHistoryWindow(a.messages, fetched, during)
				} else {
					keepHistory := preserve && presentation.MessagesOverlap(a.messages, fetched)
					a.messages = presentation.ReconcileRecent(a.messages, fetched, during, keepHistory)
				}
				if len(a.messages) > 200 {
					if preserve && !follow {
						a.messages = a.messages[:200]
					} else {
						a.messages = a.messages[len(a.messages)-200:]
					}
				}
				atLatest := true
				if len(a.messages) > 0 && len(fetched) > 0 {
					presentation.SortMessages(fetched)
					last, recent := a.messages[len(a.messages)-1], fetched[len(fetched)-1]
					atLatest = len(last.ID) > len(recent.ID) || len(last.ID) == len(recent.ID) && last.ID >= recent.ID
				}
				if !a.freshShown {
					lastID := ""
					if len(a.messages) > 0 {
						lastID = a.messages[len(a.messages)-1].ID
					}
					initialAnchor := ""
					if a.restorePosition != nil {
						initialAnchor = a.restorePosition.Anchor
					}
					a.messages = presentation.CachedWindow(a.messages, initialAnchor, 30)
					if len(a.messages) > 0 && a.messages[len(a.messages)-1].ID != lastID {
						atLatest = false
					}
				}
				a.atLatest = atLatest
				if !preserve {
					a.olderDone = end
				}
				a.loadedEpoch = requestEpoch
			}
			if !older {
				a.showingCached = false
			}
			a.rememberUsers(a.messages)
			if preserve && a.restorePosition != nil {
				anchor = presentation.NearestMessage(a.messages, a.restorePosition.Anchor)
			}
			a.renderMessages(!older && (!preserve || follow), anchor)
			a.startupDirty = true
			if !older && !a.freshShown {
				a.freshShown = true
				netdebug.Startup("fresh_messages", a.startedAt, len(a.messages))
			}
			a.updateHeader()
			a.updateSuggestions()
			if a.gatewayFailure != nil {
				a.showError(a.gatewayFailure)
			} else if len(messages) == 0 && older {
				a.status.SetText("No older messages available")
			} else {
				a.status.SetText("Messages loaded")
			}
			if !older && a.readyRefreshPending && requestEpoch < a.gatewayEpoch {
				a.readyRefreshPending = false
				a.loadMessages(false)
			}
		})
	}()
}
func (a *App) onEvent(e discord.Event) {
	if !discord.AcceptEvent(e, a.current.ID) {
		return
	}
	if e.Status != "" {
		a.status.SetText(e.Status)
	}
	switch e.Type {
	case "AUTH_FAILED", "GATEWAY_FAILED":
		a.gatewayFailure = fmt.Errorf("%s", e.Status)
		a.showError(a.gatewayFailure)
	case "READY":
		a.gatewayEpoch++
		netdebug.Startup("gateway_ready", a.startedAt, 0)
		a.gatewayFailure = nil
		var d struct {
			Users   []discord.User    `json:"users"`
			Private []discord.Channel `json:"private_channels"`
		}
		if json.Unmarshal(e.Data, &d) == nil {
			for _, u := range d.Users {
				a.users[u.ID] = u
			}
			for _, c := range d.Private {
				for i, u := range c.Recipients {
					if known, ok := a.users[u.ID]; ok {
						c.Recipients[i] = known
					}
				}
				a.rememberChannel(c)
			}
			a.updateNav()
		}
		if a.current.ID != "" && (a.atLatest || !a.freshShown) {
			if a.loading {
				a.readyRefreshPending = true
			} else {
				a.loadMessages(false)
			}
		}
	case "RESUMED":
		a.gatewayFailure = nil
		// Discord replays missed events on resume; no REST refresh is needed.
	case "CHANNEL_CREATE", "CHANNEL_UPDATE":
		var c discord.Channel
		if json.Unmarshal(e.Data, &c) == nil {
			a.rememberChannel(c)
			a.updateNav()
		}
	case "CHANNEL_DELETE":
		var c discord.Channel
		if json.Unmarshal(e.Data, &c) == nil {
			delete(a.channels, c.ID)
			a.updateNav()
			if a.current.ID == c.ID {
				a.showError(fmt.Errorf("this conversation is no longer available"))
			}
		}
	case "MESSAGE_CREATE":
		var m discord.Message
		if json.Unmarshal(e.Data, &m) != nil || m.ID == "" {
			return
		}
		if a.cache != nil {
			a.cache.Put([]discord.Message{m})
		}
		a.rememberUsers([]discord.Message{m})
		if m.ChannelID == a.current.ID {
			a.revision++
			a.revisions[m.ID] = a.revision
			if a.atLatest {
				bottom := a.nearBottom()
				a.messages = presentation.Merge(a.messages, []discord.Message{m}, true)
				if len(a.messages) > 200 {
					a.messages = a.messages[len(a.messages)-200:]
				}
				a.renderMessages(bottom, "")
			}
		}
	case "MESSAGE_UPDATE":
		var keys struct {
			ID        string `json:"id"`
			ChannelID string `json:"channel_id"`
		}
		if json.Unmarshal(e.Data, &keys) != nil {
			return
		}
		var source []discord.Message
		if keys.ChannelID == a.current.ID {
			a.revision++
			a.revisions[keys.ID] = a.revision
			source = a.messages
		}
		for i, m := range source {
			if m.ID == keys.ID {
				next, err := presentation.PatchMessage(m, e.Data)
				if err != nil {
					return
				}
				source[i] = next
				if a.cache != nil {
					a.cache.Put([]discord.Message{next})
				}
				if keys.ChannelID == a.current.ID {
					a.messages = source
					a.rememberUsers([]discord.Message{next})
					a.renderMessages(false, "")
				}
				break
			}
		}
	case "MESSAGE_DELETE", "MESSAGE_DELETE_BULK":
		var p struct {
			ID        string   `json:"id"`
			IDs       []string `json:"ids"`
			ChannelID string   `json:"channel_id"`
		}
		if json.Unmarshal(e.Data, &p) != nil {
			return
		}
		if p.ID != "" {
			p.IDs = append(p.IDs, p.ID)
		}
		removed := map[string]bool{}
		for _, id := range p.IDs {
			removed[id] = true
			if a.cache != nil {
				a.cache.Delete(p.ChannelID, id)
			}
			if p.ChannelID == a.current.ID {
				a.revision++
				a.revisions[id] = a.revision
			}
		}
		if p.ChannelID == a.current.ID {
			next := a.messages[:0]
			for _, m := range a.messages {
				if !removed[m.ID] {
					next = append(next, m)
				}
			}
			a.messages = next
			a.renderMessages(false, "")
		}
	}
}
func lenNonzero(v map[string]int) int {
	n := 0
	for _, c := range v {
		if c > 0 {
			n++
		}
	}
	return n
}
func (a *App) scrollInfo() win.SCROLLINFO {
	s := win.SCROLLINFO{}
	s.CbSize = uint32(unsafe.Sizeof(s))
	s.FMask = win.SIF_ALL
	win.GetScrollInfo(a.history.Handle(), win.SB_VERT, &s)
	return s
}
func (a *App) nearBottom() bool {
	s := a.scrollInfo()
	return int64(s.NPos)+int64(s.NPage) >= int64(s.NMax)-32
}
func (a *App) scrollTo(pos int32) {
	s := win.SCROLLINFO{CbSize: uint32(unsafe.Sizeof(win.SCROLLINFO{})), FMask: win.SIF_POS, NPos: pos}
	win.SetScrollInfo(a.history.Handle(), win.SB_VERT, &s, true)
	a.history.SendMessage(win.WM_VSCROLL, win.SB_THUMBPOSITION, 0)
}
func (a *App) channelNames() map[string]string {
	names := make(map[string]string, len(a.channels))
	for id, c := range a.channels {
		names[id] = c.Label()
	}
	return names
}

// appendRenderedMessages preserves existing native controls when the timeline
// only grows at its end (or drops its oldest item at the 200-message limit).
func (a *App) appendRenderedMessages(bottom bool, anchor string, oldPos int32) bool {
	if len(a.renderedIDs) == 0 || len(a.cards) != len(a.renderedIDs) {
		return false
	}
	drop, found := 0, false
	for candidate := 0; candidate < len(a.renderedIDs); candidate++ {
		retained := len(a.renderedIDs) - candidate
		if retained > len(a.messages) {
			continue
		}
		matches := true
		for i := 0; i < retained; i++ {
			id := a.renderedIDs[candidate+i]
			if a.messages[i].ID != id || !presentation.SameMessage(a.renderedMessages[id], a.messages[i]) {
				matches = false
				break
			}
		}
		if matches {
			drop, found = candidate, true
			break
		}
	}
	if !found {
		return false
	}

	gen, renderID := a.generation, a.renderID
	a.history.SetSuspended(true)
	for i := 0; i < drop; i++ {
		id := a.renderedIDs[i]
		if card := a.cards[id]; card != nil {
			card.Dispose()
		}
		delete(a.cards, id)
		delete(a.renderedMessages, id)
	}
	a.renderedIDs = append([]string(nil), a.renderedIDs[drop:]...)
	if len(a.renderedIDs) < len(a.messages) {
		names := a.channelNames()
		for _, m := range a.messages[len(a.renderedIDs):] {
			mediaCtx := a.mediaCtx
			if mediaCtx == nil {
				mediaCtx = a.session
			}
			card, err := a.newMessageCard(m, names, &a.previewCount, mediaCtx, gen, renderID)
			if err != nil {
				a.showError(err)
				a.history.SetSuspended(false)
				return true
			}
			a.cards[m.ID] = card
			a.renderedIDs = append(a.renderedIDs, m.ID)
			a.renderedMessages[m.ID] = m
		}
	}
	a.history.SetSuspended(false)
	a.scheduleHistoryScroll(bottom, anchor, oldPos, gen, renderID)
	return true
}

func (a *App) scheduleHistoryScroll(bottom bool, anchor string, oldPos int32, gen, renderID int) {
	a.historyScrollID++
	id, inputID := a.historyScrollID, a.historyInputID
	offset := 0
	if a.restorePosition != nil && a.restorePosition.Anchor == anchor {
		offset = a.restorePosition.Offset
	}
	deadline := time.Now().Add(time.Second)
	lastGeometry := int64(-1)
	var settle func()
	settle = func() {
		if a.generation != gen || a.renderID != renderID || a.historyScrollID != id {
			return
		}
		// Walk computes layout asynchronously. Wait for actual ordered card bounds,
		// not a fixed 100ms delay during which the viewport visibly jumps.
		ready, end, geometry := true, 0, int64(a.scrollInfo().NMax)
		for _, messageID := range a.renderedIDs {
			card := a.cards[messageID]
			if card == nil || card.IsDisposed() {
				ready = false
				break
			}
			b := card.BoundsPixels()
			if b.Height <= 0 || b.Width <= 0 || b.Y < end {
				ready = false
				break
			}
			end = b.Y + b.Height
			geometry = geometry*31 + int64(end+b.Width)
		}
		if (!ready || geometry != lastGeometry) && time.Now().Before(deadline) {
			lastGeometry = geometry
			time.AfterFunc(8*time.Millisecond, func() { a.post(settle) })
			return
		}
		if a.historyInputID == inputID {
			if bottom {
				a.scrollTo(a.scrollInfo().NMax)
			} else if card := a.cards[anchor]; anchor != "" && card != nil {
				a.scrollTo(int32(card.BoundsPixels().Y + offset))
			} else {
				a.scrollTo(oldPos)
			}
		}
		a.endHistoryPaint()
		if a.freshShown {
			a.restorePosition = nil
		}
		a.historySettled = true
		if a.showingCached && !a.cachedPresented {
			a.cachedPresented = true
			netdebug.Startup("cached_presented", a.startedAt, len(a.messages))
		}
		if !a.showingCached && a.freshShown && !a.freshPresented {
			a.freshPresented = true
			netdebug.Startup("fresh_presented", a.startedAt, len(a.messages))
		}
		a.loadVisiblePreviews()
		a.maybeLoadOlder()
	}
	a.post(settle)
}

func (a *App) renderMessages(bottom bool, anchor string) {
	start := time.Now()
	strategy := "render_full"
	defer func() { netdebug.Startup(strategy, start, len(a.messages)) }()
	if !bottom && anchor == "" && len(a.renderedIDs) > 0 {
		position := a.captureReadingPosition()
		a.restorePosition = &position
		anchor = position.Anchor
	}
	if !bottom && anchor != "" && len(a.renderedIDs) > 0 {
		a.beginHistoryPaint()
	}
	a.historySettled = false
	oldPos := a.scrollInfo().NPos
	if a.appendRenderedMessages(bottom, anchor, oldPos) {
		strategy = "render_append"
		return
	}
	if a.patchRenderedMessages(bottom, anchor, oldPos) {
		strategy = "render_patch"
		return
	}
	a.renderSerial++
	a.renderID = a.renderSerial
	renderID, gen := a.renderID, a.generation
	if a.cancelMedia != nil {
		a.cancelMedia()
	}
	ctx, cancel := context.WithCancel(a.session)
	a.cancelMedia = cancel
	a.mediaCtx = ctx
	a.clearHistory()
	a.history.SetSuspended(true)
	defer a.history.SetSuspended(false)
	a.cards = map[string]*walk.Composite{}
	a.renderedMessages = map[string]discord.Message{}
	if len(a.messages) == 0 {
		label, _ := walk.NewLabel(a.history)
		if label != nil {
			label.SetText("No messages here yet.")
		}
		a.scheduleHistoryScroll(bottom, anchor, oldPos, gen, renderID)
		return
	}
	names := a.channelNames()
	a.previewCount = 0
	for _, message := range a.messages {
		m := message
		card, err := a.newMessageCard(m, names, &a.previewCount, ctx, gen, renderID)
		if err != nil {
			a.showError(err)
			return
		}
		a.cards[m.ID] = card
		a.renderedIDs = append(a.renderedIDs, m.ID)
		a.renderedMessages[m.ID] = m
	}
	a.scheduleHistoryScroll(bottom, anchor, oldPos, gen, renderID)
}
