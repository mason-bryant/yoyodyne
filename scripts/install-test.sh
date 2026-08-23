#!/usr/bin/env bash
#
# install-test.sh - exercise scripts/install.sh against a fabricated release
# and a fabricated machine, so what it does on a clean one is executed rather
# than asserted.
#
#   scripts/install-test.sh
#
# The install script's whole value is what it does on a machine nobody here
# controls: a platform it has no binary for, a PATH the binary is not on, a
# download that is not what the release published, a prerequisite that is not
# installed. None of those can be produced by running it on this machine, and
# all of them can be fabricated: `curl` and `uname` are stubs on PATH, so the
# platform is whatever a case says and the releases are files in a temporary
# directory, and the PATH each case runs with holds nothing but the stubs and
# the system tools.
#
# Nothing leaves the temporary root, which is removed on exit: every case
# installs into its own directory, HOME is inside it, and no case reaches the
# network -- a URL with no fixture behind it is a 404, which is one of the
# cases.
#
# Requires bash, tar, and one of shasum or sha256sum.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
installer="$repository/scripts/install.sh"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/install-test.XXXXXX")"
failures=0

trap 'rm -rf "$scratch"' EXIT

# The PATH every case runs with: its own stubs, then the system tools the
# script legitimately needs. It deliberately excludes the directories a
# developer's go, npm, bd, and claude live in, so "not installed" is a state
# these cases can actually produce.
system_path="/usr/bin:/bin:/usr/sbin:/sbin"

step()   { printf '\n=== %s\n' "$*"; }
pass()   { printf '  ok: %s\n' "$*"; }
fail()   { printf '  FAIL: %s\n' "$*"; failures=$((failures + 1)); }

contains() {
  case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $1" ;; esac
}
missing() {
  case "$1" in (*"$2"*) fail "$3 -- got: $1" ;; (*) pass "$3" ;; esac
}

sha256_of() {
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  else sha256sum "$1" | awk '{print $1}'
  fi
}

# fabricate builds one case's machine: a stub directory holding curl and uname,
# a home, an install directory, and a served directory the stub curl answers
# from. $1 names the case; the rest of the state is exported into the run.
case_root=""
stub_dir=""
served=""
install_dir=""
request_log=""
fabricate() {
  case_root="$scratch/$1"
  stub_dir="$case_root/stubs"
  served="$case_root/served"
  install_dir="$case_root/bin"
  request_log="$case_root/requests"
  mkdir -p "$stub_dir" "$served" "$case_root/home"
  : > "$request_log"
  go_version=""

  # Stub curl: every URL it is asked for is logged, and answered from $served
  # by the last segment of its path. A URL with no file behind it fails the way
  # curl -f fails on a 404, which is what an unpublished release looks like.
  cat > "$stub_dir/curl" <<'SH'
#!/usr/bin/env bash
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    -*) shift ;;
    *)  url="$1"; shift ;;
  esac
done
printf '%s\n' "$url" >> "$STUB_REQUEST_LOG"
file="$STUB_SERVED/${url##*/}"
[ -f "$file" ] || { echo "curl: (22) not found: $url" >&2; exit 22; }
if [ -n "$out" ]; then cp "$file" "$out"; else cat "$file"; fi
SH

  # Stub uname: the platform is whatever the case says it is.
  cat > "$stub_dir/uname" <<'SH'
#!/usr/bin/env bash
case "${1:-}" in
  -m) printf '%s\n' "$STUB_UNAME_M" ;;
  *)  printf '%s\n' "$STUB_UNAME_S" ;;
esac
SH

  chmod +x "$stub_dir/curl" "$stub_dir/uname"
}

# stub_go writes a go that reports $1 as its version and, on `go install`,
# writes a binary into $GOBIN the way the real one would.
go_version=""
stub_go() {
  go_version="$1"
  cat > "$stub_dir/go" <<'SH'
#!/usr/bin/env bash
case "$1" in
  version) echo "go version go$STUB_GO_VERSION darwin/arm64" ;;
  install)
    printf '%s\n' "go $*" >> "$STUB_REQUEST_LOG"
    mkdir -p "$GOBIN"
    printf '#!/bin/sh\nif [ "$1" = version ]; then echo v9.9.9; fi\n' > "$GOBIN/yoyo"
    chmod +x "$GOBIN/yoyo"
    ;;
esac
SH
  chmod +x "$stub_dir/go"
}

# stub_tool writes a stub named $1 whose body is $2.
stub_tool() {
  printf '#!/usr/bin/env bash\n%s\n' "$2" > "$stub_dir/$1"
  chmod +x "$stub_dir/$1"
}

# publish writes one release into the served directory: the archive for $2
# (a goos_goarch stem) at tag $1, a checksums.txt covering it, and the API
# answer naming that tag as the newest. $3 of "corrupt" publishes a checksum
# that does not match what was actually built.
publish() {
  local tag="$1" stem="$2" mode="${3:-good}" archive sum
  archive="yoyo_${tag}_${stem}.tar.gz"
  mkdir -p "$case_root/pkg"
  printf '#!/bin/sh\nif [ "$1" = version ]; then echo v9.9.9; fi\n' > "$case_root/pkg/yoyo"
  chmod +x "$case_root/pkg/yoyo"
  tar -czf "$served/$archive" -C "$case_root/pkg" yoyo
  rm -rf "$case_root/pkg"
  if [ "$mode" = "corrupt" ]; then
    sum="0000000000000000000000000000000000000000000000000000000000000000"
  else
    sum="$(sha256_of "$served/$archive")"
  fi
  printf '%s  %s\n' "$sum" "$archive" > "$served/checksums.txt"
  printf '{"tag_name": "%s", "name": "%s"}\n' "$tag" "$tag" > "$served/latest"
}

# install_run runs the installer on the fabricated machine and captures
# everything it said. $1 is the PATH prefix ahead of the stubs (empty for
# none), $2 the value of SHELL; the rest are the installer's own arguments.
output=""
status=0
install_run() {
  local path_prefix="$1" shell="$2"
  shift 2
  local path="$stub_dir:$system_path"
  [ -z "$path_prefix" ] || path="$path_prefix:$path"
  set +e
  output="$(
    HOME="$case_root/home" \
    PATH="$path" \
    SHELL="$shell" \
    TMPDIR="$case_root" \
    STUB_SERVED="$served" \
    STUB_REQUEST_LOG="$request_log" \
    STUB_UNAME_S="${uname_s:-Darwin}" \
    STUB_UNAME_M="${uname_m:-arm64}" \
    STUB_GO_VERSION="${go_version:-1.24.0}" \
    bash "$installer" "$@" 2>&1
  )"
  status=$?
  set -e
}

step "a release download on a platform a release covers"
fabricate release-download
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
install_run "" /bin/zsh --dir "$install_dir"
[ "$status" = "0" ] && pass "the install succeeded" || fail "the install exited $status -- got: $output"
contains "$output" "platform: darwin/arm64" "the platform is detected from uname"
contains "$output" "release: v9.9.9" "the newest release is the one installed when no tag is given"
contains "$(cat "$request_log")" "api.github.com" "the newest release is asked for rather than guessed"
contains "$output" "checksum matches" "the download is checked against the release's checksums"
contains "$output" "reports v9.9.9" "the installed binary is run and says which version it is"
if [ -x "$install_dir/yoyo" ]; then pass "the binary is in the install directory"; else fail "no executable at $install_dir/yoyo"; fi
# What it says to do next has to be the steps the README documents, or a
# machine that followed this script and a reader who followed the README are
# being sent to different places.
contains "$output" "bd init && yoyo init" "the next step is the one the README's getting started names"
contains "$output" "yoyo chat" "and the step after it"

step "a tag named on the command line"
fabricate named-tag
uname_s=Darwin uname_m=arm64
publish v1.2.3 darwin_arm64
install_run "" /bin/zsh --dir "$install_dir" --version v1.2.3
contains "$output" "release: v1.2.3" "--version installs the tag it names"
missing "$(cat "$request_log")" "api.github.com" "a named tag is not looked up"
contains "$(cat "$request_log")" "releases/download/v1.2.3/yoyo_v1.2.3_darwin_arm64.tar.gz" "the archive URL is built from the tag and the platform"

step "a download that is not what the release published"
fabricate corrupt-download
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64 corrupt
install_run "" /bin/zsh --dir "$install_dir"
[ "$status" != "0" ] && pass "a checksum mismatch refuses" || fail "a corrupt download installed anyway -- got: $output"
contains "$output" "does not match the checksum" "the refusal says what was wrong"
contains "$output" "Nothing was installed" "the refusal says the machine was left alone"
if [ -e "$install_dir/yoyo" ]; then fail "a corrupt download was installed"; else pass "nothing was written to the install directory"; fi

step "a release that does not exist"
fabricate absent-release
uname_s=Darwin uname_m=arm64
install_run "" /bin/zsh --dir "$install_dir" --version v0.0.0
[ "$status" != "0" ] && pass "a missing archive refuses" || fail "a missing archive did not refuse -- got: $output"
contains "$output" "could not be downloaded" "the refusal names the download that failed"
contains "$output" "releases" "the refusal points at the releases page"

step "a platform no release covers, with Go"
fabricate source-fallback
uname_s=Linux uname_m=aarch64
stub_go 1.24.0
install_run "" /bin/bash --dir "$install_dir"
[ "$status" = "0" ] && pass "the install succeeded" || fail "the install exited $status -- got: $output"
contains "$output" "no release binary is published for linux/arm64" "the fallback says why it is building rather than downloading"
contains "$(cat "$request_log")" "go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest" "go install is given the module path the README names"
contains "$output" "reports v9.9.9" "the built binary is run and says which version it is"

step "a platform no release covers, without Go"
fabricate no-route
uname_s=Linux uname_m=aarch64
install_run "" /bin/bash --dir "$install_dir"
[ "$status" != "0" ] && pass "nothing to install from refuses" || fail "the install did not refuse -- got: $output"
contains "$output" "Go 1.24 or newer is not installed" "the refusal names the prerequisite that would have made a route"

step "a Go too old to build with"
fabricate old-go
uname_s=Linux uname_m=aarch64
stub_go 1.21.0
install_run "" /bin/bash --dir "$install_dir"
[ "$status" != "0" ] && pass "a Go older than the module needs refuses" || fail "an old Go was used anyway -- got: $output"
contains "$output" "Go 1.24 or newer" "the refusal names the version needed"

step "a platform yoyo does not run on"
fabricate unsupported-platform
uname_s=Windows_NT uname_m=x86_64
install_run "" "" --dir "$install_dir"
[ "$status" != "0" ] && pass "an unsupported operating system refuses" || fail "Windows was not refused -- got: $output"
contains "$output" "no Windows binary" "the refusal says what is and is not supported"

step "the install directory is not on PATH"
fabricate path-line-zsh
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
install_run "" /bin/zsh --dir "$install_dir"
contains "$output" "is not on your PATH" "an install nobody can run yet says so"
contains "$output" ".zshrc" "the profile named is the one the running shell reads"
contains "$output" "export PATH=\"\$PATH:$install_dir\"" "the line names the directory the binary went into"

step "the install directory is not on PATH, under fish"
fabricate path-line-fish
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
install_run "" /usr/local/bin/fish --dir "$install_dir"
contains "$output" "fish_add_path $install_dir" "fish gets the line fish actually takes"

step "the install directory is on PATH"
fabricate on-path
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
mkdir -p "$install_dir"
install_run "$install_dir" /bin/zsh --dir "$install_dir"
contains "$output" "is on your PATH" "an install that is already runnable says so"
missing "$output" "is not on your PATH" "no PATH line is printed for a directory already on it"

step "another yoyo earlier on PATH"
fabricate shadowed
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
mkdir -p "$install_dir" "$case_root/other"
printf '#!/bin/sh\necho v0.0.1\n' > "$case_root/other/yoyo"
chmod +x "$case_root/other/yoyo"
install_run "$case_root/other:$install_dir" /bin/zsh --dir "$install_dir"
contains "$output" "resolves to $case_root/other/yoyo first" "a shadowed install is named rather than reported as success"

step "prerequisites that are not installed"
fabricate prereqs-missing
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
# No --install-prereqs: this is the default run, and the README's claim about
# it -- that nothing but the binary is written unless you ask -- is what these
# last two checks hold to.
stub_tool npm 'echo "npm was run, and should not have been" >&2; exit 1'
install_run "" /bin/zsh --dir "$install_dir"
[ "$status" = "0" ] && pass "a missing prerequisite is named rather than treated as a failed install" || fail "the install exited $status -- got: $output"
contains "$output" "bd is not installed" "bd is checked"
contains "$output" "github.com/gastownhall/beads" "bd is named with where to get it"
contains "$output" "claude is not installed" "claude is checked"
contains "$output" "code.claude.com" "claude is named with where to get it"
contains "$output" "Still needed before a run: bd claude" "the summary lists what is still missing"
contains "$output" "authenticated" "claude's other requirement is named too"
contains "$output" "--install-prereqs" "the flag that would have installed it is named"
missing "$output" "npm install" "nothing is installed without being asked"
missing "$output" "claude.ai/install.sh" "no second script is fetched and run without being asked"

step "prerequisites that are installed"
fabricate prereqs-present
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
stub_tool bd 'echo "bd version 1.1.2"'
stub_tool claude 'echo "1.0.0"'
install_run "" /bin/zsh --dir "$install_dir"
contains "$output" "bd: $stub_dir/bd" "an installed bd is reported with where it is"
contains "$output" "claude: $stub_dir/claude" "an installed claude is reported with where it is"
missing "$output" "Still needed before a run" "nothing is listed as missing when both are there"

step "a missing claude is installed when it is asked for, with npm"
fabricate claude-installed-npm
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
stub_tool bd 'echo "bd version 1.1.2"'
# The npm the installer would reach for, doing what a successful install does:
# leaving a claude on PATH.
stub_tool npm 'printf "#!/bin/sh\necho 1.0.0\n" > "$(dirname "$0")/claude"; chmod +x "$(dirname "$0")/claude"'
install_run "" /bin/zsh --dir "$install_dir" --install-prereqs
contains "$output" "npm install -g @anthropic-ai/claude-code" "the install it runs is printed before it runs"
contains "$output" "because --install-prereqs was passed" "it says whose decision the install was"
contains "$output" "claude: $stub_dir/claude" "the recheck finds what the installer left"
missing "$output" "Still needed before a run" "a prerequisite that was installed is not still listed as missing"

step "a missing claude is installed when it is asked for, without npm"
fabricate claude-installed-curl
uname_s=Darwin uname_m=arm64
publish v9.9.9 darwin_arm64
stub_tool bd 'echo "bd version 1.1.2"'
# The fallback route, which is the one branch that fetches a second script and
# runs it. The stub curl answers claude.ai/install.sh from the served directory
# like any other URL, so what runs here is a fixture rather than the network.
cat > "$served/install.sh" <<'SH'
#!/bin/sh
dir="$(dirname "$(command -v curl)")"
printf '#!/bin/sh\necho 1.0.0\n' > "$dir/claude"
chmod +x "$dir/claude"
SH
install_run "" /bin/zsh --dir "$install_dir" --install-prereqs
contains "$(cat "$request_log")" "https://claude.ai/install.sh" "with no npm, the installer claude documents is what is fetched"
contains "$output" "claude: $stub_dir/claude" "the recheck finds what that installer left"
missing "$output" "Still needed before a run" "a prerequisite that was installed is not still listed as missing"

step "the platforms a release covers"
# The installer decides whether to download or build from its own list of
# released platforms. That list is the Makefile's PLATFORMS, and nothing but
# this check keeps them the same -- a platform dropped from the release would
# otherwise be found by whoever runs the installer on it, as a 404.
makefile_platforms="$(sed -n 's/^PLATFORMS ?= //p' "$repository/Makefile")"
installer_platforms="$(sed -n 's/^released_platforms="\(.*\)"$/\1/p' "$installer")"
if [ -n "$makefile_platforms" ] && [ "$makefile_platforms" = "$installer_platforms" ]; then
  pass "install.sh and the Makefile name the same platforms: $installer_platforms"
else
  fail "the Makefile builds \"$makefile_platforms\" and install.sh downloads \"$installer_platforms\""
fi

step "--help"
fabricate help
install_run "" /bin/zsh --help
[ "$status" = "0" ] && pass "--help exits 0" || fail "--help exited $status"
contains "$output" "--from-source" "the flags are documented where they are typed"
contains "$output" "--install-prereqs" "including the one that lets it change anything else"

printf '\n'
if [ "$failures" -eq 0 ]; then
  printf 'install.sh does what it says on a machine that has nothing.\n'
else
  printf '%d check(s) failed.\n' "$failures"
  exit 1
fi
