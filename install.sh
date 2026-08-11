#!/bin/sh
# Install the Flagsmith CLI.
#
#   curl -fsSL https://get.flagsmith.com | sh
#   curl -fsSL https://get.flagsmith.com | sh -s -- --version <tag>
#
# A variable assignment in front of `curl` applies to curl, not to sh, so pass
# the version as a flag or export it first.
set -eu

DEFAULT_VERSION="v2.0.0-beta.3" # x-release-please-version

REPO="Flagsmith/flagsmith-cli"
BIN_NAME="flagsmith"
BASE_URL="${FLAGSMITH_CLI_BASE_URL:-https://github.com/${REPO}/releases/download}"

usage() {
	cat <<EOF
Install the Flagsmith CLI.

Usage: install.sh [options]

Options:
  -v, --version <tag>   Version to install (default: ${DEFAULT_VERSION})
      --bin-dir <dir>   Where to install (default: \$HOME/.local/bin)
      --no-modify-path  Leave shell startup files alone
      --dry-run         Report what would be installed, then stop
  -h, --help            Show this message

Environment:
  FLAGSMITH_CLI_VERSION     Same as --version
  FLAGSMITH_INSTALL_DIR     Same as --bin-dir
  FLAGSMITH_NO_MODIFY_PATH  Set to 1 for --no-modify-path
  FLAGSMITH_CLI_BASE_URL    Release download base URL
EOF
}

say() { printf '%s\n' "$*"; }
err() {
	printf '%s\n' "install.sh: $*" >&2
	exit 1
}

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || err "need '$1' (command not found)"
}

download() {
	if [ "$DOWNLOADER" = curl ]; then
		case "$1" in
		# Refuse a downgrade to http on our own URLs; a base URL someone set
		# themselves is their call.
		https://*) curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "$2" "$1" ;;
		*) curl -fsSL --retry 3 -o "$2" "$1" ;;
		esac
	else
		wget --quiet --output-document="$2" "$1"
	fi
}

parse_args() {
	VERSION="${FLAGSMITH_CLI_VERSION:-$DEFAULT_VERSION}"
	INSTALL_DIR="${FLAGSMITH_INSTALL_DIR:-}"
	NO_MODIFY_PATH="${FLAGSMITH_NO_MODIFY_PATH:-0}"
	DRY_RUN=0

	while [ $# -gt 0 ]; do
		case "$1" in
		-v | --version)
			[ $# -ge 2 ] || err "--version needs a value, e.g. --version ${DEFAULT_VERSION}"
			VERSION="$2"
			shift 2
			;;
		--bin-dir)
			[ $# -ge 2 ] || err "--bin-dir needs a value"
			INSTALL_DIR="$2"
			shift 2
			;;
		--no-modify-path)
			NO_MODIFY_PATH=1
			shift
			;;
		--dry-run)
			DRY_RUN=1
			shift
			;;
		-h | --help)
			usage
			exit 0
			;;
		*) err "unknown option '$1' (try --help)" ;;
		esac
	done

	case "$VERSION" in
	v*) ;;
	*) VERSION="v${VERSION}" ;;
	esac
	: "${INSTALL_DIR:=${HOME}/.local/bin}"
}

# detect_platform sets OS and ARCH to the halves of a release archive name.
detect_platform() {
	OS=$(uname -s)
	ARCH=$(uname -m)

	case "$OS" in
	Linux) OS=linux ;;
	Darwin) OS=darwin ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		err "Windows is not supported by this script — download the .zip from https://github.com/${REPO}/releases"
		;;
	*) err "unsupported operating system '${OS}'" ;;
	esac

	case "$ARCH" in
	x86_64 | amd64) ARCH=amd64 ;;
	aarch64 | arm64) ARCH=arm64 ;;
	*) err "unsupported architecture '${ARCH}' — 'go install github.com/${REPO}/v2@${VERSION}' builds from source" ;;
	esac

	# uname reports x86_64 under Rosetta.
	if [ "$OS" = darwin ] && [ "$ARCH" = amd64 ] &&
		[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
		ARCH=arm64
	fi
}

verify_checksum() {
	_archive="$1"
	_name=$(basename "$_archive")
	_matches=$(awk -v name="$_name" '$2 == name || $2 == "*" name {print $1}' "$2")
	[ "$(printf '%s' "$_matches" | grep -c .)" = 1 ] ||
		err "expected exactly one checksum for ${_name} in checksums.txt"

	if command -v sha256sum >/dev/null 2>&1; then
		_actual=$(sha256sum "$_archive" | awk '{print $1}')
	elif command -v shasum >/dev/null 2>&1; then
		_actual=$(shasum -a 256 "$_archive" | awk '{print $1}')
	elif command -v openssl >/dev/null 2>&1; then
		_actual=$(openssl dgst -sha256 "$_archive" | awk '{print $NF}')
	else
		err "need 'sha256sum', 'shasum' or 'openssl' to verify the download"
	fi

	[ "$_actual" = "$_matches" ] ||
		err "checksum mismatch for ${_name}: expected ${_matches}, got ${_actual}"
}

# write_env_scripts writes the snippets the startup files source. They live
# under our own directory: $HOME/.local/bin/env belongs to cargo-dist.
write_env_scripts() {
	mkdir -p "$(dirname "$ENV_SCRIPT")"
	cat >"$ENV_SCRIPT" <<EOF
# Added by the Flagsmith CLI installer.
case ":\${PATH}:" in
	*:"${INSTALL_DIR}":*) ;;
	*) export PATH="${INSTALL_DIR}:\${PATH}" ;;
esac
EOF
	cat >"${ENV_SCRIPT}.fish" <<EOF
# Added by the Flagsmith CLI installer.
if not contains "${INSTALL_DIR}" \$PATH
	set -gx PATH "${INSTALL_DIR}" \$PATH
end
EOF
}

# add_ci_path makes the CLI available to later steps of a GitHub Actions job.
# GITHUB_PATH does not expand variables, so write the resolved directory.
add_ci_path() {
	[ -n "${GITHUB_PATH:-}" ] || return 0
	printf '%s\n' "$INSTALL_DIR" >>"$GITHUB_PATH"
	say "  added ${INSTALL_DIR} to \$GITHUB_PATH"
}

# add_source_line appends to a startup file, once.
add_source_line() {
	grep -qF "$2" "$1" && return 0
	printf '\n%s\n' "$2" >>"$1"
	say "  updated $1"
}

modify_path() {
	case ":${PATH}:" in
	*:"${INSTALL_DIR}":*) return 0 ;;
	esac

	write_env_scripts
	_line=". \"${ENV_SCRIPT}\""
	_edited=0
	for _rc in .profile .bashrc .bash_profile .bash_login .zshrc .zshenv; do
		if [ -f "${HOME}/${_rc}" ]; then
			add_source_line "${HOME}/${_rc}" "$_line"
			_edited=1
		fi
	done
	if [ "$_edited" = 0 ]; then
		printf '%s\n' "$_line" >>"${HOME}/.profile"
		say "  created ${HOME}/.profile"
	fi
	if [ -d "${HOME}/.config/fish" ]; then
		mkdir -p "${HOME}/.config/fish/conf.d"
		printf 'source "%s"\n' "${ENV_SCRIPT}.fish" >"${HOME}/.config/fish/conf.d/flagsmith.fish"
		say "  updated ${HOME}/.config/fish/conf.d/flagsmith.fish"
	fi
	PATH_MODIFIED=1
}

main() {
	parse_args "$@"

	need_cmd uname
	need_cmd tar
	if command -v curl >/dev/null 2>&1; then
		DOWNLOADER=curl
	elif command -v wget >/dev/null 2>&1; then
		DOWNLOADER=wget
	else
		err "need 'curl' or 'wget'"
	fi

	detect_platform
	ENV_SCRIPT="${XDG_DATA_HOME:-${HOME}/.local/share}/flagsmith/env"
	PATH_MODIFIED=0

	archive_name="${BIN_NAME}_${VERSION#v}_${OS}_${ARCH}.tar.gz"
	archive_url="${BASE_URL}/${VERSION}/${archive_name}"
	sums_url="${BASE_URL}/${VERSION}/checksums.txt"

	if [ "$DRY_RUN" = 1 ]; then
		say "would install ${BIN_NAME} ${VERSION} (${OS}/${ARCH}) to ${INSTALL_DIR}"
		say "  archive:   ${archive_url}"
		say "  checksums: ${sums_url}"
		return 0
	fi

	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t flagsmith)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "downloading ${BIN_NAME} ${VERSION} (${OS}/${ARCH})"
	download "$archive_url" "${tmp}/${archive_name}" ||
		err "cannot download ${archive_url}
If ${VERSION} was released moments ago its archives may still be uploading — retry shortly, or choose a version with --version."
	download "$sums_url" "${tmp}/checksums.txt" || err "cannot download ${sums_url}"
	verify_checksum "${tmp}/${archive_name}" "${tmp}/checksums.txt"

	tar -xzf "${tmp}/${archive_name}" -C "$tmp" "$BIN_NAME"
	mkdir -p "$INSTALL_DIR"
	chmod 755 "${tmp}/${BIN_NAME}"
	mv -f "${tmp}/${BIN_NAME}" "${INSTALL_DIR}/${BIN_NAME}"

	installed=$("${INSTALL_DIR}/${BIN_NAME}" --version 2>/dev/null) ||
		err "${INSTALL_DIR}/${BIN_NAME} was installed but will not run — wrong platform?"
	say "installed ${installed} to ${INSTALL_DIR}/${BIN_NAME}"

	if [ "$NO_MODIFY_PATH" != 1 ]; then
		modify_path
		add_ci_path
	fi

	say ""
	if [ "$PATH_MODIFIED" = 1 ]; then
		say "Run '. \"${ENV_SCRIPT}\"' or open a new shell, then '${BIN_NAME} init' to get started."
	else
		say "Run '${BIN_NAME} init' to get started."
	fi
}

main "$@"
