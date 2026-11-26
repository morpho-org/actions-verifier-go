import base64
import hashlib
import json
import re
import subprocess
import tarfile
import tempfile
from pathlib import Path


ROOT = Path(__file__).resolve().parent
PACKAGE = r"(?:@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*"
LOCK_PATH = re.compile(rf"(?:node_modules/{PACKAGE}/)*node_modules/({PACKAGE})")
EXACT_VERSION = re.compile(r"[0-9]+\.[0-9]+\.[0-9]+")


def validate_members(archive):
    with tarfile.open(archive, "r:gz") as bundle:
        for member in bundle:
            parts = member.name.split("/")
            if (
                parts[0] != "package"
                or any(part in {"", ".", ".."} for part in parts)
                or "node_modules" in parts[1:]
                or member.type not in {tarfile.REGTYPE, tarfile.AREGTYPE, tarfile.DIRTYPE}
                or member.issparse()
            ):
                raise ValueError("unsafe member in a locked registry tarball")


def install_package(path, entry, archive):
    matched = LOCK_PATH.fullmatch(path)
    version = entry["version"]
    if not matched or not EXACT_VERSION.fullmatch(version):
        raise ValueError("invalid package path or version in lockfile")
    name = matched[1]
    filename = name.rsplit("/", 1)[-1]
    url = f"https://registry.npmjs.org/{name}/-/{filename}-{version}.tgz"
    if entry["resolved"] != url:
        raise ValueError(f"unexpected registry URL for {name}@{version}")

    subprocess.run(
        ["curl", "--disable", "--fail", "--silent", "--show-error", "--retry", "2",
         "--proto", "=https", "--output", archive, url],
        check=True,
    )
    with archive.open("rb") as stream:
        digest = hashlib.file_digest(stream, "sha512").digest()
    if entry["integrity"] != "sha512-" + base64.b64encode(digest).decode("ascii"):
        raise ValueError(f"SHA-512 integrity mismatch for {name}@{version}")
    validate_members(archive)

    destination = ROOT / path
    destination.mkdir(parents=True)
    subprocess.run(
        ["tar", "--extract", "--gzip", "--file", archive, "--directory", destination,
         "--strip-components=1", "--no-same-owner", "--no-same-permissions", "--keep-old-files"],
        check=True,
    )


def main():
    manifest = json.loads((ROOT / "package.json").read_text(encoding="utf-8"))
    lockfile = json.loads((ROOT / "package-lock.json").read_text(encoding="utf-8"))
    if lockfile["lockfileVersion"] != 3:
        raise ValueError("expected an npm v3 lockfile")
    packages = lockfile["packages"]
    dependencies = manifest["dependencies"]
    if not dependencies or dependencies != packages[""]["dependencies"]:
        raise ValueError("package.json dependencies do not match the lockfile root")
    for name, version in dependencies.items():
        if not EXACT_VERSION.fullmatch(version):
            raise ValueError(f"package.json must pin an exact version for {name}")
        if packages.get(f"node_modules/{name}", {}).get("version") != version:
            raise ValueError(f"package.json version for {name} does not match its locked package entry")

    entries = {path: entry for path, entry in packages.items() if path}
    if not entries:
        raise ValueError("lockfile contains no registry tarballs")

    (ROOT / "node_modules").mkdir()
    with tempfile.TemporaryDirectory() as scratch:
        archive = Path(scratch) / "package.tgz"
        for path, entry in sorted(entries.items(), key=lambda item: item[0].count("/")):
            install_package(path, entry, archive)
    print(f"Verified and extracted {len(entries)} locked registry tarballs")


if __name__ == "__main__":
    main()
