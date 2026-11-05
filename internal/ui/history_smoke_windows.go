//go:build windows

package ui

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"discrud/internal/discord"
	"discrud/internal/storage"
	"github.com/lxn/win"
)

type historySmokeBackend struct {
	discord.Backend
	olderCalls atomic.Int32
}

func (b *historySmokeBackend) Messages(ctx context.Context, ch, before string) ([]discord.Message, error) {
	if before != "" {
		b.olderCalls.Add(1)
		select {
		case <-time.After(150 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if ch == "history-regression" {
		var page []discord.Message
		cursor, _ := strconv.Atoi(before)
		for i := 649; i >= 0 && len(page) < 50; i-- {
			if before != "" && 10000+i >= cursor {
				continue
			}
			page = append(page, discord.Message{ID: fmt.Sprint(10000 + i), ChannelID: ch, GuildID: "demo-server", Author: discord.User{ID: "history-author", Username: "History"}, Content: "Isolated deep history regression fixture", Timestamp: time.Unix(int64(2000+i), 0)})
		}
		return page, nil
	}
	return b.Backend.Messages(ctx, ch, before)
}

type feedSmokeProbe struct {
	count    int
	id       string
	handle   uintptr
	position storage.ReadingPosition
	backend  *historySmokeBackend
	report   map[string]any
}

func (a *App) smokeScrollHistory(p *feedSmokeProbe) {
	p.report = map[string]any{}
	p.backend = &historySmokeBackend{Backend: a.backend}
	a.backend = p.backend
	p.count = len(a.messages)
	if p.count < 3 {
		return
	}
	p.id = a.messages[0].ID
	p.handle = uintptr(a.cards[p.id].Handle())
	a.restorePosition = nil
	a.scrollTo(int32(a.cards[a.messages[2].ID].BoundsPixels().Y + 7))
	// Exercise the actual Win32 scroll observer, not the Older button/API.
	for i := 0; i < 5; i++ {
		a.history.SendMessage(win.WM_MOUSEWHEEL, uintptr(120)<<16, 0)
	}
	p.position = a.captureReadingPosition()
}
func (a *App) finishSmokeScroll(p *feedSmokeProbe) {
	p.report["automatic_history_loaded"] = len(a.messages) > p.count
	p.report["history_prepend_preserved_card"] = a.cards[p.id] != nil && uintptr(a.cards[p.id].Handle()) == p.handle
	// Read actual native geometry, not a pending restorePosition hint.
	p.report["history_prepend_pixel_stable"] = a.restorePosition == nil && a.captureReadingPosition() == p.position
	p.report["history_requests_single_flight"] = p.backend.olderCalls.Load() <= 1
}

// Called from the smoke driver goroutine. All controls remain UI-thread owned.
func (a *App) smokeChannelSwitches(p *feedSmokeProbe) map[string]any {
	run := func(fn func()) { done := make(chan struct{}); a.post(func() { fn(); close(done) }); <-done }
	var ids []string
	var handles []uintptr
	var position storage.ReadingPosition
	var scrollHandle uintptr
	var general discord.Channel
	run(func() {
		general = a.current
		position = a.captureReadingPosition()
		scrollHandle = uintptr(a.history.Handle())
		for _, m := range a.messages {
			ids = append(ids, m.ID)
			handles = append(handles, uintptr(a.cards[m.ID].Handle()))
		}
		start := time.Now()
		// Switch back before the other channel's HTTP result can be applied.
		for i := 0; i < 5; i++ {
			a.openChannel(discord.Channel{ID: "dm-alex", Type: 1, Recipients: []discord.User{{ID: "101", Username: "alex"}}})
			a.openChannel(general)
		}
		p.report["channel_switch_10_transitions_ms"] = time.Since(start).Milliseconds()
		p.report["channel_switch_immediate_messages_preserved"] = len(ids) == len(a.messages)
	})
	time.Sleep(400 * time.Millisecond)
	run(func() {
		preserved := a.current.ID == general.ID && len(ids) == len(a.messages) && uintptr(a.history.Handle()) == scrollHandle
		for i, id := range ids {
			if i >= len(a.messages) || a.messages[i].ID != id || a.cards[id] == nil || uintptr(a.cards[id].Handle()) != handles[i] {
				preserved = false
				break
			}
		}
		p.report["channel_switch_native_feeds_preserved"] = preserved
		p.report["channel_switch_reading_position_preserved"] = a.restorePosition == nil && a.captureReadingPosition() == position
		p.report["channel_native_cache_bounded"] = len(a.channelViews) <= 2
		a.editor.SetText("@al")
		a.editor.Select(3, 3)
		a.updateSuggestions()
	})
	a.smokeDeepHistory(p, run, general)
	return p.report
}

func (a *App) smokeDeepHistory(p *feedSmokeProbe, run func(func()), general discord.Channel) {
	settle := func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			done := false
			run(func() { done = a.historySettled && !a.loading })
			if done {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
	}
	deep := discord.Channel{ID: "history-regression", GuildID: "demo-server", Name: "Offline history fixture", Type: 0}
	run(func() { a.openChannel(deep) })
	settle()
	startCalls := p.backend.olderCalls.Load()
	first := ""
	run(func() {
		if len(a.messages) > 0 {
			first = a.messages[0].ID
		}
	})
	stable := true
	for i := 0; i < 14; i++ {
		var position storage.ReadingPosition
		run(func() {
			if len(a.messages) < 3 {
				stable = false
				return
			}
			a.restorePosition = nil
			a.scrollTo(int32(a.cards[a.messages[2].ID].BoundsPixels().Y + 7))
			position = a.captureReadingPosition()
		})
		settle()
		run(func() {
			if a.restorePosition != nil || a.captureReadingPosition() != position {
				stable = false
			}
		})
	}
	var position storage.ReadingPosition
	var firstDeep string
	run(func() {
		p.report["deep_history_scrolls_past_200"] = len(a.messages) == 200 && len(a.messages) > 0 && a.messages[0].ID < first
		p.report["deep_history_pixel_stable"] = stable
		contiguous := true
		for i := 1; i < len(a.messages); i++ {
			prev, _ := strconv.Atoi(a.messages[i-1].ID)
			next, _ := strconv.Atoi(a.messages[i].ID)
			if next != prev+1 {
				contiguous = false
			}
		}
		p.report["deep_history_has_no_gaps"] = contiguous
		calls := int(p.backend.olderCalls.Load() - startCalls)
		initial, _ := strconv.Atoi(first)
		oldest := initial
		if len(a.messages) > 0 {
			oldest, _ = strconv.Atoi(a.messages[0].ID)
		}
		revealed := initial - oldest
		// Short cached prefixes can keep the viewport inside the prefetch zone,
		// legitimately revealing another batch in the same scroll interaction.
		p.report["deep_history_rest_pages"] = calls
		p.report["deep_history_messages_revealed"] = revealed
		p.report["deep_history_reuses_cached_prefixes"] = calls <= (revealed+49)/50 && calls < 14
		position = a.captureReadingPosition()
		if len(a.messages) > 0 {
			firstDeep = a.messages[0].ID
		}
		a.openChannel(general)
		a.openChannel(deep)
	})
	settle()
	run(func() {
		p.report["deep_history_refresh_does_not_splice_gap"] = len(a.messages) == 200 && a.messages[0].ID == firstDeep && a.captureReadingPosition() == position && !a.atLatest
		a.openChannel(general)
	})
	settle()
	run(func() { a.editor.SetText("@al"); a.editor.Select(3, 3); a.updateSuggestions() })
}
