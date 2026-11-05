//go:build windows

package ui

import (
	"context"
	"time"

	"discrud/internal/discord"
	"discrud/internal/netdebug"
	"discrud/internal/storage"
)

func (a *App) metadataTTL() time.Duration {
	ttl := time.Duration(a.settings.CacheTTLHours * float64(time.Hour))
	if ttl > storage.MetadataTTL {
		ttl = storage.MetadataTTL
	}
	return ttl
}
func (a *App) rebuildServers() {
	g := a.current.GuildID
	if a.current.ID == "" && a.settings.LastOwner == a.me.ID {
		g = a.settings.LastGuild
	}
	if g == "" {
		g = a.selectedGuild()
	}
	names := []string{"Direct messages"}
	selected := 0
	for i, guild := range a.guilds {
		names = append(names, guild.Name)
		if guild.ID == g {
			selected = i + 1
		}
	}
	a.navUpdating = true
	a.servers.SetModel(names)
	a.servers.SetCurrentIndex(selected)
	a.navUpdating = false
	a.updateNav()
}
func (a *App) restoreStartup(s storage.StartupSnapshot) {
	a.guilds = s.Guilds
	a.guildsAt = s.GuildsAt
	a.dmsAt = s.DMsAt
	a.channelsAt = s.ChannelsAt
	if a.channelsAt == nil {
		a.channelsAt = map[string]time.Time{}
	}
	a.rolesAt = s.RolesAt
	if a.rolesAt == nil {
		a.rolesAt = map[string]time.Time{}
	}
	if s.Roles != nil {
		a.roles = s.Roles
	}
	if s.MyRoles != nil {
		a.myRoles = s.MyRoles
	}
	for _, c := range s.Channels {
		a.rememberChannel(c)
	}
	for g, at := range a.channelsAt {
		a.guildLoaded[g] = storage.Fresh(at, a.metadataTTL())
	}
	var last discord.Channel
	if a.settings.LastOwner == a.me.ID && a.settings.LastChannel != "" {
		last = a.channels[a.settings.LastChannel]
		if last.ID == "" && s.LastChannel.ID == a.settings.LastChannel {
			last = s.LastChannel
		}
		if last.ID == "" {
			last = discord.Channel{ID: a.settings.LastChannel, GuildID: a.settings.LastGuild, Name: "Saved conversation", Type: 1}
			if last.GuildID != "" {
				last.Type = 0
			}
		}
	}
	if last.ID != "" {
		a.rememberChannel(last)
		if last.GuildID != "" {
			found := false
			for _, g := range a.guilds {
				if g.ID == last.GuildID {
					found = true
				}
			}
			if !found {
				a.guilds = append(a.guilds, discord.Guild{ID: last.GuildID, Name: "Saved server"})
			}
		}
	}
	a.rebuildServers()
	if s.Position.Channel == last.ID && !s.Position.FollowLatest {
		position := s.Position
		a.restorePosition = &position
	}
	if last.ID != "" {
		a.openChannel(last)
	}
}
func (a *App) refreshNavigation() {
	backend := a.backend
	if !storage.Fresh(a.guildsAt, a.metadataTTL()) {
		a.task(func(ctx context.Context) func() {
			guilds, err := backend.Guilds(ctx)
			return func() {
				if err != nil {
					if a.current.ID == "" {
						a.showError(err)
					}
					return
				}
				a.guilds = guilds
				a.guildsAt = time.Now()
				a.rebuildServers()
				a.startupDirty = true
				a.chooseInitialChannel()
				netdebug.Startup("navigation_ready", a.startedAt, len(guilds))
			}
		})
	}
	if !storage.Fresh(a.dmsAt, a.metadataTTL()) {
		a.task(func(ctx context.Context) func() {
			dms, err := backend.DMs(ctx)
			return func() {
				a.dmsDone = true
				if err != nil {
					if a.current.ID == "" {
						a.showError(err)
					}
					a.chooseInitialChannel()
					return
				}
				// Replace DM metadata without touching other guilds or fetching their messages.
				for id, c := range a.channels {
					if c.GuildID == "" {
						delete(a.channels, id)
					}
				}
				for _, c := range dms {
					a.rememberChannel(c)
				}
				a.dmsAt = time.Now()
				a.startupDirty = true
				a.updateNav()
				a.chooseInitialChannel()
			}
		})
	} else {
		a.dmsDone = true
		a.chooseInitialChannel()
	}
	// Only metadata for the selected/browsed guild; never prefetch other chats.
	if a.current.GuildID != "" {
		a.serverChanged()
		a.loadGuildRoles(a.current.GuildID)
	}
}
func (a *App) chooseInitialChannel() {
	if a.current.ID != "" {
		return
	}
	for _, row := range a.rows {
		if row.channel.GuildID == "" && row.channel.Supported() {
			a.openChannel(row.channel)
			return
		}
	}
	if a.dmsDone && len(a.guilds) > 0 {
		a.servers.SetCurrentIndex(1)
		a.serverChanged()
	}
}
func (a *App) snapshotStartup() storage.StartupSnapshot {
	s := storage.StartupSnapshot{Owner: a.me.ID, LastChannel: a.current, Position: a.captureReadingPosition(), Guilds: a.guilds, GuildsAt: a.guildsAt, DMsAt: a.dmsAt, ChannelsAt: a.channelsAt, RolesAt: a.rolesAt, Roles: a.roles, MyRoles: a.myRoles}
	for _, c := range a.channels {
		s.Channels = append(s.Channels, c)
	}
	return s
}

// Captured/encoded on the UI thread; fsync happens on a worker. A single
// saver prevents an older snapshot overtaking a newer snapshot at shutdown.
func (a *App) saveStartup() {
	if a.me.ID == "" || (a.suppressStartupSave && !a.startupDirty) {
		return
	}
	a.suppressStartupSave = false
	s, dir := a.snapshotStartup(), a.dir
	// Serialize a small bounded envelope before handing immutable bytes to the
	// writer, avoiding races with UI-owned maps and native controls.
	a.startupSaver.Save(dir, s)
	a.savedPosition = s.Position
	a.startupDirty = false
}
func (a *App) loadGuildRoles(g string) {
	if g == "" || a.rolesLoading[g] || storage.Fresh(a.rolesAt[g], a.metadataTTL()) {
		return
	}
	a.rolesLoading[g] = true
	backend := a.backend
	a.task(func(ctx context.Context) func() {
		// Metadata is optional and comes after the chat restore; no dependency on it
		// for fetching/rendering the selected conversation.
		roles, err := backend.Roles(ctx, g)
		if ctx.Err() != nil {
			return nil
		}
		mine, mineErr := backend.MyRoles(ctx, g)
		return func() {
			delete(a.rolesLoading, g)
			if err == nil {
				a.roles[g] = map[string]string{}
				for _, r := range roles {
					if r.ID != g {
						a.roles[g][r.ID] = r.Name
					}
				}
			}
			if mineErr == nil {
				a.myRoles[g] = map[string]bool{}
				for _, r := range mine {
					a.myRoles[g][r] = true
				}
			}
			if err == nil && mineErr == nil {
				a.rolesAt[g] = time.Now()
				a.startupDirty = true
			}
			a.updateSuggestions()
			netdebug.Startup("metadata_ready", a.startedAt, len(roles))
		}
	})
}
