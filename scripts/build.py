"""Build the native Windows application. No Node, browser runtime, or C compiler needed."""
import argparse
import json
import os
import pathlib
import shutil
import subprocess

root = pathlib.Path(__file__).resolve().parent.parent
os.chdir(root)
parser = argparse.ArgumentParser()
parser.add_argument("--test", action="store_true")
parser.add_argument("--debug", action="store_true")
args = parser.parse_args()
local = root / ".tools" / "go" / "bin" / "go.exe"
go = str(local) if local.exists() else shutil.which("go")
if not go:
    raise SystemExit("Install Go 1.26+ or run: python scripts/bootstrap.py")

def run(*command):
    print("+", *command, flush=True)
    subprocess.run(command, check=True)

run(go, "fmt", "./...")
run(go, "mod", "tidy")
run(go, "run", "github.com/akavel/rsrc@v0.10.2", "-manifest", "packaging/app.manifest",
    "-arch", "amd64", "-o", "cmd/discrud/rsrc_windows_amd64.syso")
if args.test:
    run(go, "test", "./...")
    run(go, "vet", "./internal/discord", "./internal/storage", "./internal/presentation", "./internal/demo", "./internal/netdebug", "./internal/gifplayer", "./internal/gifsource")
    # RichEdit callbacks and COM vtables receive native, non-Go pointers.
    run(go, "vet", "-unsafeptr=false", "./internal/ui", "./internal/nativevideo", "./cmd/discrud")
(root / "dist").mkdir(exist_ok=True)
flags = "" if args.debug else "-s -w -H=windowsgui"
run(go, "build", "-trimpath", "-ldflags", flags, "-o", "dist/discrud.exe", "./cmd/discrud")
(root / "dist" / "Demo.cmd").write_text('@echo off\ncd /d "%~dp0"\nstart "" discrud.exe --demo\n')
# Binary redistribution notices; development toolchain/build tool are not shipped.
# Lazy module loading may leave transitive modules without a Dir until downloaded.
run(go, "mod", "download", "all")
modules = subprocess.check_output([go, "list", "-m", "-json", "all"], text=True)
decoder = json.JSONDecoder()
notices = ["Discrud third-party notices\n\n"]
while modules.strip():
    item, end = decoder.raw_decode(modules.lstrip())
    modules = modules.lstrip()[end:]
    if item.get("Main"):
        continue
    folder = pathlib.Path(item["Dir"])
    license_file = next((folder / name for name in ("LICENSE", "LICENSE.txt", "LICENSE.md") if (folder / name).exists()), None)
    if not license_file:
        raise RuntimeError(f"Missing license for {item['Path']}")
    notices.append(f"\n{'=' * 72}\n{item['Path']} {item.get('Version', '')}\n\n" + license_file.read_text(encoding="utf-8"))
goroot = pathlib.Path(subprocess.check_output([go, "env", "GOROOT"], text=True).strip())
notices.append("\n" + "=" * 72 + "\nGo runtime / standard library\n\n" + (goroot / "LICENSE").read_text(encoding="utf-8"))
(root / "dist" / "THIRD_PARTY_NOTICES.txt").write_text("".join(notices), encoding="utf-8")
print("Ready:", root / "dist" / "discrud.exe")
print("Executable size:", round((root / "dist" / "discrud.exe").stat().st_size / 1048576, 2), "MiB")
