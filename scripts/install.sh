#!/usr/bin/env bash
# Install git-stack from GitHub Releases (or go install as fallback).
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/benwyrosdick/git-stack/main/scripts/install.sh | bash
#
# Env:
#   GIT_STACK_REPO          default: benwyrosdick/git-stack
#   GIT_STACK_VERSION       pin a tag, e.g. v0.1.0
#   GIT_STACK_INSTALL_DIR   default: ~/.local/bin
#   GIT_STACK_FROM_SOURCE=1 force go install instead of release binary
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

install_from_source() {
  need_cmd go
  info "installing from source via go install"
  GOBIN="$INSTALL_DIR" go install "github.com/${REPO}/cmd/git-stack@latest"
  info "installed ${INSTALL_DIR}/${BIN_NAME}"
  if ! command -v "$BIN_NAME" >/dev/null 2>&1; then
    info "add to PATH: export PATH=\"${INSTALL_DIR}:\$PATH\""
  fi
  "${INSTALL_DIR}/${BIN_NAME}" version 2>/dev/null || true
  info "done. run: git-stack help"
  exit 0
}

if [[ "${GIT_STACK_FROM_SOURCE:-}" == "1" ]]; then
  install_from_source
fi

need_cmd tar

# Resolve latest tag from GitHub Releases API
resolve_tag() {
  if [[ -n "${GIT_STACK_VERSION:-}" ]]; then
    local t="$GIT_STACK_VERSION"
    [[ "$t" == v* ]] || t="v$t"
    echo "$t"
    return 0
  fi
  local api json t
  api="https://api.github.com/repos/${REPO}/releases/latest"
  if ! json="$(curl -fsSL -H 'Accept: application/vnd.github+json' "$api" 2>/dev/null)"; then
    return 1
  fi
  # Prefer jq if present; else sed
  if command -v jq >/dev/null 2>&1; then
    t="$(printf '%s' "$json" | jq -r '.tag_name // empty')"
  else
    t="$(printf '%s' "$json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
  fi
  [[ -n "$t" && "$t" != "null" ]] || return 1
  echo "$t"
}

tag=""
if ! tag="$(resolve_tag)"; then
  info "no GitHub release found for ${REPO}"
  if command -v go >/dev/null 2>&1; then
    info "falling back to go install"
    install_from_source
  fi
  die "no release published yet and 'go' is not on PATH.
  Publish a release (git tag v0.1.0 && git push origin v0.1.0), or install with:
    go install github.com/${REPO}/cmd/git-stack@latest"
fi

# GoReleaser name_template: {{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}
# Version is usually without leading v.
ver="${tag#v}"
asset="${BIN_NAME}_${ver}_${os}_${arch}.tar.gz"
url="https://github.com/${REPO}/releases/download/${tag}/${asset}"
checksums_url="https://github.com/${REPO}/releases/download/${tag}/checksums.txt"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

info "version ${tag} (${os}/${arch})"
info "downloading ${url}"
if ! curl -fsSL -o "$tmp/archive.tar.gz" "$url"; then
  for alt in \
    "${BIN_NAME}_${tag}_${os}_${arch}.tar.gz" \
    "${BIN_NAME}_${os}_${arch}.tar.gz"
  do
    url="https://github.com/${REPO}/releases/download/${tag}/${alt}"
    info "retry ${url}"
    if curl -fsSL -o "$tmp/archive.tar.gz" "$url"; then
      asset="$alt"
      break
    fi
  done
  if [[ ! -s "$tmp/archive.tar.gz" ]]; then
    if command -v go >/dev/null 2>&1; then
      info "binary download failed; falling back to go install"
      install_from_source
    fi
    die "download failed for ${tag} ${os}/${arch}
  Check: https://github.com/${REPO}/releases/tag/${tag}"
  fi
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
