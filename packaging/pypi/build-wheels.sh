#!/usr/bin/env bash
# Build PyPI wheels around the binaries from GoReleaser.
#
# hatchling builds each wheel (see pyproject.toml) and `wheel tags` stamps the
# platform tag on it, so there is no packaging code of our own to maintain.
#
# Reads dist/artifacts.json + dist/metadata.json, writes wheels to dist/pypi/.
#
#     packaging/pypi/build-wheels.sh [dist-dir]
set -euo pipefail

dist=${1:-dist}
here=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo=$(cd "$here/../.." && pwd)
out=$repo/$dist/pypi

# CGO is disabled, so the Linux binaries are static and run on musl too.
# The macOS tags are a minimum, kept at https://go.dev/wiki/MinimumRequirements
platform_tag() {
	case "$1/$2" in
	darwin/amd64) echo macosx_12_0_x86_64 ;;
	darwin/arm64) echo macosx_12_0_arm64 ;;
	linux/amd64) echo manylinux2014_x86_64.musllinux_1_1_x86_64 ;;
	linux/arm64) echo manylinux2014_aarch64.musllinux_1_1_aarch64 ;;
	windows/amd64) echo win_amd64 ;;
	windows/arm64) echo win_arm64 ;;
	*) return 1 ;;
	esac
}

version=$(jq -re '.tag | ltrimstr("v")' "$dist/metadata.json")
rm -rf "$out" "${here:?}/bin"
mkdir -p "$out" "$here/bin"
cp "$repo/README.md" "$repo/LICENSE" "$here/"

built=0
while read -r goos goarch path; do
	tag=$(platform_tag "$goos" "$goarch") || {
		echo "skipping $goos/$goarch: no wheel platform tag" >&2
		continue
	}
	script=flagsmith
	[ "$goos" = windows ] && script=flagsmith.exe
	rm -f "$here"/bin/flagsmith*
	install -m 755 "$path" "$here/bin/$script"

	FLAGSMITH_CLI_VERSION=$version uv build --quiet --wheel "$here" --out-dir "$out"
	uvx --from wheel wheel tags --python-tag py3 --abi-tag none \
		--platform-tag "$tag" --remove "$out/flagsmith_cli-"*"-py3-none-any.whl"
	echo "$goos/$goarch -> $tag"
	built=$((built + 1))
done < <(jq -re '.[] | select(.type == "Binary") | [.goos, .goarch, .path] | @tsv' "$dist/artifacts.json")

rm -rf "${here:?}/bin" "$here/README.md" "$here/LICENSE"
[ "$built" -gt 0 ] || {
	echo "no binaries found in $dist/artifacts.json" >&2
	exit 1
}
