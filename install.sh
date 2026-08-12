#!/bin/sh

set -eu

PROGRAM="llm-wiki"
REPOSITORY="ifelseboy-big/personal_llvm_wiki"
MIN_GO_MAJOR=1
MIN_GO_MINOR=25

requested_version=${LLM_WIKI_VERSION:-latest}
install_dir=${LLM_WIKI_INSTALL_DIR:-}
github_token=${GH_TOKEN:-${GITHUB_TOKEN:-}}
github_client=
force=0
quiet=0
work_dir=
staged_binary=

say() {
	if [ "$quiet" -eq 0 ]; then
		printf '%s\n' "$*"
	fi
}

warn() {
	printf 'warning: %s\n' "$*" >&2
}

fail() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

usage() {
	cat <<'EOF'
Install or upgrade llm-wiki from an official GitHub release.

Usage:
  install.sh [--version <version>] [--install-dir <directory>] [--force] [--quiet]

Options:
  --version <version>       Release to install, such as 0.0.1 (default: latest)
  --install-dir <directory> Binary directory (default: $HOME/.local/bin)
  --force                   Reinstall even when the requested version is installed
  --quiet                   Suppress progress messages
  -h, --help                Show this help

Environment:
  LLM_WIKI_VERSION          Same as --version
  LLM_WIKI_INSTALL_DIR      Same as --install-dir
  GH_TOKEN, GITHUB_TOKEN    GitHub token fallback when gh is unavailable
  CC, CXX                   C and C++ compilers used by CGO
EOF
}

cleanup() {
	if [ -n "$staged_binary" ] && [ -e "$staged_binary" ]; then
		rm -f "$staged_binary"
	fi
	if [ -n "$work_dir" ] && [ -d "$work_dir" ]; then
		case "${work_dir##*/}" in
			llm-wiki-install.*) rm -rf "$work_dir" ;;
			*) warn "refusing to remove unexpected temporary directory: $work_dir" ;;
		esac
	fi
}

trap cleanup EXIT
trap 'exit 1' HUP INT TERM

while [ "$#" -gt 0 ]; do
	case "$1" in
		--version)
			[ "$#" -ge 2 ] || fail "--version requires a value"
			requested_version=$2
			shift 2
			;;
		--install-dir)
			[ "$#" -ge 2 ] || fail "--install-dir requires a value"
			install_dir=$2
			shift 2
			;;
		--force)
			force=1
			shift
			;;
		--quiet)
			quiet=1
			shift
			;;
		-h|--help)
			usage
			exit 0
			;;
		--)
			shift
			;;
		*)
			fail "unknown option: $1"
			;;
	esac
done

if [ -z "$install_dir" ]; then
	[ -n "${HOME:-}" ] || fail "HOME is not set; use --install-dir"
	install_dir=$HOME/.local/bin
fi

case "$install_dir" in
	/*) ;;
	*) fail "install directory must be an absolute path: $install_dir" ;;
esac

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

valid_version() {
	version=${1#v}
	case "$version" in
		''|*[!0-9.]*) return 1 ;;
	esac
	old_ifs=$IFS
	IFS=.
	set -- $version
	IFS=$old_ifs
	[ "$#" -eq 3 ] || return 1
	for component in "$@"; do
		case "$component" in
			''|*[!0-9]*) return 1 ;;
		esac
	done
	printf '%s\n' "$version"
}

normalize_version() {
	input=$1
	if ! valid_version "$input"; then
		fail "version must use MAJOR.MINOR.PATCH: $input"
	fi
}

resolve_latest_version() {
	if [ "$github_client" = gh ]; then
		tag=$(gh api "repos/$REPOSITORY/releases/latest" --jq .tag_name 2>/dev/null) \
			|| fail "cannot resolve the latest release; check GitHub authentication and repository access"
	else
		release_json=$(curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
			-H "Authorization: Bearer $github_token" \
			-H 'Accept: application/vnd.github+json' \
			"https://api.github.com/repos/$REPOSITORY/releases/latest") \
			|| fail "cannot resolve the latest release; check GH_TOKEN and repository access"
		tag=$(printf '%s\n' "$release_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
		[ -n "$tag" ] || fail "GitHub returned release metadata without tag_name"
	fi
	normalize_version "$tag"
}

download_release() {
	version=$1
	output=$2
	if [ "$github_client" = gh ]; then
		gh api "repos/$REPOSITORY/tarball/v$version" >"$output" \
			|| fail "cannot download llm-wiki $version; check GitHub authentication and repository access"
	else
		curl --proto '=https' --tlsv1.2 --fail --location --silent --show-error \
			-H "Authorization: Bearer $github_token" \
			-H 'Accept: application/vnd.github+json' \
			--output "$output" \
			"https://api.github.com/repos/$REPOSITORY/tarball/v$version" \
			|| fail "cannot download llm-wiki $version; check GH_TOKEN and repository access"
	fi
}

select_github_client() {
	if command -v gh >/dev/null 2>&1; then
		github_client=gh
	elif [ -n "$github_token" ]; then
		require_command curl
		github_client=curl
	else
		fail "private release access requires an authenticated GitHub CLI (gh auth login) or GH_TOKEN"
	fi
}

installed_version() {
	binary=$1
	[ -x "$binary" ] || return 1
	if output=$("$binary" --version 2>/dev/null); then
		case "$output" in
			"$PROGRAM version "*) valid_version "${output#"$PROGRAM version "}" ;;
			*) return 1 ;;
		esac
	else
		return 1
	fi
}

if [ "$requested_version" = latest ]; then
	select_github_client
	version=$(resolve_latest_version)
else
	version=$(normalize_version "$requested_version")
fi

target=$install_dir/$PROGRAM
current_version=
if current_version=$(installed_version "$target"); then
	if [ "$current_version" = "$version" ] && [ "$force" -eq 0 ]; then
		say "$PROGRAM $version is already installed at $target"
		exit 0
	fi
fi

if [ -z "$github_client" ]; then
	select_github_client
fi

for command_name in tar go mktemp mkdir install mv find date sed head; do
	require_command "$command_name"
done

go_version=$(go env GOVERSION 2>/dev/null) || fail "cannot determine the Go version"
go_version=${go_version#go}
go_major=${go_version%%.*}
go_rest=${go_version#*.}
go_minor=${go_rest%%.*}
case "$go_major:$go_minor" in
	*[!0-9:]*) fail "Go $MIN_GO_MAJOR.$MIN_GO_MINOR or newer is required; found $go_version" ;;
esac
if [ "$go_major" -lt "$MIN_GO_MAJOR" ] || { [ "$go_major" -eq "$MIN_GO_MAJOR" ] && [ "$go_minor" -lt "$MIN_GO_MINOR" ]; }; then
	fail "Go $MIN_GO_MAJOR.$MIN_GO_MINOR or newer is required; found $go_version"
fi

cc_value=${CC:-$(go env CC 2>/dev/null)}
cxx_value=${CXX:-$(go env CXX 2>/dev/null)}
[ -n "$cc_value" ] || fail "cannot determine the C compiler; set CC"
[ -n "$cxx_value" ] || fail "cannot determine the C++ compiler; set CXX"
cc_command=${cc_value%% *}
cxx_command=${cxx_value%% *}
require_command "$cc_command"
require_command "$cxx_command"

temp_root=${TMPDIR:-/tmp}
[ -d "$temp_root" ] || fail "temporary directory does not exist: $temp_root"
work_dir=$(mktemp -d "$temp_root/llm-wiki-install.XXXXXX") || fail "cannot create a temporary directory"
archive=$work_dir/source.tar.gz
archive_list=$work_dir/archive.list
archive_verbose_list=$work_dir/archive.verbose.list
source_dir=$work_dir/source
built_binary=$work_dir/$PROGRAM

say "Downloading llm-wiki $version..."
download_release "$version" "$archive"

tar -tzf "$archive" >"$archive_list" || fail "downloaded release archive is invalid"
tar -tvzf "$archive" >"$archive_verbose_list" || fail "cannot inspect the release archive"
while IFS= read -r entry; do
	kind=$(printf '%.1s' "$entry")
	case "$kind" in
		l|h) fail "release archive contains links" ;;
	esac
done <"$archive_verbose_list"
archive_root=
while IFS= read -r entry; do
	case "$entry" in
		''|/*|../*|*/../*|*/..) fail "release archive contains an unsafe path" ;;
	esac
	case "$entry" in
		*/*) entry_root=${entry%%/*} ;;
		*) fail "release archive has an unexpected layout" ;;
	esac
	if [ -z "$archive_root" ]; then
		archive_root=$entry_root
	elif [ "$archive_root" != "$entry_root" ]; then
		fail "release archive has multiple roots"
	fi
done <"$archive_list"
[ -n "$archive_root" ] || fail "release archive is empty"

mkdir -p "$source_dir"
tar -xzf "$archive" -C "$source_dir" --strip-components=1 || fail "cannot extract the release archive"
if [ -n "$(find "$source_dir" -type l -print -quit)" ]; then
	fail "release archive contains symbolic links"
fi
[ -f "$source_dir/go.mod" ] || fail "release archive does not contain go.mod"
IFS= read -r module_line <"$source_dir/go.mod" || fail "cannot read release go.mod"
[ "$module_line" = "module llm-wiki" ] || fail "release archive has an unexpected Go module"
[ -f "$source_dir/cmd/llm-wiki/main.go" ] || fail "release archive does not contain the llm-wiki command"

build_date=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
say "Building llm-wiki $version with Go $go_version..."
(
	cd "$source_dir"
	CGO_ENABLED=1 CC="$cc_value" CXX="$cxx_value" go build \
		-tags 'fts5 sqlite_omit_load_extension' \
		-trimpath \
		-ldflags "-s -w -X llm-wiki/internal/app.Version=$version -X llm-wiki/internal/app.Commit=v$version -X llm-wiki/internal/app.Date=$build_date" \
		-o "$built_binary" \
		./cmd/llm-wiki
) || fail "cannot build llm-wiki $version"

mkdir -p "$install_dir" || fail "cannot create install directory: $install_dir"
[ -d "$install_dir" ] || fail "install target is not a directory: $install_dir"
if [ -e "$target" ] && { [ ! -f "$target" ] || [ -L "$target" ]; }; then
	fail "refusing to replace a non-regular file: $target"
fi

staged_binary=$(mktemp "$install_dir/.llm-wiki-install.XXXXXX") || fail "cannot stage the new binary in $install_dir"
install -m 0755 "$built_binary" "$staged_binary" || fail "cannot stage the new binary"
staged_version=$(installed_version "$staged_binary") || fail "the new binary failed its version check"
[ "$staged_version" = "$version" ] || fail "the new binary reported version $staged_version, expected $version"
mv -f "$staged_binary" "$target" || fail "cannot replace $target"
staged_binary=

if [ -n "$current_version" ]; then
	say "Upgraded $PROGRAM from $current_version to $version at $target"
else
	say "Installed $PROGRAM $version at $target"
fi

case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*) warn "$install_dir is not on PATH; open a new shell after adding it to your shell PATH" ;;
esac
