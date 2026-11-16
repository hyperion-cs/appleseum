#!/bin/sh

# Keep the body together so a streamed download is parsed before installation starts.
main() {
  set -eu

  repo=hyperion-cs/appleseum
  fail() { printf '%s\n' "$*" >&2; exit 1; }
  download() {
    if command -v curl >/dev/null 2>&1; then
      curl -fsSL --retry 3 -o "$2" "$1"
    elif command -v wget >/dev/null 2>&1; then
      wget -q -O "$2" "$1"
    else
      fail 'curl or wget is required'
    fi
  }
  sha256() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1"; else shasum -a 256 "$1"; fi
  }

  kernel=$(uname -s)
  case "$kernel" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) os=$kernel ;;
  esac
  if [ "$(uname -o 2>/dev/null || :)" = Android ]; then os=android; fi
  machine=$(uname -m)
  case "$machine" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    i[36]86) arch=386 ;;
    *) arch=$machine ;;
  esac
  case "$os-$arch" in
    # iSH on iOS/iPadOS emulates 32-bit x86 Linux.
    linux-386) target=ios-ish ;;
    linux-amd64|linux-arm64|darwin-amd64|darwin-arm64|android-arm64) target=$os-$arch ;;
    *) fail "Unsupported platform: $os/$arch" ;;
  esac

  bin_dir=${APPLESEUM_INSTALL_DIR:-$HOME/.local/bin}
  # Termux keeps user commands in PREFIX/bin, which is already on PATH.
  if [ -z "${APPLESEUM_INSTALL_DIR:-}" ] && [ -n "${TERMUX_VERSION:-}" ] && [ -n "${PREFIX:-}" ]; then
    bin_dir=$PREFIX/bin
  fi
  dest=$bin_dir/appleseum
  if [ -e "$dest" ] && [ ! -f "$dest" ]; then fail "The executable path is not a file: $dest"; fi
  # Installation only replaces the executable. The server migrates its data on startup.
  if [ -f "$dest" ]; then updating=true; else updating=false; fi

  tmp_dir=$(mktemp -d)
  staged=
  trap 'rm -rf -- "$tmp_dir" ${staged:+"$staged"}' 0
  trap 'exit 1' HUP INT TERM

  if [ -n "${APPLESEUM_VERSION:-}" ]; then
    release=https://github.com/$repo/releases/download/appleseum-v${APPLESEUM_VERSION#v}
  else
    release=https://github.com/$repo/releases/latest/download
  fi
  # The checksum manifest also names the archive, so "latest" needs no API request.
  download "$release/SHA256SUMS" "$tmp_dir/SHA256SUMS" || fail 'Could not download release checksums'
  archive=
  while read -r sum name; do
    case "$name" in appleseum-v*-"$target".tar.gz) archive=$name expected=$sum ;; esac
  done <"$tmp_dir/SHA256SUMS"
  case "$archive" in
    '') fail "The release has no archive for $target" ;;
    *[!0-9A-Za-z.-]*) fail "Unexpected archive name: $archive" ;;
  esac

  if [ "$updating" = true ]; then action=Updating; else action=Installing; fi
  printf '%s %s (%s)\n' "$action" "${archive%-"$target".tar.gz}" "$target"
  download "$release/$archive" "$tmp_dir/archive.tar.gz" || fail "Could not download $archive"
  actual=$(sha256 "$tmp_dir/archive.tar.gz") || fail 'sha256sum or shasum is required'
  [ "${actual%% *}" = "$expected" ] || fail 'Checksum mismatch; the existing installation was not changed'

  mkdir -p "$bin_dir"
  staged=$(mktemp "$bin_dir/.appleseum.XXXXXX")
  tar -xzOf "$tmp_dir/archive.tar.gz" appleseum >"$staged" && [ -s "$staged" ] ||
    fail "Could not extract appleseum from $archive; the existing installation was not changed"
  chmod 755 "$staged"
  mv -f -- "$staged" "$dest"
  staged=

  if [ "$updating" = true ]; then printf 'Updated: %s\n' "$dest"; else printf 'Installed: %s\n' "$dest"; fi
  case ":$PATH:" in
    *":$bin_dir:"*) run=appleseum ;;
    *)
      run="\"$dest\""
      printf 'Add this directory to PATH in your shell configuration: %s\n' "$bin_dir"
      ;;
  esac
  if [ "$updating" = true ]; then
    printf 'Data was not changed. Start %s with the same --data directory or working directory as before.\n' "$run"
  else
    printf 'Run: %s\n' "$run"
  fi
}

main "$@"
