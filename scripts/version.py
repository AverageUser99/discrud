"""Resolve a stable build version from Git tags; no source-file bumps needed."""
import argparse
import os
import pathlib
import re
import subprocess

STABLE = re.compile(r"v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)")
ROOT = pathlib.Path(__file__).resolve().parent.parent


def parse_tag(tag):
    match = STABLE.fullmatch(tag)
    return tuple(map(int, match.groups())) if match else None


def choose_version(tags, head_tags):
    versions = [version for tag in tags if (version := parse_tag(tag)) is not None]
    at_head = [version for tag in head_tags if (version := parse_tag(tag)) is not None]
    # Rebuilding a tagged commit must preserve its version, including CI retries.
    if at_head:
        return ".".join(map(str, max(at_head)))
    if not versions:
        return "0.1.0"
    major, minor, patch = max(versions)
    return f"{major}.{minor}.{patch + 1}"


def resolve_version():
    def git(*args):
        return subprocess.check_output(
            ["git", *args], cwd=ROOT, text=True, encoding="utf-8"
        ).splitlines()

    return choose_version(git("tag", "--list"), git("tag", "--points-at", "HEAD"))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--github-output", action="store_true")
    args = parser.parse_args()
    version = resolve_version()
    print(version)
    if args.github_output:
        with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
            output.write(f"version={version}\n")
