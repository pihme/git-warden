#!/bin/sh
# Install git-everref, the external program the Pull Guard runs, at a pinned
# version with a pinned SHA-256. Optional: any other way of putting
# git-everref on PATH works too. pull-guard itself never downloads anything.
#
#   scripts/install-everref.sh [PREFIX]     default PREFIX: $HOME/.local/bin
#
# To move to another version, update VERSION and both checksums from the
# release's checksums.txt, and everref.version in the Pull Guard's
# defaults.yaml.
set -eu

VERSION=1.0.0
SHA256_linux_amd64=0f87be25d89a31b23d1851edc917751023d56ab10925f1fe695afd8b31c33861
SHA256_linux_arm64=d69453cc8c59c408c9b6ae8b42676e9f815e3ed6e30567dd9413a1b1553cdbe1

prefix=${1:-"$HOME/.local/bin"}
case "$(uname -s)" in
Linux) os=linux ;;
*) echo "install-everref: unsupported OS $(uname -s); build git-everref v$VERSION from source" >&2; exit 1 ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) echo "install-everref: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
eval "want=\$SHA256_${os}_${arch}"

tarball="git-everref_${VERSION}_${os}_${arch}.tar.gz"
url="https://github.com/daojyun/git-everref/releases/download/v${VERSION}/${tarball}"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

curl -fsSL --retry 3 -o "$tmp/$tarball" "$url"
echo "$want  $tmp/$tarball" | sha256sum -c - >/dev/null || {
	echo "install-everref: SHA-256 mismatch for $tarball; not installing" >&2
	exit 1
}
tar -xzf "$tmp/$tarball" -C "$tmp" git-everref
got=$("$tmp/git-everref" --version)
[ "$got" = "everref version v$VERSION" ] || {
	echo "install-everref: unexpected version output: $got" >&2
	exit 1
}
mkdir -p "$prefix"
install -m 0755 "$tmp/git-everref" "$prefix/git-everref"
echo "installed git-everref v$VERSION to $prefix/git-everref"
