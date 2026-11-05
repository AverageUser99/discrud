"""Exercise real Windows UI against the offline backend; no Discord credentials used."""
import argparse
import json
import pathlib
import subprocess
import tempfile

root = pathlib.Path(__file__).resolve().parent.parent
report = root / "dist" / "smoke.json"
report.unlink(missing_ok=True)
parser = argparse.ArgumentParser()
parser.add_argument("--debug", action="store_true", help="also verify the executable's network diagnostics")
parser.add_argument("--resume", action="store_true", help="restart against the same isolated cache and verify cached startup restore")
args = parser.parse_args()
debug_log = root / "dist" / "network-debug-smoke.log"
extra = []
if args.debug:
    debug_log.unlink(missing_ok=True)
    extra = ["--debug", "--debug-log", str(debug_log)]
def check_feed(data):
    for key in ("automatic_history_loaded", "history_prepend_preserved_card", "history_prepend_pixel_stable",
                "history_requests_single_flight", "channel_switch_immediate_messages_preserved",
                "channel_switch_native_feeds_preserved", "channel_switch_reading_position_preserved",
                "channel_native_cache_bounded", "deep_history_scrolls_past_200",
                "deep_history_pixel_stable", "deep_history_has_no_gaps", "deep_history_reuses_cached_prefixes",
                "deep_history_refresh_does_not_splice_gap"):
        assert data.get(key), f"feed regression: {key}"
    assert data["channel_switch_10_transitions_ms"] < 1000, "warm channel switching stalled the UI"

with tempfile.TemporaryDirectory(prefix="Discrud-smoke-") as data_dir:
    command = [str(root / "dist" / "discrud.exe"), "--demo", "--smoke", "--data-dir", data_dir, *extra]
    # Give the GUI-subsystem process valid diagnostic handles even when the
    # launching harness has no console; surface native panic output on failure.
    result = subprocess.run(command, cwd=root, timeout=40, capture_output=True, text=True)
    if args.resume and result.returncode == 0:
        check_feed(json.loads(report.read_text()))  # Verify the cold run, not just the restarted one.
        result = subprocess.run(command, cwd=root, timeout=40, capture_output=True, text=True)
if result.returncode:
    print(result.stdout)
    print(result.stderr)
    raise SystemExit(f"Native smoke process failed: {result.returncode}")
if not report.exists():
    raise SystemExit("Native smoke test did not write its report")
data = json.loads(report.read_text())
print(json.dumps(data, indent=2))
assert data["conversation"] == "general", "channel navigation failed"
if args.resume:
    assert data["startup_cached_restore"], "restart did not restore a cached conversation"
assert data["messages"] >= 65, "history pagination or sending failed"
check_feed(data)
for key in ("gif_native_loaded", "gif_native_timer_and_paint", "gif_reuses_bitmap_and_layout",
            "gif_offscreen_paused", "gif_resumes_when_visible", "gif_hidden_window_paused",
            "gif_minimized_paused", "gif_native_cleanup", "gif_idle_until_hover",
            "gif_mouse_leave_paused", "gif_hover_reentry_resumes", "gif_link_without_embed_resolved",
            "gif_click_retry_recovers"):
    assert data.get(key), f"native GIF regression: {key}"
for key in ("mp4_native_loaded", "mp4_hover_loop_and_bitmap_reuse", "mp4_mouse_leave_paused",
            "mp4_hover_resumes", "mp4_native_cleanup"):
    assert data.get(key), f"native MP4 regression: {key}"
assert data["mention_suggestions"] >= 1, "mention autocomplete failed"
assert not data["error_visible"], data["error"]
assert data["mention_inserted"], "mention interaction failed"
assert data["background_channel_ignored"], "background message was processed"
assert data["gateway_failure_stays_visible"], "connection failure was hidden by ordinary error clearing"
assert data["reading_position_restored"], "message anchor/pixel offset was not restored"
assert data["history_cards_have_layout"], "reconciled native cards became hidden or collapsed"
assert data["incremental_edit_preserved_other_cards"], "editing one message rebuilt unrelated native controls"
assert all(data.get(key) for key in ("reply_ping_default_off", "reply_ping_toggle_on", "reply_ping_toggle_off", "reply_ping_cleared")), "reply ping toggle failed"
assert data["history_bounds"]["Height"] >= 250, "history viewport collapsed"
assert data["editor_client_bounds"]["Width"] >= 320, "composer width collapsed"
assert data["editor_client_bounds"]["Height"] < 64, "empty composer is not compact"
for key in ("composer_compact", "composer_grows_for_newlines", "composer_growth_capped",
            "composer_shrinks_after_clear", "composer_reflows_on_resize"):
    assert data.get(key), f"composer sizing regression: {key}"
for size in ("narrow", "wide", "restored"):
    assert data[f"composer_{size}_typed_lines"] == 1, f"composer wraps prematurely at {size} width"
    assert data[f"composer_{size}_format_width"] >= data[f"composer_{size}_client_width"] - 32, f"composer text rectangle is stale at {size} width"
assert data["native_rich_text_characters"] > 0, "native RichEdit message is empty"
assert data.get("message_context_menu"), "message actions are not available through the context menu"
assert data.get("incremental_append_preserved_existing_card"), "new message rebuilt the existing timeline"
assert data.get("tray_close_hides") and data.get("tray_restore_visible"), "tray lifecycle failed"
assert data["token_dialog_ok"] and data["token_dialog_masked"] and data["token_dialog_cleared"], "manual token dialog masking/cancel failed"
if args.debug:
    network = debug_log.read_text()
    assert "enabled;" in network, "debug logging did not start"
    assert "totals web_requests=0 http_attempts=0 ws_handshakes=0 ws_rx_messages=0 ws_tx_messages=0" in network, "offline demo unexpectedly used the network"
    print("Network debug smoke passed: offline counters remained zero.")
print("Native UI smoke passed. Report: dist/smoke.json")
