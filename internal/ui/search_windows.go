//go:build windows

package ui

import (
	"context"
	"fmt"
	"strings"

	"discrud/internal/discord"
	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"
)

// Search is deliberately separate from the live timeline and its cache.
func (a *App) searchMessages() {
	backend, ok := a.backend.(discord.SearchBackend)
	if !ok || a.current.ID == "" || a.session == nil {
		return
	}
	channel := a.current
	ctx, cancel := context.WithCancel(a.session)
	defer cancel()
	var dlg *walk.Dialog
	var content, author, mention *walk.LineEdit
	var authorSuggestions, mentionSuggestions *walk.ListBox
	var authorComplete, mentionComplete *searchCompletion
	var list *walk.ListBox
	var status, detail *walk.TextLabel
	var searchButton, closeButton *walk.PushButton
	var results []discord.Message
	var query discord.SearchQuery
	var total int
	var serial int
	open := true
	showDetail := func() {
		if list == nil || detail == nil {
			return
		}
		i := list.CurrentIndex()
		if i < 0 || i >= len(results) {
			detail.SetText("")
			return
		}
		m := results[i]
		body := m.Content
		if body == "" {
			body = "[Attachment or embed]"
		}
		detail.SetText(m.AuthorName() + " · " + m.Timestamp.Local().Format("Jan 02 2006 15:04") + "\n" + body)
	}
	var run func(discord.SearchQuery)
	run = func(q discord.SearchQuery) {
		serial++
		requestID := serial
		status.SetText("Searching…")
		go func() {
			found, err := backend.SearchMessages(ctx, q)
			a.post(func() {
				if !open || ctx.Err() != nil || requestID != serial {
					return
				}
				if err != nil {
					status.SetText(err.Error())
					return
				}
				query = q
				total = found.Total
				results = found.Messages
				labels := make([]string, 0, len(results))
				for _, m := range results {
					body := strings.ReplaceAll(strings.ReplaceAll(m.Content, "\n", " "), "\r", " ")
					if body == "" {
						body = "[Attachment or embed]"
					}
					r := []rune(body)
					if len(r) > 100 {
						body = string(r[:100]) + "…"
					}
					labels = append(labels, m.AuthorName()+" · "+m.Timestamp.Local().Format("Jan 02 15:04")+" · "+body)
				}
				list.SetModel(labels)
				if len(labels) > 0 {
					list.SetCurrentIndex(0)
				}
				showDetail()
				status.SetText(fmt.Sprintf("%d results · showing %d–%d", found.Total, q.Offset+boolInt(len(results) > 0), q.Offset+len(results)))
			})
		}()
	}
	start := func() {
		from, err := discord.ResolveSearchUser(author.Text(), a.users)
		if err != nil {
			status.SetText("From: " + err.Error())
			return
		}
		mentions, err := discord.ResolveSearchUser(mention.Text(), a.users)
		if err != nil {
			status.SetText("Mentions: " + err.Error())
			return
		}
		text := strings.TrimSpace(content.Text())
		if text == "" && from == "" && mentions == "" {
			status.SetText("Enter search text or a user filter.")
			return
		}
		run(discord.SearchQuery{GuildID: channel.GuildID, ChannelID: channel.ID, Content: text, AuthorID: from, MentionID: mentions})
	}
	err := (Dialog{AssignTo: &dlg, Title: "Search · " + channel.Label(), Size: Size{Width: 720, Height: 560}, MinSize: Size{Width: 500, Height: 400}, DefaultButton: &searchButton, CancelButton: &closeButton, Layout: VBox{}, Children: []Widget{
		LineEdit{AssignTo: &content, CueBanner: "Search message text"},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			Composite{StretchFactor: 1, Layout: VBox{MarginsZero: true}, Children: []Widget{
				LineEdit{AssignTo: &author, CueBanner: "From @user or user ID"},
				ListBox{AssignTo: &authorSuggestions, Visible: false, MinSize: Size{Height: 64}, MaxSize: Size{Height: 96}, OnItemActivated: func() {
					if authorComplete != nil {
						authorComplete.complete()
					}
				}},
			}},
			Composite{StretchFactor: 1, Layout: VBox{MarginsZero: true}, Children: []Widget{
				LineEdit{AssignTo: &mention, CueBanner: "Mentions @user or user ID"},
				ListBox{AssignTo: &mentionSuggestions, Visible: false, MinSize: Size{Height: 64}, MaxSize: Size{Height: 96}, OnItemActivated: func() {
					if mentionComplete != nil {
						mentionComplete.complete()
					}
				}},
			}},
		}},
		TextLabel{Text: "Type @ for loaded users; choose a suggestion and press Tab. A user ID also works."},
		TextLabel{AssignTo: &status, Text: "Enter a search and press Search."},
		ListBox{AssignTo: &list, StretchFactor: 1, OnCurrentIndexChanged: showDetail},
		TextLabel{AssignTo: &detail, MinSize: Size{Height: 60}},
		Composite{Layout: HBox{MarginsZero: true}, Children: []Widget{
			PushButton{AssignTo: &searchButton, Text: "Search", OnClicked: start},
			PushButton{Text: "Previous", OnClicked: func() {
				if query.Offset > 0 {
					q := query
					q.Offset -= 25
					if q.Offset < 0 {
						q.Offset = 0
					}
					run(q)
				}
			}},
			PushButton{Text: "Next", OnClicked: func() {
				if len(results) > 0 && query.Offset+25 < total && query.Offset < 9975 {
					q := query
					q.Offset += 25
					run(q)
				}
			}},
			HSpacer{}, PushButton{AssignTo: &closeButton, Text: "Close", OnClicked: func() { dlg.Cancel() }},
		}},
	}}).Create(a.window)
	if err != nil {
		a.showError(err)
		return
	}
	authorComplete = &searchCompletion{field: author, list: authorSuggestions, users: a.users}
	mentionComplete = &searchCompletion{field: mention, list: mentionSuggestions, users: a.users}
	author.TextChanged().Attach(authorComplete.update)
	mention.TextChanged().Attach(mentionComplete.update)
	authorInstalled := authorComplete.install()
	mentionInstalled := mentionComplete.install()
	if !authorInstalled || !mentionInstalled {
		status.SetText("Tab completion is unavailable; activate a suggestion with the mouse.")
	}
	dlg.Run()
	open = false
	cancel()
	dlg.Dispose()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
