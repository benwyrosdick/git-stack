#!/usr/bin/env bash
# Install git-stack from GitHub Releases.
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/benwyrosdick/git-stack/main/scripts/install.sh | bash
set -euo pipefail

REPO="${GIT_STACK_REPO:-benwyrosdick/git-stack}"
BIN_NAME="git-stack"
INSTALL_DIR="${GIT_STACK_INSTALL_DIR:-${HOME}/.local/bin}"

die() { echo "git-stack install: $*" >&2; exit 1; }
info() { echo "git-stack install: $*" >&2; }

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "need '$1' on PATH"
}

need_cmd curl
need_cmd tar
need_cmd uname
need_cmd mktemp

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) die "unsupported architecture: $arch" ;;
esac
case "$os" in
  linux|darwin) ;;
  *) die "unsupported OS: $os" ;;
esac

# Resolve latest tag
if [[ -n "${GIT_STACK_VERSION:-}" ]]; then
  tag="$GIT_STACK_VERSION"
  [[ "$tag" == v* ]] || tag="v$tag"
else
  tag="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -1)"
  [[ -n "$tag" ]] || die "could not resolve latest release for ${REPO} (has a release been published?)"
fi

asset="${BIN_NAME}_${tag#v}_${os}_${arch}.tar.gz"
# GoReleaser default: project_version_os_arch
# Also try without 'v' stripped variants
url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
checksums_url="https://github.com/${REPO}/releases/download/${tag}/checksums.txt"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

info "downloading ${url}"
if ! curl -fsSL -o "$tmp/archive.tar.gz" "$url"; then
  # alternate asset name patterns
  for alt in \
    "${BIN_NAME}_${os}_${arch}.tar.gz" \
    "${BIN_NAME}_${tag}_${os}_${arch}.tar.gz"
  do
    url="https://github.com/${REPO}/releases/download/${tag}/${alt}"
    info "retry ${url}"
    if curl -fsSL -o "$tmp/archive.tar.gz" "$url"; then
      asset="$alt"
      break
    fi
  done
  [[ -f "$tmp/archive.tar.gz" && -s "$tmp/archive.tar.gz" ]] \
    || die "download failed; check that release ${tag} has a binary for ${os}/${arch}"
fi

if curl -fsSL -o "$tmp/checksums.txt" "$checksums_url" 2>/dev/null; then
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$tmp" && grep -E " ${asset}\$" checksums.txt | sha256sum -c -) \
      || die "checksum verification failed"
    info "checksum ok"
  elif command -v shasum >/dev/null 2>&1; then
    expect="$(grep -E " ${asset}\$" "$tmp/checksums.txt" | awk '{print $1}')"
    got="$(shasum -a 256 "$tmp/archive.tar.gz" | awk '{print $1}')"
    [[ "$expect" == "$got" ]] || die "checksum verification failed"
    info "checksum ok"
  fi
else
  info "no checksums.txt; skipping verify"
fi

tar -xzf "$tmp/archive.tar.gz" -C "$tmp"
bin_path=""
if [[ -x "$tmp/${BIN_NAME}" ]]; then
  bin_path="$tmp/${BIN_NAME}"
else
  bin_path="$(find "$tmp" -type f -name "$BIN_NAME" | head -1)"
fi
[[ -n "$bin_path" && -f "$bin_path" ]] || die "binary not found in archive"

mkdir -p "$INSTALL_DIR"
install -m 755 "$bin_path" "${INSTALL_DIR}/${BIN_NAME}"
info "installed ${INSTALL_DIR}/${BIN_NAME}"

if ! command -v "$BIN_NAME" >/dev/null 2>&1; then
  info "add to PATH: export PATH=\"${INSTALL_DIR}:\$PATH\""
fi

"${INSTALL_DIR}/${BIN_NAME}" version 2>/dev/null \
  || "${INSTALL_DIR}/${BIN_NAME}" --version 2>/dev/null \
  || true

info "done. run: git-stack help"
