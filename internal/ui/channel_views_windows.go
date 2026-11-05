//go:build windows

package ui

import (
	"context"
	"time"

	"discrud/internal/discord"
	"discrud/internal/storage"
	"github.com/lxn/walk"
)

// Keep two inactive native feeds, not just their data. A -> B -> A needs no
// RichEdit reconstruction, thumbnail downloads, or network response to paint.
// All are owned by the validated session and disposed on logout/cache clear.
type channelView struct {
	history                          *walk.ScrollView
	messages                         []discord.Message
	position                         storage.ReadingPosition
	atLatest, olderDone              bool
	cards                            map[string]*walk.Composite
	ids                              []string
	rendered                         map[string]discord.Message
	revisions                        map[string]int
	revision, renderID, previewCount int
	previews                         []*pendingPreview
	mediaCtx                         context.Context
	cancelMedia                      context.CancelFunc
	savedAt                          time.Time
}

func (v *channelView) dispose() {
	if v.cancelMedia != nil {
		v.cancelMedia()
	}
	v.history.Dispose()
}

func (a *App) clearChannelViews() {
	for _, v := range a.channelViews {
		v.dispose()
	}
	a.channelViews = map[string]*channelView{}
}

func (a *App) switchChannelView(ch string) (*channelView, error) {
	a.endHistoryPaint()
	defer a.refreshAnimations()
	if a.channelViews == nil {
		a.channelViews = map[string]*channelView{}
	}
	var restored *channelView
	if v := a.channelViews[ch]; v != nil {
		delete(a.channelViews, ch)
		valid := a.cache != nil
		cached := map[string]bool{}
		if a.cache != nil {
			for _, m := range a.cache.Get(ch) {
				cached[m.ID] = true
			}
		}
		for _, m := range v.messages {
			if !cached[m.ID] {
				valid = false
				break
			}
		}
		if valid {
			restored = v
		} else {
			v.dispose()
		}
	}
	old := a.history
	if a.current.ID != "" {
		// Native cards are cached, but hidden channels must not retain decoded
		// animation frames or video decoders against the shared budget.
		for _, p := range a.previews {
			if p.animation != nil && (p.animation.loading || p.animation.hasAnimation()) {
				a.releasePreview(p.animation)
			}
		}
		a.channelViews[a.current.ID] = &channelView{
			history: old, messages: a.messages, position: a.captureReadingPosition(),
			atLatest: a.atLatest, olderDone: a.olderDone, cards: a.cards,
			ids: a.renderedIDs, rendered: a.renderedMessages, revisions: a.revisions,
			revision: a.revision, renderID: a.renderID, previewCount: a.previewCount,
			previews: a.previews, mediaCtx: a.mediaCtx, cancelMedia: a.cancelMedia, savedAt: time.Now(),
		}
		old.SetVisible(false)
	}
	if restored != nil {
		a.history = restored.history
	} else if a.current.ID != "" {
		parent := old.Parent()
		index := parent.Children().Index(old)
		history, err := walk.NewScrollView(parent)
		if err != nil {
			old.SetVisible(true)
			delete(a.channelViews, a.current.ID)
			return nil, err
		}
		history.SetMinMaxSize(walk.Size{Width: 200, Height: 250}, walk.Size{})
		layout := walk.NewVBoxLayout()
		layout.SetMargins(walk.Margins{HNear: 8, VNear: 8, HFar: 8, VFar: 8})
		layout.SetSpacing(8)
		history.SetLayout(layout)
		parent.Children().Remove(history)
		parent.Children().Insert(index, history)
		if box, ok := parent.Layout().(*walk.BoxLayout); ok {
			box.SetStretchFactor(history, 1)
		}
		a.history = history
		a.observeHistory()
	}
	for len(a.channelViews) > 2 {
		key := ""
		for id, v := range a.channelViews {
			if key == "" || v.savedAt.Before(a.channelViews[key].savedAt) {
				key = id
			}
		}
		a.channelViews[key].dispose()
		delete(a.channelViews, key)
	}
	a.historyScrollID++
	a.historySettled = false
	a.cards, a.renderedMessages = nil, nil
	a.renderedIDs, a.previews = nil, nil
	a.mediaCtx, a.cancelMedia = nil, nil
	a.previewCount = 0
	a.renderSerial++
	a.renderID = a.renderSerial
	if restored != nil {
		a.cards, a.renderedMessages = restored.cards, restored.rendered
		a.renderedIDs, a.previews = restored.ids, restored.previews
		a.mediaCtx, a.cancelMedia = restored.mediaCtx, restored.cancelMedia
		a.previewCount, a.renderID = restored.previewCount, restored.renderID
	}
	a.history.SetVisible(true)
	return restored, nil
}

// Trigger well before the top edge. Single-flight, end detection and a retry
// cooldown prevent duplicate requests and empty-page/error loops.
func (a *App) maybeLoadOlder() {
	if !a.historySettled || a.loading || a.olderDone || len(a.messages) == 0 || time.Now().Before(a.historyRetryAt) {
		return
	}
	s := a.scrollInfo()
	threshold := int32(s.NPage) * 2
	if threshold < 800 {
		threshold = 800
	}
	if s.NPos <= threshold {
		a.loadMessages(true)
	}
}
