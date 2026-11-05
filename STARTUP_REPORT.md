# Startup optimization / live profiling

Measured on the development Windows machine on 2026-10-28, using the real saved-session `dist/discrud.exe --debug`. Live profiling was read-only: account/navigation/history GETs and thumbnail downloads only. No automated live sends, edits, deletions, or login attempts were performed. The separate native smoke test used the isolated offline backend.

## Before

`dist/before-1.log`:

- Account -> guild list -> DM list -> channels -> roles -> membership were serialized before opening the saved conversation.
- First thumbnail requests started about 2.14 seconds after diagnostics enabled; selected history returned around 2.91 seconds. The old build did not have first-render/layout milestones, so these are network milestones, not exact first-paint measurements.
- 23 HTTP attempts + 1 Gateway handshake. Six account/navigation/role reads, one history read, and 16 media reads.
- Many successful HTTP 200 image responses could not be decoded as WebP, so their downloads did not produce reusable thumbnails.

## Implemented

- Validate the account first, then restore local metadata/messages immediately. Network metadata is not on the warm startup critical path.
- Restore the message anchor/pixel offset, with a nearest-message fallback. Initially build only 30 native cards around that anchor or the latest messages; explicit history pagination still supports up to 200.
- Label cached content while syncing. Normally do one authoritative recent-page fetch after READY. A bounded REST-only fallback is available if the Gateway is unavailable; a late READY can legitimately require another gap-closing refresh.
- Cache navigation/role metadata for 15 minutes or configured retention, whichever is shorter; refresh stale independent metadata asynchronously, only for the selected/browsed guild.
- Load only visible/near-visible thumbnail slots using two workers, single-flight fetches, a 32 MiB/account PNG disk cache and at most 16 decoded images. Add pure-Go WebP decoding. Preserve the existing content/host/response-size/dimension checks.
- Reuse unchanged native RichEdit controls and reconcile changed cards rather than rebuilding the entire timeline. Compare timestamps by instant, not internal time-zone representation: cache JSON's `Z` and Discord's `+00:00` were falsely making every message look changed.
- Keep message/thumbnail/startup caches bounded and account-scoped. Disk snapshot writes are atomic, serialized, and coalesced; reading the cache does not renew server metadata or message fetch ages.
- HTTP 403/404 clears the selected cached view; successful history responses cannot hide a failed Gateway connection.

## Measured after

| Run | Cached controls ready | Cached layout/scroll complete | Fresh controls ready | Fresh layout/scroll complete | HTTP attempts | WS handshakes |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `verified-1.log` (metadata expired, 55 s observation) | 558 ms | 1,091 ms | 2,070 ms | 2,186 ms | 7 | 1 |
| `warm-1.log` (metadata hit, 10 s observation) | 970 ms | 1,501 ms | 2,831 ms | 2,947 ms | 2 | 1 |
| `release-1.log` (final binary, one new visible thumbnail) | 489 ms | 1,263 ms | 1,902 ms | 2,029 ms | 3 | 1 |

These are sampled wall-clock measurements, not latency guarantees. Account/network latency varies, and `*_presented` is an application layout/scroll milestone, not an OS-compositor timestamp. The warm sample had a slower account response/validation phase despite making fewer requests.

- The final binary's `release-1.log` used **account validation + one selected-chat history request + one successful WebP thumbnail cache miss**. Its history refresh reused controls in 19 ms, and intentional shutdown was logged as `cause=cancelled`.
- Warm startup used only **account validation + one selected-chat history request**. No guild/DM/channel/role/membership or media GETs in that sample. This was 2 versus the baseline's 23 HTTP attempts (about 91% fewer for this workload); future new visible attachments or expired metadata legitimately add requests.
- The 55-second run refreshed expired metadata in parallel, then its HTTP count stayed at **7** through the rest of the observation. One Gateway connection remained open and a scheduled heartbeat received an ACK. No reconnect loop occurred. The final close was our profiler's intentional shutdown.
- Earlier WebP-cache verification in `optimized-2.log` recorded **3 thumbnail hits, 0 misses, and 0 media GETs**. The later saved viewport had no visible image slots, so zero hit/miss counters there are expected.
- Before semantic timestamp comparison, sampled recent-page renders took approximately **520–871 ms** rebuilding/reconciling a 50-message page. After comparison/control reuse, sampled refresh renders were **10–28 ms**. Initial 30-card construction in the verified sample took **76 ms**.
- The account's READY payload was still around **5.7 MB**. This remains a server-controlled fresh-session cost, but no longer blocks cached chat display. No speculative Gateway capabilities, extra REST polling, or persistent session-resume credentials were added to reduce it.
- Built GUI executable: **11.77 MiB**. No browser runtime, database service, or new background process is required. Logs remain sanitized and opt-in.

## Checks

- `python scripts/build.py --test`: tests, vet, manifest, GUI build, third-party notices.
- `python scripts/smoke.py --debug --resume`: two isolated executable runs, cached conversation restored on restart, offline network counters zero; navigation/composer/tray checks; reading anchor + pixel offset restored; unchanged cards survive appends/edits; reordered cards remain visible and laid out; terminal connection error remains visible.
- `go test -race ./...`: passed across all packages with the local toolchain.
- Live profiling: existing message cache with 862 retained entries across visited conversations; only the saved selected chat was refreshed. No cache/message contents or account identifiers are reproduced here.

## Reproduce

Close existing instances first. The profiler refuses to terminate or interfere with another instance.

```powershell
python scripts/build.py --test
python scripts/profile_startup.py --label startup --seconds 20 --runs 2
# Completely isolated, offline profiling:
python scripts/profile_startup.py --demo --label demo-startup --seconds 10 --runs 2
# Normal manual use:
.\dist\discrud.exe --debug
```

The profiler gracefully stops only the process it launched. Read `dist/startup-1.log` and `dist/startup-2.log`; do not share saved credentials, cache files, message contents, or login QR images.
