#!/usr/bin/env python3
"""Build six standalone binaries and deterministic archives, never publish."""
import gzip
import hashlib
import io
import os
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parent.parent


def main():
    version = sys.argv[1] if len(sys.argv) == 2 else ""
    if not re.fullmatch(r"v\d+\.\d+\.\d+(?:-[a-zA-Z0-9.-]+)?", version):
        raise SystemExit("Usage: python3 scripts/release.py v0.1.0")
    go = os.environ.get("GO_BINARY", "go")
    dist = ROOT / "dist" / version
    dist.mkdir(parents=True, exist_ok=True)
    sums = []
    for target_os in ("darwin", "linux", "windows"):
        for arch in ("amd64", "arm64"):
            name = "playops.exe" if target_os == "windows" else "playops"
            binary = dist / f"{target_os}-{arch}" / name
            binary.parent.mkdir(parents=True, exist_ok=True)
            env = dict(os.environ, CGO_ENABLED="0", GOOS=target_os, GOARCH=arch)
            print(f"Building {target_os}/{arch}", flush=True)
            subprocess.run(
                [go, "build", "-buildvcs=false", "-trimpath", "-ldflags",
                 f"-s -w -X playops/internal/cli.Version={version}",
                 "-o", str(binary), "./cmd/playops"],
                cwd=ROOT, env=env, check=True,
            )
            files = {name: binary.read_bytes(), "LICENSE": (ROOT / "LICENSE").read_bytes(),
                     "README.md": (ROOT / "README.md").read_bytes()}
            for doc in sorted((ROOT / "docs").rglob("*")):
                if doc.is_file():
                    files[doc.relative_to(ROOT).as_posix()] = doc.read_bytes()
            stem = f"playops_{version}_{target_os}_{arch}"
            if target_os == "windows":
                archive = dist / f"{stem}.zip"
                with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED) as output:
                    for filename, data in files.items():
                        entry = zipfile.ZipInfo(filename, date_time=(1980, 1, 1, 0, 0, 0))
                        entry.external_attr = (0o755 if filename == name else 0o644) << 16
                        entry.compress_type = zipfile.ZIP_DEFLATED
                        output.writestr(entry, data)
            else:
                archive = dist / f"{stem}.tar.gz"
                with archive.open("wb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", filename="", mtime=0) as compressed:
                    with tarfile.open(fileobj=compressed, mode="w") as output:
                        for filename, data in files.items():
                            entry = tarfile.TarInfo(filename)
                            entry.size = len(data)
                            entry.mode = 0o755 if filename == name else 0o644
                            entry.mtime = 0
                            output.addfile(entry, io.BytesIO(data))
            sums.append(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
    (dist / "SHA256SUMS").write_text("".join(sorted(sums)), encoding="utf-8")
    print(f"Archives and SHA256SUMS: {dist}", flush=True)


if __name__ == "__main__":
    main()
