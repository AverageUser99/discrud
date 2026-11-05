"""Read-only startup profiling of the actual GUI. Never sends chat messages.
Live mode uses the application's saved session; --demo uses isolated offline data.
Refuses to run beside an existing Discrud instance. Closes only the process it starts.
"""
import argparse
import csv
import ctypes
from ctypes import wintypes
import pathlib
import subprocess
import tempfile
import time

root = pathlib.Path(__file__).resolve().parent.parent
parser = argparse.ArgumentParser()
parser.add_argument("--demo", action="store_true")
parser.add_argument("--seconds", type=float, default=20)
parser.add_argument("--runs", type=int, default=1)
parser.add_argument("--label", default="startup")
args = parser.parse_args()
existing = subprocess.run(["tasklist", "/FI", "IMAGENAME eq discrud.exe", "/FO", "CSV", "/NH"], capture_output=True, text=True, check=True)
if any(row and row[0].lower() == "discrud.exe" for row in csv.reader(existing.stdout.splitlines())):
    raise SystemExit("Close existing Discrud instances first; profiling will not touch them.")
user32 = ctypes.WinDLL("user32", use_last_error=True)
callback_type = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)
user32.EnumWindows.argtypes = [callback_type, wintypes.LPARAM]
user32.GetWindowThreadProcessId.argtypes = [wintypes.HWND, ctypes.POINTER(wintypes.DWORD)]
user32.GetWindowThreadProcessId.restype = wintypes.DWORD
user32.PostThreadMessageW.argtypes = [wintypes.DWORD, wintypes.UINT, wintypes.WPARAM, wintypes.LPARAM]

def quit_gui(process):
    threads = set()
    @callback_type
    def visit(hwnd, _):
        pid = wintypes.DWORD()
        tid = user32.GetWindowThreadProcessId(hwnd, ctypes.byref(pid))
        if pid.value == process.pid:
            threads.add(tid)
        return True
    user32.EnumWindows(visit, 0)
    for tid in threads:
        user32.PostThreadMessageW(tid, 0x0012, 0, 0)  # WM_QUIT; Run() performs normal cleanup.
    try:
        process.wait(timeout=15)
    except subprocess.TimeoutExpired:
        process.terminate()
        process.wait(timeout=10)
        raise RuntimeError("GUI did not quit gracefully; profiling process terminated")

(root / "dist").mkdir(exist_ok=True)
with tempfile.TemporaryDirectory(prefix="Discrud-profile-") as demo_dir:
    for run in range(1, args.runs + 1):
        log = root / "dist" / f"{args.label}-{run}.log"
        command = [str(root / "dist" / "discrud.exe"), "--debug", "--debug-log", str(log)]
        if args.demo:
            command += ["--demo", "--data-dir", demo_dir]
        start = time.monotonic()
        process = subprocess.Popen(command, cwd=root)
        try:
            time.sleep(args.seconds)
        finally:
            if process.poll() is None:
                quit_gui(process)
        print(f"run={run} exit={process.returncode} elapsed_s={time.monotonic()-start:.2f} log={log}")
        if process.returncode:
            raise SystemExit(process.returncode)
