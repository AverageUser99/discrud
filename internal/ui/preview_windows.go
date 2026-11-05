//go:build windows

package ui

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"

	"discrud/internal/discord"
	"discrud/internal/gifsource"
	"github.com/lxn/walk"
)

func previewHeight(w, h int) int {
	if w <= 0 || h <= 0 {
		return 160
	}
	return max(1, min(240, int(int64(h)*360/int64(w))))
}

func (a *App) addMediaPreview(card *walk.Composite, att discord.Attachment, count *int, ctx context.Context, gen int) {
	if *count >= 24 {
		return
	}
	*count++
	height := previewHeight(att.Width, att.Height)
	if isGIF(att) || isMP4(att) {
		view, err := a.newAnimatedGIF(card, a.history)
		if err != nil {
			return
		}
		view.SetContextMenu(card.ContextMenu())
		view.SetMinMaxSize(walk.Size{Height: height}, walk.Size{Width: 360, Height: height})
		view.SetToolTipText("Hover to play GIF · move away to pause · use the link above to open externally")
		a.previews = append(a.previews, &pendingPreview{view: view.CustomWidget, animation: view, attachmentID: att.ID, start: func() { a.startGIF(view, att, ctx, gen) }})
		return
	}
	view, err := walk.NewImageView(card)
	if err != nil {
		return
	}
	view.SetContextMenu(card.ContextMenu())
	view.SetMode(walk.ImageViewModeShrink)
	view.SetMinMaxSize(walk.Size{Height: height}, walk.Size{Width: 360, Height: height})
	view.SetToolTipText("Click to view image · wheel to zoom · drag to pan")
	view.MouseDown().Attach(func(_, _ int, button walk.MouseButton) {
		if button == walk.LeftButton {
			a.openImageViewer(att)
		}
	})
	thumbnails, isDemo := a.thumbnails, a.opt.Demo
	a.previews = append(a.previews, &pendingPreview{view: view, attachmentID: att.ID, start: func() {
		go func() {
			select {
			case a.mediaSem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-a.mediaSem }()
			img, err := cachedThumbnail(ctx, thumbnails, att, isDemo)
			if err != nil {
				return
			}
			a.post(func() {
				if gen != a.generation || ctx.Err() != nil || view.IsDisposed() {
					return
				}
				bitmap, err := walk.NewBitmapFromImage(img)
				if err == nil {
					// Unknown or stale metadata must not force an incorrectly sized preview.
					bounds := img.Bounds()
					actualHeight := previewHeight(bounds.Dx(), bounds.Dy())
					view.SetMinMaxSize(walk.Size{Height: actualHeight}, walk.Size{Width: 360, Height: actualHeight})
					view.AddDisposable(bitmap)
					view.SetImage(bitmap)
				}
			})
		}()
	}})
}

func clippedText(s string, limit int) string {
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "…"
	}
	return s
}

// Klipy sharing links are resolved separately, including messages with no
// Discord embed at all. Other providers retain Discord's embed-only behavior.
func (a *App) addEmbeds(card *walk.Composite, m discord.Message, count *int, ctx context.Context, gen int) {
	handled := a.addGIFLinks(card, m, count, ctx, gen)
	for i, embed := range m.Embeds {
		if page, ok := gifsource.CanonicalPage(embed.URL); ok && handled[page] {
			continue
		}
		if i >= 4 {
			break
		}
		title := embed.Title
		if title == "" {
			title = embed.URL
		}
		if embed.Video != nil && !mediaIsGIF(embed.Video) && !mediaIsVideo(embed.Video) && !strings.EqualFold(embed.Type, "gifv") {
			title += " · Video opens in browser"
		}
		if safeWebURL(embed.URL) {
			button, err := walk.NewPushButton(card)
			if err == nil {
				button.SetContextMenu(card.ContextMenu())
				button.SetText(clippedText(title, 120))
				raw := embed.URL
				button.Clicked().Attach(func() { a.openExternal(raw) })
			}
		}
		if embed.Description != "" {
			label, err := walk.NewTextLabel(card)
			if err == nil {
				label.SetContextMenu(card.ContextMenu())
				label.SetText(clippedText(embed.Description, 400))
			}
		}
		media := embed.Image
		if media == nil {
			media = embed.Thumbnail
		}
		// Prefer an actual GIF source over a static poster when supplied.
		if mediaIsGIF(embed.Thumbnail) {
			media = embed.Thumbnail
		}
		if mediaIsGIF(embed.Video) || mediaIsVideo(embed.Video) || (strings.EqualFold(embed.Type, "gifv") && embed.Video != nil) {
			media = embed.Video
		}
		if media == nil {
			continue
		}
		if !gifsource.AllowedMedia(media.ProxyURL) && !gifsource.AllowedMedia(media.URL) && !(a.opt.Demo && (media.URL == "demo://sample.gif" || media.URL == "demo://sample.mp4")) {
			continue
		}
		contentType := "image/unknown"
		if mediaIsGIF(media) {
			contentType = "image/gif"
		}
		if media == embed.Video && (mediaIsVideo(media) || strings.EqualFold(embed.Type, "gifv")) {
			contentType = "video/mp4"
		}
		id := fmt.Sprintf("embed-%x", sha256.Sum256([]byte(media.URL+"\x00"+media.ProxyURL)))
		a.addMediaPreview(card, discord.Attachment{ID: id, URL: media.URL, ProxyURL: media.ProxyURL, Width: media.Width, Height: media.Height, ContentType: contentType}, count, ctx, gen)
	}
}
func mediaIsVideo(m *discord.EmbedMedia) bool {
	return m != nil && (gifsource.VideoURL(m.URL) || gifsource.VideoURL(m.ProxyURL) || strings.EqualFold(m.ContentType, "video/mp4"))
}
func mediaIsGIF(m *discord.EmbedMedia) bool {
	return m != nil && isGIF(discord.Attachment{URL: m.URL, ProxyURL: m.ProxyURL})
}
