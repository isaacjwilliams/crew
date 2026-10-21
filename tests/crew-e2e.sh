#!/usr/bin/env bash
# crew end to end: a bare "remote", a clone, a fresh crew home, and stub claude/gh.
# Covers new/ready/dispatch/feedback/followup/reply/sync/cleanup/gardener/promote.
# Variables below are read inside check's eval strings, which shellcheck can't see.
# shellcheck disable=SC2034
. "$(dirname "$0")/lib.sh"

SB=$(new_sandbox crew)
isolate_home "$SB/home"
export CLAUDECODE=1          # crew must strip this before launching claude
L=$HOME/crew
make_home "$L"
# The home's origin: the person's private repo, which the ledger is pushed to.
git init -q --bare "$SB/home.git" && git -C "$L" remote add origin "$SB/home.git" && git -C "$L" push -q -u origin main
# This machine runs the crew: it holds the lease, as `crew host up` would leave it.
export CREW_HOST=tower
echo tower >"$L/host" && git -C "$L" add host && git -C "$L" commit -qm "host: tower" && git -C "$L" push -q
# Waits up to 5 seconds for origin to have every commit the home has.
pushed() {
  local _
  for _ in $(seq 1 50); do
    [[ $(git -C "$L" rev-parse @) == "$(git --git-dir="$SB/home.git" rev-parse main)" ]] && return 0
    sleep 0.1
  done
  return 1
}
export PATH="$L/bin:$PATH"

git init -q --bare "$SB/remote.git"
git clone -q "$SB/remote.git" "$SB/webapp" 2>/dev/null
cd "$SB/webapp" || exit 2
echo "# Conventions" >AGENTS.md; git add -A; git commit -qm init; git push -q -u origin main; git remote set-head origin main

setstate() {  # state pid [status]: status says what the live process is doing (busy, waiting on a prompt)
  jq --arg s "$1" --argjson pid "$2" --arg st "${3:-}" 'map(.state=$s | .pid=$pid | if $st == "" then del(.status) else .status=$st end)' "$HOME/agents.json" >"$HOME/a" && mv "$HOME/a" "$HOME/agents.json"; }
transcript() {  # session ended|running: the end of the session's transcript, a finished turn or one under way
  mkdir -p "$HOME/.claude/projects/stub"
  local f="$HOME/.claude/projects/stub/$1.jsonl"
  echo '{"type":"user","message":{"role":"user","content":"New input"}}' >"$f"
  echo '{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}' >>"$f"
  if [[ $2 == ended ]]; then echo '{"type":"system","subtype":"turn_duration"}' >>"$f"; fi
}
fill() { sed -i -e '/^## Goal$/a x' -e '/^## Structure$/a s' -e '/^## Plan$/a 1. y' -e '/^## Acceptance criteria$/a - z' "$1"; }
section() { awk -v h="$2" '$0==h {on=1; next} on && /^## / {exit} on' "$1"; }

crew project add >/dev/null
PE=$(ls "$L"/projects/*/project.env); PD=$(dirname "$PE"); P=$(basename "$PD")
check "project add records the remote in project.env, and this machine's clone in local.env" '[[ $(crew config REMOTE) == "$SB/remote.git" ]] && ! grep -q "^REPO=" "$PE" && [[ $(cat "$PD/local.env") == *"REPO=\"$SB/webapp\""* ]]'
check "local.env isn't committed, project.env is" '[[ -z $(git -C "$L" ls-files "$PD/local.env") && -n $(git -C "$L" ls-files "$PE") && -z $(git -C "$L" status --porcelain) ]]'
check "crew config --project reads a project from anywhere" '[[ $(cd "$HOME" && crew config REPO --project "$P") == "$SB/webapp" ]]'
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)   # it checks claude --version, not a session
check "crew doctor finds this machine's clone" '[[ $doctor_out == *"ok   $P: cloned on this machine"* ]]'
mv "$PD/local.env" "$HOME/local.env.saved"
out=$(crew new "No clone here" --project "$P" 2>&1)
check "a project with no clone on this machine fails clearly" '[[ $out == *"no clone of $P on this machine"* ]]'
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)   # it checks claude --version, not a session
check "crew doctor flags a project with no clone here" '[[ $doctor_out == *"FAIL $P: cloned on this machine"* ]]'
mv "$HOME/local.env.saved" "$PD/local.env"
CO=$(cd "$HOME" && crew checkout "$P")
check "checkout makes the lead's copy at origin's default branch, and prints its path" '[[ $CO == "$HOME/agent-work/$P/_lead" && $(git -C "$CO" rev-parse HEAD) == $(git rev-parse origin/main) && -z $(git -C "$CO" branch --show-current) ]]'
git clone -q "$SB/remote.git" "$SB/other-clone" 2>/dev/null
(cd "$SB/other-clone" && echo more >>AGENTS.md && git commit -qam more && git push -q origin main)
crew checkout "$P" >/dev/null 2>&1
check "checkout moves the lead's copy to a new commit on the remote" '[[ $(git -C "$CO" rev-parse HEAD) == $(git -C "$SB/other-clone" rev-parse HEAD) ]]'
git pull -q --ff-only   # the clone the tasks use catches up with that push
check "a home without crew.env runs on the defaults" '[[ ! -e $L/config/crew.env && $(crew config BRANCH_PREFIX) == agent/ && $(crew config MAX_WORKERS) == 3 ]]'
echo 'MAX_WORKERS=7' >"$L/config/crew.env"
check "crew.env overrides the defaults" '[[ $(crew config MAX_WORKERS) == 7 && $(crew config MAX_IN_FLIGHT) == 5 ]]'
echo 'MAX_WORKERS=2' >>"$PE"
check "project.env overrides crew.env" '[[ $(crew config MAX_WORKERS) == 2 ]]'
sed -i '/^MAX_WORKERS=2$/d' "$PE"; rm "$L/config/crew.env"
F1=$(crew new "Saved search filters")
check "a ledger commit reaches origin soon after the command" 'pushed && [[ $(git --git-dir="$SB/home.git" log -1 --format=%s main) == "new "*"/001-saved-search-filters" ]]'
printf '#!/bin/sh\nsleep 2\n' >"$SB/home.git/hooks/pre-receive"; chmod +x "$SB/home.git/hooks/pre-receive"   # a slow push
F2=$(crew new "Saved search filters")
check "a push still running holds none of crew's locks" 'wait_for "! flock -n \"$L/.crew-push.lock\" true" && flock -n "$L/.crew.lock" true && flock -n "$L/.crew-tick.lock" true'
rm "$SB/home.git/hooks/pre-receive"
F3=$(crew new "修复登录")
check "two quick commands both reach origin" 'pushed && [[ $(git --git-dir="$SB/home.git" log -2 --format=%s main) == *"003-task-3"*"002-saved-search-filters"* ]]'
git clone -q "$SB/home.git" "$SB/home2"
(cd "$SB/home2" && mkdir -p reports && echo "from the other clone" >reports/note.md && git add reports && git commit -qm "a note" && git push -q origin main)
crew set 1 title "Saved search filters, kept" >/dev/null
check "a commit pushed from elsewhere is rebased in, and the ledger still reaches origin" 'pushed && [[ -f $L/reports/note.md && $(git -C "$L" log -2 --format=%s) == *"set 001-saved-search-filters title"*"a note"* ]]'
git -C "$L" remote set-url origin "$SB/nowhere.git"; crew log 1 "made while origin is out of reach" >/dev/null
wait_for 'grep -q nowhere "$L/logs/push.log"'; flock "$L/.crew-push.lock" true   # until the background push has given up
check "a push that fails in the background gives up and leaves the commit" '[[ $(git -C "$L" rev-parse @) != "$(git --git-dir="$SB/home.git" rev-parse main)" ]] && grep -q nowhere "$L/logs/push.log"'
git -C "$L" remote set-url origin "$SB/home.git"
crew tick >/dev/null 2>&1
check "crew tick ends by pushing what a background push missed" '[[ $(git -C "$L" rev-parse @) == "$(git --git-dir="$SB/home.git" rev-parse main)" ]]'
# An agent's own edits, through its PostToolUse hook.
hook() { jq -n --arg p "$1" --arg d "${2:-$HOME}" '{cwd: $d, tool_input: {file_path: $p}}' | crew commit --hook; }
n=$(git -C "$L" rev-list --count @)
echo "- [ ] A question from the hook test" >>"$F1"
check "an agent's edit to a task file is committed by its hook, and pushed" 'hook "$F1" && [[ $(git -C "$L" log -1 --format=%s) == "edit projects/$P/tasks/001-saved-search-filters.md" ]] && pushed'
echo "more" >>"$L/reports/note.md"
check "a relative path is read against the hook's cwd" 'hook reports/note.md "$L" && [[ $(git -C "$L" log -1 --format=%s) == "edit reports/note.md" ]]'
n=$(git -C "$L" rev-list --count @)
check "an edit outside the ledger is left alone" 'hook "$SB/webapp/AGENTS.md" && hook "$L/config/defaults.env" && [[ $(git -C "$L" rev-list --count @) == "$n" ]]'
check "malformed hook JSON exits 0" 'echo "{not json" | crew commit --hook && crew commit --hook </dev/null'


check "same title gets a unique branch" '[[ $(crew get 1 branch) != "$(crew get 2 branch)" ]]'
check "non-ascii title falls back to task-N" '[[ $(basename "$F3") == 003-task-3.md && $(crew get 3 branch) == agent/task-3 ]]'
check "ready refuses an empty brief" '! crew ready 1 2>/dev/null'
fill "$F1"; sed -i '/^s$/d' "$F1"
check "ready refuses a brief without a structure" '[[ $(crew ready 1 2>&1) == *"fill in: Structure"* ]]'
sed -i '/^## Structure$/a s' "$F1"; crew ready 1 >/dev/null
check "find: no match exits non-zero" '! crew find nomatch 2>/dev/null'
check "find: ambiguous exits non-zero" '! crew find saved 2>/dev/null'
check "dispatch starts a worker" 'crew dispatch 1 >/dev/null'
WT=$(crew get 1 worktree)
check "worktree is on the task branch" '[[ $(git -C "$WT" branch --show-current) == agent/saved-search-filters ]]'
check "session id recorded, starting with the job id" '[[ $(crew get 1 worker_session) == "$(crew get 1 worker_job)"-* ]]'
check "job id recorded without color codes" '[[ $(crew get 1 worker_job) =~ ^[0-9a-f]{8}$ ]]'
check "workers start with the configured auto-compact window" 'grep -- "--agent implementer" "$HOME/claude-calls.log" | tail -1 | grep -q -- " --autocompact 400k "'
check "workers get the shipped settings file when the project adds none" '[[ $(cat "$HOME/claude-settings.last") == "$L/config/worker-settings.json" ]]'
check "git worktrees don't call wt" '[[ ! -e $HOME/wt-calls.log ]]'
check "worker can update the ledger from its worktree" '(cd "$WT" && crew set 1 pr https://github.com/isaac/webapp/pull/12 && crew set 1 status pr-open) >/dev/null'
check "find by PR number" '[[ $(crew find "#12") == 001-saved-search-filters ]]'
check "followup refuses while the worker is working" '! crew followup 1 --note hi 2>/dev/null'
setstate blocked 4242 waiting
check "followup refuses while the worker waits on a prompt" '! crew followup 1 --note hi 2>/dev/null'
setstate "done" null
check "followup refuses with nothing new" '! crew followup 1 2>/dev/null'
out=$(crew feedback 1)
check "the review round has the user's comments only (not bob's, not a worker reply, not a pending review)" 'r=$(section "$F1" "## Review rounds"); [[ $r == *"comment 111"* && $r != *"comment 112"* && $r != *"comment 113"* && $r != *"comment 114"* ]]'
check "feedback lists conversation comments and review summaries by ID, but not worker replies" '[[ $out == *"comment 115"* && $out != *"comment 117"* && $out == *"review 900 (changes_requested)"* ]]'
check "collaborators' comments go under Open questions, unchecked, for the user" '[[ $(section "$F1" "## Open questions") == *"- [ ] @bob commented on \`src/search.ts:50\` in PR #12, comment 112 — [link](u112)"*"> nit from bob"* && $(section "$F1" "## Open questions") == *"- [ ] @bob reviewed PR #12 (commented), review 902"* && $(section "$F1" "## Review rounds") != *"bob"* ]]'
check "comments from people without write access, and from bots, are never pulled" '! grep -q "Drive-by idea\|Coverage report" "$F1"'
check "followup resumes on a new round" 'crew followup 1 >/dev/null && [[ $(crew get 1 status) == addressing-feedback && $(crew get 1 last_round_sent) == 1 ]]'
check "resume passes the recorded session" 'grep -q -- "--resume $(crew get 1 worker_session) " "$HOME/claude-calls.log"'
check "a review-round followup tells the worker to reply with crew reply" 'grep -- "--resume" "$HOME/claude-calls.log" | tail -1 | grep -q "Reply to each comment in the round with .crew reply."'
setstate "done" null
check "note-only followup doesn't resend the old round" 'crew followup 1 --note "use defaults" >/dev/null && grep -- "--resume" "$HOME/claude-calls.log" | tail -1 | grep -v "Round 1" | grep -q "use defaults" && [[ $(crew get 1 status) == working ]]'
setstate "done" null; touch "$HOME/fail-resume"; crew feedback 1 --since 1970-01-01T00:00:00Z >/dev/null
check "failed resume falls back to a fresh worker" 'crew followup 1 2>&1 | grep -q "(fresh)"'
rm -f "$HOME/fail-resume"
setstate "done" 4242; job=$(crew get 1 worker_job); sess=$(crew get 1 worker_session); rows=$(jq length "$HOME/agents.json")
out=$(crew followup 1 --note "open but idle" 2>&1)
last_resume=$(grep -- "--resume" "$HOME/claude-calls.log" | tail -1)
check "resume passes no launch flags, only the session and prompt" '[[ $last_resume == "claude --resume $sess --bg New input"* && $last_resume != *" --agent "* && $last_resume != *" --settings "* && $last_resume != *" --permission-mode "* && $last_resume != *" --add-dir "* && $last_resume != *" --autocompact "* ]]'
check "followup stops an idle worker whose process is still open, then continues it in place" '[[ $(crew get 1 worker_job) == "$job" && $(crew get 1 worker_session) == "$sess" && $(jq length "$HOME/agents.json") == "$rows" && $out != *copy* ]] && grep -q "^claude stop $job\$" "$HOME/claude-calls.log"'
setstate blocked null
out=$(crew followup 1 --note "process gone" 2>&1)
check "followup continues a blocked worker whose process is gone in place" '[[ $(crew get 1 worker_job) == "$job" && $(crew get 1 worker_session) == "$sess" && $(jq length "$HOME/agents.json") == "$rows" && $out != *copy* ]] && [[ $(grep -c "^claude stop" "$HOME/claude-calls.log") == 1 ]]'
setstate blocked 4242 busy
out=$(crew followup 1 --note "answered" 2>&1)
check "followup continues a worker that ended its turn on a question, though a shell it left running keeps its process busy" '[[ $(crew get 1 worker_session) == "$sess" && $out != *copy* ]] && [[ $(grep -c "^claude stop" "$HOME/claude-calls.log") == 2 ]]'
setstate working 4242 busy; transcript "$sess" running
check "followup refuses a working worker whose transcript shows the turn under way" '! crew followup 1 --note "mid-turn" 2>/dev/null'
transcript "$sess" ended
out=$(crew followup 1 --note "turn over" 2>&1)
check "followup continues a worker whose turn ended, though a shell it left running keeps it working" '[[ $(crew get 1 worker_session) == "$sess" && $out != *copy* ]] && [[ $(grep -c "^claude stop" "$HOME/claude-calls.log") == 3 ]]'
rm -f "$HOME/.claude/projects/stub/$sess.jsonl"
setstate "done" 4242; touch "$HOME/stop-noop"
out=$(crew followup 1 --note "stop didn't take" 2>&1)
copy=$(jq -r '.[-1].sessionId' "$HOME/agents.json")
check "a copy started on resume is recorded, with a warning" '[[ $out == *"started a copy"* && $(crew get 1 worker_session) == "$copy" && $copy != "$sess" && $(crew get 1 worker_job) == "$(jq -r ".[-1].id" "$HOME/agents.json")" ]]'
rm -f "$HOME/stop-noop"
setstate "done" null; touch "$HOME/placeholder-session"; sess=$(crew get 1 worker_session)
out=$(crew followup 1 --note "placeholder" 2>&1)
check "a resume whose process first reports a new session ID keeps the recorded session" '[[ $(crew get 1 worker_session) == "$sess" && $out != *copy* ]]'
rm -f "$HOME/placeholder-session"
setstate "done" null; touch "$HOME/fail-resume-late"; job=$(crew get 1 worker_job)
out=$(crew followup 1 --note "late failure" 2>&1)
check "a resumed job that fails straight away falls back to a fresh worker" '[[ $out == *"failed straight away"* && $out == *"(fresh)"* && $(crew get 1 worker_job) != "$job" ]]'
rm -f "$HOME/fail-resume-late"
check "running workers are counted by recorded job, whatever their name" '[[ $(crew status) == *"workers running: 1/"* ]]'
setstate blocked null
check "a worker that ended its turn on a question doesn't count as running" '[[ $(crew status) == *"workers running: 0/"* ]]'
check "reply goes to the thread root" 'crew reply 1 113 "done" >/dev/null && grep -q "pulls/12/comments/111/replies" "$HOME/gh-calls.log"'
check "a thread reply carries the crew marker" '[[ $(cat "$HOME/gh-last-body") == *"<!-- crew -->" ]]'
out=$(crew reply 1 115 "Done in 969c9c9." 2>&1); body=$(cat "$HOME/gh-last-body")
want=$'> [Comment](u115): Also keep the cutover dates as they are.\n\nDone in 969c9c9.\n\n<!-- crew -->'
check "reply to a conversation comment posts a PR comment that quotes it" '[[ $out == *"replied to comment 115"* && $body == "$want" ]] && grep -q "issues/12/comments -f body=" "$HOME/gh-calls.log"'
out=$(crew reply 1 900 "All addressed." 2>&1); body=$(cat "$HOME/gh-last-body")
check "reply to a review summary posts a PR comment that quotes it" '[[ $out == *"replied to review 900"* && $body == "> [Review](r900): See inline."* && $body == *"All addressed."* ]]'
rm -f "$HOME/gh-last-body"
check "reply refuses a comment on another PR" '! crew reply 1 116 x 2>/dev/null && [[ ! -e $HOME/gh-last-body ]]'
check "reply refuses an ID that is no comment or review on the PR" '! crew reply 1 999 x 2>/dev/null && [[ ! -e $HOME/gh-last-body ]]'
echo 'REPLY_TO_THREADS="no"' >>"$(ls -d "$L"/projects/*)/project.env"
check "per-project REPLY_TO_THREADS is honoured" '! crew reply 1 111 x 2>/dev/null'
setstate "done" null; crew feedback 1 --since 1970-01-01T00:00:00Z >/dev/null
check "with REPLY_TO_THREADS off, a review-round followup tells the worker not to reply" 'crew followup 1 >/dev/null && grep -- "--resume" "$HOME/claude-calls.log" | tail -1 | grep -q "Don.t reply to the comments on the PR"'
fill "$F2"; sed -i 's/^depends_on:$/depends_on: 001/' "$F2"; crew ready 2 >/dev/null
check "dispatch waits for an unmerged dependency" '! crew dispatch 2 2>/dev/null'
check "sync marks merged PRs" 'crew sync >/dev/null && [[ $(crew get 1 status) == merged ]]'
setstate "done" null
check "dependency satisfied after merge" 'crew dispatch 2 >/dev/null'
# setsid: a process without a terminal, like a worker's background shell.
(cd "$WT" && exec setsid sleep 300) & IN_WT=$!
(cd "$HOME" && exec setsid sleep 300) & ELSEWHERE=$!
script -qc "cd '$WT' && exec sleep 301" /dev/null </dev/null >/dev/null 2>&1 & WITH_TTY=$!
until IN_TTY=$(pgrep -fx "sleep 301"); do sleep 0.1; done
out=$(cd "$WT" && crew cleanup 1 2>&1)
check "cleanup removes the worktree, run from inside it too" '[[ $out == *"cleaned up"* && ! -d $WT ]]'
check "cleanup ends a background process left running in the worktree, and nothing else" '[[ $out == *"stopping 1 process(es) still running in $WT"* ]] && [[ ! -e /proc/$IN_WT/cwd ]] && kill -0 $ELSEWHERE'
check "cleanup leaves a process with a terminal running in the worktree" 'kill -0 $IN_TTY'
kill $ELSEWHERE $IN_TTY $WITH_TTY 2>/dev/null || true

# WORKTREE_TOOL=worktrunk: task worktrees come from `wt`, so the repo's hooks run.
PD=$(ls -d "$L"/projects/*)
echo 'WORKTREE_TOOL="worktrunk"' >>"$PD/project.env"
check "config shows WORKTREE_TOOL" 'crew config WORKTREE_TOOL | grep -qx worktrunk'
F4=$(crew new "Instance per worktree"); fill "$F4"; crew ready 4 >/dev/null
WT4="$HOME/agent-work/$(basename "$PD")/$(basename "$F4" .md)"
setstate "done" null
touch "$HOME/wt-unapproved"
out=$(crew dispatch 4 2>&1)
check "unapproved wt hooks stop the dispatch" '[[ $out == *"wt config approvals add"* && ! -d $WT4 && $(crew get 4 status) == ready && -z $(crew get 4 worker_job) ]]'
rm -f "$HOME/wt-unapproved"; touch "$HOME/wt-elsewhere"
check "a branch wt puts elsewhere stops the dispatch" '! crew dispatch 4 >/dev/null 2>&1 && [[ $(crew get 4 status) == ready ]]'
rm -f "$HOME/wt-elsewhere"; touch "$HOME/wt-hook-fail"
check "a failing setup hook stops the dispatch" '! crew dispatch 4 >/dev/null 2>&1 && [[ -d $WT4 && -z $(crew get 4 worktree) ]]'
rm -f "$HOME/wt-hook-fail"
out=$(crew dispatch 4 2>&1)
check "a half-set-up worktree isn't reused" '[[ $out == *"left over"* && $(crew get 4 status) == ready ]]'
wt -C "$SB/webapp" remove "$WT4" >/dev/null; git branch -q -D agent/instance-per-worktree; : >"$HOME/wt-hooks.log"
echo 'flock -n "$CREW_HOME/.crew.lock" true && echo ledger-unlocked' >"$HOME/wt-hook-cmd"
out=$(crew dispatch 4 2>&1)
check "worktrunk dispatch starts a worker" '[[ $(crew get 4 status) == working && -n $(crew get 4 worker_job) ]]'
check "wt creates the worktree under WORK_ROOT on the task branch" '[[ $(crew get 4 worktree) == "$WT4" && $(git -C "$WT4" branch --show-current) == agent/instance-per-worktree ]]'
check "wt switch gets --create, the base and the path" 'grep -F -- "switch --no-cd --format json --config-set worktree-path=\"$WT4\" --create --base origin/main agent/instance-per-worktree" "$HOME/wt-calls.log" >/dev/null'
check "the pre-start hook ran" '[[ -f $WT4/.instance.yml ]] && grep -qx "pre-start agent/instance-per-worktree" "$HOME/wt-hooks.log"'
check "hook output goes to the log, not the terminal" '[[ $out != *"Running pre-start"* ]] && grep -q "Running pre-start" "$L/logs/worktree-$(basename "$PD")-$(basename "$F4" .md).log"'
check "crew never approves hooks on the user's behalf" '! grep -q -- "--yes" "$HOME/wt-calls.log"'
check "the ledger isn't locked while hooks run" 'grep -qx ledger-unlocked "$HOME/wt-hooks.log"'
crew set 4 status abandoned >/dev/null; J4=$(crew get 4 worker_job)
setstate working 4242; touch "$HOME/stop-noop"
out=$(crew cleanup 4 2>&1)
check "cleanup leaves the worktree of a worker it can't stop" '[[ $out == *"worker $J4 is still running"* && -d $WT4 && -n $(crew get 4 worktree) ]] && jq -e --arg id "$J4" "any(.[]; .id==\$id)" "$HOME/agents.json" >/dev/null'
rm -f "$HOME/stop-noop"
cat >"$HOME/wt-hook-cmd" <<EOF
echo "worker rows: \$(jq '[.[] | select(.id == "$J4")] | length' "\$HOME/agents.json")"
EOF
check "cleanup removes a worktrunk worktree with wt remove" 'crew cleanup 4 >/dev/null && [[ ! -d $WT4 ]] && grep -F -- "remove --no-delete-branch --foreground $WT4" "$HOME/wt-calls.log" >/dev/null'
check "cleanup stops a running worker and removes it before the worktree" 'grep -qx "claude stop $J4" "$HOME/claude-calls.log" && grep -qx "claude rm $J4" "$HOME/claude-calls.log" && grep -qx "worker rows: 0" "$HOME/wt-hooks.log"'
rm -f "$HOME/wt-hook-cmd"
check "the pre-remove hook ran" 'grep -qx "pre-remove $WT4" "$HOME/wt-hooks.log"'
check "cleanup leaves branch deletion to crew" 'git show-ref --verify --quiet refs/heads/agent/instance-per-worktree'
echo 'WORKTREE_TOOL="jj"' >>"$PD/project.env"
F5=$(crew new "Per-project settings"); fill "$F5"; crew ready 5 >/dev/null
setstate "done" null
check "an unknown WORKTREE_TOOL stops the dispatch" '! crew dispatch 5 2>&1 | grep -q dispatched && [[ $(crew get 5 status) == ready ]]'
sed -i '/^WORKTREE_TOOL=/d' "$PD/project.env"
echo 'WORKER_AUTOCOMPACT_WINDOW="lots"' >>"$PD/project.env"
check "a bad WORKER_AUTOCOMPACT_WINDOW stops the dispatch before the worktree" '! crew dispatch 5 >/dev/null 2>&1 && [[ ! -d $HOME/agent-work/$(basename "$PD")/$(basename "$F5" .md) && $(crew get 5 status) == ready ]]'
sed -i 's/^WORKER_AUTOCOMPACT_WINDOW=.*/WORKER_AUTOCOMPACT_WINDOW="250k"/' "$PD/project.env"

# projects/<p>/worker-settings.json adds to the shipped worker settings.
echo '{"permissions": {' >"$PD/worker-settings.json"
check "invalid project worker settings stop the dispatch" '! crew dispatch 5 >/dev/null 2>&1 && [[ ! -d $HOME/agent-work/$(basename "$PD")/$(basename "$F5" .md) ]]'
cat >"$PD/worker-settings.json" <<'EOF'
{"permissions": {"allow": ["Bash(bin/rspec *)", "Bash(git push)"], "deny": [], "defaultMode": "bypassPermissions"},
 "env": {"PGHOST": "/run/postgresql"}}
EOF
# A value the shipped file sets always wins; give the test ledger's copy one to defend.
jq '.permissions.defaultMode = "default"' "$L/config/worker-settings.json" >"$HOME/ws" && mv "$HOME/ws" "$L/config/worker-settings.json"
crew dispatch 5 >/dev/null
check "ledger commits take only the ledger, not an edit to the home's own files" '! git -C "$L" diff --quiet HEAD -- config/worker-settings.json && [[ $(git -C "$L" log -1 --format=%s) == "dispatch "* ]]'
S=$(cat "$HOME/claude-settings.last")
check "project settings add allow rules without duplicates" 'jq -e ".permissions.allow | (index(\"Bash(bin/rspec *)\") != null) and (map(select(. == \"Bash(git push)\")) | length == 1)" <<<"$S" >/dev/null'
check "per-project WORKER_AUTOCOMPACT_WINDOW is honoured" 'grep -- "--agent implementer" "$HOME/claude-calls.log" | tail -1 | grep -q -- " --autocompact 250k "'
check "project settings add new keys" 'jq -e ".env.PGHOST == \"/run/postgresql\"" <<<"$S" >/dev/null'
check "project settings can't override a value the shipped file sets" 'jq -e ".permissions.defaultMode == \"default\"" <<<"$S" >/dev/null'
check "project settings can't drop deny rules" 'jq -e ".permissions.deny | index(\"Bash(gh pr merge *)\") != null" <<<"$S" >/dev/null'
P=$(ls -d "$L"/projects/*)/proposals/2026-09-25-x.md
sed -e 's/^title: .*/title: "Extract retry"/' -e "s/^project: .*/project: $(basename "$(dirname "$(dirname "$P")")")/" "$L/templates/proposal.md" >"$P"
check "promote an open proposal with a branch name" 'PF=$(crew promote 2026-09-25-x --branch retry-helper) && [[ $(basename "$PF") == 006-retry-helper.md && $(crew get 6 branch) == agent/retry-helper ]]'
check "promote refuses an accepted proposal" '! crew promote 2026-09-25-x 2>/dev/null'
check "gardener runs and exits 0" 'crew gardener >/dev/null 2>&1'
check "gardener records its progress" '[[ -s $(ls -d "$L"/projects/*)/gardener.last ]]'
check "CLAUDECODE never reaches claude" '! grep -q LEAK "$HOME/claude-calls.log"'
check "ledger commits every mutation" '[[ $(git -C "$L" log --oneline | wc -l) -gt 10 ]]'
check "no open proposals prints nothing on stdout" '[[ -z $(crew proposals 2>/dev/null) ]]'

# The lead names the branch; the task id and worktree follow it.
F7=$(crew new "Keep a contestant's type when an update omits it" --branch contestant-type-on-update)
check "--branch names the branch and the task" '[[ $(basename "$F7") == 007-contestant-type-on-update.md && $(crew get 7 branch) == agent/contestant-type-on-update && $(crew get 7 title) == *"when an update omits it" ]]'
check "--branch that's already taken gets the task number" '[[ $(crew get "$(crew new "Same name again" --branch contestant-type-on-update)" branch) == agent/contestant-type-on-update-8 ]]'
n=$(ls "$PD/tasks" | wc -l)
for bad in "Contestant_Type" "agent/contestant-type" "trailing-" "$(printf 'x%.0s' {1..41})"; do
  check "--branch refuses '${bad:0:24}'" '! crew new "Bad branch" --branch "$bad" 2>/dev/null && [[ $(ls "$PD/tasks" | wc -l) == "$n" ]]'
done
sed 's/^status: .*/status: open/' "$P" >"${P%x.md}y.md"
check "promote refuses a bad --branch and leaves the proposal open" '! crew promote 2026-09-25-y --branch Nope 2>/dev/null && [[ $(crew proposals) == *2026-09-25-y* && $(ls "$PD/tasks" | wc -l) == "$n" ]]'

# Stacked tasks: 9 ← 10 ← 11, each based on the unmerged branch of the one before.
tip() { git rev-parse "refs/heads/agent/$1"; }
work() {  # worktree file — what a worker does: commit and push its branch
  (cd "$1" && echo x >"$2" && git add "$2" && git commit -qm "add $2" && git push -q -u origin HEAD 2>/dev/null)
}
last_prompt() { grep -- "--resume" "$HOME/claude-calls.log" | tail -1; }
echo OPEN >"$HOME/gh-pr-state"
inflight() { crew status | sed -n 's/.*tasks in flight: \([0-9]*\).*/\1/p'; }
n0=$(inflight)
F9=$(crew new "Season model" --branch season-model); fill "$F9"; crew ready 9 >/dev/null
setstate "done" null; crew dispatch 9 >/dev/null; WT9=$(crew get 9 worktree)
check "an unstacked task records the commit it started from" '[[ $(crew get 9 base_sha) == "$(git rev-parse origin/main)" ]]'
F10=$(crew new "Season admin" --branch season-admin --on 9); fill "$F10"; crew ready 10 >/dev/null
check "--on bases the task on its parent's branch and keeps the dependency" '[[ $(crew get 10 base) == agent/season-model && $(crew get 10 depends_on) == 009-season-model ]]'
setstate "done" null; out=$(crew dispatch 10 2>&1)
check "a stacked task waits until its parent's branch is on origin" '[[ $out == *"on origin yet"* && $(crew get 10 status) == ready && -z $(crew get 10 worktree) ]]'
work "$WT9" model.txt; crew set 9 pr https://github.com/isaac/webapp/pull/21; crew set 9 status pr-open
echo "MAX_IN_FLIGHT=$((n0 + 1))" >>"$PD/project.env"
check "a pushed, unmerged parent is enough to dispatch, even at the in-flight cap" 'crew dispatch 10 >/dev/null 2>&1'
WT10=$(crew get 10 worktree); T9=$(tip season-model)
check "the stacked worktree starts on the parent's tip, which is recorded" '[[ $(git -C "$WT10" rev-parse HEAD) == "$T9" && $(crew get 10 base_sha) == "$T9" ]]'
check "the worker is told which task and PR it is stacked on" 'grep -q "stacked on task 009-season-model (PR #21): agent/season-model" "$HOME/claude-calls.log"'
check "a stack counts once against MAX_IN_FLIGHT" '[[ $(inflight) == $((n0 + 1)) ]]'
setstate "done" null
check "an unstacked task still stops at the in-flight cap" 'fill "$F7"; crew ready 7 >/dev/null; ! crew dispatch 7 >/dev/null 2>&1'
sed -i '/^MAX_IN_FLIGHT=/d' "$PD/project.env"
work "$WT10" admin.txt; crew set 10 pr https://github.com/isaac/webapp/pull/22; crew set 10 status pr-open
out=$(crew status)
check "status lists a stacked task under its parent, columns aligned" '[[ $(grep -A1 "^  009-season-model " <<<"$out" | tail -1) == "  └ 010-season-admin                 pr-open "* ]]'
out=$(crew sync)
check "sync leaves a stack alone while the parent hasn't moved" '[[ $out != *restack* && $(crew get 9 status) == pr-open ]]'

work "$WT9" model2.txt; T9b=$(tip season-model)
out=$(crew sync)
check "sync reports a child whose parent's branch moved, without restacking it" '[[ $out == *"010-season-admin: needs a restack, 009-season-model moved"* && $(crew get 10 base_sha) == "$T9" && $(git rev-parse "$(tip season-admin)~1") == "$T9" ]]'
check "status shows the restack that's needed" '[[ $(crew status) == *"needs restack: 009-season-model moved"* ]]'
setstate "done" null; crew restack 10 >/dev/null
check "restack sends the worker the rebase, cut at the parent's old tip" '[[ $(last_prompt) == *"git rebase --onto $T9b $T9\`"* && $(last_prompt) != *"gh pr edit --base"* && $(crew get 10 status) == working ]]'
setstate "done" null; n=$(grep -c -- "--resume" "$HOME/claude-calls.log")
check "restack won't send the same restack twice" '[[ $(crew restack 10 2>&1) == *"already asked"* && $(grep -c -- "--resume" "$HOME/claude-calls.log") == "$n" && $(crew get 10 restack_sent) == "$T9b" ]]'
check "restack --again sends it anyway" 'crew restack 10 --again >/dev/null && [[ $(grep -c -- "--resume" "$HOME/claude-calls.log") == $((n + 1)) ]]'
(cd "$WT10" && git rebase -q --onto "$T9b" "$T9" && git push -q --force-with-lease 2>/dev/null); crew set 10 status pr-open
out=$(crew sync)
check "sync sees the restack and moves base_sha to the parent's new tip" '[[ $out != *restack* && $(crew get 10 base_sha) == "$T9b" ]]'
setstate "done" null
check "restack has nothing to do for a task that's up to date" '[[ $(crew restack 10) == *"nothing to restack"* ]]'
check "restack refuses a task that isn't stacked" '! crew restack 9 2>/dev/null'

F11=$(crew new "Season picker" --branch season-picker --on 10); fill "$F11"; crew ready 11 >/dev/null
crew dispatch 11 >/dev/null; WT11=$(crew get 11 worktree); T10=$(tip season-admin)
work "$WT11" picker.txt; crew set 11 status pr-open
check "status nests a three-deep stack in order" '[[ $(crew status | grep -A2 "^  009-season-model " | tail -1) == "    └ 011-season-picker "* ]]'
# The user squash-merges 9, and GitHub deletes its branch.
git merge -q --squash origin/agent/season-model >/dev/null && git commit -qm "Season model (#21)" && git push -q origin main
git push -q origin --delete agent/season-model
crew set 9 status merged
out=$(crew sync)
check "sync reports the child of a merged parent" '[[ $out == *"010-season-admin: needs a restack, 009-season-model merged"* ]]'
check "cleanup of a merged parent leaves its stack workable" 'crew cleanup 9 >/dev/null && ! git show-ref --verify --quiet refs/heads/agent/season-model'
crew observe 10 >/dev/null
check "observe falls back to base_sha when the parent's branch is gone" 'grep -q "Review the change $T9b...HEAD" "$HOME/claude-calls.log"'
setstate "done" null
check "restack goes bottom-up" '[[ $(crew restack 11 2>&1) == *"restack it first"* ]]'
crew restack 10 >/dev/null; M=$(git rev-parse origin/main)
check "after a merge, restack targets the default branch and has the worker retarget the PR" '[[ $(last_prompt) == *"git rebase --onto $M $T9b\`"* && $(last_prompt) == *"gh pr edit --base main"* && $(last_prompt) == *"crew set 010-season-admin base main"* ]]'
check "crew leaves base alone until the worker has restacked" '[[ $(crew get 10 base) == agent/season-model ]]'
(cd "$WT10" && git rebase -q --onto "$M" "$T9b" && git push -q --force-with-lease 2>/dev/null); crew set 10 base main; crew set 10 status pr-open
T10b=$(tip season-admin)
out=$(crew sync)
check "a restacked parent is done, and its own child is next" '[[ $out != *"010-season-admin: needs"* && $out == *"011-season-picker: needs a restack, 010-season-admin moved"* ]]'
setstate "done" null; crew restack 11 >/dev/null
check "restack cuts at the parent's old tip after the parent was rebased" '[[ $(last_prompt) == *"git rebase --onto $T10b $T10\`"* ]]'
(cd "$WT11" && git rebase -q --onto "$T10b" "$T10" && git push -q --force-with-lease 2>/dev/null); crew set 11 status pr-open
out=$(crew sync)
check "the rebased stack holds only its own commits on top of main" '[[ $(git rev-list --count "$M..$(tip season-picker)") == 2 && $out != *restack* && $(crew get 11 base_sha) == "$T10b" ]]'
check "--on refuses a merged task" '! crew new "Too late" --on 9 2>/dev/null'
F12=$(crew new "Season export" --branch season-export --on 11); fill "$F12"; crew ready 12 >/dev/null
crew set 11 status merged
setstate "done" null; crew dispatch 12 >/dev/null
check "a task whose parent merged before dispatch starts from the parent's base" '[[ $(crew get 12 base) == agent/season-admin && $(git -C "$(crew get 12 worktree)" rev-parse HEAD) == "$T10b" ]]'
rm -f "$HOME/gh-pr-state"

# crew tick, starting from a ledger with nothing in flight.
for f in "$PD"/tasks/*.md; do has=$(crew get "$f" status); [[ $has == merged ]] || crew set "$f" status abandoned; done
crew cleanup --merged >/dev/null 2>&1
echo OPEN >"$HOME/gh-pr-state"
resumes() { grep -c -- "--resume" "$HOME/claude-calls.log"; }
F13=$(crew new "Tick routing" --branch tick-routing); fill "$F13"; crew ready 13 >/dev/null
crew dispatch 13 >/dev/null; crew set 13 pr https://github.com/isaac/webapp/pull/12; crew set 13 status pr-open
setstate working 4242; n=$(resumes)
out=$(crew tick)
check "tick records a new round but leaves a busy worker alone" '[[ $out == *"013-tick-routing: review round 1 recorded"* && $out == *"013-tick-routing: round 1 waits for its worker (working)"* && $out == *"2 comment(s) from collaborators, for you"* && $(crew get 13 last_round_sent) == 0 && $(resumes) == "$n" && $(crew get 13 status) == pr-open ]]'
setstate "done" null
out=$(crew tick)
check "tick sends the round once the worker is idle, without recording it again" '[[ $out == "013-tick-routing: round 1 sent to its worker" && $(crew get 13 last_round_sent) == 1 && $(crew get 13 review_rounds) == 1 && $(resumes) == $((n + 1)) && $(crew get 13 status) == addressing-feedback ]]'
crew set 13 status pr-open; setstate working 4242
out=$(crew tick)
check "a task with open questions isn't marked waiting on the user while its worker is busy" '[[ -z $out && $(crew get 13 status) == pr-open ]]'
setstate "done" null
out=$(crew tick)
check "an idle worker's task with unchecked open questions is waiting on the user" '[[ $out == "013-tick-routing: waiting on you (2 open question(s))" && $(crew get 13 status) == needs-input ]]'
calls=$(grep -c "pulls/12/reviews" "$HOME/gh-calls.log")
out=$(crew tick)
check "a tick with nothing to do prints nothing and resumes no one" '[[ -z $out && $(resumes) == $((n + 1)) && $(crew get 13 status) == needs-input ]]'
check "tick doesn't read the comments of a PR that hasn't changed since they were last read" '[[ $(grep -c "pulls/12/reviews" "$HOME/gh-calls.log") == "$calls" ]]'
echo 2099-01-01T00:00:00Z >"$HOME/gh-pr-updated"; crew tick >/dev/null
check "tick reads them again once GitHub says the PR changed" '[[ $(crew get 13 pr_updated) == 2099-01-01T00:00:00Z && $(grep -c "pulls/12/reviews" "$HOME/gh-calls.log") -gt "$calls" ]]'
rm -f "$HOME/gh-pr-updated"
crew set 13 feedback_since 1970-01-01T00:00:00Z; setstate blocked 4242 waiting; n=$(resumes)
out=$(crew tick)
check "tick holds a round for a worker with an open prompt, and says so" '[[ $out == *"013-tick-routing: round 2 waits for its worker (blocked)"* && $(crew get 13 last_round_sent) == 1 && $(resumes) == "$n" ]]'
setstate blocked 4242 busy
out=$(crew tick)
check "tick sends a round to a worker that ended its turn on a question" '[[ $out == *"013-tick-routing: round 2 sent to its worker"* && $(crew get 13 last_round_sent) == 2 && $(resumes) == $((n + 1)) && $(crew get 13 status) == addressing-feedback ]]'
setstate "done" null; crew set 13 status pr-open
s13=$(crew get 13 worker_session); crew set 13 feedback_since 1970-01-01T00:00:00Z; setstate working 4242 busy; transcript "$s13" ended; n=$(resumes)
check "status shows a worker whose turn ended as idle, though a shell it left running keeps it working" '[[ $(crew status) == *"worker: idle, background task running"* ]]'
out=$(crew tick)
check "tick sends a round to a worker whose turn ended, though a shell it left running keeps it working" '[[ $out == *"013-tick-routing: round 3 sent to its worker"* && $(crew get 13 last_round_sent) == 3 && $(resumes) == $((n + 1)) ]]'
rm -f "$HOME/.claude/projects/stub/$s13.jsonl"; setstate "done" null; crew set 13 status pr-open
out=$(flock "$L/.crew-tick.lock" crew tick)
check "a second tick doesn't run while one is running" '[[ $out == "another crew tick is running" ]]'
check "launched sessions never inherit crew's lock fds" '! grep -q "LEAK LOCK FD" "$HOME/claude-calls.log"'
mkdir -p "$L/projects/zz-other"; printf 'REMOTE="%s"\nDEFAULT_BRANCH="main"\n' "$SB/other.git" >"$L/projects/zz-other/project.env"
out=$(crew tick 2>&1 >/dev/null)
check "tick skips a project with no clone here, rather than using the previous project's" '[[ $out == *"zz-other: skipped"* && $out == *"no clone of zz-other on this machine"* ]]'
rm -r "$L/projects/zz-other"

# Restacks: 15 is stacked on 14; 14's branch moves.
F14=$(crew new "Tick parent" --branch tick-parent); fill "$F14"; crew ready 14 >/dev/null
crew set 13 status abandoned; setstate "done" null; crew dispatch 14 >/dev/null; WT14=$(crew get 14 worktree)
work "$WT14" p1.txt; crew set 14 pr https://github.com/isaac/webapp/pull/31; crew set 14 status pr-open
F15=$(crew new "Tick child" --branch tick-child --on 14); fill "$F15"; crew ready 15 >/dev/null
crew dispatch 15 >/dev/null; WT15=$(crew get 15 worktree)
work "$WT15" c1.txt; crew set 15 pr https://github.com/isaac/webapp/pull/32; crew set 15 status pr-open
work "$WT14" p2.txt; setstate "done" null; n=$(resumes)
out=$(crew tick)
check "tick restacks a stacked task whose parent moved" '[[ $out == "015-tick-child: restack sent to its worker (014-tick-parent moved)" && $(last_prompt) == *"git rebase --onto $(tip tick-parent)"* && $(resumes) == $((n + 1)) ]]'
crew set 15 status pr-open; setstate "done" null
out=$(crew tick 2>&1)
check "tick doesn't send the same restack again when the worker didn't manage it" '[[ -z $out && $(resumes) == $((n + 1)) ]]'

echo MERGED >"$HOME/gh-pr-state"; setstate "done" null; touch "$HOME/rm-fail"
out=$(crew tick 2>&1)
check "tick cleans up merged tasks" '[[ $out == *"014-tick-parent: merged; worktree cleaned up"* && ! -d $WT14 && $(crew get 14 status) == merged ]]'
check "a worker that won't leave agent view is reported, not a reason to keep the worktree" '[[ $out == *"couldn'"'"'t remove worker $(crew get 14 worker_job) from agent view"* ]]'
rm -f "$HOME/rm-fail"
rm -f "$HOME/gh-pr-state"

# Failover: a task whose worktree isn't on this machine, as on one the task moved to.
echo OPEN >"$HOME/gh-pr-state"; setstate "done" null
F16=$(crew new "Moved task" --branch moved-task); fill "$F16"; crew ready "$F16" >/dev/null; crew dispatch "$F16" >/dev/null
WT16=$(crew get "$F16" worktree); work "$WT16" m.txt; T16=$(tip moved-task)
git -C "$SB/webapp" worktree remove --force "$WT16"; git -C "$SB/webapp" branch -q -D agent/moved-task
crew set "$F16" worktree "/elsewhere/agent-work/$(basename "$PD")/$(basename "$F16" .md)"
setstate "done" null; crew followup "$F16" --note "carry on" >/dev/null 2>&1
check "a followup brings back a worktree whose branch is on origin, at its pushed commit" '[[ $(git -C "$WT16" rev-parse HEAD) == "$T16" && $(git -C "$WT16" branch --show-current) == agent/moved-task ]]'
check "and records this machine's path for it, saying how it came back" '[[ $(crew get "$F16" worktree) == "$WT16" ]] && grep -q "recreated worktree from origin/agent/moved-task" "$F16"'
F17=$(crew new "Never pushed" --branch never-pushed); fill "$F17"; crew ready "$F17" >/dev/null; setstate "done" null; crew dispatch "$F17" >/dev/null
WT17=$(crew get "$F17" worktree); (cd "$WT17" && echo x >n.txt && git add n.txt && git commit -qm "not pushed")
rm -rf "$WT17"; git -C "$SB/webapp" worktree prune; git -C "$SB/webapp" branch -q -D agent/never-pushed
setstate "done" null; crew followup "$F17" --note "carry on" >/dev/null 2>&1
check "a task whose branch never reached origin starts over from its base, and says so" '[[ $(git -C "$WT17" rev-parse HEAD) == $(git rev-parse origin/main) && $(crew get "$F17" base_sha) == $(git rev-parse origin/main) ]] && grep -q "started over: agent/never-pushed was never pushed from another machine" "$F17"'
crew set "$F17" worktree "/elsewhere/x"; rm -rf "$WT17"; git -C "$SB/webapp" worktree prune
out=$(crew observe "$F17" 2>&1)
check "observe has nothing to do for a worktree that isn't here" '[[ $out == *"nothing to observe"* ]]'
crew set "$F16" status abandoned >/dev/null; crew set "$F16" worktree "/elsewhere/y"; rm -rf "$WT16"; git -C "$SB/webapp" worktree prune
out=$(crew cleanup "$F16" 2>&1)
check "cleanup of a task whose worktree isn't here just clears it" '[[ $out == *"cleaned up"* && -z $(crew get "$F16" worktree) ]]'
crew set "$F17" status abandoned >/dev/null
# crew tick re-homes a task in progress whose worker and worktree stayed on another machine.
moved_away() {  # file: its job unknown here, its worktree gone, as on the machine it moved to
  local j; j=$(crew get "$1" worker_job)
  jq --arg id "$j" 'map(select(.id != $id))' "$HOME/agents.json" >"$HOME/a" && mv "$HOME/a" "$HOME/agents.json"
  rm -rf "$(crew get "$1" worktree)"; git -C "$SB/webapp" worktree prune
}
fresh_launches() { grep -c -- "--agent implementer" "$HOME/claude-calls.log"; }
F18=$(crew new "Working when it moved" --branch working-moved); fill "$F18"; crew ready "$F18" >/dev/null; setstate "done" null; crew dispatch "$F18" >/dev/null
WT18=$(crew get "$F18" worktree); work "$WT18" w.txt; moved_away "$F18"; setstate "done" null; n=$(fresh_launches)
out=$(crew tick 2>&1)
check "tick gives a working task that moved here a fresh worker, in a worktree made here" '[[ $out == *"$(basename "$F18" .md): its worker and worktree aren"?"t on this machine; a fresh worker picks it up"* && $(fresh_launches) == $((n + 1)) && -d $WT18 ]] && grep -q "This task moved to tower; your earlier session is gone" "$HOME/claude-calls.log"'
out=$(crew tick 2>&1)
check "and only once: the next tick finds the new worker" '[[ $out != *"a fresh worker picks it up"* && $(fresh_launches) == $((n + 1)) ]]'
crew set "$F18" status abandoned >/dev/null
F19=$(crew new "Open PR when it moved" --branch pr-moved); fill "$F19"; crew ready "$F19" >/dev/null; setstate "done" null; crew dispatch "$F19" >/dev/null
WT19=$(crew get "$F19" worktree); work "$WT19" p.txt
crew set "$F19" pr https://github.com/isaac/webapp/pull/12; crew set "$F19" status pr-open; crew set "$F19" feedback_since 2099-01-01T00:00:00Z
moved_away "$F19"; setstate "done" null; n=$(fresh_launches)
out=$(crew tick 2>&1)
check "tick leaves a moved task with an open PR alone while nothing waits for its worker" '[[ $out != *"$(basename "$F19" .md)"* && ! -d $WT19 && $(fresh_launches) == "$n" ]]'
crew set "$F19" feedback_since 1970-01-01T00:00:00Z
out=$(crew tick 2>&1)
check "when a round arrives, its followup makes the worktree here" '[[ $out == *"$(basename "$F19" .md): round 1 sent to its worker"* && -d $WT19 && $(git -C "$WT19" branch --show-current) == agent/pr-moved ]]'
crew set "$F19" status abandoned >/dev/null
rm -f "$HOME/gh-pr-state"

# Goals: a goal lead plans every item; crew tick dispatches the ready tasks, passes
# the goal lead its input, and closes the goal once every task is done.
gget() { sed -n "s/^$2: //p" "$1" | head -n 1; }  # goal-file key
last_launch() {  # the latest --bg call, with every line of its prompt
  awk '/^claude / {bg = / --bg /; if (bg) c = $0; next} bg {c = c "\n" $0} END {print c}' "$HOME/claude-calls.log"; }
echo OPEN >"$HOME/gh-pr-state"; setstate "done" null
GF=$(crew goal new "Ship the season page"); G=001-ship-the-season-page
check "goal new creates a draft goal" '[[ $GF == "$PD/goals/$G.md" && $(gget "$GF" status) == draft && $(crew goal path 1) == "$GF" ]]'
check "goal start refuses a goal without the user's words" '! crew goal start 1 2>/dev/null && [[ $(gget "$GF" status) == draft ]]'
sed -i '/^## Goal$/a "Ship every card in the Season column." (tracker: the Season column)' "$GF"
out=$(crew goal start 1); GJ=$(gget "$GF" lead_job); CO=$(gget "$GF" checkout)
gl=$(grep -- "--agent goal-lead" "$HOME/claude-calls.log" | tail -1)
check "goal start launches the goal lead with the home as an extra directory" '[[ $(gget "$GF" status) == running && -n $GJ && $gl == *"--add-dir $L --bg --agent goal-lead --permission-mode auto --settings {"*"} You are the goal lead for goal $G "* ]]'
check "the goal lead gets the lead's settings, all but their agent" '[[ $(cat "$HOME/claude-settings.last") == "$(jq -c "del(.agent)" "$L/lead/.claude/settings.json")" ]] && jq -e ".hooks.PreToolUse and (has(\"agent\") | not)" "$HOME/claude-settings.last" >/dev/null'
check "the goal lead works in a detached checkout of the default branch" '[[ $(jq -r --arg id "$GJ" ".[] | select(.id == \$id) | .cwd" "$HOME/agents.json") == "$CO" && -z $(git -C "$CO" branch --show-current) && $(git -C "$CO" rev-parse HEAD) == $(git -C "$SB/webapp" rev-parse origin/main) ]]'
GT1=$(crew new "Season header" --goal 1); T1=$(basename "$GT1" .md)
GT2=$(crew new "Season standings" --goal 1); T2=$(basename "$GT2" .md)
NG=$(crew new "Not in a goal"); fill "$NG"; crew ready "$NG" >/dev/null
check "new --goal makes the task one of the goal's" '[[ $(crew get "$GT1" goal) == "$G" && -z $(crew get "$NG" goal) ]]'
check "new --goal refuses a goal that doesn't exist" '! crew new "Stray" --goal 9 2>/dev/null'
sed -i '/^## Open questions$/a - [ ] Add a standings table? It needs a migration. I recommend yes.' "$GT2"
check "status lists the goal with its tasks under it, and a parked task's open questions" '[[ $(crew status) == *"goal $G"*"running"*"0 of 2 task(s) merged"$'"'"'\n'"'"'"    $T1"*$'"'"'\n'"'"'"    $T2"*"draft"*"1 open question(s)"* ]]'
fill "$GT1"; crew ready "$GT1" >/dev/null
echo "MAX_WORKERS=0" >>"$L/config/crew.env"
out=$(crew tick 2>&1)
check "a goal task held back by a cap waits without a word" '[[ $out != *"$T1"* && $(crew get "$GT1" status) == ready ]]'
sed -i '/^MAX_WORKERS=0$/d' "$L/config/crew.env"
out=$(crew tick 2>&1)
check "tick dispatches a running goal's ready task, and no other ready task" '[[ $out == *"$T1: dispatched for goal $G"* && $(crew get "$GT1" status) == working && $(crew get "$NG" status) == ready ]]'
# The stub's PR has comments of its own; none of these tests are about them.
crew set "$GT1" feedback_since 2099-01-01T00:00:00Z; crew set "$GT1" status needs-input
sed -i '/^## Open questions$/a - [ ] @bob commented on PR #40, comment 77 — [link](u77)' "$GT1"
setstate "done" null; n=$(resumes)
out=$(crew tick 2>&1)
check "a collaborator's comment on a goal task isn't sent to the goal lead" '[[ $out != *"goal $G"* && $(resumes) == "$n" ]]'
sed -i '/^## Open questions$/a - [ ] Which header style? A or B. I recommend A.' "$GT1"
out=$(crew tick 2>&1)
check "tick sends a goal task's worker question to the goal lead" '[[ $out == *"goal $G: input sent to its goal lead"* && $(last_launch) == "claude --resume $(gget "$GF" lead_session) --bg New input for goal $G"*"$T1: its worker has asked something"* && $(resumes) == $((n + 1)) ]]'
setstate "done" null
out=$(crew tick 2>&1)
check "a question the goal lead has already been sent isn't sent again" '[[ $out != *"goal $G"* && $(resumes) == $((n + 1)) ]]'
setstate working 4242
out=$(crew goal note 1 "Use style B for the header" 2>&1)
check "a note to a busy goal lead is recorded and waits" '[[ $out == *"crew tick passes it to the goal lead"* && $(section "$GF" "## Decisions") == *"from the user: Use style B for the header"* && $(gget "$GF" unsent_notes) == 1 ]]'
out=$(crew tick 2>&1)
check "tick says when input waits for a busy goal lead" '[[ $out == *"goal $G: input waits for its goal lead (working)"* ]]'
setstate "done" null
out=$(crew tick 2>&1)
check "tick sends the note once the goal lead is free" '[[ $out == *"goal $G: input sent to its goal lead"* && $(last_launch) == *"1 new note(s) from the user"* && $(last_launch) != *"$T1:"* && $(gget "$GF" unsent_notes) == 0 ]]'
rm -rf "$CO"; setstate "done" null   # as on a machine the goal moved to
crew goal note 1 "Keep the header small" >/dev/null 2>&1
check "a goal followup brings back a goal lead's checkout that isn't here" '[[ -d $CO && $(git -C "$CO" rev-parse HEAD) == $(git -C "$SB/webapp" rev-parse origin/main) && $(last_launch) == "claude --resume "*"New input for goal $G"* ]]'
crew set "$GT1" status merged; crew set "$GT2" status abandoned; setstate "done" null
out=$(crew tick 2>&1)
check "tick closes a goal once every task is merged or abandoned, removing its goal lead and checkout" '[[ $out == *"goal $G: done"* && $(gget "$GF" status) == done && ! -d $CO && -z $(gget "$GF" checkout) ]] && grep -qx "claude rm $GJ" "$HOME/claude-calls.log"'
out=$(crew goal note 1 "Pick up the new cards" 2>&1)
check "a note reopens a done goal, in a fresh checkout" '[[ $out == *"input sent to its goal lead"* && $(gget "$GF" status) == running && -d $CO ]]'
GT3=$(crew new "Season footer" --goal 1); fill "$GT3"; crew ready "$GT3" >/dev/null
setstate working 4242
out=$(crew goal abandon 1 2>&1)
check "abandon stops the goal lead, mid-turn too, and removes its checkout" '[[ $out == *"goal $G: abandoned"* && $(gget "$GF" status) == abandoned && ! -d $CO ]] && grep -qx "claude stop $(gget "$GF" lead_job)" "$HOME/claude-calls.log"'
setstate "done" null
out=$(crew tick 2>&1)
check "an abandoned goal's ready task is left for the lead to dispatch" '[[ $out != *"$(basename "$GT3" .md)"* && $(crew get "$GT3" status) == ready ]]'
check "a task can't join an abandoned goal" '! crew new "Late" --goal 1 2>/dev/null'
rm -f "$HOME/gh-pr-state"

summary crew-e2e
