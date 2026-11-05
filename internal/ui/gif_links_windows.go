//go:build windows

package ui

import (
	"context"
	"crypto/sha256"
	"fmt"

	"discrud/internal/discord"
	"discrud/internal/gifsource"
	"github.com/lxn/walk"
)

// Link discovery is independent of Discord's embed timing/availability. Merge
// late embed hints into the same canonical link and never render a second poster.
func gifLinkSources(m discord.Message) ([]string, map[string][]gifsource.Hint) {
	pages := gifsource.Links(m.Content)
	hints := map[string][]gifsource.Hint{}
	seen := map[string]bool{}
	for _, page := range pages {
		seen[page] = true
	}
	for _, embed := range m.Embeds {
		page, ok := gifsource.CanonicalPage(embed.URL)
		if !ok {
			continue
		}
		if !seen[page] && len(pages) < gifsource.MaxLinks {
			seen[page] = true
			pages = append(pages, page)
		}
		for _, media := range []*discord.EmbedMedia{embed.Image, embed.Thumbnail, embed.Video} {
			if media != nil {
				hints[page] = append(hints[page], gifsource.Hint{URL: media.URL, ProxyURL: media.ProxyURL, GIF: media == embed.Image, Video: media == embed.Video})
			}
		}
	}
	return pages, hints
}
func (a *App) addGIFLinks(card *walk.Composite, m discord.Message, count *int, ctx context.Context, gen int) map[string]bool {
	pages, hints := gifLinkSources(m)
	handled := map[string]bool{}
	for _, page := range pages {
		handled[page] = true
		if *count >= 24 {
			continue
		}
		button, err := walk.NewPushButton(card)
		if err == nil {
			button.SetContextMenu(card.ContextMenu())
			button.SetText("Klipy GIF · Open original link")
			raw := page
			button.Clicked().Attach(func() { a.openExternal(raw) })
		}
		view, err := a.newAnimatedGIF(card, a.history)
		if err != nil {
			continue
		}
		*count++
		view.SetContextMenu(card.ContextMenu())
		// Reserve stable geometry before metadata or media arrives.
		view.SetMinMaxSize(walk.Size{Height: 180}, walk.Size{Width: 360, Height: 180})
		view.SetToolTipText("Hover to play GIF · move away to pause · click an unavailable preview to retry")
		pageHints := append([]gifsource.Hint(nil), hints[page]...)
		load := func(ctx context.Context) ([]byte, error) { return a.gifResolver.Load(ctx, page, pageHints) }
		start := func() { a.startGIFLoader(view, load, ctx, gen) }
		view.retry = func() { a.gifResolver.Invalidate(page); start() }
		a.previews = append(a.previews, &pendingPreview{view: view.CustomWidget, animation: view, attachmentID: fmt.Sprintf("link-%x", sha256.Sum256([]byte(page))), start: start})
	}
	return handled
}
