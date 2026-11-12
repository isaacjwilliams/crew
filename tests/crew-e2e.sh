#!/usr/bin/env bash
# crew end to end: a bare "remote", a clone, a fresh crew home, and stub claude/gh.
# Covers new/ready/dispatch/feedback/followup/attach/reply/sync/cleanup/gardener/promote.
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
# The latest --settings sets CREW_PROJECT to project $1 and CREW_HOME to the home: a
# background session takes its environment from Claude Code's daemon, not from crew.
session_env() { jq -e --arg p "$1" --arg h "$L" '.env.CREW_PROJECT == $p and .env.CREW_HOME == $h' "$HOME/claude-settings.last" >/dev/null; }

crew project add >/dev/null
PE=$(ls "$L"/projects/*/project.env); PD=$(dirname "$PE"); P=$(basename "$PD")
check "project add records the remote in project.env, and this machine's clone in local.env" '[[ $(crew config REMOTE) == "$SB/remote.git" ]] && ! grep -q "^REPO=" "$PE" && [[ $(cat "$PD/local.env") == *"REPO=\"$SB/webapp\""* ]]'
check "local.env isn't committed, project.env is" '[[ -z $(git -C "$L" ls-files "$PD/local.env") && -n $(git -C "$L" ls-files "$PE") && -z $(git -C "$L" status --porcelain) ]]'
check "crew config --project reads a project from anywhere" '[[ $(cd "$HOME" && crew config REPO --project "$P") == "$SB/webapp" ]]'
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)   # it checks claude --version, not a session
check "crew doctor finds this machine's clone" '[[ $doctor_out == *"ok   $P: cloned on this machine"* ]]'
check "crew doctor checks that the lead, workers and gardener load project memory" '[[ $(grep -c "ok   .* loads project memory (crew memory --hook)" <<<"$doctor_out") == 3 ]]'
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
check "crew.env overrides the defaults" '[[ $(crew config MAX_WORKERS) == 7 && $(crew config GOAL_MAX_IN_FLIGHT) == 3 ]]'
echo 'MAX_WORKERS=2' >>"$PE"
check "project.env overrides crew.env" '[[ $(crew config MAX_WORKERS) == 2 ]]'
check "automated review runs up to 3 times by default, and crew config lists it" '[[ $(crew config AUTO_REVIEWS) == 3 ]] && crew config | grep -qx AUTO_REVIEWS=3'
echo 'AUTO_REVIEWS=0' >>"$PE"
check "a project can turn automated review off" '[[ $(cd "$HOME" && crew config AUTO_REVIEWS --project "$P") == 0 ]]'
sed -i '/^AUTO_REVIEWS=0$/d' "$PE"
check "Codex is off in automated review by default, with 2 rounds between the reviewers when it's on" '[[ $(crew config CODEX_REVIEW) == no && $(crew config AUTO_REVIEW_REBUTTALS) == 2 ]] && crew config | grep -qx CODEX_MODEL_EFFORT=xhigh'
# crew codex: one read-only review by Codex, through tests/stubs/codex.
mkdir -p "$SB/rev" && echo "Adversarially review the briefing." >"$SB/rev/prompt.md"
out=$(cd "$SB" && crew codex webapp rev/prompt.md rev/answer-0 2>&1); st=$?
check "crew codex runs codex exec read-only with approvals off, in a new session in the review root, from paths relative to the caller" \
  '((st == 0)) && grep -qxF "codex exec -c sandbox_mode=\"read-only\" -c approval_policy=\"never\" -m gpt-6-astra -c model_reasoning_effort=\"xhigh\" --skip-git-repo-check --json -o $SB/rev/answer-0.md -C $SB/webapp - (in $SB/webapp)" "$HOME/codex-calls.log"'
check "crew codex gives Codex the prompt file on stdin, and writes its reply and events" \
  'cmp -s "$SB/rev/prompt.md" "$HOME/codex-stdin.last" && [[ $(cat "$SB/rev/answer-0.md") == "stub review" ]] && grep -q turn.completed "$SB/rev/answer-0.events.jsonl" && [[ $out == "final message: $SB/rev/answer-0.md" ]]'
echo 'CODEX_MODEL="gpt-next"' >>"$PE"
crew codex "$SB/webapp" "$SB/rev/prompt.md" "$SB/rev/answer-1" >/dev/null 2>&1
check "crew codex uses the project's model" '[[ $(tail -1 "$HOME/codex-calls.log") == *" -m gpt-next "* ]]'
sed -i '/^CODEX_MODEL=/d' "$PE"
check "crew codex takes no subcommand: a stray argument is refused" '! crew codex start "$SB/webapp" "$SB/rev/prompt.md" "$SB/rev/answer-2" 2>/dev/null'
touch "$HOME/codex-fail"
out=$(crew codex "$SB/webapp" "$SB/rev/prompt.md" "$SB/rev/answer-0" 2>&1); st=$?
check "crew codex passes on a failing codex's status" '((st == 3)) && [[ $out == *"codex exited with status 3"* ]]'
rm "$HOME/codex-fail"
check "CLAUDECODE never reaches codex" '! grep -q LEAK "$HOME/codex-calls.log"'
echo 'CODEX_REVIEW=yes' >>"$L/config/crew.env"
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)
check "with CODEX_REVIEW=yes in crew.env, crew doctor checks each project's codex is installed and signed in" \
  '[[ $doctor_out == *"ok   $P: codex installed (CODEX_REVIEW=yes)"* && $doctor_out == *"ok   $P: codex signed in"* ]]'
touch "$HOME/codex-signed-out"
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1); st=$?
check "crew doctor fails its required checks for a signed-out codex" '((st != 0)) && [[ $doctor_out == *"FAIL $P: codex signed in — run '"'codex login'"'"* && $doctor_out == *"Some required checks failed."* ]]'
rm "$HOME/codex-signed-out"
mkdir -p "$SB/no-codex"
for s in "$TESTS_DIR"/stubs/*; do [[ $s == */codex ]] || ln -sf "$s" "$SB/no-codex/"; done
no_codex_path=$SB/no-codex
IFS=: read -ra dirs <<<"$PATH"
for d in "${dirs[@]}"; do [[ $d == "$TESTS_DIR/stubs" || -e $d/codex ]] || no_codex_path+=:$d; done
doctor_out=$(env -u CLAUDECODE PATH="$no_codex_path" crew doctor 2>&1); st=$?
check "crew doctor fails its required checks when CODEX_REVIEW=yes and codex isn't installed" '((st != 0)) && [[ $doctor_out == *"FAIL $P: codex installed (CODEX_REVIEW=yes)"* ]]'
printf 'CODEX_REVIEW=maybe\nAUTO_REVIEW_REBUTTALS=3\n' >>"$PE"
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)
check "crew doctor reports a CODEX_REVIEW that isn't yes or no, and AUTO_REVIEW_REBUTTALS outside 0-2" \
  '[[ $doctor_out == *"FAIL $P: CODEX_REVIEW is yes or no (got '"'maybe'"')"* && $doctor_out == *"FAIL $P: AUTO_REVIEW_REBUTTALS is 0, 1 or 2 (got '"'3'"')"* ]]'
sed -i -e '/^CODEX_REVIEW=maybe$/d' -e '/^AUTO_REVIEW_REBUTTALS=3$/d' "$PE"; sed -i '/^CODEX_REVIEW=yes$/d' "$L/config/crew.env"
sed -i '/^MAX_WORKERS=2$/d' "$PE"; rm "$L/config/crew.env"
F1=$(crew new "Saved search filters")
check "a ledger commit reaches origin soon after the command" 'pushed && [[ $(git --git-dir="$SB/home.git" log -1 --format=%s main) == "new "*"/001-saved-search-filters" ]]'
config=$(stat -c %i "$L/.git/config"); crew log 1 "pushed" >/dev/null
check "a ledger push leaves the home's git config alone, so crew tick always finds the upstream there" 'pushed && [[ $(stat -c %i "$L/.git/config") == "$config" ]]'
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
git -C "$L" branch -q --unset-upstream
out=$(crew tick 2>&1)
check "a tick that can't read origin's branch doesn't stand down" '[[ $out != *"standing down"* && $(cat "$L/host") == tower ]]'
git -C "$L" branch -q -u origin/main
# An agent's own edits, through its PostToolUse hook.
hook() { jq -n --arg t "$1" '{tool_name: $t, tool_input: {command: "python3 - <<EOF"}}' | crew commit --hook; }
echo "- [ ] A question from the hook test" >>"$F1"
check "an agent's edit to a task file is committed by its hook, and pushed" 'hook Edit && [[ $(git -C "$L" log -1 --format=%s) == "edit projects/$P/tasks/001-saved-search-filters.md" ]] && pushed'
echo "more" >>"$L/reports/note.md"; echo "- [ ] Another question" >>"$F1"
check "a script that edits two ledger files through Bash is committed as one edit" 'hook Bash && [[ $(git -C "$L" log -1 --format=%s) == "edit 2 files" ]] && git -C "$L" diff --quiet @'
n=$(git -C "$L" rev-list --count @)
echo "# a local change" >>"$L/config/defaults.env"
check "a call that changed nothing in the ledger commits nothing, and leaves the home's own files alone" 'hook Bash && [[ $(git -C "$L" rev-list --count @) == "$n" ]] && ! git -C "$L" diff --quiet -- config/defaults.env'
git -C "$L" checkout -q -- config/defaults.env
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
check "workers get the shipped settings, with their project and the home, when the project adds none" \
  '[[ $(jq -c "del(.env)" "$HOME/claude-settings.last") == "$(jq -c . "$L/config/worker-settings.json")" ]] && session_env "$P"'
check "git worktrees don't call wt" '[[ ! -e $HOME/wt-calls.log ]]'
check "worker can update the ledger from its worktree" '(cd "$WT" && crew set 1 pr https://github.com/isaac/webapp/pull/12 && crew set 1 status pr-open) >/dev/null'
check "find by PR number" '[[ $(crew find "#12") == 001-saved-search-filters ]]'
check "attach attaches to the task's worker from the current directory" \
  '(cd "$WT" && env -u CLAUDECODE crew attach 1) && [[ $(tail -1 "$HOME/claude-calls.log") == "claude attach $(crew get 1 worker_job)" && $(cat "$HOME/claude.cwd") == "$WT" ]]'
check "attach takes a PR number" 'env -u CLAUDECODE crew attach "#12" && [[ $(tail -1 "$HOME/claude-calls.log") == "claude attach $(crew get 1 worker_job)" ]]'
calls=$(wc -l <"$HOME/claude-calls.log"); out=$(env -u CLAUDECODE crew attach 2 2>&1)
check "attach refuses a task that has no worker, and runs no claude" '[[ $out == *"no worker has run for 002-"*" yet" && $(wc -l <"$HOME/claude-calls.log") == "$calls" ]]'
calls=$(wc -l <"$HOME/claude-calls.log"); out=$(crew attach 1 2>&1)
check "attach refuses inside a Claude Code session, and runs no claude" '[[ $out == *"needs your own terminal"* && $(wc -l <"$HOME/claude-calls.log") == "$calls" ]]'
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
check "a review-round followup tells the worker to reply with crew reply" 'grep -- "--resume" "$HOME/claude-calls.log" | tail -1 | grep -q "Reply to each comment in those rounds with .crew reply."'
setstate "done" null
check "note-only followup doesn't resend the old round" 'crew followup 1 --note "use defaults" >/dev/null && grep -- "--resume" "$HOME/claude-calls.log" | tail -1 | grep -v "round 1 under" | grep -q "use defaults" && [[ $(crew get 1 status) == working ]]'
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
check "reply refuses an ID that isn't a number" '[[ $(crew reply 1 abc x 2>&1) == *"comment id must be a number"* && ! -e $HOME/gh-last-body ]]'
reacted() { grep -c "^gh api graphql --silent -f query=mutation.*addReaction.* -f subject=$1 -f content=$2\$" "$HOME/gh-calls.log"; }   # node-id CONTENT
reactions() { grep -c "^gh api graphql " "$HOME/gh-calls.log"; }
out=$(crew react 1 eyes 900 111 115 2>&1)
check "react adds 👀 to a review summary, an inline and a conversation comment in one call, by node ID" '[[ $out == "reacted eyes to 3 comment(s) on PR #12" && $(reacted PRR_900 EYES) == 1 && $(reacted PRRC_111 EYES) == 1 && $(reacted IC_115 EYES) == 1 && $(reactions) == 3 ]]'
check "react adds 🚀" 'crew react 1 rocket 111 >/dev/null && [[ $(reacted PRRC_111 ROCKET) == 1 ]]'
n=$(reactions)
check "react refuses a comment on another PR, and reacts to none of the IDs" '[[ $(crew react 1 eyes 111 116 2>&1) == *"comment 116 isn'"'"'t on PR #12"* && $(reactions) == "$n" ]]'
check "react refuses someone else's review, and reacts to none of the IDs" '[[ $(crew react 1 eyes 111 902 2>&1) == *"review 902 on PR #12 isn'"'"'t by isaac"* && $(reactions) == "$n" ]]'
check "react refuses an ID that isn't a number, and reacts to none of the IDs" '[[ $(crew react 1 eyes 111 abc 2>&1) == *"comment id must be a number"* && $(reactions) == "$n" ]]'
check "react refuses any reaction but eyes and rocket" '[[ $(crew react 1 heart 111 2>&1) == *"must be eyes or rocket"* && $(reactions) == "$n" ]]'
check "react refuses a task without a PR" '[[ -z $(crew get 2 pr) ]] && ! crew react 2 eyes 111 2>/dev/null && [[ $(reactions) == "$n" ]]'
printf 'Automated review\n\nNothing found.\n' >"$HOME/summary.md"
review() { sed -i "/^## Review rounds\$/i ### Automated review $2\\n" "$1"; }   # task-file review-number
check "review-summary refuses a task that records no automated review" '[[ $(crew review-summary 1 "$HOME/summary.md" 2>&1) == *"records no automated review"* && ! -e $HOME/gh-last-body ]]'
review "$F1" 1
out=$(crew review-summary 1 "$HOME/summary.md" 2>&1); body=$(cat "$HOME/gh-last-body")
want=$'Automated review\n\nNothing found.\n\n<!-- crew review-summary: review 1 -->\n<!-- crew -->'
check "review-summary posts the file on the task's PR, tagged as crew's and as the summary up to the newest review" '[[ $out == "posted the automated-review summary on PR #12" && $body == "$want" ]] && grep -q "issues/12/comments -f body=Automated review" "$HOME/gh-calls.log"'
printf 'Automated review\n\nOne fixed.\n' >"$HOME/summary.md"; posts=$(grep -c "issues/12/comments -f body=" "$HOME/gh-calls.log")
out=$(crew review-summary 1 "$HOME/summary.md" 2>&1)
check "a second summary for the same newest review edits the first instead of posting another" '[[ $out == "updated the automated-review summary on PR #12" && $(cat "$HOME/gh-summaries/120") == "Automated review"*"One fixed."* && $(grep -c "issues/12/comments -f body=" "$HOME/gh-calls.log") == "$posts" ]] && grep -q "PATCH repos/{owner}/{repo}/issues/comments/120 " "$HOME/gh-calls.log"'
sed -i "/^### Automated review 1\$/a Summary posted" "$F1"; review "$F1" 2; review "$F1" 3
printf 'Automated review\n\nTwo more ran.\n' >"$HOME/summary.md"
out=$(crew review-summary 1 "$HOME/summary.md" 2>&1)
check "once a later review is recorded, the next pass's summary is posted new and the first is left alone" '[[ $out == "posted the automated-review summary on PR #12" && $(cat "$HOME/gh-summaries/121") == *"Two more ran."*"<!-- crew review-summary: review 3 -->"* && $(cat "$HOME/gh-summaries/120") == *"One fixed."* ]]'
printf 'Automated review\n\nTwo more ran, edited.\n' >"$HOME/summary.md"
out=$(crew review-summary 1 "$HOME/summary.md" 2>&1)
check "within the later pass, a second summary edits that pass's comment" '[[ $out == "updated the automated-review summary on PR #12" && $(cat "$HOME/gh-summaries/121") == *"edited."* && $(cat "$HOME/gh-summaries/120") == *"One fixed."* && ! -e $HOME/gh-summaries/122 ]]'
out=$(crew feedback 1 --since 1970-01-01T00:00:00Z)
check "crew feedback doesn't read the summaries back" '[[ $out == *"comment 115"* && $out != *"comment 120"* && $out != *"comment 121"* ]] && ! grep -q "One fixed\|Two more" "$F1"'
rm -rf "$HOME/gh-last-body" "$HOME/gh-summaries"
check "review-summary refuses a task without a PR" '[[ -z $(crew get 2 pr) ]] && ! crew review-summary 2 "$HOME/summary.md" 2>/dev/null && [[ ! -e $HOME/gh-last-body ]]'
: >"$HOME/summary.md"
check "review-summary refuses an empty file" '! crew review-summary 1 "$HOME/summary.md" 2>/dev/null && [[ ! -e $HOME/gh-last-body ]]'
echo 'REPLY_TO_THREADS="no"' >>"$(ls -d "$L"/projects/*)/project.env"
check "per-project REPLY_TO_THREADS is honoured" '! crew reply 1 111 x 2>/dev/null'
check "react doesn't depend on REPLY_TO_THREADS" 'crew react 1 eyes 111 >/dev/null'
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
 "env": {"PGHOST": "/run/postgresql", "CREW_PROJECT": "elsewhere"}}
EOF
# A value the shipped file sets always wins; give the test ledger's copy one to defend.
jq '.permissions.defaultMode = "default"' "$L/config/worker-settings.json" >"$HOME/ws" && mv "$HOME/ws" "$L/config/worker-settings.json"
crew dispatch 5 >/dev/null
check "ledger commits take only the ledger, not an edit to the home's own files" '! git -C "$L" diff --quiet HEAD -- config/worker-settings.json && [[ $(git -C "$L" log -1 --format=%s) == "dispatch "* ]]'
S=$(cat "$HOME/claude-settings.last")
check "project settings add allow rules without duplicates" 'jq -e ".permissions.allow | (index(\"Bash(bin/rspec *)\") != null) and (map(select(. == \"Bash(git push)\")) | length == 1)" <<<"$S" >/dev/null'
check "per-project WORKER_AUTOCOMPACT_WINDOW is honoured" 'grep -- "--agent implementer" "$HOME/claude-calls.log" | tail -1 | grep -q -- " --autocompact 250k "'
check "project settings add new keys" 'jq -e ".env.PGHOST == \"/run/postgresql\"" <<<"$S" >/dev/null'
check "project settings can't change the worker's project" 'session_env "$(basename "$PD")"'
check "project settings can't override a value the shipped file sets" 'jq -e ".permissions.defaultMode == \"default\"" <<<"$S" >/dev/null'
check "project settings can't drop deny rules" 'jq -e ".permissions.deny | index(\"Bash(gh pr merge *)\") != null" <<<"$S" >/dev/null'
P=$(ls -d "$L"/projects/*)/proposals/2026-09-25-x.md
sed -e 's/^title: .*/title: "Extract retry"/' -e "s/^project: .*/project: $(basename "$(dirname "$(dirname "$P")")")/" -e 's/^source: .*/source: gardener/' \
  -e '/^## Proposal$/a Move the three retry loops into lib/retry.' -e '/^## First step$/a Extract the helper from api/client.' "$L/templates/proposal.md" >"$P"
check "promote an open proposal with a branch name" 'PF=$(crew promote 2026-09-25-x --branch retry-helper) && [[ $(basename "$PF") == 006-retry-helper.md && $(crew get 6 branch) == agent/retry-helper ]]'
NL=$'\n'
check "a promoted gardener proposal gives the task its proposal and first step" '[[ $(section "$PF" "## Context") == *"written by the gardener."*"Proposal:""$NL""Move the three retry loops"*"First step:""$NL""Extract the helper"* && $(section "$PF" "## Context") != *"Why it matters:"* ]]'
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
out=$(crew new "Stray flags" "--branch stray --on 7" 2>&1)
check "new refuses an argument that looks like a flag but isn't one, and names it" '[[ $out == *"'"'"'--branch stray --on 7'"'"' isn'"'"'t an option here; usage: crew new"* && $(ls "$PD/tasks" | wc -l) == "$n" ]]'
check "new refuses a flag with no value, rather than put it in the title" '! crew new "No branch" --branch 2>/dev/null && [[ $(ls "$PD/tasks" | wc -l) == "$n" ]]'
sed 's/^status: .*/status: open/' "$P" >"${P%x.md}y.md"
check "promote refuses a bad --branch and leaves the proposal open" '! crew promote 2026-09-25-y --branch Nope 2>/dev/null && [[ $(crew proposals) == *2026-09-25-y* && $(ls "$PD/tasks" | wc -l) == "$n" ]]'

# Stacked tasks: 9 ← 10 ← 11, each based on the unmerged branch of the one before.
tip() { git rev-parse "refs/heads/agent/$1"; }
work() {  # worktree file — what a worker does: commit and push its branch
  (cd "$1" && echo x >"$2" && git add "$2" && git commit -qm "add $2" && git push -q -u origin HEAD 2>/dev/null)
}
last_prompt() { grep -- "--resume" "$HOME/claude-calls.log" | tail -1; }
restack_push() {  # worktree — runs the push the last restack note gives, as written
  local cmd; read -ra cmd <<<"$(last_prompt | grep -o 'git push --force-with-lease=[^`]*')"
  (cd "$1" && "${cmd[@]}" -q 2>/dev/null)
}
echo OPEN >"$HOME/gh-pr-state"
F9=$(crew new "Season model" --branch season-model); fill "$F9"; crew ready 9 >/dev/null
setstate "done" null; crew dispatch 9 >/dev/null; WT9=$(crew get 9 worktree)
check "an unstacked task records the commit it started from" '[[ $(crew get 9 base_sha) == "$(git rev-parse origin/main)" ]]'
F10=$(crew new "Season admin" --branch season-admin --on 9); fill "$F10"; crew ready 10 >/dev/null
check "--on bases the task on its parent's branch and keeps the dependency" '[[ $(crew get 10 base) == agent/season-model && $(crew get 10 depends_on) == 009-season-model ]]'
setstate "done" null; out=$(crew dispatch 10 2>&1)
check "a stacked task waits until its parent's branch is on origin" '[[ $out == *"on origin yet"* && $(crew get 10 status) == ready && -z $(crew get 10 worktree) ]]'
work "$WT9" model.txt; crew set 9 pr https://github.com/isaac/webapp/pull/21; crew set 9 status pr-open
check "a pushed, unmerged parent is enough to dispatch" 'crew dispatch 10 >/dev/null 2>&1'
WT10=$(crew get 10 worktree); T9=$(tip season-model)
check "the stacked worktree starts on the parent's tip, which is recorded" '[[ $(git -C "$WT10" rev-parse HEAD) == "$T9" && $(crew get 10 base_sha) == "$T9" ]]'
check "the worker is told which task and PR it is stacked on" 'grep -q "stacked on task 009-season-model (PR #21): agent/season-model" "$HOME/claude-calls.log"'
work "$WT10" admin.txt; crew set 10 pr https://github.com/isaac/webapp/pull/22; crew set 10 status pr-open
out=$(crew status)
check "status lists a stacked task under its parent, columns aligned" '[[ $(grep -A1 "^  009-season-model " <<<"$out" | tail -1) == "  └ 010-season-admin                 pr-open "* ]]'
out=$(crew sync)
check "sync leaves a stack alone while the parent hasn't moved" '[[ $out != *restack* && $(crew get 9 status) == pr-open ]]'

work "$WT9" model2.txt; T9b=$(tip season-model)
out=$(crew sync)
check "sync reports a child whose parent's branch moved, without restacking it" '[[ $out == *"010-season-admin: needs a restack, 009-season-model moved"* && $(crew get 10 base_sha) == "$T9" && $(git rev-parse "$(tip season-admin)~1") == "$T9" ]]'
check "status shows the restack that's needed" '[[ $(crew status) == *"needs restack: 009-season-model moved"* ]]'
setstate "done" null; L10=$(git rev-parse origin/agent/season-admin); crew restack 10 >/dev/null
check "restack gives the worker a push with the branch's origin commit as the lease" '[[ $(last_prompt) == *"\`git push --force-with-lease=agent/season-admin:$L10 origin agent/season-admin\`"* ]]'
check "restack sends the worker the rebase, cut at the parent's old tip" '[[ $(last_prompt) == *"git rebase --onto $T9b $T9\`"* && $(last_prompt) != *"gh pr edit --base"* && $(crew get 10 status) == working ]]'
setstate "done" null; n=$(grep -c -- "--resume" "$HOME/claude-calls.log")
check "restack won't send the same restack twice" '[[ $(crew restack 10 2>&1) == *"already asked"* && $(grep -c -- "--resume" "$HOME/claude-calls.log") == "$n" && $(crew get 10 restack_sent) == "$T9b" ]]'
check "restack --again sends it anyway" 'crew restack 10 --again >/dev/null && [[ $(grep -c -- "--resume" "$HOME/claude-calls.log") == $((n + 1)) ]]'
(cd "$WT10" && git rebase -q --onto "$T9b" "$T9") && restack_push "$WT10"; crew set 10 status pr-open
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
(cd "$WT10" && git rebase -q --onto "$M" "$T9b") && restack_push "$WT10"; crew set 10 base main; crew set 10 status pr-open
T10b=$(tip season-admin)
out=$(crew sync)
check "a restacked parent is done, and its own child is next" '[[ $out != *"010-season-admin: needs"* && $out == *"011-season-picker: needs a restack, 010-season-admin moved"* ]]'
setstate "done" null; crew restack 11 >/dev/null
check "restack cuts at the parent's old tip after the parent was rebased" '[[ $(last_prompt) == *"git rebase --onto $T10b $T10\`"* ]]'
(cd "$WT11" && git rebase -q --onto "$T10b" "$T10") && restack_push "$WT11"; crew set 11 status pr-open
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
check "tick records a new round but leaves a busy worker alone" '[[ $out == *"013-tick-routing: review round 1 recorded"* && $out == *"013-tick-routing: round 1 waiting for its worker (working)"* && $out == *"2 comment(s) from collaborators, for you"* && $(crew get 13 last_round_sent) == 0 && $(resumes) == "$n" && $(crew get 13 status) == pr-open ]]'
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
check "tick holds a round for a worker with an open prompt, and says so" '[[ $out == *"013-tick-routing: round 2 waiting for its worker (blocked)"* && $(crew get 13 last_round_sent) == 1 && $(resumes) == "$n" ]]'
setstate blocked 4242 busy
out=$(crew tick)
check "tick sends a round to a worker that ended its turn on a question" '[[ $out == *"013-tick-routing: round 2 sent to its worker"* && $(crew get 13 last_round_sent) == 2 && $(resumes) == $((n + 1)) && $(crew get 13 status) == addressing-feedback ]]'
setstate "done" null; crew set 13 status pr-open
s13=$(crew get 13 worker_session); crew set 13 feedback_since 1970-01-01T00:00:00Z; setstate working 4242 busy; transcript "$s13" ended; n=$(resumes)
check "status shows a worker whose turn ended as idle, though a shell it left running keeps it working" '[[ $(crew status) == *"worker: idle, background task running"* ]]'
out=$(crew tick)
check "tick sends a round to a worker whose turn ended, though a shell it left running keeps it working" '[[ $out == *"013-tick-routing: round 3 sent to its worker"* && $(crew get 13 last_round_sent) == 3 && $(resumes) == $((n + 1)) ]]'
rm -f "$HOME/.claude/projects/stub/$s13.jsonl"; setstate working 4242; crew set 13 status pr-open
crew set 13 feedback_since 1970-01-01T00:00:00Z; crew tick >/dev/null; crew set 13 feedback_since 1970-01-01T00:00:00Z; waiting=$(crew tick 2>/dev/null)
setstate "done" null
out=$(crew tick)
check "rounds that piled up behind a busy worker are all named while they wait, and in its prompt" '[[ $waiting == *"013-tick-routing: rounds 4 to 5 waiting for its worker (working)"* && $out == *"013-tick-routing: rounds 4 to 5 sent to its worker"* && $(crew get 13 last_round_sent) == 5 && $(last_prompt) == *"New review feedback is in rounds 4 to 5 under"* ]]'
# Another automated review, asked for on the PR (a `crew review` comment) or with crew review.
round() { awk -v h="### Round $2 " 'index($0, h) == 1 {on=1; next} on && /^##/ {exit} on && NF' "$1"; }
comment() { jq -cn --arg id "$1" --arg who "$2" --arg as "$3" --arg b "$4" '{id: ($id | tonumber), user: {login: $who, type: "User"}, author_association: $as, body: $b, html_url: ("u" + $id), created_at: "2026-09-25T21:40:00Z", issue_url: "https://api.github.com/repos/isaac/webapp/issues/12"}'; }
{ comment 130 isaac OWNER $'  Crew Review\r\n'; comment 131 bob COLLABORATOR "crew review"; comment 132 isaac OWNER "Once that's in, crew review it again."; comment 133 isaac OWNER "crew review"; } >"$HOME/gh-more-comments"
crew set 13 status pr-open; crew set 13 feedback_since 2026-09-25T21:35:00Z; setstate working 4242; n=$(resumes)
crew feedback 13 >/dev/null 2>&1; r6=$(round "$F13" 6)
check "the user's 'crew review' comment, in any case and with space around it, becomes a request line in a new round" '[[ $r6 == *"- [ ] Automated review requested (comment 130)"* && $r6 != *"Crew Review"* ]]'
check "two 'crew review' comments in one round each get a request line, so the worker reacts to both" '[[ $r6 == *$'"'"'- [ ] Automated review requested (comment 130)\n- [ ] Automated review requested (comment 133)'"'"'* ]]'
check "a longer comment that mentions crew review is an ordinary comment" '[[ $r6 == *"- [ ] comment 132 — [link](u132)"*"crew review it again"* && $r6 != *"(comment 132)"* ]]'
check "a collaborator's 'crew review' goes under Open questions for the user, not into the round" '[[ $(section "$F13" "## Open questions") == *"@bob commented on PR #12, comment 131"* && $r6 != *131* ]]'
rm "$HOME/gh-more-comments"
rounds1=$(crew get 1 review_rounds)
check "crew review refuses a task with no open PR (merged, or none at all), and records nothing" '! crew review 1 2>/dev/null && ! crew review 3 2>/dev/null && [[ $(crew get 1 review_rounds) == "$rounds1" ]] && ! grep -q "Automated review requested" "$F1" "$F3"'
echo CLOSED >"$HOME/gh-pr-state"; crew sync >/dev/null 2>&1; rounds13=$(crew get 13 review_rounds)
check "crew review refuses a task in flight whose PR was closed unmerged" '[[ $(crew get 13 pr_state) == closed && $(crew get 13 status) == pr-open ]] && ! crew review 13 2>/dev/null && [[ $(crew get 13 review_rounds) == "$rounds13" ]]'
echo OPEN >"$HOME/gh-pr-state"; crew sync >/dev/null 2>&1
out=$(crew review 13 2>/dev/null)
check "crew review appends a round holding only the request, and leaves it for a busy worker" '[[ $(round "$F13" 7) == "- [ ] Automated review requested (crew review)" && $out == *"crew tick sends it once worker"* && $(crew get 13 last_round_sent) == 5 && $(resumes) == "$n" ]]'
setstate "done" null; out=$(crew tick 2>/dev/null)
check "crew tick sends the request with the round before it" '[[ $out == *"013-tick-routing: rounds 6 to 7 sent to its worker"* && $(crew get 13 last_round_sent) == 7 && $(resumes) == $((n + 1)) ]]'
crew set 13 status pr-open; setstate "done" null
check "crew review sends the request to an idle worker straight away" 'crew review 13 >/dev/null 2>&1 && [[ $(crew get 13 last_round_sent) == 8 && $(resumes) == $((n + 2)) && $(crew get 13 status) == addressing-feedback && $(last_prompt) == *"New review feedback is in round 8 under"* ]]'
# Red CI on a PR: crew tick sends it to the worker as a review round once the worker has
# handed the PR over (pr-open), once for each head commit, and asks you past MAX_CI_FIXES.
ci() {  # head name:conclusion…: the PR's head commit and its check runs; a conclusion of - is one still running
  echo "$1" >"$HOME/gh-pr-head"; shift
  local c; for c in "$@"; do
    jq -cn --arg n "${c%%:*}" --arg c "${c#*:}" '{__typename: "CheckRun", name: $n} + if $c == "-" then {status: "IN_PROGRESS"} else {status: "COMPLETED", conclusion: $c} end'
  done | jq -cs . >"$HOME/gh-pr-checks"
}
sed -i '/^## Open questions$/,/^## /s/^- \[ \]/- [x]/' "$F13"   # the user has answered them
setstate "done" null; crew set 13 status working; n=$(resumes)
ci aaaa1111ffff test:FAILURE lint:SUCCESS docs:SKIPPED
out=$(crew tick 2>/dev/null)
check "red CI before the worker hands its PR over is recorded by sync, and left to the worker" '[[ $(crew get 13 ci_state) == failing && $(crew get 13 review_rounds) == 8 && $out != *CI* && $(resumes) == "$n" ]] && ! grep -q "CI failed on" "$F13"'
out=$(crew ci-fix 13); code=$?
check "crew ci-fix, run by hand before the hand-over, leaves it alone too" '[[ $out == *"is working, and its CI is its worker'"'"'s own business"* && $code == 3 && $(crew get 13 review_rounds) == 8 ]]'
crew set 13 status pr-open; ci aaaa1111ffff test:FAILURE lint:-
out=$(crew tick 2>/dev/null)
check "red CI while another check on the head is still running is left alone" '[[ $(crew get 13 ci_state) == running && $(crew get 13 review_rounds) == 8 && -z $out ]]'
ci aaaa1111ffff test:FAILURE lint:SUCCESS docs:SKIPPED build:CANCELLED
out=$(crew tick 2>/dev/null)
check "once every check has finished, a tick records the red CI as a round and sends it to the idle worker" '[[ $out == $'"'"'013-tick-routing: CI round 9 recorded: red on aaaa111\n013-tick-routing: round 9 sent to its worker'"'"' && $(round "$F13" 9) == "- [ ] CI failed on aaaa111: test" && $(crew get 13 last_round_sent) == 9 && $(crew get 13 status) == addressing-feedback && $(resumes) == $((n + 1)) ]]'
setstate "done" null; crew set 13 status pr-open
out=$(crew tick 2>/dev/null)
check "a second tick on the same red head records nothing" '[[ -z $out && $(crew get 13 review_rounds) == 9 && $(resumes) == $((n + 1)) ]]'
echo 'MAX_CI_FIXES=0' >>"$PE"; ci bbbb2222ffff test:FAILURE
out=$(crew tick 2>/dev/null)
check "with MAX_CI_FIXES=0, crew leaves red CI alone" '[[ -z $out && $(crew get 13 review_rounds) == 9 && $(crew get 13 status) == pr-open ]] && ! grep -q bbbb222 "$F13"'
sed -i '/^MAX_CI_FIXES=0$/d' "$PE"
printf '#!/bin/sh\ncrew set 13 status abandoned\n' >"$HOME/gh-pr-view-hook"; chmod +x "$HOME/gh-pr-view-hook"
out=$(crew ci-fix 13)
check "a task abandoned while crew ci-fix reads GitHub is sent nothing" '[[ $out == "nothing to do: 013-tick-routing is abandoned" && $(crew get 13 review_rounds) == 9 && $(crew get 13 status) == abandoned ]] && ! grep -q bbbb222 "$F13"'
rm "$HOME/gh-pr-view-hook"; n=$(resumes); WT13=$(crew get 13 worktree); crew set 13 worktree /elsewhere/13
out=$(crew followup 13 --note "carry on" 2>&1)
check "crew followup won't resume the worker of an abandoned task, or rebuild its worktree" '[[ $out == *"013-tick-routing is abandoned; not resuming its worker"* && $(resumes) == "$n" && $(crew get 13 status) == abandoned && $(crew get 13 worktree) == /elsewhere/13 ]]'
crew set 13 worktree "$WT13"; crew set 13 status pr-open
# Only crew ci-fix asks for headRefOid, so the hook leaves crew sync's read alone.
printf '#!/bin/sh\ncase "$*" in *headRefOid*) crew set 13 status abandoned ;; esac\n' >"$HOME/gh-pr-view-hook"; chmod +x "$HOME/gh-pr-view-hook"
sed -i '/^## Open questions$/a - [ ] Euros or pounds?' "$F13"
crew tick >/dev/null 2>&1
check "a task abandoned during a tick isn't marked needs-input by it" '[[ $(crew get 13 status) == abandoned ]]'
rm "$HOME/gh-pr-view-hook"; sed -i '/^- \[ \] Euros or pounds?$/d' "$F13"; crew set 13 status pr-open
echo CLOSED >"$HOME/gh-pr-state"
out=$(crew tick 2>/dev/null); byhand=$(crew ci-fix 13)
check "red CI on a PR closed without merging is left alone, by tick and by hand" '[[ -z $out && $byhand == *"PR #12 is closed"* && $(crew get 13 pr_state) == closed && $(crew get 13 status) == pr-open && $(crew get 13 review_rounds) == 9 ]]'
echo OPEN >"$HOME/gh-pr-state"
crew tick >/dev/null 2>&1; setstate "done" null; crew set 13 status pr-open; ci cccc3333ffff test:FAILURE lint:FAILURE
out=$(crew tick 2>/dev/null)
check "past MAX_CI_FIXES CI rounds, red CI on a new head is a question for you, not a round" '[[ $out == *"013-tick-routing: CI still red after 2 CI rounds, for you"* && $out == *"waiting on you (1 open question(s))"* && $(crew get 13 review_rounds) == 10 && $(crew get 13 status) == needs-input ]] && [[ $(section "$F13" "## Open questions") == *"- [ ] CI is still red on cccc333 after 2 CI rounds (MAX_CI_FIXES=2): test, lint. Look at it yourself, or tell the worker to try again."* ]]'
crew set 13 status pr-open; sed -i 's/^- \[ \] CI is still red/- [x] CI is still red/' "$F13"
out=$(crew tick 2>/dev/null)
check "that question is asked once, though its head stays red" '[[ -z $out && $(grep -c "CI is still red on cccc333" "$F13") == 1 ]]'
out=$(crew ci-fix 13); code=$?
check "crew ci-fix by hand says why it does nothing, and exits 3" '[[ $out == *"the red CI on cccc333 is reported already"* && $code == 3 ]]'
# The worker's comment on why CI was red: one for each commit the task file reports.
printf 'CI was red\n\nA flaky test.\n' >"$HOME/ci.md"
out=$(crew ci-summary 13 bbbb222 "$HOME/ci.md" 2>&1)
check "ci-summary posts the file for the commit it names, tagged as crew's" '[[ $out == "posted the CI summary for bbbb222 on PR #12" && $(cat "$HOME/gh-summaries/120") == $'"'"'CI was red\n\nA flaky test.\n\n<!-- crew ci-summary: bbbb222 -->\n<!-- crew -->'"'"' ]]'
printf 'CI was red\n\nA flaky test; the rerun passed.\n' >"$HOME/ci.md"
out=$(crew ci-summary 13 bbbb2222ffff "$HOME/ci.md" 2>&1)
check "a second ci-summary for the same commit edits the first, given its full SHA too" '[[ $out == "updated the CI summary for bbbb222 on PR #12" && $(cat "$HOME/gh-summaries/120") == *"the rerun passed."* && ! -e $HOME/gh-summaries/121 ]]'
printf 'CI was red\n\nStill red.\n' >"$HOME/ci.md"
out=$(crew ci-summary 13 cccc333 "$HOME/ci.md" 2>&1)
check "the commit the open question past MAX_CI_FIXES names gets a comment of its own, and the earlier one is left alone" '[[ $out == "posted the CI summary for cccc333 on PR #12" && $(cat "$HOME/gh-summaries/121") == *"<!-- crew ci-summary: cccc333 -->"* && $(cat "$HOME/gh-summaries/120") == *"the rerun passed."* ]]'
echo 'MAX_CI_FIXES=3' >>"$PE"; ci dddd4444ffff test:FAILURE; crew set 13 status pr-open; crew ci-fix 13 >/dev/null; sed -i '/^MAX_CI_FIXES=3$/d' "$PE"
printf 'CI was red\n\nOlder round, written last.\n' >"$HOME/ci.md"
out=$(crew ci-summary 13 bbbb222 "$HOME/ci.md" 2>&1)
check "with a newer CI round recorded, a comment for an older one still goes to its own commit" '[[ $out == "updated the CI summary for bbbb222 on PR #12" && $(cat "$HOME/gh-summaries/120") == *"written last."* && ! -e $HOME/gh-summaries/122 ]]'
rm -f "$HOME/gh-last-body"
check "ci-summary refuses a commit the task reports no red CI on" '[[ $(crew ci-summary 13 eeee555 "$HOME/ci.md" 2>&1) == *"reports no red CI on eeee555"* && ! -e $HOME/gh-last-body ]]'
out=$(crew feedback 13 --since 1970-01-01T00:00:00Z)
check "crew feedback doesn't read the CI summaries back" '[[ $out == *"comment 115"* && $out != *"comment 120"* && $out != *"comment 121"* ]]'
rm -rf "$HOME/gh-summaries" "$HOME/gh-pr-head" "$HOME/gh-pr-checks"; crew set 13 last_round_sent "$(crew get 13 review_rounds)"
setstate "done" null; crew set 13 status pr-open
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
check "goal new leaves max_in_flight to GOAL_MAX_IN_FLIGHT unless told" '[[ -z $(gget "$GF" max_in_flight) ]]'
n=$(ls "$PD/goals" | wc -l)
check "goal new refuses a --max-in-flight that isn't a whole number" '! crew goal new "Bad cap" --max-in-flight two 2>/dev/null && [[ $(ls "$PD/goals" | wc -l) == "$n" ]]'
check "goal new refuses an unknown flag and a flag with no value" '! crew goal new "Stray" "--max-in-flight 2" 2>/dev/null && ! crew goal new "No cap" --max-in-flight 2>/dev/null && [[ $(ls "$PD/goals" | wc -l) == "$n" ]]'
GF2=$(crew goal new "Capped goal" --max-in-flight 2)
check "goal new --max-in-flight sets the goal's max_in_flight" '[[ $(gget "$GF2" max_in_flight) == 2 ]]'
crew goal abandon "$GF2" >/dev/null
check "goal start refuses a goal without the user's words" '! crew goal start 1 2>/dev/null && [[ $(gget "$GF" status) == draft ]]'
sed -i '/^## Goal$/a "Ship every card in the Season column." (tracker: the Season column)' "$GF"
out=$(crew goal start 1); GJ=$(gget "$GF" lead_job); CO=$(gget "$GF" checkout)
gl=$(grep -- "--agent goal-lead" "$HOME/claude-calls.log" | tail -1)
check "goal start launches the goal lead with the home as an extra directory" '[[ $(gget "$GF" status) == running && -n $GJ && $gl == *"--add-dir $L --bg --mcp-config $L/projects/$(gget "$GF" project)/mcp.local.json --agent goal-lead --permission-mode auto --settings {"*"} You are the goal lead for goal $G "* ]]'
check "the goal lead gets the lead's settings, all but their agent, with its project and the home" \
  '[[ $(jq -c "del(.env)" "$HOME/claude-settings.last") == "$(jq -c "del(.agent)" "$L/lead/.claude/settings.json")" ]] && jq -e ".hooks.PreToolUse and (has(\"agent\") | not)" "$HOME/claude-settings.last" >/dev/null && session_env "$(gget "$GF" project)"'
check "the goal lead works in a detached checkout of the default branch" '[[ $(jq -r --arg id "$GJ" ".[] | select(.id == \$id) | .cwd" "$HOME/agents.json") == "$CO" && -z $(git -C "$CO" branch --show-current) && $(git -C "$CO" rev-parse HEAD) == $(git -C "$SB/webapp" rev-parse origin/main) ]]'
GT1=$(crew new "Season header" --goal 1); T1=$(basename "$GT1" .md)
GT2=$(crew new "Season standings" --goal 1); T2=$(basename "$GT2" .md)
NG=$(crew new "Not in a goal"); fill "$NG"; crew ready "$NG" >/dev/null
check "new --goal makes the task one of the goal's" '[[ $(crew get "$GT1" goal) == "$G" && -z $(crew get "$NG" goal) ]]'
check "new --goal refuses a goal that doesn't exist" '! crew new "Stray" --goal 9 2>/dev/null'
sed -i '/^## Open questions$/a - [ ] Add a standings table? It needs a migration. I recommend yes.' "$GT2"
check "status lists the goal with its tasks under it, and a parked task's open questions" '[[ $(crew status) == *"goal $G"*"running"*"0 of 2 task(s) merged, 0/3 in flight"$'"'"'\n'"'"'"    $T1"*$'"'"'\n'"'"'"    $T2"*"draft"*"1 open question(s)"* ]]'
fill "$GT1"; crew ready "$GT1" >/dev/null
crew goal cap 1 0 >/dev/null
out=$(crew tick 2>&1)
check "goal cap sets max_in_flight, and a goal task over it waits without a word" '[[ $(gget "$GF" max_in_flight) == 0 && $out != *"$T1"* && $(crew get "$GT1" status) == ready ]]'
check "goal cap refuses a value that isn't a whole number" '! crew goal cap 1 -1 2>/dev/null && ! crew goal cap 1 two 2>/dev/null && [[ $(gget "$GF" max_in_flight) == 0 ]]'
crew goal cap 1 1 >/dev/null
echo "MAX_WORKERS=0" >>"$L/config/crew.env"
out=$(crew tick 2>&1)
check "a goal task held back by a cap waits without a word" '[[ $out != *"$T1"* && $(crew get "$GT1" status) == ready ]]'
sed -i '/^MAX_WORKERS=0$/d' "$L/config/crew.env"
out=$(crew tick 2>&1)
check "tick dispatches a running goal's ready task, and no other ready task" '[[ $out == *"$T1: dispatched for goal $G"* && $(crew get "$GT1" status) == working && $(crew get "$NG" status) == ready ]]'
GS=$(crew new "Season header styles" --goal "$GF" --on "$GT1"); fill "$GS"; crew ready "$GS" >/dev/null
out=$(crew dispatch "$GS" 2>&1); code=$?
check "a task stacked on one of its goal's counts against the goal's max_in_flight" '[[ $code == 4 && $out == *"goal $G already has 1 task(s) in flight (max_in_flight 1)"* && $(crew get "$GS" status) == ready ]]'
check "status shows a goal's tasks in flight against its max_in_flight" '[[ $(crew status) == *"goal $G"*"0 of 3 task(s) merged, 1/1 in flight"* ]]'
crew set "$GS" status abandoned >/dev/null; crew goal cap 1 3 >/dev/null
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
# crew tick dispatches the ready tasks of running goals highest priority first,
# then by ID, across every goal.
GF3=$(crew goal new "Season extras" --max-in-flight 1); G3=$(basename "$GF3" .md)
sed -i '/^## Goal$/a "Ship the extras."' "$GF3"; crew goal start "$GF3" >/dev/null
goal_task() { local f; f=$(crew new "$2" --goal "$1"); fill "$f"; crew ready "$f" >/dev/null; echo "$f"; }
PA=$(goal_task "$GF3" "Extras A"); PB=$(goal_task "$GF3" "Extras B"); PC=$(goal_task "$GF3" "Extras C"); PH=$(goal_task "$GF3" "Extras held")
TA=$(basename "$PA" .md); TB=$(basename "$PB" .md); TC=$(basename "$PC" .md); TH=$(basename "$PH" .md)
crew set "$PB" priority 1; crew set "$PC" priority 1; crew set "$PH" priority 5; crew set "$PH" depends_on "$(basename "$GT3" .md)"
crew set "$NG" priority 2
sum=$(cksum <"$PC")
check "crew set refuses a priority that isn't a whole number, and leaves the file as it was" '! crew set "$PC" priority x 2>/dev/null && ! crew set "$PC" priority 1.5 2>/dev/null && [[ $(cksum <"$PC") == "$sum" ]]'
check "crew set takes a negative priority, or an empty one" 'crew set "$PA" priority -2 && [[ $(crew get "$PA" priority) == -2 ]] && crew set "$PA" priority && [[ -z $(crew get "$PA" priority) ]]'
st=$(crew status)
check "status shows the priority beside a goal's ready task, and not beside one outside a goal" '[[ $(grep "$TB" <<<"$st") == *"ready"*"priority 1" && $(grep "$TA" <<<"$st") != *priority* && $(grep "$(basename "$NG" .md)" <<<"$st") != *priority* ]]'
sed -i 's/^priority: *$/priority: x/' "$PA"   # written by hand
setstate "done" null
out=$(crew tick 2>&1)
check "tick dispatches the highest priority ready task first, a tie going to the lower ID, past one a dependency holds" '[[ $out == *"$TB: dispatched for goal $G3"* && $out != *"$TA: dispatched"* && $out != *"$TC: dispatched"* && $out != *"$TH: dispatched"* && $(crew get "$PB" status) == working && $(crew get "$PC" status) == ready && $(crew get "$PH" status) == ready ]]'
check "tick warns of a priority that isn't a whole number, and status shows it as ?" '[[ $out == *"$TA: priority '"'"'x'"'"' isn'"'"'t a whole number; treating it as 0"* && $(crew status | grep "$TA") == *"priority ?" ]]'
# With room for one more worker, a review round sent in the tick takes it before
# a ready task (a lower ID than the round's task) can.
GF4=$(crew goal new "Season promos" --max-in-flight 1); G4=$(basename "$GF4" .md)
sed -i '/^## Goal$/a "Ship the promos."' "$GF4"; crew goal start "$GF4" >/dev/null
PQ=$(goal_task "$GF4" "Promo banner"); TQ=$(basename "$PQ" .md); crew set "$PQ" priority 2
crew goal cap "$GF3" 3 >/dev/null; crew set "$PB" feedback_since 2099-01-01T00:00:00Z; crew set "$PB" review_rounds 1
echo "MAX_WORKERS=1" >>"$PE"; setstate "done" null
out=$(crew tick 2>&1)
check "tick sends review rounds before it dispatches, so they count against MAX_WORKERS" '[[ $out == *"$TB: round 1 sent to its worker"* && $out != *"dispatched"* && $(crew get "$PA" status) == ready ]]'
setstate "done" null
out=$(crew tick 2>&1)
check "the one free worker slot goes to the highest priority task of any goal" '[[ $out == *"$TQ: dispatched for goal $G4"* && $out != *"$TC: dispatched"* && $(crew get "$PC" status) == ready ]]'
sed -i '/^MAX_WORKERS=1$/d' "$PE"
# A goal abandoned while the tick dispatches an earlier task (its worktree hooks
# running) has none of its tasks dispatched after.
crew goal cap "$GF4" 3 >/dev/null; PR=$(goal_task "$GF4" "Promo footer"); TR=$(basename "$PR" .md); crew set "$PR" priority 3
echo 'WORKTREE_TOOL="worktrunk"' >>"$PE"
printf 'crew goal abandon %s >/dev/null 2>&1; rm -f "$HOME/wt-hook-cmd"\n' "$G3" >"$HOME/wt-hook-cmd"
setstate "done" null
out=$(crew tick 2>&1)
check "a goal abandoned during an earlier dispatch in the tick has none of its tasks dispatched" '[[ $out == *"$TR: dispatched for goal $G4"* && $(gget "$GF3" status) == abandoned && $out != *"$TC"* && $(crew get "$PC" status) == ready ]]'
sed -i '/^WORKTREE_TOOL=/d' "$PE"; rm -f "$HOME/wt-hook-cmd"
crew goal abandon "$GF4" >/dev/null 2>&1
for f in "$PA" "$PB" "$PC" "$PH" "$PQ" "$PR" "$NG"; do crew set "$f" status abandoned; done
rm -f "$HOME/gh-pr-state"

# Proposals from anyone: a title and a paragraph, kept out of the task list until promoted.
PJ=$(basename "$PD")
SP=$(crew propose "Give each jurisdiction a PricingStrategy" --project "$PJ" --source user \
  --why "Tax, currency and receipts vary by place." --revisit-when "a second pricing rule varies by place: currency, say")
check "propose writes an open proposal from a title and a paragraph" '[[ $(basename "$SP") == "$(date +%F)-give-each-jurisdiction-a-pricings.md" && $(crew get "$SP" status) == open && $(crew get "$SP" source) == user && $(section "$SP" "## Why it matters") == "Tax, currency and receipts vary by place." ]]'
check "propose keeps a revisit-when that needs quoting" '[[ $(crew get "$SP" revisit_when) == "a second pricing rule varies by place: currency, say" ]]'
check "proposals lists who wrote it and when to revisit it" '[[ $(cd "$HOME" && crew proposals --project "$PJ") == *"$(basename "$SP" .md)"*" user "*"Give each jurisdiction a PricingStrategy""$NL"*"revisit when: a second pricing rule"* ]]'
check "status counts open proposals from any source" '[[ $(crew status) == *"open proposals: "* && $(crew status) != *"gardener proposals"* ]]'
check "propose refuses a second proposal with the same name" '! crew propose "Give each jurisdiction a PricingStrategy" --project "$PJ" 2>/dev/null'
check "propose refuses an unknown source" '! crew propose "Something else" --project "$PJ" --source bob 2>/dev/null && [[ ! -e $(dirname "$SP")/$(date +%F)-something-else.md ]]'
WP=$(crew propose "Share the header partial" --project "$PJ" --source worker:7 --why "Two pages copy it.")
check "a worker's proposal names its task by full id, and the source defaults to lead" '[[ $(crew get "$WP" source) == worker:007-contestant-type-on-update && $(crew get "$(crew propose "Lead idea" --project "$PJ")" source) == lead ]]'
check "propose refuses a worker source that names no task" '! crew propose "Orphan idea" --project "$PJ" --source worker:999 2>/dev/null'
n=$(ls "$(dirname "$SP")" | wc -l)
check "propose refuses flags that arrive as one argument, rather than put them in the title" '! crew propose "Stray flags" "--project $PJ --source worker:7" 2>/dev/null && [[ $(ls "$(dirname "$SP")" | wc -l) == "$n" ]]'
check "propose refuses a flag with no value" '! crew propose "No reason" --project "$PJ" --why 2>/dev/null && [[ $(ls "$(dirname "$SP")" | wc -l) == "$n" ]]'
ST=$(crew promote "$SP" --branch pricing-strategy)
check "promote turns a short proposal into a draft task" '[[ $(crew get "$ST" status) == draft && $(crew get "$SP" status) == accepted && $(crew get "$SP" task) == $(basename "$ST" .md) ]]'
check "a short proposal's task names its source and keeps only the sections it has" '[[ $(section "$ST" "## Context") == *"written by the user."*"Revisit when: a second pricing rule"*"Why it matters:""$NL""Tax, currency and receipts vary by place."* && $(section "$ST" "## Context") != *"Proposal:"* && $(section "$ST" "## Context") != *"First step:"* ]]'
out=$(crew proposals --project "$PJ"); all_out=$(crew proposals --any-status --project "$PJ")
check "proposals lists the open ones, and --any-status the rest too" '[[ $out != *"$(basename "$SP" .md)"* && $all_out == *"$(basename "$SP" .md)"*" accepted "* ]]'

# Project scope: a command works on --project, else CREW_PROJECT (which `lead` and
# crew's own sessions set), else the current directory's repo, else every project.
git init -q --bare "$SB/shop.git"; git clone -q "$SB/shop.git" "$SB/shop" 2>/dev/null
(cd "$SB/shop" && echo "# Conventions" >AGENTS.md && git add -A && git commit -qm init && git push -q -u origin main && git remote set-head origin main && crew project add >/dev/null)
P2=$(cd "$SB/shop" && crew config PROJECT)
S1=$(crew new "Shop checkout" --project "$P2")
QT=$(crew new "Which currency" --project "$PJ"); sed -i '/^## Open questions$/a - [ ] Euros or pounds?' "$QT"
out=$(cd "$HOME" && crew status)
check "outside a repo, status shows every project" '[[ $out == *"$NL$PJ$NL"* && $out == *"$NL$P2$NL"* && $out != *"other projects"* ]]'
out=$(cd "$HOME" && crew find 1 2>&1)
check "outside a repo, a task number two projects share is ambiguous" '[[ $out == *ambiguous* ]]'
TWIN=$L/projects/$P2/tasks/$(basename "$F1"); sed "s/^project: .*/project: $P2/" "$F1" >"$TWIN"
calls=$(wc -l <"$HOME/claude-calls.log"); out=$(cd "$HOME" && env -u CLAUDECODE crew attach "$(basename "$F1" .md)" 2>&1)
check "outside a repo, a task ID two projects share is ambiguous, naming each project, and attach runs no claude" \
  '[[ $out == *ambiguous*"($PJ)"*"($P2)"* && $(wc -l <"$HOME/claude-calls.log") == "$calls" ]]'
check "CREW_PROJECT resolves a task ID two projects share in its project" '[[ $(CREW_PROJECT=$P2 crew get "$(basename "$F1" .md)" project) == "$P2" ]]'
# A busy worker's hint names its task so that it resolves back to that task here.
cp "$HOME/agents.json" "$HOME/agents.bak"
jq --arg id "$(crew get "$F1" worker_job)" 'map(select(.id != $id)) + [{id: $id, state: "working", pid: 4242, sessionId: ($id + "-busy")}]' \
  "$HOME/agents.bak" >"$HOME/agents.json"
out=$(CREW_PROJECT=$P2 crew followup "$F1" --note hi 2>&1); want="crew attach '$F1',"
check "a busy worker's hint names its task by path where another project's task with that ID would win" '[[ $out == *"$want"* ]]'
out=$(CREW_PROJECT=$PJ crew followup "$F1" --note hi 2>&1); want="crew attach $(basename "$F1" .md),"
check "a busy worker's hint names its task by ID where that ID finds it" '[[ $out == *"$want"* ]]'
mv "$HOME/agents.bak" "$HOME/agents.json"
rm "$TWIN"
out=$(CREW_PROJECT=$P2 crew status)
check "CREW_PROJECT scopes status to its project, over the current directory's" '[[ $out == *"$NL$P2$NL"* && $out != *"$NL$PJ$NL"* ]]'
check "scoped status ends with what waits on you in other projects" '[[ $out == *"waiting on you in other projects"*"$PJ: "*"task(s) with open questions"* ]]'
check "CREW_PROJECT resolves a task number in its project first" '[[ $(CREW_PROJECT=$P2 crew find 1) == 001-shop-checkout ]]'
check "crew new goes to CREW_PROJECT's project, over the current directory's" '[[ $(CREW_PROJECT=$P2 crew new "Shop cart") == "$L/projects/$P2/tasks/"* ]]'
out=$(CREW_PROJECT=all crew status)
check "CREW_PROJECT=all is every project, wherever the shell is" '[[ $out == *"$NL$PJ$NL"* && $out == *"$NL$P2$NL"* ]]'
out=$(CREW_PROJECT=all crew new "Nowhere" 2>&1)
check "with every project in scope, crew new asks which one" '[[ $out == *"which project?"* ]]'
out=$(crew status --project all)
check "--project all is every project, like --all" '[[ $out == *"$NL$PJ$NL"* && $out == *"$NL$P2$NL"* ]]'
out=$(CREW_PROJECT=nope crew status 2>&1)
check "an unknown CREW_PROJECT fails clearly" '[[ $out == *"CREW_PROJECT is '"'"'nope'"'"', which isn'"'"'t a project registered with crew"* ]]'
out=$(cd "$HOME" && crew list --project "$P2")
check "list takes --project" '[[ $out == *001-shop-checkout* && $out != *saved-search* ]]'
PP=$(crew propose "Ship to Canada" --project "$P2")
check "proposals --all is every project" '[[ $(crew proposals --all) == *"$(basename "$PP" .md)"* && $(crew proposals) != *"$(basename "$PP" .md)"* ]]'
check "sync and cleanup --merged take --project" '(cd "$HOME" && crew sync --project "$P2" >/dev/null && [[ $(crew cleanup --merged --project "$P2") == "Nothing to clean up." ]])'
fill "$S1"; crew ready "$S1" >/dev/null; setstate "done" null
(cd "$HOME" && CREW_PROJECT=$PJ crew dispatch "$S1" >/dev/null 2>&1)
check "a worker's settings name its task's project, not the caller's" 'session_env "$P2"'
check "a worker's launch sets no CREW_PROJECT, which its session wouldn't get" '[[ $(grep -- "--name crew-$P2-001-shop-checkout" "$HOME/claude-env.log" | tail -1) == "unset claude "* ]]'
(cd "$HOME" && CREW_PROJECT=$PJ crew gardener --project "$P2" >/dev/null 2>&1)
check "the gardener runs with its own settings, which load project memory, and its project" \
  '[[ $(jq -c "del(.env)" "$HOME/claude-settings.last") == "$(jq -c . "$L/config/gardener-settings.json")" ]] && session_env "$P2"'
(cd "$HOME" && CREW_PROJECT=$P2 crew gardener --project "$PJ" >/dev/null 2>&1)
check "the gardener of another project gets that project" 'session_env "$PJ"'
(cd "$SB/shop" && CREW_PROJECT=$P2 crew observe >/dev/null 2>&1)
check "crew observe runs with the read-only settings and no project, so its observer gets no project memory" \
  '[[ $(cat "$HOME/claude-settings.last") == "$L/config/readonly-settings.json" && $(grep -- "--agent casual-observer" "$HOME/claude-env.log" | tail -1) == "unset claude "* ]]'
check "gardener takes --project, not a bare project name" '! crew gardener "$P2" 2>/dev/null'

# Project memory: agents suggest facts with crew remember; the lead moves the ones
# the user approves into memory.md, which every agent of the project loads.
MS=$L/projects/$PJ/memory-suggestions.md
out=$(cd "$HOME" && crew remember "Dashboard columns are unlimited" --why "The user chose it over a cap of 12." --project "$PJ")
check "remember creates the suggestions file and prints its path" '[[ $out == "$MS" && $(head -1 "$MS") == "# Memory suggestions: $PJ" ]]'
check "remember suggests the fact as an unchecked item in the shape of an entry, from the lead by default" '[[ $(tail -1 "$MS") == "- [ ] **Dashboard columns are unlimited** The user chose it over a cap of 12. (lead, $(date +%F))" ]]'
(cd "$HOME" && crew remember "PR screenshots go in Dropbox" --why "Collaborators can't see them" --source worker:7 --project "$PJ" >/dev/null)
check "remember appends, and names a worker's task by full id" '[[ $(grep -c "^- \[ \]" "$MS") == 2 && $(tail -1 "$MS") == "- [ ] **PR screenshots go in Dropbox** Collaborators can'"'"'t see them. (worker:007-contestant-type-on-update, $(date +%F))" ]]'
check "remember commits the suggestion" '[[ -z $(git -C "$L" status --porcelain -- "$MS") && $(git -C "$L" log -1 --format=%s) == "remember $PJ" ]]'
check "remember refuses a bad source, an empty fact and a missing --why" \
  '! crew remember "X" --why "Y" --source bob --project "$PJ" 2>/dev/null && ! crew remember "" --why "Y" --project "$PJ" 2>/dev/null && ! crew remember "X" --project "$PJ" 2>/dev/null && [[ $(grep -c "^- " "$MS") == 2 ]]'
check "remember refuses an unknown flag and a flag with no value" \
  '! crew remember "X" "--why Y" --project "$PJ" 2>/dev/null && ! crew remember "X" --project "$PJ" --why 2>/dev/null && [[ $(grep -c "^- " "$MS") == 2 ]]'
out=$(crew status --project "$PJ")
check "status counts a project's memory suggestions waiting on the user" '[[ $out == *"  memory suggestions: 2  (projects/$PJ/memory-suggestions.md)"* ]]'
out=$(crew status --project "$P2")
check "status counts memory suggestions among what waits on you in other projects" '[[ $out == *"waiting on you in other projects"*"$PJ: "*"2 memory suggestion(s)"* ]]'
sed -i 's/^- \[ \] \(.*\)$/- [x] \1 → kept/' "$MS"
check "status leaves out settled suggestions" '[[ $(crew status --project "$PJ") != *"memory suggestions"* && $(crew status --project "$P2") != *"memory suggestion"* ]]'
out=$(crew memory --project "$PJ" 2>&1)
check "memory says so when the project has none yet" '[[ $out == "No project memory for $PJ yet." ]]'
printf '# Project memory: %s\n\n- **Dashboard columns are unlimited** The user chose it over a cap of 12. (lead, %s)\n' "$PJ" "$(date +%F)" >"$L/projects/$PJ/memory.md"
out=$(cd "$HOME" && crew memory --project "$PJ")
check "memory prints a line naming the file, the rule for weighing it, then the file" \
  '[[ $out == "Project memory for $PJ ($L/projects/$PJ/memory.md): facts the user approved.$NL""Where it disagrees with the code or the ledger, they win"*"$NL$NL$(cat "$L/projects/$PJ/memory.md")" ]]'
check "the SessionStart hook, run as a worker's settings give it, prints the memory of the session's project" \
  '[[ $(cd "$HOME" && CREW_PROJECT=$PJ bash -c "$(jq -r ".hooks.SessionStart[0].hooks[0].command" "$L/config/worker-settings.json")" </dev/null) == "$out" ]]'
check "the hook prints nothing for a session on every project, on none, or on one without memory" \
  '[[ -z $(CREW_PROJECT=all crew memory --hook) && -z $(cd "$HOME" && env -u CREW_PROJECT crew memory --hook) && -z $(CREW_PROJECT=$P2 crew memory --hook) ]]'
{ printf '# Project memory: %s\n\n' "$P2"; head -c 10000 /dev/zero | tr '\0' x; echo; } >"$L/projects/$P2/memory.md"
out=$(CREW_PROJECT=$P2 crew memory --hook)
check "a memory too long for Claude Code to pass on whole makes the hook say to read the file instead" \
  '[[ $out == "Project memory for $P2"*"Read $L/projects/$P2/memory.md in full now"* && $(wc -m <<<"$out") -le 10000 ]]'
check "crew memory still prints a memory that long whole" '[[ $(crew memory --project "$P2" | tail -1) == "$(tail -1 "$L/projects/$P2/memory.md")" ]]'
rm "$L/projects/$P2/memory.md"
check "the hook exits 0 whatever it's given: hook JSON, an unknown project, no crew home" \
  'echo "{\"agent_type\":\"casual-observer\"}" | CREW_PROJECT=$PJ crew memory --hook >/dev/null && echo "{not json" | CREW_PROJECT=nope crew memory --hook && CREW_HOME=$SB crew memory --hook'

# MCP servers for one project: its mcp.json, expanded into its git-ignored
# mcp.local.json, which its workers and goal leads start with.
M2=$L/projects/$P2/mcp.json; ML2=$L/projects/$P2/mcp.local.json
echo '{"mcpServers": {"tracker": {"command": "tracker-mcp", "env": {"TOKEN": "${SHOP_TOKEN}"}}}}' >"$M2"
git -C "$L" add "$M2"; git -C "$L" commit -qm "mcp.json for $P2"
S2=$(crew new "Shop refunds" --project "$P2"); fill "$S2"; crew ready "$S2" >/dev/null; setstate "done" null
out=$(crew dispatch "$S2" 2>&1)
check "an unset \${NAME} in mcp.json stops the dispatch before its worktree, with an error naming the file and the setting" \
  '[[ $out == *"$M2: server"*"\${SHOP_TOKEN} isn"*"set"* && ! -d $HOME/agent-work/$P2/$(basename "$S2" .md) && $(crew get "$S2" status) == ready ]]'
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)   # it checks claude --version, not a session
check "crew doctor reports it" '[[ $doctor_out == *"FAIL $P2: mcp.json reads, and every \${NAME} in it is set — $M2"*"SHOP_TOKEN"* ]]'
PT=$(crew new "Plain task" --project "$PJ"); fill "$PT"; crew ready "$PT" >/dev/null; setstate "done" null
crew dispatch "$PT" >/dev/null 2>&1
MLJ=$L/projects/$PJ/mcp.local.json
check "it doesn't hold back another project's dispatch, whose worker starts with its own mcp.local.json, empty" \
  '[[ $(crew get "$PT" status) == working && $(grep -- "--name crew-$PJ-$(basename "$PT" .md)" "$HOME/claude-calls.log") == *" --bg --mcp-config $MLJ --agent implementer "* && $(jq -c . "$MLJ") == "{\"mcpServers\":{}}" ]]'
echo 'SHOP_TOKEN="s3cret"' >>"$L/projects/$P2/local.env"; setstate "done" null
crew dispatch "$S2" >/dev/null 2>&1
check "a worker for a project with mcp.json starts with --mcp-config and its mcp.local.json" \
  '[[ $(crew get "$S2" status) == working && $(grep -- "--name crew-$P2-$(basename "$S2" .md)" "$HOME/claude-calls.log") == *" --bg --mcp-config $ML2 --agent implementer "* ]]'
check "mcp.local.json names each server for its project and fills in the setting from local.env" '[[ $(jq -r ".mcpServers[\"${P2}_tracker\"].env.TOKEN" "$ML2") == s3cret ]]'
check "mcp.local.json is git-ignored and only you can read it" 'git -C "$L" check-ignore -q "$ML2" && [[ $(stat -c %a "$ML2") == 600 ]]'
check "the secret is in no commit and no command line" '[[ -z $(git -C "$L" log -p -S s3cret --oneline -- projects) ]] && ! grep -q s3cret "$HOME/claude-calls.log"'
sed -i 's/^SHOP_TOKEN=.*/SHOP_TOKEN="n3w"/' "$L/projects/$P2/local.env"; setstate "done" null
crew followup "$S2" --note "token changed" >/dev/null 2>&1
check "a resume passes no --mcp-config, and the file it started with is rebuilt first" \
  '[[ $(grep -- "--resume" "$HOME/claude-calls.log" | tail -1) == "claude --resume $(crew get "$S2" worker_session) --bg New input"* && $(jq -r ".mcpServers[\"${P2}_tracker\"].env.TOKEN" "$ML2") == n3w ]]'
G2F=$(crew goal new "Shop season" --project "$P2"); sed -i '/^## Goal$/a "Ship the shop."' "$G2F"
crew goal start "$G2F" >/dev/null
check "a goal lead for the project starts with --mcp-config too" '[[ $(grep -- "--agent goal-lead" "$HOME/claude-calls.log" | tail -1) == *" --bg --mcp-config $ML2 --agent goal-lead "* ]]'
check "a goal lead's settings name its own project" 'session_env "$P2"'
git -C "$L" rm -q "$M2"; git -C "$L" commit -qm "no mcp.json for $P2"; setstate "done" null
out=$(crew followup "$S2" --note "servers gone" 2>&1)
check "once the project drops its mcp.json, a resume finds its mcp.local.json empty but valid" '[[ $out == *"(resume)"* && $(jq -c . "$ML2") == "{\"mcpServers\":{}}" ]]'
echo '{"mcpServers": {"wiki": {"type": "http", "url": "https://wiki.example/mcp"}}}' >"$L/projects/$PJ/mcp.json"
git -C "$L" add "$L/projects/$PJ/mcp.json"; git -C "$L" commit -qm "mcp.json for $PJ"; setstate "done" null
out=$(crew followup "$PT" --note "a wiki now" 2>&1)
check "a worker started before its project had an mcp.json finds its servers when it resumes" \
  '[[ $out == *"(resume)"* && $(grep -- "--name crew-$PJ-$(basename "$PT" .md)" "$HOME/claude-calls.log") == *"--mcp-config $MLJ "* && $(jq -r ".mcpServers[\"${PJ}_wiki\"].url" "$MLJ") == https://wiki.example/mcp ]]'
# crew tick rebuilds them all, and one project's broken mcp.json holds back no other.
echo '{"mcpServers": {"wiki": {"type": "http", "url": "https://wiki.example/mcp"}, "docs": {"type": "http", "url": "https://docs.example/mcp"}}}' >"$L/projects/$PJ/mcp.json"
echo '{"mcpServers": {"tracker": {"command": "tracker-mcp", "env": {"TOKEN": "${NOT_SET}"}}}}' >"$M2"
git -C "$L" add "$L/projects/$PJ/mcp.json" "$M2"; git -C "$L" commit -qm "mcp.json edits"
err=$(crew tick 2>&1 >/dev/null); rc=$?
check "crew tick brings mcp.local.json and the leads' servers up to date" \
  '[[ $(jq -r ".mcpServers[\"${PJ}_docs\"].url" "$MLJ") == https://docs.example/mcp && $(jq -r ".mcpServers[\"${PJ}_docs\"].url" "$L/lead/.mcp.json") == https://docs.example/mcp ]]'
check "a project whose mcp.json is broken is reported, keeps the mcp.local.json it had, and the tick carries on" '[[ $rc == 0 && $err == *"$M2: server"*"\${NOT_SET} isn"*"set"* && $(jq -c . "$ML2") == "{\"mcpServers\":{}}" ]]'

summary crew-e2e
