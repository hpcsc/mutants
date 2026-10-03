#!/bin/sh

set -eu

repo="hpcsc/mutants"
binary_name="mutants"
archive_name_base="mutants"
checksums_name="checksums.txt"
api="${GITHUB_API_URL:-https://api.github.com}"
token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
install_dir="${INSTALL_DIR:-${HOME}/.local/bin}"

flag_channel=
dir_flag=

die() {
	printf 'install: %s\n' "$*" >&2
	exit 1
}

need() {
	command -v "$1" >/dev/null 2>&1 || die "missing dependency: $1"
}

platform() {
	os=$(uname -s)
	case "$os" in
	Linux) goos=linux ;;
	Darwin) goos=darwin ;;
	*) die "unsupported operating system: $os" ;;
	esac

	machine=$(uname -m)
	case "$machine" in
	x86_64 | amd64) goarch=amd64 ;;
	aarch64 | arm64) goarch=arm64 ;;
	*) die "unsupported architecture: $machine" ;;
	esac

	printf '%s-%s\n' "$goos" "$goarch"
}

api_releases() {
	auth_args=
	if [ -n "$token" ]; then
		auth_args="-H Authorization: Bearer $token"
	fi

	curl -fsSL --retry 3 \
		-H 'Accept: application/vnd.github+json' \
		-H 'X-GitHub-Api-Version: 2022-11-28' \
		-H 'user-agent: mutants-install' \
		$auth_args \
		"$api/repos/$repo/releases?per_page=100" 2>/dev/null
}

# the API URL of an asset works for a private repository when the request has a token
download_asset() {
	url=$1
	auth_args=
	if [ -n "$token" ]; then
		auth_args="-H Authorization: Bearer $token"
	fi

	curl -fsSL --retry 3 \
		-H 'Accept: application/octet-stream' \
		-H 'user-agent: mutants-install' \
		$auth_args \
		"$url" 2>/dev/null
}

usage() {
	cat <<'EOF'
Usage: install.sh [OPTIONS]

  --dir PATH       install directory, default ~/.local/bin
  --channel CH     release or prerelease; asked interactively when omitted
  -h, --help       print this help
EOF
}

parse_args() {
	while [ "$#" -gt 0 ]; do
		case "$1" in
		--dir)
			shift
			[ "$#" -gt 0 ] || die '--dir needs a value'
			install_dir=$1
			dir_flag=yes
			;;
		--channel)
			shift
			[ "$#" -gt 0 ] || die '--channel needs a value'
			flag_channel=$1
			;;
		-h | --help)
			usage
			exit 0
			;;
		*)
			die "unknown argument: $1"
			;;
		esac
		shift
	done
}

require_tools() {
	for tool in curl jq tar gzip; do
		need "$tool"
	done

	checksummer=
	if command -v sha256sum >/dev/null 2>&1; then
		checksummer=sha256sum
	elif command -v shasum >/dev/null 2>&1; then
		checksummer='shasum -a 256'
	else
		die 'missing dependency: sha256sum (or shasum)'
	fi
}

choose_channel() {
	flag=$1
	if [ -n "$flag" ]; then
		case "$flag" in
		release | prerelease)
			printf '%s\n' "$flag"
			return
			;;
		*) die "unknown channel: $flag (release or prerelease)" ;;
		esac
	fi

	printf 'Which release channel do you want to install from?\n' >&2
	printf '  1) release    the latest stable release\n' >&2
	printf '  2) prerelease the latest pre-release\n' >&2
	printf 'Choose [1]: ' >&2
	read -r choice || :
	case "${choice:-1}" in
	1 | '') printf 'release\n' ;;
	2) printf 'prerelease\n' ;;
	*) die "invalid choice: $choice" ;;
	esac
}

versions_in_channel() {
	channel=$1
	releases_json=$2
	if [ "$channel" = prerelease ]; then
		printf '%s\n' "$releases_json" |
			jq -r '[.[] | select(.prerelease == true) | .tag_name] | reverse | .[]'
	else
		printf '%s\n' "$releases_json" |
			jq -r '[.[] | select(.prerelease == false) | .tag_name] | reverse | .[]'
	fi
}

list_versions() {
	printf '%s\n' "$1" | awk '{ printf "  %2d) %s\n", NR, $0 }'
}

choose_version() {
	matches=$1
	version_count=$(printf '%s\n' "$matches" | sed -n '$=')

	if [ "$version_count" -eq 1 ]; then
		printf '%s\n' "$matches"
		return
	fi

	printf 'Which version? [1]: ' >&2
	read -r pick || :
	case "$pick" in
	'' | *[!0-9]*) pick=1 ;;
	esac
	if [ "$pick" -lt 1 ] || [ "$pick" -gt "$version_count" ]; then
		die "invalid version: $pick"
	fi
	printf '%s\n' "$matches" | sed -n "${pick}p"
}

choose_install_dir() {
	default=$1
	if [ -n "$dir_flag" ]; then
		printf '%s\n' "$default"
		return
	fi

	printf 'Install mutants to [%s]: ' "$default" >&2
	read -r custom || :
	if [ -n "$custom" ]; then
		default=$custom
	fi
	printf '%s\n' "$default"
}

# find_release sets the globals archive_url and checksums_url
find_release() {
	releases_json=$1
	tag=$2
	release_json=$(printf '%s\n' "$releases_json" | jq -c --arg tag "$tag" '.[] | select(.tag_name == $tag)')
	[ -n "$release_json" ] || die "could not find release $tag"

	archive_url=$(printf '%s\n' "$release_json" |
		jq -r --arg name "$archive_name" '.assets[] | select(.name == $name) | .url')
	checksums_url=$(printf '%s\n' "$release_json" |
		jq -r --arg name "$checksums_name" '.assets[] | select(.name == $name) | .url')
	[ -n "$archive_url" ] && [ "$archive_url" != null ] || die "release $tag has no $archive_name"
	[ -n "$checksums_url" ] && [ "$checksums_url" != null ] || die "release $tag has no $checksums_name"
}

# download_and_verify sets the global tmp, the folder of the archive that extract_binary reads
download_and_verify() {
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT

	download_asset "$archive_url" >"$tmp/$archive_name"
	download_asset "$checksums_url" >"$tmp/$checksums_name"

	expected=$(awk -v name="$archive_name" '$2 == name { print $1 }' "$tmp/$checksums_name")
	[ -n "$expected" ] || die "$checksums_name has no line for $archive_name"

	actual=$($checksummer "$tmp/$archive_name" | awk '{ print $1 }')
	[ "$expected" = "$actual" ] || die "checksum mismatch for $archive_name: got $actual, want $expected"
}

# extract_binary sets the global binary
extract_binary() {
	mkdir -p "$tmp/root"
	tar -xzf "$tmp/$archive_name" -C "$tmp/root"
	binary=$(find "$tmp/root" -type f -name "$binary_name" | head -n 1)
	[ -n "$binary" ] || die "$archive_name does not contain a $binary_name binary"
}

install_binary() {
	mkdir -p "$install_dir"
	install -m 0755 "$binary" "$install_dir/$binary_name"
}

print_done() {
	printf '\nmutants %s installed to %s/%s\n' "$tag" "$install_dir" "$binary_name"
	case ":$PATH:" in
	*":$install_dir:"*) ;;
	*)
		printf 'Add %s to your PATH, for example:\n  export PATH=%s:$PATH\n' "$install_dir" "$install_dir"
		;;
	esac
}

main() {
	parse_args "$@"
	require_tools

	platform_name=$(platform)
	archive_name="$archive_name_base-$platform_name.tar.gz"

	channel=$(choose_channel "$flag_channel")
	releases_json=$(api_releases) || die 'could not fetch the releases of hpcsc/mutants'

	matches=$(versions_in_channel "$channel" "$releases_json")
	[ -n "$matches" ] || die "there is no $channel release of mutants yet"

	printf '\nAvailable mutants %s releases:\n' "$channel"
	list_versions "$matches"
	tag=$(choose_version "$matches")
	install_dir=$(choose_install_dir "$install_dir")

	find_release "$releases_json" "$tag"
	download_and_verify
	extract_binary
	install_binary
	print_done
}

main "$@"