#!/bin/sh

set -eu

repository_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
test_root=$(mktemp -d "${TMPDIR:-/tmp}/llm-wiki-installer-test.XXXXXX")

cleanup() {
	case "${test_root##*/}" in
		llm-wiki-installer-test.*) rm -rf "$test_root" ;;
	esac
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

fail() {
	printf 'installer test failed: %s\n' "$*" >&2
	exit 1
}

fixture=$test_root/personal_llvm_wiki-0.0.1
mkdir -p "$fixture/cmd/llm-wiki"
printf '%s\n' 'module llm-wiki' >"$fixture/go.mod"
printf '%s\n' 'package main' >"$fixture/cmd/llm-wiki/main.go"
tar -czf "$test_root/source.tar.gz" -C "$test_root" personal_llvm_wiki-0.0.1

unsafe_fixture=$test_root/personal_llvm_wiki-0.0.3
mkdir -p "$unsafe_fixture/cmd/llm-wiki"
printf '%s\n' 'module llm-wiki' >"$unsafe_fixture/go.mod"
printf '%s\n' 'package main' >"$unsafe_fixture/cmd/llm-wiki/main.go"
ln -s /tmp "$unsafe_fixture/unsafe-link"
tar -czf "$test_root/unsafe-source.tar.gz" -C "$test_root" personal_llvm_wiki-0.0.3

fake_bin=$test_root/fake-bin
mkdir -p "$fake_bin"

cat >"$fake_bin/gh" <<'EOF'
#!/bin/sh
set -eu
endpoint=${2:-}
[ "${1:-}" = api ] || exit 2
case "$endpoint" in
	repos/ifelseboy-big/personal_llvm_wiki/releases/latest)
		[ "${3:-}" = --jq ] || exit 3
		[ "${4:-}" = .tag_name ] || exit 4
		printf '%s\n' 'v0.0.1'
		;;
	repos/ifelseboy-big/personal_llvm_wiki/tarball/v0.0.3)
		cat "$FAKE_UNSAFE_ARCHIVE"
		;;
	repos/ifelseboy-big/personal_llvm_wiki/tarball/v*)
		cat "$FAKE_SOURCE_ARCHIVE"
		;;
	*) exit 5 ;;
esac
EOF

cat >"$fake_bin/go" <<'EOF'
#!/bin/sh
set -eu
case "${1:-}" in
	env)
		case "${2:-}" in
			GOVERSION) printf '%s\n' 'go1.25.1' ;;
			CC) printf '%s\n' 'fake-cc' ;;
			CXX) printf '%s\n' 'fake-cxx' ;;
			*) exit 2 ;;
		esac
		;;
	build)
		output=
		shift
		while [ "$#" -gt 0 ]; do
			if [ "$1" = -o ]; then
				output=$2
				shift 2
			else
				shift
			fi
		done
		[ -n "$output" ] || exit 3
		printf '%s\n' build >>"$FAKE_BUILD_LOG"
		printf '#!/bin/sh\nprintf "llm-wiki version %%s\\n" "%s"\n' "${FAKE_BUILD_VERSION:-0.0.1}" >"$output"
		chmod 0755 "$output"
		;;
	*) exit 4 ;;
esac
EOF

for compiler in fake-cc fake-cxx; do
	cat >"$fake_bin/$compiler" <<'EOF'
#!/bin/sh
exit 0
EOF
done
chmod 0755 "$fake_bin/gh" "$fake_bin/go" "$fake_bin/fake-cc" "$fake_bin/fake-cxx"

install_dir=$test_root/install-bin
build_log=$test_root/build.log
: >"$build_log"

run_installer() {
	FAKE_SOURCE_ARCHIVE=$test_root/source.tar.gz \
	FAKE_UNSAFE_ARCHIVE=$test_root/unsafe-source.tar.gz \
	FAKE_BUILD_LOG=$build_log \
	PATH="$fake_bin:$PATH" \
	LLM_WIKI_INSTALL_DIR="$install_dir" \
	sh "$repository_root/install.sh" "$@"
}

output=$(run_installer)
printf '%s\n' "$output" | grep -F "Installed llm-wiki 0.0.1 at $install_dir/llm-wiki" >/dev/null \
	|| fail "latest release was not installed"
[ "$("$install_dir/llm-wiki" --version)" = 'llm-wiki version 0.0.1' ] \
	|| fail "installed binary did not pass the version smoke test"

output=$(run_installer)
printf '%s\n' "$output" | grep -F 'llm-wiki 0.0.1 is already installed' >/dev/null \
	|| fail "repeat installation was not idempotent"
[ "$(wc -l <"$build_log" | tr -d ' ')" = 1 ] || fail "idempotent install rebuilt the binary"

mkdir -p "$test_root/no-tools"
output=$(PATH="$test_root/no-tools" LLM_WIKI_INSTALL_DIR="$install_dir" \
	/bin/sh "$repository_root/install.sh" --version 0.0.1)
case "$output" in
	*'llm-wiki 0.0.1 is already installed'*) ;;
	*) fail "pinned idempotent install unnecessarily required GitHub access" ;;
esac

if FAKE_BUILD_VERSION=9.9.9 run_installer --version 0.0.2 >"$test_root/failed.out" 2>"$test_root/failed.err"; then
	fail "installer accepted a binary with the wrong version"
fi
[ "$("$install_dir/llm-wiki" --version)" = 'llm-wiki version 0.0.1' ] \
	|| fail "failed upgrade replaced the working binary"

if run_installer --version 0.0.3 >"$test_root/unsafe.out" 2>"$test_root/unsafe.err"; then
	fail "installer accepted an archive containing a symbolic link"
fi
grep -F 'release archive contains links' "$test_root/unsafe.err" >/dev/null \
	|| fail "unsafe archive rejection was not explicit"

builds_before=$(wc -l <"$build_log" | tr -d ' ')
if run_installer --version not-a-version >"$test_root/invalid.out" 2>"$test_root/invalid.err"; then
	fail "installer accepted an invalid version"
fi
builds_after=$(wc -l <"$build_log" | tr -d ' ')
[ "$builds_before" = "$builds_after" ] || fail "invalid version reached the build step"

find "$install_dir" -name '.llm-wiki-install.*' -print -quit | grep . >/dev/null \
	&& fail "installer left a staged binary behind"

printf '%s\n' 'installer tests passed'
