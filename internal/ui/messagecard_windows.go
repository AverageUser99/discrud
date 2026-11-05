//go:build windows

package ui

import (
	"context"
	"fmt"
	"strings"

	"discrud/internal/discord"
	"discrud/internal/presentation"
	"github.com/lxn/walk"
)

// newMessageCard builds the controls for one message. Message actions live in a
// context menu so the timeline stays compact and each message creates fewer
// native controls.
func (a *App) newMessageCard(m discord.Message, names map[string]string, previewCount *int, ctx context.Context, gen, renderID int) (*walk.Composite, error) {
	card, err := walk.NewComposite(a.history)
	if err != nil {
		return nil, err
	}
	layout := walk.NewVBoxLayout()
	layout.SetMargins(walk.Margins{HNear: 8, VNear: 6, HFar: 8, VFar: 6})
	layout.SetSpacing(4)
	card.SetLayout(layout)
	card.SetBackground(a.background)

	menu, err := walk.NewMenu()
	if err != nil {
		card.Dispose()
		return nil, err
	}
	card.AddDisposable(menu)
	card.SetContextMenu(menu)
	addAction := func(text string, fn func()) {
		action := walk.NewAction()
		_ = action.SetText(text)
		action.Triggered().Attach(fn)
		_ = menu.Actions().Add(action)
	}
	addAction("Reply", func() { a.replyTo(m) })
	addAction("Copy message", func() { _ = walk.Clipboard().SetText(m.Content) })
	if m.Author.ID == a.me.ID {
		addAction("Edit message", func() { a.editMessage(m) })
		addAction("Delete message", func() { a.deleteMessage(m) })
	}

	header, err := walk.NewLabel(card)
	if err != nil {
		card.Dispose()
		return nil, err
	}
	header.SetContextMenu(menu)
	header.SetFont(a.bold)
	title := m.AuthorName() + "  ·  " + m.Timestamp.Local().Format("Jan 02  15:04")
	if m.EditedTimestamp != nil {
		title += "  (edited)"
	}
	if m.MentionsUser(a.me.ID, a.myRoles[m.GuildID]) {
		title += "  ·  @ mention"
		header.SetTextColor(walk.RGB(145, 85, 0))
	}
	header.SetText(title)

	if m.MessageReference != nil {
		preview := "Reply to message " + m.MessageReference.MessageID
		if r := m.ReferencedMessage; r != nil {
			preview = "↳ " + r.AuthorName() + ": " + r.Content
			preview = strings.ReplaceAll(preview, "\n", " ")
			runes := []rune(preview)
			if len(runes) > 150 {
				preview = string(runes[:150]) + "…"
			}
		}
		label, createErr := walk.NewTextLabel(card)
		if createErr == nil {
			label.SetContextMenu(menu)
			label.SetText(preview)
			label.SetTextColor(walk.RGB(100, 100, 110))
		}
	}

	body := m.Content
	if body == "" && len(m.Attachments) == 0 && len(m.Embeds) == 0 {
		body = fmt.Sprintf("[Embed, sticker, or unsupported message content · type %d]", m.Type)
	}
	if body != "" {
		resolved := presentation.ResolveMentions(body, a.users, a.roles[m.GuildID], names)
		rich, createErr := newRichText(card, resolved, a.openExternal)
		if createErr != nil {
			card.Dispose()
			return nil, createErr
		}
		rich.SetContextMenu(menu)
	}

	for _, attachment := range m.Attachments {
		att := attachment
		button, createErr := walk.NewPushButton(card)
		if createErr == nil {
			button.SetContextMenu(menu)
			title := att.Filename
			if att.Size > 0 {
				title += fmt.Sprintf(" · %.1f KiB", float64(att.Size)/1024)
			}
			button.SetText(title)
			button.Clicked().Attach(func() { a.openExternal(att.URL) })
		}
		if strings.HasPrefix(att.ContentType, "image/") || att.Width > 0 && att.Height > 0 || isGIF(att) || isMP4(att) {
			a.addMediaPreview(card, att, previewCount, ctx, gen)
		}
	}
	a.addEmbeds(card, m, previewCount, ctx, gen)
	return card, nil
}
