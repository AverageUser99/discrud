"""Measure this build on an offline, 50-message demo workload. No account accessed."""
import ctypes as c
from ctypes import wintypes as w
import json
import pathlib
import subprocess
import tempfile
import time

root = pathlib.Path(__file__).resolve().parent.parent
kernel, psapi, user = c.windll.kernel32, c.windll.psapi, c.windll.user32

class Memory(c.Structure):
    _fields_ = [("cb", w.DWORD), ("PageFaultCount", w.DWORD)] + [(name, c.c_size_t) for name in (
        "PeakWorkingSetSize", "WorkingSetSize", "QuotaPeakPagedPoolUsage", "QuotaPagedPoolUsage",
        "QuotaPeakNonPagedPoolUsage", "QuotaNonPagedPoolUsage", "PagefileUsage", "PeakPagefileUsage", "PrivateUsage")]

kernel.GetProcessTimes.argtypes = [w.HANDLE] + [c.POINTER(w.FILETIME)] * 4
psapi.GetProcessMemoryInfo.argtypes = [w.HANDLE, c.POINTER(Memory), w.DWORD]
user.PostMessageW.argtypes = [w.HWND, w.UINT, w.WPARAM, w.LPARAM]
user.GetWindowThreadProcessId.argtypes = [w.HWND, c.POINTER(w.DWORD)]

def cpu_seconds(handle):
    values = [w.FILETIME() for _ in range(4)]
    if not kernel.GetProcessTimes(handle, *(c.byref(v) for v in values)):
        raise c.WinError()
    return sum((v.dwHighDateTime << 32) | v.dwLowDateTime for v in values[2:]) / 10_000_000

with tempfile.TemporaryDirectory(prefix="Discrud-benchmark-") as data_dir:
    proc = subprocess.Popen([str(root / "dist" / "discrud.exe"), "--demo", "--data-dir", data_dir])
    try:
        time.sleep(8)  # Allow the initial cache flush and native layout to settle.
        if proc.poll() is not None:
            raise RuntimeError("Application exited before benchmark")
        handle = w.HANDLE(int(proc._handle))
        start = time.monotonic()
        before = cpu_seconds(handle)
        time.sleep(10)
        after = cpu_seconds(handle)
        elapsed = time.monotonic() - start
        memory = Memory(cb=c.sizeof(Memory))
        if not psapi.GetProcessMemoryInfo(handle, c.byref(memory), memory.cb):
            raise c.WinError()
        result = {
            "workload": "offline demo, initial 50-message server history, no interaction during sample",
            "sample_seconds": round(elapsed, 2),
            "idle_cpu_percent_one_core": round((after-before)/elapsed*100, 3),
            "working_set_mib": round(memory.WorkingSetSize/1048576, 2),
            "private_bytes_mib": round(memory.PrivateUsage/1048576, 2),
            "peak_working_set_mib": round(memory.PeakWorkingSetSize/1048576, 2),
            "executable_mib": round((root/"dist/discrud.exe").stat().st_size/1048576, 2),
        }
        (root/"dist/benchmark.json").write_text(json.dumps(result, indent=2))
        print(json.dumps(result, indent=2))
        callback_type = c.WINFUNCTYPE(w.BOOL,w.HWND,w.LPARAM)
        @callback_type
        def close_own_window(hwnd,param):
            pid=w.DWORD()
            user.GetWindowThreadProcessId(hwnd,c.byref(pid))
            if pid.value==proc.pid and user.IsWindowVisible(hwnd):
                user.PostMessageW(hwnd,0x0010,0,0)
            return True
        user.EnumWindows(close_own_window,0)
        proc.wait(timeout=10)
    finally:
        if proc.poll() is None:
            proc.terminate()
            proc.wait(timeout=5)
