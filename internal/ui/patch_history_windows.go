//go:build windows

package ui

import "discrud/internal/presentation"

// Reconcile native cards by message ID. Fresh history pages commonly add/drop a
// few messages; retaining the other RichEdit controls avoids a costly full rebuild.
func (a *App) patchRenderedMessages(bottom bool, anchor string, oldPos int32) bool {
	if len(a.messages) == 0 || len(a.renderedIDs) == 0 || len(a.cards) != len(a.renderedIDs) {
		return false
	}
	overlap := 0
	wanted := make(map[string]bool, len(a.messages))
	for _, m := range a.messages {
		wanted[m.ID] = true
		if a.cards[m.ID] != nil {
			overlap++
		}
	}
	if overlap == 0 {
		return false
	}
	a.history.SetSuspended(true)
	defer a.history.SetSuspended(false)
	for id, card := range a.cards {
		if !wanted[id] {
			card.Dispose()
			delete(a.cards, id)
			delete(a.renderedMessages, id)
		}
	}
	names := a.channelNames()
	for i, m := range a.messages {
		card := a.cards[m.ID]
		if card != nil && !presentation.SameMessage(a.renderedMessages[m.ID], m) {
			card.Dispose()
			card = nil
		}
		if card == nil {
			a.previewCount = 0
			for _, p := range a.previews {
				if !p.view.IsDisposed() {
					a.previewCount++
				}
			}
			var err error
			card, err = a.newMessageCard(m, names, &a.previewCount, a.mediaCtx, a.generation, a.renderID)
			if err != nil {
				a.showError(err)
				return false
			}
			a.cards[m.ID] = card
			a.renderedMessages[m.ID] = m
		}
		children := a.history.Children()
		if children.Index(card) != i {
			if err := children.Remove(card); err != nil {
				a.showError(err)
				return false
			}
			if err := children.Insert(i, card); err != nil {
				card.Dispose()
				a.showError(err)
				return false
			}
			// Walk hides a widget when detaching it; reparenting does not undo that.
			card.SetVisible(true)
		}
	}
	a.renderedIDs = nil
	for _, m := range a.messages {
		a.renderedIDs = append(a.renderedIDs, m.ID)
	}
	live := a.previews[:0]
	for _, p := range a.previews {
		if !p.view.IsDisposed() {
			live = append(live, p)
		}
	}
	a.previews = live
	a.scheduleHistoryScroll(bottom, anchor, oldPos, a.generation, a.renderID)
	return true
}
