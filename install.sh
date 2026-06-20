#!/bin/sh
# seek installer — downloads the prebuilt release binary for your platform.
#
#   curl -fsSL https://raw.githubusercontent.com/Rishang/seek/main/install.sh | sh
#
# Env overrides:
#   SEEK_VERSION   release tag to install (default: latest, e.g. v0.1.0)
#   SEEK_BIN_DIR   install directory (default: /usr/local/bin, else ~/.local/bin)
#   SEEK_REPO      owner/repo (default: Rishang/seek)
#
# POSIX sh, no bashisms — runs under dash/ash/busybox.
set -eu

REPO="${SEEK_REPO:-Rishang/seek}"
VERSION="${SEEK_VERSION:-latest}"

say()  { printf '%s\n' "seek-install: $*"; }
err()  { printf '%s\n' "seek-install: error: $*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

# --- pick a downloader ----------------------------------------------------
if have curl; then
  dl()    { curl -fsSL "$1" -o "$2"; }
  dltext(){ curl -fsSL "$1"; }
elif have wget; then
  dl()    { wget -qO "$2" "$1"; }
  dltext(){ wget -qO- "$1"; }
else
  err "need curl or wget on PATH"
fi

# --- detect platform (supported devices, must match release.yml matrix) ---
os=$(uname -s)
case "$os" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) err "unsupported OS '$os'. Windows: download the .zip from the releases page, or build from source (see README)." ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)        ARCH=amd64 ;;
  aarch64|arm64)       ARCH=arm64 ;;
  *) err "unsupported architecture '$arch' (supported: amd64, arm64)" ;;
esac

# --- resolve version ------------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  say "resolving latest release of $REPO ..."
  VERSION=$(dltext "https://api.github.com/repos/$REPO/releases/latest" \
    | grep -m1 '"tag_name"' \
    | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/')
  [ -n "$VERSION" ] || err "could not resolve latest release tag (no releases yet?)"
fi
say "installing seek $VERSION ($OS/$ARCH)"

# --- download + extract ---------------------------------------------------
# Asset name and inner binary name mirror .github/workflows/release.yml:
#   archive: seek-<tag>-<os>-<arch>.tar.gz   inner binary: seek-<os>-<arch>
asset="seek-${VERSION}-${OS}-${ARCH}.tar.gz"
binname="seek-${OS}-${ARCH}"
base="https://github.com/$REPO/releases/download/$VERSION"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

say "downloading $asset"
dl "$base/$asset" "$tmp/$asset" || err "download failed: $base/$asset"

# ponytail: the release workflow ships no checksums.txt, so there is nothing to
# verify against — GitHub serves the asset over TLS. Add a checksums step to
# release.yml (and verification here) if you want end-to-end integrity.

tar -xzf "$tmp/$asset" -C "$tmp" || err "extract failed"
[ -f "$tmp/$binname" ] || err "archive did not contain '$binname'"
chmod +x "$tmp/$binname"

# --- choose install dir ---------------------------------------------------
if [ -n "${SEEK_BIN_DIR:-}" ]; then
  BIN_DIR="$SEEK_BIN_DIR"
elif [ -w /usr/local/bin ] 2>/dev/null; then
  BIN_DIR=/usr/local/bin
else
  BIN_DIR="$HOME/.local/bin"
fi
mkdir -p "$BIN_DIR"

if mv "$tmp/$binname" "$BIN_DIR/seek" 2>/dev/null; then
  :
elif have sudo && [ "$BIN_DIR" = /usr/local/bin ]; then
  say "writing to $BIN_DIR via sudo"
  sudo mv "$tmp/$binname" "$BIN_DIR/seek"
else
  err "cannot write to $BIN_DIR (set SEEK_BIN_DIR to a writable dir)"
fi

say "installed seek to $BIN_DIR/seek"
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) say "note: $BIN_DIR is not on your PATH — add it: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac
say "run: seek --help"
