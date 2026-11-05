# Testing the first MVP

## Automated checks

`python scripts/build.py --test` runs tests and vet, embeds the Windows manifest, and produces a stripped GUI executable.

Coverage includes:
- Remote-auth RSA/OAEP exchange, nonce proof, fingerprint verification, ticket decryption, cancellation.
- Manual token normalization, read-only identity validation, bot rejection, cancelled sign-in, and credential-save failures without overwriting a prior session.
- Local gateway identify, dispatch, heartbeat, resume, reconnect request, invalid hello bounds.
- Selected-channel message filtering (including edits/deletes), background sequence tracking, opcode 37 subscription replacement/clearing on guild/DM switches, and no duplicate/idle subscription packets.
- Subscription contract checks distinguish the guild message-delivery gate (`typing`) from member-list ranges (`channels`): active guild gate enabled, roster/member/thread lists empty, no per-chat subscription packet within a guild. Active messages and scheduled heartbeat ACKs after subscribing; short-lived READY backoff; protocol rejection stops retries; numeric close-code diagnostics without leaking server reasons; HTTP 401 is correctly flagged as a failure. These are local protocol checks, not proof that Discord accepts the payload.
- Message payloads, multipart uploads, redirect credential isolation, sanitized errors, rate limits without automatic sends.
- Partial message updates, deduplication, UTF-16 mention offsets, RTF escaping/basic formatting.
- Cache TTL, ownership isolation, eviction, deletion, clearing, concurrent access, settings validation. Reads remain responsive during a blocked disk flush; concurrent updates persist and Clear cannot be resurrected by an older flush. Contiguous history-page persistence across two channels/restart; missing/expired/deleted page members force REST rather than skipping a gap; recently fetched deep history survives per-channel eviction; empty page metadata obeys the file budget.
- Isolated Windows Credential Manager round trip, never the real session entry.
- Media host/scheme restrictions, WebP decoder registration, GIF source classification, and local demo operations. GIF block preflight rejects excessive downloads/canvases/frame counts/total decoded pixels before DecodeAll; malformed/truncated inputs, partial frames, transparency, background/previous disposal, timing clamps, finite/infinite looping, shared budget reclamation and zero steady-state compositor allocations are covered. Embed-only updates and message cache round trips preserve previews.
- Account/TTL/size isolation of startup snapshots; coalesced latest-write persistence and clear invalidation. Thumbnail disk/memory bounds, account isolation, expiry, cancellation and in-flight clear; recent-page deletion reconciliation and semantic cached timestamp comparison.
- Cached initial-window bounds, nearest-message fallback, preserved reading anchor/pixel offset, incremental edits retaining unrelated controls, and visibility/layout after native-card reordering.
- Debug HTTP success/failure/concurrency counts, unchanged request/response bodies, WebSocket handshake/message counters, and sanitized logs that exclude credentials, URL secrets and message content.
- HTTP identity headers consistently identify Discrud rather than an official browser/build.

For live diagnostics, launch `dist/discrud.exe --debug` (optional `--debug-log PATH`). Confirm the console and local log show five-second cumulative totals. Idle HTTP counts should stay flat after loading; WebSocket heartbeat/push counts may grow. Navigate/send/load history to see the corresponding categories. `web_requests` includes handshake attempts; WebSocket messages are counted separately. Payload byte counts are not full wire bandwidth. Logs are overwritten each debug run. Without `--debug`, no debug console/log is created.

`python scripts/smoke.py` launches the actual native UI with an isolated offline data directory. It checks:
- Real Win32 scroll-triggered history loading with an injected 150 ms older-request delay, duplicate-scroll single-flight protection, unchanged card handles and exact anchor/pixel preservation.
- Ten rapid A/B channel transitions retain the same loaded messages, native feed/card handles and reading position, without waiting for refresh; timing is recorded in the report.
- A synthetic 650-message channel exercises the sliding window beyond 200 messages, reuse of hidden cached page prefixes, pixel-stable prepends and refreshes without splicing disjoint history ranges.
- Native GIF loading from a standalone Klipy sharing link with no embed via an offline page/metadata/GIF transport, recovery from a failed request via the native retry click handler, actual hover-triggered timer-driven animation/painting, no playback before hover, pause on mouse leave and resume on re-entry, bitmap reuse and unchanged layout, offscreen/hidden/minimized pause, resume and bitmap/context/budget cleanup.
- Sending via the composer's native Enter handler.
- Non-empty RichEdit text and a non-collapsed history viewport.
- Mention suggestion insertion and background message rejection without cache growth.
- Terminal live-connection errors remain visible when ordinary request errors are cleared; successful history loading must not be treated as a healthy Gateway connection.
- Tray close/hide and restore.
- Native token-field masking and clearing on cancel, using a synthetic value only; no live token or credential-store access.
- No visible application error.

`python scripts/smoke.py --debug --resume` additionally restarts against the same isolated cache, verifies cached conversation restoration, reading anchor/offset restoration, and native card visibility/layout. It remains entirely offline.

The smoke test is not pixel/visual validation. It writes `dist/smoke.json` and exits. `python scripts/smoke.py --debug` additionally launches the debug flag and verifies that the offline demo's network counters remain zero in `dist/network-debug-smoke.log`.

`python scripts/benchmark.py` measures an offline server-history workload and writes `dist/benchmark.json`. These measurements are not a promise about live-account performance.

## Read-only startup profiling

`python scripts/profile_startup.py --label startup --seconds 20 --runs 2` launches the real saved-session executable with `--debug`, then gracefully closes only its own process. It refuses to run beside another Discrud instance. Live mode performs account/navigation/history reads and thumbnail GETs; it never invokes smoke sends or message mutations. Use `--demo` for isolated offline runs. Logs are under `dist/<label>-<run>.log`; inspect those logs rather than tokens/cache contents.

Compare the first run after cache/metadata expiry with the next warm run. Verify: cached rendering does not wait for guild/role requests, usually one selected-history request after READY, metadata hits skip their REST calls, visible thumbnail disk hits cause no GET, API totals stay flat while idle, and heartbeat ACKs keep arriving. `cached_messages` means controls built; `cached_presented` includes deferred layout/scroll completion.

## Manual offline checks

Launch `dist/Demo.cmd`.

- [ ] Layout is readable; resize and toggle Browse without hidden/overlapping controls.
- [ ] Browse the demo server, DM, and group DM.
- [ ] Ctrl+K switches; Ctrl+L focuses the composer.
- [ ] Enter sends once; Shift+Enter makes a real newline. Type a longer multiline draft and resize the window; verify the composer stays wide and readable.
- [ ] Type @al, press Tab, and verify insertion. Try user, role, and channel suggestions.
- [ ] Right-click a message for Reply / Copy (and Edit / Delete on your own); verify actions are not shown as permanent buttons. Cancel editing.
- [ ] Select/copy text, read Unicode/basic formatting, click a normal link.
- [ ] See the sample image and the Klipy-link fixture's animated color bars (synthetic offline media, not the site's live GIF). Demo attachment links explain that they are placeholders. Hover to play and move away to pause on the current frame. Scroll the GIF away and back, switch channels, minimize, and restore; playback must remain hover-only without shifting messages.
- [ ] Choose a file/paste a clipboard image; verify attachment names before sending.
- [ ] Switching conversations discards unsent text/files; restarting restores no draft.
- [ ] Both close settings work; tray Open and Quit remain usable.

## Manual live-account checks

**Accept account risk first. Prefer a test account. Never post tokens, QR images, or private message content in bug reports.**

- [ ] QR appears; cancel on mobile, expire it, and retry.
- [ ] Scan and approve your own QR. If a challenge is required, verify a useful error rather than a bypass.
- [ ] Choose Use existing user token. Confirm masking, enter only your own raw token locally, and validate. Never include it in a screenshot or message.
- [ ] Try cancelling, an invalid value, and a bot-prefixed value; verify nothing is saved/replaced. A rejected real token must not silently fall back to another method.
- [ ] With a valid own-account token, verify identity validation succeeds and conversations load. Clear your clipboard after pasting.
- [ ] Restart and confirm the same session resumes without scanning or token entry again.
- [ ] Browse a server and permitted text channel; handle read-only/inaccessible channels gracefully.
- [ ] Read an existing one-to-one DM and an existing group DM.
- [ ] Send multiline text; observe exactly one message in the official client.
- [ ] Receive a new message, an edit, and a deletion from another client.
- [ ] Receive a GIF attachment and a GIF-bearing Discord embed, including a delayed embed-only update. Check transparency/partial frames and finite loops. Send just `https://klipy.com/gifs/scared-63`, then `https://klipy.com/gifs/travolta-network`. A preview slot must appear even without a Discord embed; provider metadata/GIF resolution runs lazily and the same hover player handles the result. Confirm a late embed does not duplicate the GIF, leaving the channel cancels pending work, and failed slots can be clicked to retry. If the server blocks the request or supplies no actual GIF, verify the retry/browser fallback rather than assuming offline fixture tests verified the real site.
- [ ] Reply; mention a user/role/channel. Confirm reply pings default to off, can be toggled with Ping author, and reset for a new reply. Mass mention parsing must stay disabled.
- [ ] Edit/delete your own messages. Ensure deletion requires confirmation.
- [ ] Upload a small file and a clipboard image. Open a received attachment externally.
- [ ] Receive messages/mentions/edits/deletions in an unselected channel: no badge, UI update, cache mutation, or history request. Switch to it and verify fresh history loads.
- [ ] Switch within a server, across servers, and to a DM; verify active messages still arrive, and previous guild subscriptions are cleared. Observe idle traffic: only required Gateway heartbeats, no periodic REST history requests.
- [ ] Scroll upward without clicking Older: history should arrive before the top edge without duplicates or a visible reading-position jump. Repeat beyond 200 messages; switch A → B → A and verify both loaded windows/offsets survive. Test slow/offline requests, retry after errors, end-of-history and Latest during an in-flight request.
- [ ] Disconnect the network, try a send, reconnect, and inspect the conversation before explicitly retrying.
- [ ] Verify reconnect/resume and catch-up behavior; check for gaps after a prolonged disconnect.
- [ ] Run `--debug`, leave one conversation selected for several minutes, and confirm no repeated READY/identify/resume-invalid loop. If disconnected, collect numeric close codes/cause and retry delays (no token or raw payload). Verify Ctrl+C exits instead of leaving the app reconnecting in the background.
- [ ] Set cache retention to one minute, wait for maintenance, restart, and verify expiration.
- [ ] Clear Cache and inspect messages.json removal; remember new traffic can refill it.
- [ ] Log out, confirm Credential Manager entry/cache removal, and reopen to the QR login screen.
- [ ] Measure resources with your actual server/DM workload.

## Reporting

Include the build date, demo/live mode, steps, expected/actual behavior, and a redacted screenshot if useful. Never include credentials, a login QR, or a raw cache file.
