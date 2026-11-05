//go:build windows

package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"

	"discrud/internal/discord"
	"discrud/internal/presentation"
	"github.com/lxn/walk"
)

func (a *App) clearComposer() {
	if a.sending {
		return
	}
	a.reply = nil
	a.editing = ""
	a.uploads = nil
	a.failed = nil
	a.editor.SetText("")
	a.replyText.SetVisible(false)
	a.replyPing.SetChecked(false)
	a.replyPing.SetVisible(false)
	a.filesText.SetVisible(false)
	a.suggestions.SetVisible(false)
	a.retry.SetVisible(false)
	a.sendButton.SetText("Send")
}
func (a *App) replyTo(m discord.Message) {
	if a.sending {
		return
	}
	a.editing = ""
	a.reply = &discord.Reference{MessageID: m.ID, ChannelID: m.ChannelID, GuildID: a.current.GuildID}
	a.replyPing.SetChecked(false)
	a.replyPing.SetVisible(true)
	a.replyText.SetText("Replying to " + m.AuthorName())
	a.replyText.SetVisible(true)
	a.sendButton.SetText("Send")
	a.editor.SetFocus()
}
func (a *App) editMessage(m discord.Message) {
	if a.sending || m.Author.ID != a.me.ID {
		return
	}
	if a.editor.Text() != "" {
		if walk.MsgBox(a.window, "Replace unsent text?", "Editing this message will discard the current unsent text and attachments.", walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
			return
		}
	}
	a.clearComposer()
	a.editing = m.ID
	a.editor.SetText(strings.ReplaceAll(m.Content, "\n", "\r\n"))
	a.editor.Select(0, -1)
	a.replyText.SetText("Editing your message · Clear / cancel to discard")
	a.replyText.SetVisible(true)
	a.sendButton.SetText("Save edit")
	a.editor.SetFocus()
}
func (a *App) deleteMessage(m discord.Message) {
	if a.sending || m.Author.ID != a.me.ID {
		return
	}
	if walk.MsgBox(a.window, "Delete message?", "Permanently delete this message from Discord?", walk.MsgBoxYesNo|walk.MsgBoxIconWarning) != walk.DlgCmdYes {
		return
	}
	backend := a.backend
	a.task(func(ctx context.Context) func() {
		err := backend.Delete(ctx, m.ChannelID, m.ID)
		return func() {
			if err != nil {
				a.showError(err)
				return
			}
			b, _ := jsonDelete(m.ChannelID, m.ID)
			a.onEvent(discord.Event{Type: "MESSAGE_DELETE", Data: b})
		}
	})
}
func jsonDelete(ch, id string) ([]byte, error) {
	return json.Marshal(map[string]string{"channel_id": ch, "id": id})
}
func (a *App) setSending(value bool) {
	a.sending = value
	a.editor.SetEnabled(!value)
	a.replyPing.SetEnabled(!value)
	a.sendButton.SetEnabled(!value)
	a.attachButton.SetEnabled(!value)
	a.pasteButton.SetEnabled(!value)
	a.servers.SetEnabled(!value)
	a.conversations.SetEnabled(!value)
}
func sameAttempt(old *attempt, ch string, s discord.Send) bool {
	if old == nil || old.channel != ch || old.send.Content != s.Content || old.send.ReplyPing != s.ReplyPing || len(old.send.Files) != len(s.Files) {
		return false
	}
	if (old.send.ReplyTo == nil) != (s.ReplyTo == nil) {
		return false
	}
	if old.send.ReplyTo != nil && *old.send.ReplyTo != *s.ReplyTo {
		return false
	}
	for i, f := range s.Files {
		if old.send.Files[i].Name != f.Name || len(old.send.Files[i].Data) != len(f.Data) {
			return false
		}
	}
	return true
}
func (a *App) sendMessage() {
	if a.backend == nil || a.current.ID == "" || a.sending {
		return
	}
	text := strings.ReplaceAll(a.editor.Text(), "\r\n", "\n")
	if strings.TrimSpace(text) == "" && len(a.uploads) == 0 {
		return
	}
	if len(utf16.Encode([]rune(text))) > 2000 {
		a.showError(errors.New("the MVP composer is limited to 2,000 UTF-16 characters per message"))
		return
	}
	send := discord.Send{Content: text, ReplyTo: a.reply, ReplyPing: a.reply != nil && a.replyPing.Checked(), Files: append([]discord.Upload(nil), a.uploads...), Nonce: discord.NewNonce()}
	if sameAttempt(a.failed, a.current.ID, send) {
		send.Nonce = a.failed.send.Nonce
	}
	ch, editing, backend, cache, guild := a.current.ID, a.editing, a.backend, a.cache, a.current.GuildID
	a.clearError()
	a.setSending(true)
	a.status.SetText("Sending...")
	a.task(func(ctx context.Context) func() {
		var m discord.Message
		var err error
		if editing != "" {
			m, err = backend.Edit(ctx, ch, editing, text)
		} else {
			m, err = backend.Send(ctx, ch, send)
		}
		return func() {
			a.setSending(false)
			if err != nil {
				if editing == "" {
					a.failed = &attempt{ch, send}
					a.retry.SetVisible(true)
				}
				a.showError(err)
				a.editor.SetFocus()
				return
			}
			if m.GuildID == "" {
				m.GuildID = guild
			}
			a.clearComposer()
			if cache != nil {
				cache.Put([]discord.Message{m})
			}
			if a.current.ID == ch {
				a.messages = presentation.Merge(a.messages, []discord.Message{m}, true)
				if len(a.messages) > 200 {
					a.messages = a.messages[len(a.messages)-200:]
				}
				a.rememberUsers([]discord.Message{m})
				a.renderMessages(true, "")
			}
			a.status.SetText("Message sent")
			a.editor.SetFocus()
		}
	})
}
func (a *App) updateFileLabel() {
	var total int
	names := []string{}
	for _, f := range a.uploads {
		names = append(names, f.Name)
		total += len(f.Data)
	}
	a.filesText.SetText(fmt.Sprintf("Attached: %s · %.1f MiB / 10 MiB", strings.Join(names, ", "), float64(total)/1024/1024))
	a.filesText.SetVisible(len(a.uploads) > 0)
}
func (a *App) addUpload(f discord.Upload) {
	if a.editing != "" {
		a.showError(errors.New("attachment changes while editing are not supported in the MVP"))
		return
	}
	total := len(f.Data)
	for _, v := range a.uploads {
		total += len(v.Data)
	}
	if total > discord.MaxUploadBytes || len(a.uploads) >= 10 {
		a.showError(errors.New("uploads are limited to 10 files and 10 MiB combined"))
		return
	}
	a.failed = nil
	a.uploads = append(a.uploads, f)
	a.updateFileLabel()
	a.editor.SetFocus()
}
func (a *App) attachFile() {
	if a.sending || a.current.ID == "" {
		return
	}
	dlg := walk.FileDialog{Title: "Attach a file (10 MiB combined MVP limit)", Filter: "All files (*.*)|*.*"}
	ok, err := dlg.ShowOpen(a.window)
	if err != nil {
		a.showError(err)
		return
	}
	if !ok {
		return
	}
	ch, path := a.current.ID, dlg.FilePath
	a.task(func(ctx context.Context) func() {
		f, err := os.Open(path)
		if err != nil {
			return func() { a.showError(errors.New("could not open the selected file")) }
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, discord.MaxUploadBytes+1))
		if err == nil && len(data) > discord.MaxUploadBytes {
			err = errors.New("the selected file exceeds 10 MiB")
		}
		return func() {
			if ch != a.current.ID || a.sending {
				return
			}
			if err != nil {
				a.showError(err)
				return
			}
			a.addUpload(discord.Upload{Name: filepath.Base(path), Data: data})
		}
	})
}
func (a *App) pasteImage() bool {
	if a.sending || a.current.ID == "" {
		return false
	}
	f, found, err := clipboardImage()
	if err != nil {
		a.showError(err)
		return true
	}
	if found {
		a.addUpload(f)
	}
	return found
}
func (a *App) updateSuggestions() {
	if a.completing || a.current.ID == "" || a.sending {
		return
	}
	_, end := a.editor.Selection()
	start, query, kind, ok := presentation.MentionQuery(a.editor.Text(), end)
	if !ok {
		a.suggestions.SetVisible(false)
		a.choices = nil
		return
	}
	q := strings.ToLower(query)
	var choices []suggestion
	if kind == '@' {
		for _, u := range a.users {
			if strings.Contains(strings.ToLower(u.Name()+" "+u.Username), q) {
				choices = append(choices, suggestion{"@" + u.Name() + " (" + u.Username + ")", "<@" + u.ID + "> "})
			}
		}
		for id, name := range a.roles[a.current.GuildID] {
			if strings.Contains(strings.ToLower(name), q) {
				choices = append(choices, suggestion{"@" + name + " · role", "<@&" + id + "> "})
			}
		}
	} else {
		for _, c := range a.channels {
			if c.GuildID == a.current.GuildID && c.GuildID != "" && c.Supported() && strings.Contains(strings.ToLower(c.Label()), q) {
				choices = append(choices, suggestion{"#" + c.Label(), "<#" + c.ID + "> "})
			}
		}
	}
	sort.Slice(choices, func(i, j int) bool { return strings.ToLower(choices[i].label) < strings.ToLower(choices[j].label) })
	if len(choices) > 15 {
		choices = choices[:15]
	}
	a.choices = choices
	a.mentionStart = start
	a.mentionEnd = end
	labels := []string{}
	for _, c := range choices {
		labels = append(labels, c.label+" · Tab to insert")
	}
	a.suggestions.SetModel(labels)
	a.suggestions.SetVisible(len(labels) > 0)
	if len(labels) > 0 {
		a.suggestions.SetCurrentIndex(0)
	}
}
func (a *App) completeMention() bool {
	if len(a.choices) == 0 || !a.suggestions.Visible() {
		return false
	}
	i := a.suggestions.CurrentIndex()
	if i < 0 || i >= len(a.choices) {
		i = 0
	}
	a.completing = true
	a.editor.Select(a.mentionStart, a.mentionEnd)
	a.editor.Replace(a.choices[i].insert)
	a.completing = false
	a.suggestions.SetVisible(false)
	a.choices = nil
	a.editor.SetFocus()
	return true
}
