"""Install a checksum-verified, project-local Go toolchain (no system changes)."""
import hashlib
import pathlib
import urllib.request
import zipfile

VERSION = "go1.27.3"
SHA256 = "f26081f438319757f8a1b46d7d0b2c5ec7583d4038e485e0aa725cfde7885fff"
root = pathlib.Path(__file__).resolve().parent.parent
folder = root / ".tools"
folder.mkdir(exist_ok=True)
archive = folder / "go.zip"
print("Downloading official Go Windows amd64 archive...", flush=True)
urllib.request.urlretrieve(f"https://go.dev/dl/{VERSION}.windows-amd64.zip", archive)
if hashlib.sha256(archive.read_bytes()).hexdigest() != SHA256:
    archive.unlink()
    raise RuntimeError("Go archive checksum mismatch")
with zipfile.ZipFile(archive) as bundle:
    bundle.extractall(folder)
archive.unlink()
print("Verified Go installed in .tools/go")
