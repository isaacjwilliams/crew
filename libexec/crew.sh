#!/usr/bin/env bash
# The commands of crew that haven't moved to Go yet (cmd/crew). The Go binary
# hands them over to this script, which goes once the port is done. All
# coordination state lives in the ledger, under projects/ in the crew home;
# nothing is written to the repositories themselves except local-only entries in
# .git/info/exclude.
set -euo pipefail
shopt -u patsub_replacement 2>/dev/null || true

# The crew home is a clone of the crew repo: crew, its config, and the ledger.
# It's $CREW_HOME, which bin/crew sets, else the directory above libexec/. Exported, so the sessions and hooks crew starts find it too.
if [[ -z ${CREW_HOME:-} ]]; then CREW_HOME=$(dirname "$(dirname "$(realpath "${BASH_SOURCE[0]}")")"); fi
export CREW_HOME
[[ -f "$CREW_HOME/config/defaults.env" ]] || { echo "crew: $CREW_HOME isn't a crew home (no config/defaults.env); set CREW_HOME?" >&2; exit 1; }
# Every setting has its default in defaults.env; config/crew.env holds the person's own.
load_config() {
  # shellcheck source=/dev/null
  . "$CREW_HOME/config/defaults.env"
  # shellcheck source=/dev/null
  if [[ -f $CREW_HOME/config/crew.env ]]; then . "$CREW_HOME/config/crew.env"; fi
}
load_config

ACTIVE_STATES="working needs-input addressing-feedback pr-open"
declare -A BRANCHES=()   # filled by load_branches

# ---------------------------------------------------------------- helpers ---

die()  { echo "crew: $*" >&2; exit 1; }
warn() { echo "crew: $*" >&2; }
now()  { date -Iseconds; }
need() { command -v "$1" >/dev/null 2>&1 || die "'$1' is required but not installed"; }
has_word() { [[ " $2 " == *" $1 "* ]]; }

# crew's locks, each on a file descriptor of its own:
#   fd 7  .crew-push.lock   one ledger push at a time (push_soon, crew tick's push)
#   fd 8  .crew-tick.lock   one crew tick at a time
#   fd 9  .crew.lock        every change to the ledger (lock)
# A lock is held by the open file, so a process that inherits the fd holds it too.

# Run claude outside any parent Claude Code session's environment, with all three
# lock fds closed, so a background supervisor started here can't hold them.
cc() { PATH="$CREW_HOME/bin:$PATH" env -u CLAUDECODE claude "$@" 7>&- 8>&- 9>&-; }

# Runs a crew command that has moved to Go, without the lock fds: Go doesn't close
# fds it inherits, so the sessions it starts would hold the locks.
crew_go() { "$CREW_HOME/bin/crew" "$@" 7>&- 8>&- 9>&-; }

# Every change to the ledger takes the ledger lock with lock, which also checks
# that this machine runs the crew (require_host), so it can die with "crew runs
# on …". take_lock alone is for the commands that move the lease. The lock lasts
# until unlock or the end of the process. Opening .crew.lock again, even in the
# same process, waits for it.
take_lock() {
  exec 9>"$CREW_HOME/.crew.lock"
  flock -w 30 9 || die "ledger is locked by another crew command"
}
lock() { take_lock; require_host; }
unlock() { exec 9>&-; }

# The ledger: what crew and the agents write in the home. The rest of the home is
# crew's own code and config, which comes from upstream.
STATE_PATHS=(projects reports)

# Commits the ledger, and nothing else in the home, then starts a push in the
# background (push_soon). It never fails the command.
commit() {
  [[ $(git -C "$CREW_HOME" rev-parse --show-toplevel 2>/dev/null) == "$(realpath "$CREW_HOME")" ]] \
    || { warn "$CREW_HOME is not its own git repo; not committing"; return 0; }
  local p paths=()
  for p in "${STATE_PATHS[@]}"; do if [[ -e $CREW_HOME/$p ]]; then paths+=("$p"); fi; done
  ((${#paths[@]})) || return 0
  git -C "$CREW_HOME" add -A -- "${paths[@]}" >/dev/null 2>&1 || { warn "ledger commit failed (continuing)"; return 0; }
  git -C "$CREW_HOME" diff --cached --quiet -- "${paths[@]}" && return 0
  if git -C "$CREW_HOME" commit -qm "$1" -- "${paths[@]}" >/dev/null 2>&1; then push_soon
  else warn "ledger commit failed (continuing)"; fi
}

# ------------------------------------------------------------------ push ---
# The ledger goes to the home's origin, the person's private repo, soon after each
# commit, so a machine that dies loses seconds of decisions. A command never waits
# for that: push_soon pushes in the background. crew tick ends with a push in the
# foreground, which catches anything a background push missed.

# Whether origin has every ledger commit this machine has.
ledger_pushed() {
  [[ $(git -C "$CREW_HOME" rev-parse @ 2>/dev/null) == "$(git -C "$CREW_HOME" rev-parse '@{u}' 2>/dev/null)" ]]
}

# Takes in the commits origin has and this machine hasn't (one made on GitHub, or
# pushed from another clone), by rebasing the ledger onto them under the ledger
# lock, so no crew command commits meanwhile. Call it without the ledger lock.
# Fails if they moved the lease to another machine: that marks the lease lost
# (LEASE_LOST), and this machine must not push over them.
adopt_origin_commits() {
  git -C "$CREW_HOME" fetch -q origin || return 1
  if [[ $(origin_holder) != "$CREW_HOST" ]]; then
    touch "$LEASE_LOST"
    echo "origin says crew runs on $(origin_holder) now; not pushing. The next crew tick stands this machine down."
    return 1
  fi
  (
    exec 9>"$CREW_HOME/.crew.lock"
    flock -w 30 9 || exit 1
    git -C "$CREW_HOME" pull -q --rebase --autostash || { git -C "$CREW_HOME" rebase --abort 2>/dev/null; exit 1; }
  ) || { echo "couldn't rebase the ledger onto origin; leaving it for crew tick"; return 1; }
}

# Pushes until origin has every ledger commit: up to five tries, a second apart
# when the push fails for a reason like the network. A push origin rejects for
# commits it has that this machine lacks takes those in first.
push_ledger() {
  local out
  for _ in 1 2 3 4 5; do
    ledger_pushed && return 0
    if out=$(git -C "$CREW_HOME" push -q -u origin HEAD 2>&1); then continue; fi
    echo "$out"
    if [[ $out == *"[rejected]"* || $out == *"fetch first"* || $out == *"non-fast-forward"* ]]; then
      adopt_origin_commits || return 1
    else
      sleep 1
    fi
  done
  ledger_pushed
}

# Pushes in the foreground, after any background push that's running. Call it
# without the ledger lock.
push_now() {
  { flock -w 60 7 && push_ledger; } 7>"$CREW_HOME/.crew-push.lock"
}

# Starts a push in the background, logged to logs/push.log. It closes fds 8 and 9,
# so it never holds a lock the command that started it took. Pushes take turns on
# the push lock: one that finds another running exits and leaves the work to it.
# So the running one, after it lets go of the lock, loops to check again whether
# origin has everything: a commit made while it held the lock would otherwise wait
# for the next commit or tick.
push_soon() {
  git -C "$CREW_HOME" remote get-url origin >/dev/null 2>&1 || return 0
  mkdir -p "$CREW_HOME/logs"
  (
    exec 8>&- 9>&-
    while true; do
      { flock -n 7 || exit 0; push_ledger; } 7>"$CREW_HOME/.crew-push.lock" || exit 1
      ledger_pushed && exit 0
    done
  ) </dev/null >>"$CREW_HOME/logs/push.log" 2>&1 &
}

slugify() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]' | sed -E 's/[^a-z0-9]+/-/g; s/^-+//; s/-+$//'; }

# Main checkout root for any path inside a repo or one of its worktrees.
repo_root() {
  local common
  common=$(git -C "${1:-.}" rev-parse --path-format=absolute --git-common-dir 2>/dev/null) || return 1
  dirname "$common"
}

slug_for() {
  local root=$1 url s owner repo
  url=$(git -C "$root" remote get-url origin 2>/dev/null || true)
  if [[ -n $url ]]; then
    s=${url%.git}; s=${s%/}; s=${s//://}
    repo=${s##*/}; s=${s%/*}; owner=${s##*/}
    slugify "$owner-$repo"
  else
    slugify "$(basename "$root")"
  fi
}

project_from_cwd() {
  local root s
  root=$(repo_root .) || return 1
  s=$(slug_for "$root")
  [[ -d "$CREW_HOME/projects/$s" ]] || return 1
  echo "$s"
}

# A project's settings: the shared ones, then its project.env (committed, the same on
# every machine: REMOTE, DEFAULT_BRANCH, overrides), then its local.env (this
# machine's REPO, never committed). Each project starts again from the shared
# settings, so crew tick doesn't carry one project's overrides into the next.
load_project() {
  PDIR="$CREW_HOME/projects/$1"
  [[ -f "$PDIR/project.env" ]] || die "unknown project '$1' (run 'crew project add' in the repo)"
  # load_config resets every setting defaults.env has. These three it hasn't.
  unset REPO REMOTE DEFAULT_BRANCH
  load_config
  # shellcheck source=/dev/null
  . "$PDIR/project.env"
  # shellcheck source=/dev/null
  if [[ -f $PDIR/local.env ]]; then . "$PDIR/local.env"; fi
  [[ -n ${REPO:-} && -d $REPO ]] || die "no clone of $1 on this machine (crew host up makes one, or run 'crew project add' in your clone of it)"
}

# The cwd's project, or every registered project when run outside one (or given "all").
projects_in_scope() {  # [all]
  local p
  if [[ ${1:-} != all ]] && p=$(project_from_cwd 2>/dev/null); then echo "$p"; return; fi
  for p in "$CREW_HOME"/projects/*/; do [[ -f $p/project.env ]] && basename "$p"; done
}

# ------------------------------------------------------ front matter / md ---

fm_get() {
  K="$2" awk 'BEGIN{k=ENVIRON["K"]":"}
    NR==1 && $0=="---" {infm=1; next}
    infm && $0=="---" {exit}
    infm && index($0,k)==1 {
      v=substr($0, length(k)+1); sub(/^[ \t]+/,"",v); sub(/[ \t]+$/,"",v)
      if (v ~ /^".*"$/) { v=substr(v,2,length(v)-2); gsub(/\\"/,"\"",v) }
      print v; exit
    }' "$1"
}

section_body() {
  H="$2" awk 'BEGIN{h=ENVIRON["H"]}
    $0==h {on=1; next}
    on && /^## / {exit}
    on {
      line=$0; out=""
      while (length(line)) {
        if (inc) { p=index(line,"-->"); if (!p) { line=""; break }; line=substr(line,p+3); inc=0 }
        else { p=index(line,"<!--"); if (!p) { out=out line; line="" } else { out=out substr(line,1,p-1); line=substr(line,p+4); inc=1 } }
      }
      if (out ~ /[^ \t]/) print out
    }' "$1"
}

# An unchecked item under Open questions is a question still waiting on the user.
open_questions() {  # file -> how many there are
  section_body "$1" "## Open questions" | grep -c '^- \[ \]' || true
}

# ----------------------------------------------------------------- lease ---
# One machine runs the crew at a time: the one the home's `host` file names. A
# git push claims it (crew host up), so two machines can't both think they do.
# Every mutation checks it (lock), and so do the commands that start sessions,
# before they start one. Reads work on any machine.

CREW_HOST=${CREW_HOST:-$(hostname -s)}
CREW_UNITS=(crew-tick.timer crew-gardener.timer crew-lead.service)
# Made by a push that found on origin that another machine claimed the lease
# (adopt_origin_commits). require_host refuses every change while it exists; the
# next crew tick stands this machine down and removes it, as does crew host up.
LEASE_LOST=$CREW_HOME/.crew-lease-lost

lease_holder() { cat "$CREW_HOME/host" 2>/dev/null || true; }   # the machine the home's host file names, or nothing

# The holder as origin last told this machine, or nothing.
origin_holder() { git -C "$CREW_HOME" show '@{u}:host' 2>/dev/null || true; }

# has found that origin moved it elsewhere.
require_host() {
  local holder moved
  holder=$(lease_holder)
  if [[ -e $LEASE_LOST ]]; then
    moved=$(origin_holder)
    die "crew has moved to ${moved:-another machine}; this machine can only read until its next crew tick stands it down"
  fi
  [[ -n $holder ]] || die "no machine runs the crew; this one can only read (crew host up runs it here)"
  [[ $holder == "$CREW_HOST" ]] || die "crew runs on $holder; this machine can only read (crew host up --take moves it here)"
}

# This machine ran the crew, and origin, freshly fetched, says another one does
# now. A machine that never held the lease has nothing to stand down from.
lease_taken_elsewhere() {
  git -C "$CREW_HOME" fetch -q origin 2>/dev/null || return 1
  [[ $(origin_holder) != "$CREW_HOST" ]] || return 1
  [[ $(lease_holder) == "$CREW_HOST" || -e $LEASE_LOST ]]
}

set_units() {  # enable|disable — the crew's systemd units, now
  systemctl --user "$1" --now "${CREW_UNITS[@]}" >/dev/null 2>&1 || warn "couldn't $1 ${CREW_UNITS[*]} (systemctl --user)"
}

stop_crew_jobs() {  # every background session crew started on this machine
  local job
  while IFS= read -r job; do
    if [[ -n $job ]]; then stop_job "$job" || warn "background session $job didn't stop"; fi
  done < <(agents_json | jq -r '.[] | select((.name // "") | startswith("crew-")) | select(.pid != null) | .id')
}

# Keeps what origin doesn't have, ledger commits and uncommitted changes alike, on
# a new branch, before stand_down resets the home. The working tree is committed
# through a scratch index, so the home's own index and files stay as they are.
save_lost_work() {  # branch
  local tmpidx tree tip
  tmpidx=$(mktemp)
  GIT_INDEX_FILE=$tmpidx git -C "$CREW_HOME" read-tree HEAD
  GIT_INDEX_FILE=$tmpidx git -C "$CREW_HOME" add -A
  tree=$(GIT_INDEX_FILE=$tmpidx git -C "$CREW_HOME" write-tree)
  rm -f "$tmpidx"
  tip=HEAD
  if [[ $tree != "$(git -C "$CREW_HOME" rev-parse 'HEAD^{tree}')" ]]; then
    tip=$(git -C "$CREW_HOME" commit-tree "$tree" -p HEAD -m "Uncommitted when $CREW_HOST stood down")
  fi
  git -C "$CREW_HOME" branch -q "$1" "$tip"
}

# Another machine has claimed the lease, so this one steps back: its units off,
# its sessions stopped, what origin lacks kept on lost/<host>-<time>, and its home
# reset to origin. Run with the tick lock held.
stand_down() {
  local holder=$1 lost
  echo "crew now runs on ${holder:-no machine}; standing down here"
  set_units disable
  stop_crew_jobs
  take_lock
  if [[ -n $(git -C "$CREW_HOME" status --porcelain) || -n $(git -C "$CREW_HOME" rev-list '@{u}..@' 2>/dev/null) ]]; then
    lost="lost/$CREW_HOST-$(date +%Y%m%d-%H%M%S)"
    save_lost_work "$lost"
    echo "kept what origin didn't have on $lost"
  fi
  git -C "$CREW_HOME" reset -q --hard '@{u}'
  rm -f "$LEASE_LOST"
  unlock
  echo "stood down: units disabled, crew sessions stopped, home reset to origin"
}

# Brings in a new version of crew from the public repo (upstream). The ledger and
# the person's settings are paths the public repo never has, so the merge only
# meets their own edits to crew's code. A conflict there is theirs to settle:
# crew stops with the merge in progress and says how. Then the machine's files
# are installed again, the merge pushed, and the lead's server restarted, so new
# phone sessions run the new lead.
cmd_upgrade() {
  [[ $# == 0 ]] || die "usage: crew upgrade"
  git -C "$CREW_HOME" remote get-url upstream >/dev/null 2>&1 || die "the home has no upstream (the public crew repo) to upgrade from"
  lock
  [[ -z $(git -C "$CREW_HOME" status --porcelain) ]] \
    || die "the home has uncommitted changes; commit or discard them first (git -C $CREW_HOME status)"
  git -C "$CREW_HOME" fetch -q upstream || die "couldn't fetch upstream"
  local ref; ref=$(git -C "$CREW_HOME" symbolic-ref -q --short refs/remotes/upstream/HEAD || echo upstream/main)
  if ! git -C "$CREW_HOME" merge -q --no-edit "$ref"; then
    git -C "$CREW_HOME" status --short >&2
    die "upstream's changes conflict with edits in your home. The merge is left in progress: resolve the files above, then 'git -C $CREW_HOME commit' and run crew upgrade again. Or undo it with 'git -C $CREW_HOME merge --abort'."
  fi
  # The merge needed the lock (no ledger commits in the middle of it); installing
  # doesn't, and the push takes the lock itself if it has to rebase.
  unlock
  # The new version's crew installs it, so a change to installing takes effect now.
  "$CREW_HOME/bin/crew" install || die "couldn't install the new version; run crew install to try again"
  push_now || warn "couldn't push the upgrade to origin; crew tick pushes it later"
  systemctl --user restart crew-lead.service >/dev/null 2>&1 || warn "couldn't restart crew-lead.service"
  echo "crew upgraded to $(crew_version)"
}

# ------------------------------------------------------------- resolution ---

files_of() {  # kind(tasks|proposals) [project]
  local d
  if [[ -n ${2:-} ]]; then d=("$CREW_HOME/projects/$2/$1"); else d=("$CREW_HOME"/projects/*/"$1"); fi
  local x
  for x in "${d[@]}"; do [[ -d $x ]] && find "$x" -maxdepth 1 -name '*.md' -type f; done | sort
}

resolve_in() {  # arg files... ; 0 found, 1 none, 2 ambiguous
  local arg=$1; shift
  local f id matches=()
  if [[ $arg == \#* ]]; then
    local n=${arg#\#} pr
    for f; do pr=$(fm_get "$f" pr); [[ -n $pr && ${pr##*/} == "$n" ]] && matches+=("$f"); done
  else
    for f; do id=$(basename "$f" .md); [[ $id == "$arg" ]] && { echo "$f"; return 0; }; done
    if [[ $arg =~ ^[0-9]+$ ]]; then
      local p; p=$(printf '%03d' "$((10#$arg))")
      for f; do id=$(basename "$f" .md); [[ $id == "$p"-* ]] && matches+=("$f"); done
    else
      for f; do id=$(basename "$f" .md); [[ $id == *"$arg"* ]] && matches+=("$f"); done
    fi
  fi
  if ((${#matches[@]} == 1)); then echo "${matches[0]}"; return 0; fi
  if ((${#matches[@]} > 1)); then
    printf 'crew: "%s" is ambiguous:\n' "$arg" >&2
    printf '  %s\n' "${matches[@]##*/}" >&2
    return 2
  fi
  return 1
}

resolve() {  # kind arg
  local kind=$1 arg=$2 cur rc files=()
  if [[ -f $arg ]]; then realpath "$arg"; return 0; fi
  cur=$(project_from_cwd 2>/dev/null || true)
  if [[ -n $cur ]]; then
    mapfile -t files < <(files_of "$kind" "$cur")
    if ((${#files[@]})); then
      rc=0; resolve_in "$arg" "${files[@]}" || rc=$?
      ((rc == 0)) && return 0
      ((rc == 2)) && exit 1
    fi
  fi
  mapfile -t files < <(files_of "$kind")
  if ((${#files[@]})); then
    rc=0; resolve_in "$arg" "${files[@]}" || rc=$?
    ((rc == 0)) && return 0
    ((rc == 2)) && exit 1
  fi
  die "no ${kind%s} matches '$arg'"
}

# A task is stacked when its `base` is another task's branch; that task is its
# parent. Nothing else records the relationship, so it can't disagree with `base`.

load_branches() {  # [project] — index task files by branch, for parent_of
  BRANCHES=()
  local f p b
  while IFS= read -r f; do
    [[ -n $f ]] || continue
    p=${f%/tasks/*}; b=$(fm_get "$f" branch)
    if [[ -n $b ]]; then BRANCHES[${p##*/}:$b]=$f; fi
  done < <(files_of tasks "${1:-}")
}

parent_of() {  # file -> the parent task's file, or nothing (call load_branches first)
  local p=${1%/tasks/*} base found
  base=$(fm_get "$1" base)
  [[ -n $base ]] || return 0
  found=${BRANCHES[${p##*/}:$base]:-}
  if [[ -n $found && $found != "$1" ]]; then echo "$found"; fi
}

# The branch a task belongs on now: its own base, or, once its parent has merged,
# whatever the parent was based on (the default branch when a stack merges bottom-up).
effective_base() {  # file
  local f=$1 p n
  for ((n = 0; n < 50; n++)); do
    p=$(parent_of "$f")
    [[ -n $p && $(fm_get "$p" status) == merged ]] || break
    f=$p
  done
  fm_get "$f" base
}

# Why a stacked task's branch has to be rebased, or nothing: "<parent> merged",
# "<parent> abandoned", or "<parent> moved" when the parent's pushed branch has
# commits this one lacks. Reads the refs as last fetched; needs load_project.
restack_reason() {  # file
  local p pid branch tip
  p=$(parent_of "$1")
  [[ -n $p ]] || return 0
  branch=$(fm_get "$1" branch)
  git -C "$REPO" show-ref --verify --quiet "refs/heads/$branch" || return 0
  pid=$(basename "$p" .md)
  case $(fm_get "$p" status) in
    merged) echo "$pid merged"; return 0 ;;
    abandoned) echo "$pid abandoned"; return 0 ;;
  esac
  tip=$(git -C "$REPO" rev-parse --verify --quiet "refs/remotes/origin/$(fm_get "$p" branch)^{commit}" || true)
  [[ -n $tip ]] || return 0
  git -C "$REPO" merge-base --is-ancestor "$tip" "refs/heads/$branch" || echo "$pid moved"
}

# The commit a stacked task's branch belongs on now: the tip of its effective base
# on origin, as last fetched. Empty if that branch isn't on origin.
restack_target() {  # file
  git -C "$REPO" rev-parse --verify --quiet "refs/remotes/origin/$(effective_base "$1")^{commit}" || true
}

# A stack restacks bottom-up: a parent that is about to be rebased would only move
# this task again.
parent_needs_restack() {  # file
  local p; p=$(parent_of "$1")
  [[ -n $p ]] && has_word "$(fm_get "$p" status)" "$ACTIVE_STATES" && [[ -n $(restack_reason "$p") ]]
}

# ----------------------------------------------------------- agent sessions ---

# claude agents --json --all, or [] on failure. A live session whose transcript
# says its last turn has ended gets turnEnded: true (see JQ_BUSY).
agents_json() {
  local out s ended=()
  out=$(cc agents --json --all 2>/dev/null || true)
  jq -e 'type=="array"' >/dev/null 2>&1 <<<"$out" || { echo '[]'; return 0; }
  while IFS= read -r s; do
    if [[ -n $s ]] && turn_ended "$s"; then ended+=("$s"); fi
  done < <(jq -r '.[] | select(.state == "working" and .pid != null) | .sessionId // empty' <<<"$out")
  jq --args 'map(if (.sessionId as $s | $ARGS.positional | index($s)) then .turnEnded = true else . end)' \
    "${ended[@]}" <<<"$out"
}

# Whether a session's last turn has ended, read from its transcript: Claude Code
# writes a turn_duration entry when a turn ends, and a prompt or a reply once the
# next one starts. Fails when there's no transcript to read.
turn_ended() {  # session
  local t last
  t=$(compgen -G "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects/*/$1.jsonl" | head -n 1 || true)
  [[ -n $t ]] || return 1
  last=$(tac "$t" | jq -nr 'first(inputs | select(.type == "user" or .type == "assistant" or .subtype == "turn_duration") | .type)' 2>/dev/null || true)
  [[ $last == system ]]
}

job_state() {  # agents_json job
  [[ -n $2 ]] || return 0
  jq -r --arg id "$2" 'first(.[] | select(.id==$id) | if .turnEnded then "idle, background task running"
    else .state + (if .waitingFor then " (" + .waitingFor + ")" else "" end) end) // empty' <<<"$1"
}

# A worker is busy while a turn runs or while its live process holds an open prompt,
# such as a permission dialog (status waiting). Anything else can take a followup,
# including a blocked worker: it ended its turn on a question for the user, and the
# answer is what the followup sends. A background shell the worker started, such as
# a dev server, keeps its state working and its status busy after the turn ends, so
# the turn counts as running only while its transcript says so (turnEnded).
JQ_BUSY='def busy: (.state == "working" and (.turnEnded | not)) or .status == "waiting";'

job_listed() {  # agents_json job — whether claude agents on this machine knows the job
  [[ -n $2 ]] || return 1
  jq -e --arg id "$2" 'any(.[]; .id==$id)' <<<"$1" >/dev/null 2>&1
}

job_busy() {  # agents_json job
  [[ -n $2 ]] || return 1
  jq -e --arg id "$2" "$JQ_BUSY"' any(.[]; .id==$id and busy)' <<<"$1" >/dev/null 2>&1
}

# Stops a background job and waits up to 10 seconds for its process to close. Its
# conversation is saved, and background shells it started, such as a dev server,
# keep running for its next process. Fails if the process is still open.
stop_job() {  # job
  cc stop "$1" >/dev/null 2>&1 || true
  for _ in $(seq 1 20); do
    agents_json | jq -e --arg id "$1" 'any(.[]; .id==$id and .pid != null)' >/dev/null || return 0
    sleep 0.5
  done
  return 1
}

# checks don't each look it up again.
gh_login() {
  [[ -n ${GH_LOGIN:-} ]] || GH_LOGIN=$(gh api user --jq .login) || return 1
  echo "$GH_LOGIN"
}

# One pass over every project, doing what needs no decision: sync with GitHub,
# record new PR comments, send review rounds and restacks to idle workers, mark
# tasks that wait on the user, and clean up merged ones. The crew-tick timer runs
# it every few minutes; the lead runs it too. Every step is a crew command run in a
# subshell, so one that refuses (its `die`) skips that step and not the rest.
# Prints one line per thing it did.
cmd_tick() {
  need gh; need jq; need claude
  [[ $# == 0 ]] || die "usage: crew tick"
  exec 8>"$CREW_HOME/.crew-tick.lock"
  flock -n 8 || { echo "another crew tick is running"; return 0; }
  # A machine that ran the crew, and finds on origin that another one does now,
  # steps back and does nothing else.
  if lease_taken_elsewhere; then stand_down "$(origin_holder)"; return 0; fi
  require_host
  GH_LOGIN=$(gh_login) || die "gh is not authenticated (run 'gh auth login')"
  crew_go sync --quiet --all >/dev/null || warn "sync failed; carrying on with what the ledger has"
  local p f aj
  aj=$(agents_json)
  while IFS= read -r p; do
    # load_project dies on a broken project.env; try it in a subshell first, so
    # one broken project doesn't stop the tick for the others.
    (load_project "$p") || { warn "$p: skipped"; continue; }
    load_project "$p"
    load_branches "$p"
    while IFS= read -r f; do
      if [[ -n $f ]]; then tick_task "$f" "$aj"; fi
    done < <(files_of tasks "$p")
    while IFS= read -r f; do
      if [[ -n $f ]]; then tick_goal "$f" "$aj"; fi
    done < <(files_of goals "$p")
  done < <(projects_in_scope all)
  # A catch-all for background pushes that failed, or that a crash cut short.
  if git -C "$CREW_HOME" remote get-url origin >/dev/null 2>&1; then
    push_now >&2 || warn "couldn't push the ledger to origin; the next tick tries again"
  fi
}

goal_has_input() {  # goal-file
  local t n
  n=$(fm_get "$1" unsent_notes)
  ((${n:-0} == 0)) || return 0
  while IFS= read -r t; do
    if [[ -n $t ]] && unrouted_questions "$t" >/dev/null; then return 0; fi
  done < <(goal_tasks "$1")
  return 1
}

# One running goal's share of a tick: pass the goal lead its input once it's free,
# or close the goal once its tasks are all merged or abandoned.
tick_goal() {  # goal-file agents_json
  local g=$1 aj=$2 id job
  id=$(basename "$g" .md)
  [[ $(fm_get "$g" status) == running ]] || return 0
  job=$(fm_get "$g" lead_job)
  if goal_has_input "$g"; then
    if job_busy "$aj" "$job"; then echo "goal $id: input waits for its goal lead ($(job_state "$aj" "$job"))"
    elif crew_go goal followup "$g" >/dev/null; then echo "goal $id: input sent to its goal lead"; fi
  elif goal_finished "$g" && ! job_busy "$aj" "$job"; then
    if crew_go goal "done" "$g" >/dev/null; then echo "goal $id: done, every task merged or abandoned"; fi
  fi
}

# A task in progress that moved here from another machine: crew records a worktree
# for it that isn't here, and this machine's claude agents doesn't know its worker,
# whose session stayed behind. The fresh worker crew tick then starts is recorded,
# and listed by the next tick, so this holds once.
moved_here() {  # file agents_json
  local wt; wt=$(fm_get "$1" worktree)
  has_word "$(fm_get "$1" status)" "working addressing-feedback" && [[ -n $wt && ! -d $wt ]] && ! job_listed "$2" "$(fm_get "$1" worker_job)"
}

# GitHub moves a PR's updatedAt (recorded as pr_updated by crew sync) whenever a
# comment is posted or a review submitted. A PR that hasn't changed since crew
# feedback last read it has no new comments, and reading them costs three API calls.
# Without either timestamp, assume it may have some.
pr_may_have_new_comments() {  # file
  local updated since
  updated=$(fm_get "$1" pr_updated); since=$(fm_get "$1" feedback_since)
  [[ -z $updated || -z $since || $updated > $since ]]
}

# One task's share of a tick. Needs load_project and load_branches.
tick_task() {  # file agents_json
  local f=$1 aj=$2 id st rounds sent asked waiting reason target job
  id=$(basename "$f" .md); st=$(fm_get "$f" status)
  if [[ $st == ready ]] && goal_running "$f"; then
    # The goal lead marked it ready, which is the go-ahead. A dispatch that has to
    # wait for a cap or a dependency (exit 4) is tried again on the next tick.
    local rc=0 err
    err=$(crew_go dispatch "$f" 2>&1 >/dev/null) || rc=$?
    case $rc in
      0) echo "$id: dispatched for goal $(fm_get "$f" goal)" ;;
      4) ;;
      *) warn "$id: dispatch failed: $err" ;;
    esac
    return 0
  fi
  if [[ $st == merged ]]; then
    # A cleanup that fails (uncommitted changes, say) warns, and is tried again next tick.
    if [[ -n $(fm_get "$f" worktree) ]] && crew_go cleanup "$f" >/dev/null; then echo "$id: merged; worktree cleaned up"; fi
    return 0
  fi
  has_word "$st" "$ACTIVE_STATES" || return 0
  if [[ -n $(fm_get "$f" pr) ]] && pr_may_have_new_comments "$f"; then
    # crew feedback reports what it recorded only in the task file, so compare before and after.
    rounds=$(fm_get "$f" review_rounds); asked=$(open_questions "$f")
    if crew_go feedback "$f" >/dev/null; then
      [[ $(fm_get "$f" review_rounds) == "$rounds" ]] || echo "$id: review round $(fm_get "$f" review_rounds) recorded"
      (($(open_questions "$f") == asked)) || echo "$id: $(($(open_questions "$f") - asked)) comment(s) from collaborators, for you"
    fi
  fi
  job=$(fm_get "$f" worker_job)
  if moved_here "$f" "$aj"; then
    local err
    if err=$(crew_go followup "$f" --fresh --note "This task moved to $CREW_HOST; your earlier session is gone. Rebuild context from the task file and the branch." 2>&1 >/dev/null); then
      echo "$id: its worker and worktree aren't on this machine; a fresh worker picks it up"
    else
      warn "$id: couldn't start a fresh worker for it here: $err"
    fi
    return 0
  fi
  # The rest needs the worker free (see job_busy); a busy one is left for a later
  # tick. It does at most one thing, in this order: send an unsent review round, send
  # a restack, or mark the task as waiting on the user. Nothing goes out twice:
  # last_round_sent and restack_sent record what was sent.
  rounds=$(fm_get "$f" review_rounds); sent=$(fm_get "$f" last_round_sent)
  if job_busy "$aj" "$job"; then
    ((${rounds:-0} <= ${sent:-0})) || echo "$id: round $rounds waits for its worker ($(job_state "$aj" "$job"))"
    return 0
  fi
  if ((${rounds:-0} > ${sent:-0})); then
    if crew_go followup "$f" >/dev/null; then echo "$id: round $rounds sent to its worker"; fi
    return 0
  fi
  reason=$(restack_reason "$f")
  if [[ $st == pr-open && -n $reason && $reason != *" abandoned" ]] && ! parent_needs_restack "$f"; then
    target=$(restack_target "$f")
    if [[ -n $target && $(fm_get "$f" restack_sent) != "$target" ]]; then
      if crew_go restack "$f" >/dev/null; then echo "$id: restack sent to its worker ($reason)"; fi
      return 0
    fi
  fi
  waiting=$(open_questions "$f")
  if [[ $st != needs-input ]] && ((waiting > 0)); then
    if crew_go set "$f" status needs-input >/dev/null; then echo "$id: waiting on you ($waiting open question(s))"; fi
  fi
}

# ------------------------------------------------------------------- goals ---
# A goal is a batch of items the user hands the crew to work through without them.
# Its goal lead, a background session, writes and plan-reviews a brief for each item
# and marks it ready, answers its workers' questions, and parks what it shouldn't
# decide alone: a draft task with open questions for the user. crew tick dispatches
# the ready tasks and resumes the goal lead when there's input for it.

goal_tasks() {  # goal-file -> the files of the tasks that belong to the goal
  local id f
  id=$(basename "$1" .md)
  while IFS= read -r f; do
    if [[ -n $f && $(fm_get "$f" goal) == "$id" ]]; then echo "$f"; fi
  done < <(files_of tasks "$(fm_get "$1" project)")
}

goal_running() {  # task-file -> 0 if the task belongs to a running goal
  local g; g=$(fm_get "$1" goal)
  [[ -n $g && $(fm_get "$CREW_HOME/projects/$(fm_get "$1" project)/goals/$g.md" status 2>/dev/null) == running ]]
}

# Finished: the goal has tasks, and every one of them is merged or abandoned.
goal_finished() {  # goal-file
  local f n=0
  while IFS= read -r f; do
    [[ -n $f ]] || continue
    has_word "$(fm_get "$f" status)" "merged abandoned" || return 1
    n=$((n + 1))
  done < <(goal_tasks "$1")
  ((n > 0))
}

# A worker's open questions: the unchecked items under Open questions, leaving out
# the collaborators' PR comments crew feedback records there ("@login …"), which
# are for the user.
worker_questions() {  # task-file
  section_body "$1" "## Open questions" | grep '^- \[ \]' | grep -v '^- \[ \] @' || true
}

# A dispatched goal task whose worker has questions the goal lead hasn't been sent.
# questions_routed is a checksum of the questions last sent, so one the goal lead
# left for the user isn't sent again, but a new one is.
unrouted_questions() {  # task-file -> prints the checksum to record once they're sent
  local q sum
  has_word "$(fm_get "$1" status)" "$ACTIVE_STATES" || return 1
  q=$(worker_questions "$1")
  [[ -n $q ]] || return 1
  sum=$(cksum <<<"$q"); sum=${sum%% *}
  [[ $sum != "$(fm_get "$1" questions_routed)" ]] || return 1
  echo "$sum"
}

# Progress lines for the host commands.
say()  { printf '\033[1m%s\033[0m\n' "$*"; }
note() { printf '  %s\n' "$*"; }

crew_version() { cat "$CREW_HOME/VERSION" 2>/dev/null || echo unknown; }

# ------------------------------------------------------------------- main ---

cmd=$1; shift
case $cmd in
  upgrade) cmd_upgrade "$@" ;;
  tick) cmd_tick "$@" ;;
  *) die "unknown command '$cmd' (see 'crew help')" ;;
esac
