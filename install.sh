#!/bin/sh
# Install forge from its GitHub releases.
#
#   curl -fsSL https://raw.githubusercontent.com/daan3011/forge/main/install.sh | sh
#
# Environment:
#   FORGE_VERSION      release to install, e.g. v0.1.0 (default: the latest)
#   FORGE_INSTALL_DIR  where the binary goes (default: ~/.local/bin)
#   FORGE_BASE_URL     download base instead of GitHub releases; the archive
#                      and checksums.txt are fetched from <base>/<tag>/
set -eu

repo="daan3011/forge"
install_dir="${FORGE_INSTALL_DIR:-$HOME/.local/bin}"
base_url="${FORGE_BASE_URL:-https://github.com/$repo/releases/download}"

say() { printf '%s\n' "$*"; }
die() { printf 'forge install: %s\n' "$*" >&2; exit 1; }

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL "$1" -o "$2"; }
	final_url() { curl -fsSLI -o /dev/null -w '%{url_effective}' "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
	final_url() { wget -S --spider "$1" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r'; }
else
	die "need curl or wget"
fi

case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) die "unsupported OS $(uname -s) (forge runs on macOS and Linux)" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported CPU $(uname -m)" ;;
esac

tag="${FORGE_VERSION:-}"
if [ -z "$tag" ]; then
	# releases/latest redirects to .../releases/tag/<tag>; no API token needed.
	tag="$(final_url "https://github.com/$repo/releases/latest")"
	tag="${tag##*/}"
	case "$tag" in
	v*) ;;
	*) die "couldn't find the latest release (set FORGE_VERSION=vX.Y.Z)" ;;
	esac
fi
version="${tag#v}"
archive="forge_${version}_${os}_${arch}.tar.gz"

tmp="$(mktemp -d 2>/dev/null || mktemp -d -t forge)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "Downloading forge $tag ($os/$arch)..."
fetch "$base_url/$tag/$archive" "$tmp/$archive" || die "download failed: $base_url/$tag/$archive"
fetch "$base_url/$tag/checksums.txt" "$tmp/checksums.txt" || die "download failed: checksums.txt"

want="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
[ -n "$want" ] || die "$archive is not in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got="$(sha256sum "$tmp/$archive" | awk '{ print $1 }')"
elif command -v shasum >/dev/null 2>&1; then
	got="$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')"
else
	die "need sha256sum or shasum to verify the download"
fi
[ "$got" = "$want" ] || die "checksum mismatch for $archive (got $got, want $want)"

tar -xzf "$tmp/$archive" -C "$tmp" forge
mkdir -p "$install_dir"
# Replace atomically, so a running forge keeps its old binary.
cp "$tmp/forge" "$install_dir/.forge.new"
chmod 0755 "$install_dir/.forge.new"
mv -f "$install_dir/.forge.new" "$install_dir/forge"
say "Installed $install_dir/forge ($("$install_dir/forge" --version))"

case ":$PATH:" in
*":$install_dir:"*) ;;
*)
	say ""
	say "$install_dir is not on your PATH. Add this to your shell profile:"
	say "  export PATH=\"$install_dir:\$PATH\""
	;;
esac

say ""
"$install_dir/forge" doctor || true
say ""
say "forge installed. Fix the items above, then run forge."
