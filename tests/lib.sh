# shellcheck shell=bash
# Shared helpers for the test suites. Sourced, not executed.
#
# Every suite runs in a sandbox under tests/.tmp/ (never /tmp: crew-guard always
# allows writes under /tmp, which would hide write-guard failures), with HOME
# pointed at the sandbox and stub claude/gh/systemctl/curl binaries first on PATH.
# Nothing here touches your real ~/.claude, ~/crew or systemd, or the network.
set -uo pipefail

TESTS_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck disable=SC2034  # used by the suites that source this file
REPO_DIR=$(dirname "$TESTS_DIR")
PASS=0
FAIL=0

new_sandbox() {  # name -> prints a fresh sandbox dir; removed on exit unless KEEP_TEST_TMP=1
  # Named with $$ (the suite's PID, unchanged inside $(...)) so the EXIT trap can
  # find it even though this usually runs in a command-substitution subshell.
  mkdir -p "$TESTS_DIR/.tmp"
  mktemp -d "$TESTS_DIR/.tmp/${1:-run}.$$.XXXXXX"
}
cleanup_sandboxes() {
  if [[ ${KEEP_TEST_TMP:-0} == 1 ]]; then echo "  (kept sandboxes in $TESTS_DIR/.tmp)"; return; fi
  rm -rf "$TESTS_DIR/.tmp/"*".$$."*
  rmdir "$TESTS_DIR/.tmp" 2>/dev/null || true
}
trap cleanup_sandboxes EXIT

# Releases the stub curl serves, laid out like GitHub's download URLs:
# $RELEASES/v<version>/crew-<os>-<arch>.
RELEASES=$(new_sandbox releases)
export CREW_TEST_RELEASES=$RELEASES
ASSET=crew-$(uname -s)-$(uname -m)
# release_binary VERSION — builds crew stamped with VERSION, as a release would ship it.
release_binary() {
  mkdir -p "$RELEASES/v$1"
  (cd "$REPO_DIR" && CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$1" -o "$RELEASES/v$1/$ASSET" ./cmd/crew) \
    || { echo "couldn't build crew (is Go installed?)" >&2; exit 2; }
}
release_binary "$(cat "$REPO_DIR/VERSION")"

# isolate_home DIR — fake HOME with a git identity, stubs first on PATH, and no
# leakage of the caller's crew/Claude environment.
isolate_home() {
  export HOME=$1
  mkdir -p "$HOME"
  unset CREW_HOME CLAUDE_PROJECT_DIR CLAUDE_JOB_DIR CLAUDE_CONFIG_DIR
  export GIT_CONFIG_NOSYSTEM=1
  git config --global user.email test@example.com
  git config --global user.name test
  git config --global init.defaultBranch main
  export PATH="$TESTS_DIR/stubs:$PATH"
  [[ $(command -v claude) == "$TESTS_DIR/stubs/claude" ]] || { echo "refusing to run: stub claude isn't first on PATH" >&2; exit 2; }
  [[ $(command -v gh) == "$TESTS_DIR/stubs/gh" ]] || { echo "refusing to run: stub gh isn't first on PATH" >&2; exit 2; }
  [[ $(command -v wt) == "$TESTS_DIR/stubs/wt" ]] || { echo "refusing to run: stub wt isn't first on PATH" >&2; exit 2; }
  [[ $(command -v curl) == "$TESTS_DIR/stubs/curl" ]] || { echo "refusing to run: stub curl isn't first on PATH" >&2; exit 2; }
}

# make_home DIR — a crew home, as a clone of this repo would be once it has run:
# the working files of this checkout (uncommitted changes included), committed to
# a new git repo, with the binary for its VERSION in bin/release/.
make_home() {
  local f v
  mkdir -p "$1"
  while IFS= read -r -d '' f; do
    if [[ -e $REPO_DIR/$f ]]; then mkdir -p "$1/$(dirname "$f")"; cp -p "$REPO_DIR/$f" "$1/$f"; fi
  done < <(git -C "$REPO_DIR" ls-files -z --cached --others --exclude-standard)
  git -C "$1" init -q && git -C "$1" add -A && git -C "$1" commit -qm "crew"
  v=$(cat "$1/VERSION"); mkdir -p "$1/bin/release"; cp "$RELEASES/v$v/$ASSET" "$1/bin/release/crew-$v"
}

wait_for() {  # 'bash expression' — waits up to 5 seconds for it to hold
  local _
  for _ in $(seq 1 50); do eval "$1" && return 0; sleep 0.1; done
  return 1
}

check() {  # description 'bash expression'
  if eval "$2"; then PASS=$((PASS + 1)); else FAIL=$((FAIL + 1)); printf '  FAIL: %s\n' "$1"; fi
}

summary() {  # suite-name
  printf '%s: %d passed, %d failed\n' "$1" "$PASS" "$FAIL"
  ((FAIL == 0))
}
