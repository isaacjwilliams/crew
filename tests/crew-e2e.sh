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
# Copies the home's agents to where Claude Code loads them from, as crew install does.
install_agents() { mkdir -p "$HOME/.claude/agents" && cp "$L"/agents/*.md "$HOME/.claude/agents/"; }
install_agents
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
last_launch() {  # the latest --bg call, with every line of its prompt
  awk '/^claude / {bg = / --bg /; if (bg) c = $0; next} bg {c = c "\n" $0} END {print c}' "$HOME/claude-calls.log"; }
# runs_on TASK MODEL: the latest launch runs the task's worker on MODEL: crew started it
# with --model MODEL, or resumed in place, passing no flags, a session it started so.
runs_on() {
  local launch; launch=$(last_launch)
  [[ $launch == *" --model $2 "* || ($launch == "claude --resume $(crew get "$1" worker_session) --bg "* && $(crew get "$1" worker_model) == "$2") ]]
}
fill() { sed -i -e '/^## Goal$/a x' -e '/^## Acceptance criteria$/a - z' -e '/^## Size$/a About 10 lines.' -e 's/^risk:$/risk: low/' "$1"; }
review() { sed -i "/^## Review rounds\$/i ### Automated review $2\\n${3:+$3\\n}" "$1"; }   # task-file review-number [record-line]
clear_review() { sed -i '/^### Automated review 1$/,+1d' "$1"; }   # task-file: takes out what review wrote
section() { awk -v h="$2" '$0==h {on=1; next} on && /^## / {exit} on' "$1"; }
# What a worker does before its hand-over: it records the evidence for each acceptance
# criterion, on its branch as it is now, here or on origin. Recording it again names
# the new commit.
record_evidence() {  # task
  local f b sha n i; f=$(crew path "$1"); b=$(crew get "$1" branch)
  sha=$(git -C "$SB/webapp" rev-parse --short --verify -q "refs/heads/$b" || git -C "$SB/webapp" rev-parse --short --verify -q "refs/remotes/origin/$b")
  if section "$f" "## Evidence" | grep -q '^### [0-9]'; then
    sed -i -E -e "/^## Evidence\$/,/^## /s/^(### [0-9]+\..*) \(on [0-9a-f]+\)\$/\1/" -e "/^## Evidence\$/,/^## /s/^(### [0-9]+\..*)\$/\1 (on $sha)/" "$f"
  else
    n=$(section "$f" "## Acceptance criteria" | grep -c '^- ')
    for ((i = n; i >= 1; i--)); do  # each lands just under the heading, so the last goes first
      sed -i "/^## Evidence\$/a ### $i. (on $sha)\\nok" "$f"
    done
  fi
}
# The PR body's Evidence, as the worker builds it from the task's: an item for each entry.
pr_body() {  # task
  local f pr; f=$(crew path "$1"); pr=$(crew get "$1" pr)
  [[ -n $pr ]] || return 0
  { printf 'The objective.\n\n## Evidence\n\n'; section "$f" "## Evidence" | sed -n -E 's/^### ([0-9]+\..*)$/\1: `ok`/p'; } >"$HOME/gh-pr-body-${pr##*/}"
}
# The commit a task's evidence for its first criterion names.
evidence_commit() { sed -n -E 's/^### 1\..*\(on ([0-9a-f]+)\)$/\1/p' "$(crew path "$1")"; }
# What a worker does to put its task in pr-open: evidence for each acceptance criterion,
# the first time, in the task and the PR body, then crew hand-over. A refusal fails the
# suite, since the checks after it would run against a task that isn't pr-open.
resumes() { grep -c -- "--resume" "$HOME/claude-calls.log"; }
launches() { grep -c -- " --bg" "$HOME/claude-calls.log"; }
prompt_of() {  # task-id: the prompt of the latest restack session started for it, every line of it
  awk -v t="You are restacking task $1 " '/^claude / {on = index($0, t); if (on) c = substr($0, on); next} on {c = c "\n" $0} END {print c}' "$HOME/claude-calls.log"; }
# restacked TASK: crew's latest restack of the task started a fresh worker on Sonnet, resuming
# nothing, and the task records that session as its worker.
restacked() {
  local l; l=$(grep -F "You are restacking task $(basename "$(crew path "$1")" .md) " "$HOME/claude-calls.log" | tail -1)
  [[ $l == "claude --name "*" --model sonnet You are restacking task "* && $l == *" --agent implementer "* && $l != *--resume* &&
    $(crew get "$1" worker_model) == sonnet &&
    $(jq -r --arg j "$(crew get "$1" worker_job)" '.[] | select(.id == $j) | .sessionId' "$HOME/agents.json") == "$(crew get "$1" worker_session)" ]]
}
hand_over() {  # task: records evidence and, unless it has one, an automated review, since hand-over needs both
  section "$(crew path "$1")" "## Evidence" | grep -q '^### ' || record_evidence "$1"
  grep -q '^### Automated review ' "$(crew path "$1")" || review "$(crew path "$1")" 1
  pr_body "$1"
  crew hand-over "$1" >/dev/null || { FAIL=$((FAIL + 1)); printf '  FAIL: hand_over %s refused at line %s\n' "$1" "${BASH_LINENO[0]}"; }
}
# The latest --settings sets CREW_PROJECT to project $1, CREW_HOME to the home, and
# CREW_LEAD_FINGERPRINT to nothing: a background session takes its environment from
# Claude Code's daemon, not from crew, and a lead's own shell may have started that.
session_env() { jq -e --arg p "$1" --arg h "$L" '.env.CREW_PROJECT == $p and .env.CREW_HOME == $h and .env.CREW_LEAD_FINGERPRINT == ""' "$HOME/claude-settings.last" >/dev/null; }
# The latest --settings run crew pause --hook after every tool call.
has_pause_hook() { jq -e '.hooks.PostToolUse | any(.matcher == "*" and .hooks == [{type: "command", command: "\"${CREW_HOME:-$HOME/crew}/bin/crew\" pause --hook"}])' "$HOME/claude-settings.last" >/dev/null; }
# The latest --settings without what crew adds: its env and, for a worker or goal lead, the pause hook.
given_settings() {
  jq -c 'del(.env) | if .hooks.PostToolUse == null then . else
           .hooks.PostToolUse |= map(select(.hooks[0].command | endswith(" pause --hook") | not))
           | if .hooks.PostToolUse == [] then del(.hooks.PostToolUse) else . end end' "$HOME/claude-settings.last"; }

crew project add >/dev/null
PE=$(ls "$L"/projects/*/project.env); PD=$(dirname "$PE"); P=$(basename "$PD")
check "project add records the remote in project.env, and this machine's clone in local.env" '[[ $(crew config REMOTE) == "$SB/remote.git" ]] && ! grep -q "^REPO=" "$PE" && [[ $(cat "$PD/local.env") == *"REPO=\"$SB/webapp\""* ]]'
check "local.env isn't committed, project.env is" '[[ -z $(git -C "$L" ls-files "$PD/local.env") && -n $(git -C "$L" ls-files "$PE") && -z $(git -C "$L" status --porcelain) ]]'
check "crew config --project reads a project from anywhere" '[[ $(cd "$HOME" && crew config REPO --project "$P") == "$SB/webapp" ]]'
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)   # it checks claude --version, not a session
check "crew doctor finds this machine's clone" '[[ $doctor_out == *"ok   $P: cloned on this machine"* ]]'
check "crew doctor checks that the lead, workers and gardener load project memory" '[[ $(grep -c "ok   .* loads project memory (crew memory --hook)" <<<"$doctor_out") == 3 ]]'
check "crew doctor checks that the lead's settings tell it what crew did" '[[ $doctor_out == *"ok   lead/.claude/settings.json tells the lead what crew did (crew lead --hook)"* ]]'
jq 'del(.hooks.UserPromptSubmit)' "$L/lead/.claude/settings.json" >"$HOME/ls" && mv "$HOME/ls" "$L/lead/.claude/settings.json"
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)
check "crew doctor flags lead settings without their UserPromptSubmit hook" '[[ $doctor_out == *"FAIL lead/.claude/settings.json tells the lead what crew did (crew lead --hook)"* ]]'
git -C "$L" checkout -q -- lead/.claude/settings.json
# crew lead gives its session the fingerprint of what a lead loads, and the lead's
# UserPromptSubmit hook tells the session once that has changed.
crew lead hello >/dev/null 2>&1; fp=$(tail -1 "$HOME/claude-fingerprint.log" | cut -d" " -f1)
check "crew lead passes claude the fingerprint of what a lead loads" '[[ $fp =~ ^[0-9a-f]{12}$ && $(tail -1 "$HOME/claude-fingerprint.log") == "$fp claude --name lead"*" hello" ]]'
out=$(CREW_LEAD_FINGERPRINT=$fp crew lead --hook </dev/null); st=$?
check "the lead's hook says nothing while what a lead loads is unchanged" '((st == 0)) && [[ -z $out ]]'
echo "# changed" >>"$L/agents/lead.md"; install_agents
out=$(CREW_LEAD_FINGERPRINT=$fp crew lead --hook </dev/null); st=$?
check "after agents/lead.md changes, the lead's hook tells the session to have the user start a new lead" \
  '((st == 0)) && [[ $out == "crew'"'"'s lead instructions or settings changed since this session started (crew is now $(cat "$L/VERSION")). Tell the user once, briefly, that they should quit and start a new lead to get them." ]]'
out=$(CREW_LEAD_FINGERPRINT='' crew lead --hook </dev/null); st=$?
check "the lead's hook says nothing in a goal lead's session, which crew starts with the fingerprint empty" '((st == 0)) && [[ -z $out ]]'
git -C "$L" checkout -q -- agents; install_agents
echo 'LEAD_PERMISSION_MODE=default' >>"$PE"
out=$(CREW_LEAD_FINGERPRINT=$fp crew lead --hook </dev/null)
check "after the lead's project changes its permission mode, the lead's hook tells the session too" '[[ $out == "crew'"'"'s lead instructions or settings changed since this session started"* ]]'
sed -i '/^LEAD_PERMISSION_MODE=default$/d' "$PE"
out=$(CREW_LEAD_FINGERPRINT=$fp crew lead --hook </dev/null)
check "and says nothing once everything a lead loads is as it was" '[[ -z $out ]]'
# The lead's hook also tells each session what crew did since its previous prompt (news.go).
prompt() {  # session [event]: what the lead's hook adds to a prompt from that session; by default the phone lead's, for every project
  jq -nc --arg s "$1" --arg e "${2:-UserPromptSubmit}" '{session_id: $s, hook_event_name: $e, prompt: "hello"}' 2>/dev/null |
    CREW_PROJECT=${CREW_PROJECT:-all} crew lead --hook
}
as_worker() { CREW_LEAD_FINGERPRINT='' crew "$@" >/dev/null; }  # as a worker's session runs crew: its settings set the fingerprint empty
DID="What crew did since the user's previous message:"; HELD="What crew tick is holding back, and why:"
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
check "a worker asks to split a branch past 1000 lines by default, and crew config lists it" '[[ $(crew config MAX_PR_LINES) == 1000 ]] && crew config | grep -qx MAX_PR_LINES=1000'
check "Codex is off in automated review by default, with 2 rounds between the reviewers when it's on" '[[ $(crew config CODEX_REVIEW) == no && $(crew config AUTO_REVIEW_REBUTTALS) == 2 ]] && crew config | grep -qx CODEX_MODEL_EFFORT=xhigh'
# crew codex: one read-only review by Codex, through tests/stubs/codex.
mkdir -p "$HOME/.codex"
cat >"$HOME/.codex/config.toml" <<'TOML'
model_provider = "proxy"
mcp_servers.inline = { command = "inline" }

[model_providers.proxy]
base_url = "http://proxy.invalid/v1"

[mcp_servers.sentry]
command = "sentry"

[mcp_servers.sentry.env]
TOKEN = "x"

[projects."/home/x/repo"]
trust_level = "trusted"

[tui]
theme = "dark"
TOML
echo '{"token":"signed-in"}' >"$HOME/.codex/auth.json"
mkdir -p "$SB/rev" && echo "Adversarially review the briefing." >"$SB/rev/prompt.md"
out=$(cd "$SB" && crew codex webapp rev/prompt.md rev/answer-0 2>&1); st=$?
check "crew codex runs codex exec read-only with approvals off, in a new session in the review root, from paths relative to the caller" \
  '((st == 0)) && grep -qxF "codex exec -c sandbox_mode=\"read-only\" -c approval_policy=\"never\" -m gpt-6-astra -c model_reasoning_effort=\"xhigh\" --skip-git-repo-check --json -o $SB/rev/answer-0.md -C $SB/webapp - (in $SB/webapp)" "$HOME/codex-calls.log"'
check "crew codex gives Codex the prompt file on stdin, and writes its reply and events" \
  'cmp -s "$SB/rev/prompt.md" "$HOME/codex-stdin.last" && [[ $(cat "$SB/rev/answer-0.md") == "stub review" ]] && grep -q turn.completed "$SB/rev/answer-0.events.jsonl" && [[ $(head -1 <<<"$out") == "final message: $SB/rev/answer-0.md" ]]'
check "crew codex prints what the run cost, and writes it beside its reply: the tokens its completed turn reports, the time, and the model and effort it ran at" \
  '[[ "cost: $(cat "$SB/rev/answer-0.cost")" == "$(tail -1 <<<"$out")" && $(tail -1 <<<"$out") == "cost: 12,900 tokens (12,000 input including 8,000 cache read, 900 output); "*"s; gpt-6-astra at xhigh effort" ]]'
echo 'CODEX_MODEL="gpt-next"' >>"$PE"
crew codex "$SB/webapp" "$SB/rev/prompt.md" "$SB/rev/answer-1" >/dev/null 2>&1
check "crew codex uses the project's model" '[[ $(tail -1 "$HOME/codex-calls.log") == *" -m gpt-next "* ]]'
sed -i '/^CODEX_MODEL=/d' "$PE"
check "crew codex takes no subcommand: a stray argument is refused" '! crew codex start "$SB/webapp" "$SB/rev/prompt.md" "$SB/rev/answer-2" 2>/dev/null'
touch "$HOME/codex-fail"
out=$(crew codex "$SB/webapp" "$SB/rev/prompt.md" "$SB/rev/answer-0" 2>&1); st=$?
check "crew codex passes on a failing codex's status" '((st == 3)) && [[ $out == *"codex exited with status 3"* ]]'
check "a Codex run that ends before its turn completes still reports its time, and says its tokens weren't reported" \
  '[[ $out == *"cost: tokens not reported; "*"s; gpt-6-astra at xhigh effort"* ]]'
rm "$HOME/codex-fail"
mkdir -p "$SB/rev/answer-3.cost"
out=$(crew codex "$SB/webapp" "$SB/rev/prompt.md" "$SB/rev/answer-3" 2>&1); st=$?
check "a cost crew codex can't write leaves Codex's report and status as they were, with a warning" \
  '((st == 0)) && [[ $(cat "$SB/rev/answer-3.md") == "stub review" && $out == *"can'"'"'t keep what the run cost"* && $out == *"final message: $SB/rev/answer-3.md"* ]]'
crew codex "$SB/webapp" "$SB/rev/prompt.md" "$SB/rev/answer-4" >/dev/null 2>&1
check "crew codex runs Codex in a home with the user's config and sign-in but no MCP server and no repo trust, under any name" \
  'grep -q "base_url = \"http://proxy.invalid/v1\"" "$HOME/codex-home.last" && grep -q "theme = \"dark\"" "$HOME/codex-home.last" && grep -q signed-in "$HOME/codex-home.last" && ! grep -Eq "mcp_servers|sentry|inline|projects|trust_level" "$HOME/codex-home.last"'
check "crew codex leaves the user's own Codex home as it was" \
  'grep -q "mcp_servers.sentry" "$HOME/.codex/config.toml" && grep -q signed-in "$HOME/.codex/auth.json"'
check "CLAUDECODE never reaches codex" '! grep -q LEAK "$HOME/codex-calls.log"'
# crew review-cost: what a Claude reviewer's run cost, from its transcript.
mkdir -p "$HOME/.claude/projects/-sb-webapp/5058aa63/subagents"
cat >"$HOME/.claude/projects/-sb-webapp/5058aa63/subagents/agent-a8c4c13dcb083ebd0.jsonl" <<'EOF'
{"type":"user","timestamp":"2026-11-17T03:42:54.662Z","message":{"role":"user","content":"Review it."}}
{"type":"assistant","timestamp":"2026-11-17T03:42:56.664Z","effort":"high","message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":13332,"cache_read_input_tokens":0,"output_tokens":224}}}
{"type":"assistant","timestamp":"2026-11-17T03:42:57.997Z","effort":"high","message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":2,"cache_creation_input_tokens":13332,"cache_read_input_tokens":0,"output_tokens":224}}}
{"type":"user","timestamp":"2026-11-17T03:45:21.834Z","message":{"role":"user","content":[{"type":"tool_result"}]}}
EOF
check "crew review-cost finds a subagent's transcript by its agent ID and prints what its run cost" \
  '[[ $(crew review-cost a8c4c13dcb083ebd0) == *"; 2m27s; claude-opus-5-5 at high effort" ]]'
check "crew review-cost refuses an agent with no transcript, or a pattern for an ID" \
  '! crew review-cost a0000000000000000 2>/dev/null && ! crew review-cost "a*" 2>/dev/null'
echo 'CODEX_REVIEW=yes' >>"$L/config/crew.env"
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1 || true)
check "with CODEX_REVIEW=yes in crew.env, crew doctor checks each project's codex is installed and signed in" \
  '[[ $doctor_out == *"ok   $P: codex installed (CODEX_REVIEW=yes)"* && $doctor_out == *"ok   $P: codex signed in"* ]]'
touch "$HOME/codex-signed-out"
doctor_out=$(env -u CLAUDECODE crew doctor 2>&1); st=$?
check "crew doctor fails its required checks for a signed-out codex" '((st != 0)) && [[ $doctor_out == *"FAIL $P: codex signed in — run '"'codex login'"'"* && $doctor_out == *"Some required checks failed."* ]]'
rm "$HOME/codex-signed-out"
mkdir -p "$SB/no-codex"
for s in "$TESTS_DIR"/stubs/*; do [[ $s == */codex || $s == */crew ]] || ln -sf "$s" "$SB/no-codex/"; done   # the stub crew would hide the home's
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
fill "$F1"; sed -i -e '/^About 10 lines\.$/d' -e 's/^risk: low$/risk:/' "$F1"
check "ready refuses a brief without a size estimate" '[[ $(crew ready 1 2>&1) == *"fill in: Size"* ]]'
sed -i -e '/^## Size$/a About 10 lines.' -e '/^## Plan$/a 1. Add a filters field.' -e '/^## Goal$/i ## Structure\nA field on SearchQuery.\n' "$F1"
check "ready refuses a brief that still says how to meet its objective, naming each section and how to rewrite it" \
  '[[ $(crew ready 1 2>&1) == *"001-saved-search-filters'"'"'s brief still says how to meet its objective, under Structure and Plan"*"Rewrite it as SETUP.md'"'"'s \"Rewriting briefs that still have a plan\" says, then run crew ready 001-saved-search-filters"* && $(crew get 1 status) == draft ]]'
sed -i -e '/^1\. Add a filters field\.$/d' -e '/^## Structure$/,/^$/d' -e 's/^- z$/1. z/' "$F1"
check "ready refuses acceptance criteria that aren't '- ' items" '[[ $(crew ready 1 2>&1) == *"write each acceptance criterion as a '"'"'- '"'"' item"*"this line isn'"'"'t one: 1. z" ]]'
sed -i -e 's/^1\. z$/- z/' -e '/^- z$/a \  in detail\nAlso: z holds.' "$F1"
check "ready refuses a criterion beside the '- ' items, and takes detail indented under one" '[[ $(crew ready 1 2>&1) == *"this line isn'"'"'t one: Also: z holds." ]]'
sed -i '/^Also: z holds\.$/d' "$F1"
check "ready refuses a task without a risk tier, and says how to set one" \
  '[[ $(crew ready 1 2>&1) == *"set its risk: crew set 001-saved-search-filters risk low|medium|high"* && $(crew get 1 status) == draft ]]'
check "set refuses a risk that isn't low, medium or high" \
  '[[ $(crew set 1 risk bogus 2>&1) == *"risk must be low, medium or high, not '"'"'bogus'"'"'"* && -z $(crew get 1 risk) ]]'
crew set 1 risk low
check "ready accepts a brief with a Goal, Acceptance criteria and a Size, and no Structure or Plan" 'crew ready 1 >/dev/null && [[ $(crew get 1 status) == ready ]]'
sed -i '/^## Plan$/a 1. Add a filters field.' "$F1"
check "dispatch refuses a ready task whose brief still says how to meet its objective, --force or not" \
  '[[ $(crew dispatch 1 2>&1) == *"brief still says how to meet its objective, under Plan"* && $(crew dispatch 1 --force 2>&1) == *"under Plan"* && $(crew get 1 status) == ready && -z $(crew get 1 worker_job) ]]'
sed -i '/^1\. Add a filters field\.$/d' "$F1"
check "find: no match exits non-zero" '! crew find nomatch 2>/dev/null'
check "find: ambiguous exits non-zero" '! crew find saved 2>/dev/null'
check "dispatch starts a worker" 'CREW_LEAD_FINGERPRINT=0123456789ab crew dispatch 1 >/dev/null'
check "a dispatch run by a lead doesn't pass on the lead's fingerprint" '[[ $(grep -- "--agent implementer" "$HOME/claude-fingerprint.log" | tail -1) == "unset claude "* ]]'
check "a worker's settings set the lead's fingerprint empty, so a daemon a lead started can't hand it on" 'jq -e ".env.CREW_LEAD_FINGERPRINT == \"\"" "$HOME/claude-settings.last" >/dev/null'
WT=$(crew get 1 worktree)
check "worktree is on the task branch" '[[ $(git -C "$WT" branch --show-current) == agent/saved-search-filters ]]'
check "session id recorded, starting with the job id" '[[ $(crew get 1 worker_session) == "$(crew get 1 worker_job)"-* ]]'
check "job id recorded without color codes" '[[ $(crew get 1 worker_job) =~ ^[0-9a-f]{8}$ ]]'
check "dispatch records the fingerprint of what the worker loaded" '[[ $(crew get 1 worker_fingerprint) =~ ^[0-9a-f]{12}$ ]]'
check "workers start with the configured auto-compact window" 'grep -- "--agent implementer" "$HOME/claude-calls.log" | tail -1 | grep -q -- " --autocompact 400k "'
check "workers get the shipped settings, with their project, the home and the pause hook, when the project adds none" \
  '[[ $(given_settings) == "$(jq -c . "$L/config/worker-settings.json")" ]] && session_env "$P" && has_pause_hook'
check "git worktrees don't call wt" '[[ ! -e $HOME/wt-calls.log ]]'
# The hand-over to the user: crew hand-over is the one way into pr-open, and checks
# the task every time. Three criteria; the nested bullet isn't one.
sed -i '/^## Acceptance criteria$/a - z2\n  - nested, not a criterion\n- z3' "$F1"
check "hand-over refuses a task without a PR" '[[ $(crew hand-over 1 2>&1) == *"task has no PR"* && $(crew get 1 status) == working ]]'
mode=$(crew get 1 pr_mode); crew set 1 pr_mode none
check "hand-over needs no PR when the task opens none" '[[ $(crew hand-over 1 2>&1) == *"evidence missing for acceptance criteria 1, 2, 3 of 3"* ]]'
crew set 1 pr_mode "$mode"
check "worker can update the ledger from its worktree" \
  '(cd "$WT" && crew set 1 pr https://github.com/isaac/webapp/pull/12) && [[ $(crew get 1 pr) == https://github.com/isaac/webapp/pull/12 ]]'
refused=0
for st in draft ready working needs-input addressing-feedback merged abandoned; do
  crew set 1 status "$st"; out=$(crew set 1 status pr-open 2>&1) && continue
  [[ $out == *"use crew hand-over 001-saved-search-filters: it checks the evidence"* && $(crew get 1 status) == "$st" ]] && refused=$((refused + 1))
done
crew set 1 status working
check "crew set refuses status pr-open from every other status, and names crew hand-over" '((refused == 7))'
refused=0
crew set 1 status 'pr-open ' 2>/dev/null || refused=$((refused + 1))
crew set 1 status $'\tpr-open' 2>/dev/null || refused=$((refused + 1))
crew set 1 status pr-open "" 2>/dev/null || refused=$((refused + 1))  # joined with a space
check "crew set refuses pr-open with a space or tab around it, which reading the status back would drop" \
  '((refused == 3)) && [[ $(crew get 1 status) == working ]]'
sed -i -e '/^## Evidence$/a ### 1. z2\n```\nok\n```\n### 3. z\nok' "$F1"
check "hand-over refuses a task without evidence for every acceptance criterion, and names the missing ones" \
  'out=$(crew hand-over 1 2>&1); (($? == 1)) && [[ $out == *"evidence missing for acceptance criteria 2 of 3"* && $(crew get 1 status) == working ]]'
sed -i -e '/^### 3. z$/i ### 2. z3\nseen' -e 's/^\(- z[23]\?\)$/1. \1/' "$F1"
check "hand-over refuses acceptance criteria that aren't '- ' items, and says to ask" '[[ $(crew hand-over 1 2>&1) == *"no '"'"'- '"'"' items to show evidence for: ask"* ]]'
sed -i 's/^1\. \(- z[23]\?\)$/\1/' "$F1"
# A hand-over waiting for the ledger lock checks the task as it is once it has it
# (-o: the hand-over mustn't inherit the lock flock holds).
flock -o "$L/.crew.lock" bash -c 'crew hand-over 1 >"$HOME/hand-over.out" 2>&1 & sleep 0.5; sed -i "s/^status: .*/status: abandoned/" "$1"' _ "$F1"
check "a hand-over waiting on the lock refuses a task abandoned meanwhile" \
  'wait_for "grep -q abandoned \"\$HOME/hand-over.out\"" && [[ $(crew get 1 status) == abandoned ]]'
# A task in flight may be waiting on a collaborator's question when its worker finishes.
crew set 1 status needs-input
# Risk tiers: a branch that changes a file matching one of SENSITIVE_PATHS makes its
# task high risk, at the hand-over and at every sync; high risk needs a security review.
commit_in() { (cd "$1" && shift && for f in "$@"; do mkdir -p "$(dirname "$f")" && echo x >"$f"; done && git add -A && git commit -qm "change $*"); }
commit_in "$WT" src/search.ts src/oauth.ts
# The evidence the user reads names the commit it ran on, in the task and the PR body,
# and that commit holds the branch's own work as it is now.
check "hand-over refuses evidence that doesn't name the commit it ran on" \
  '[[ $(crew hand-over 1 2>&1) == *"evidence for acceptance criteria 1, 2, 3 doesn'"'"'t name the commit it ran on: end each entry'"'"'s heading with '"'"'(on <sha>)'"'"'"* && $(crew get 1 status) == needs-input ]]'
record_evidence 1; E1=$(git -C "$WT" rev-parse --short HEAD)
printf '## Evidence\n\n1. z (on %s): `ok`\n3. z3 (on %s): `ok`\n' "$E1" "$E1" >"$HOME/gh-pr-body-12"
check "hand-over refuses a PR body without evidence for every acceptance criterion, and names the missing ones" \
  '[[ $(crew hand-over 1 2>&1) == *"the PR body'"'"'s Evidence has no item for acceptance criteria 2 of 3: add a '"'"'<n>. … (on <sha>)'"'"' item for each"* && $(crew get 1 status) == needs-input ]]'
pr_body 1; sed -i "s/^2\.\(.*\) (on $E1)/2.\1/" "$HOME/gh-pr-body-12"
check "hand-over refuses a PR body whose evidence doesn't name the commit it ran on" \
  '[[ $(crew hand-over 1 2>&1) == *"the PR body'"'"'s Evidence for acceptance criteria 2 doesn'"'"'t name the commit it ran on"* && $(crew get 1 status) == needs-input ]]'
pr_body 1; sed -i "s/(on $E1)/(on 0123abc)/" "$HOME/gh-pr-body-12"
check "hand-over refuses evidence that names a commit it can't find" \
  '[[ $(crew hand-over 1 2>&1) == *"couldn'"'"'t check the evidence: 0123abc isn'"'"'t a commit in "* && $(crew get 1 status) == needs-input ]]'
pr_body 1; commit_in "$WT" src/filters.ts
check "hand-over refuses evidence recorded before a later commit of the branch's own, in the task and the PR body, naming each item" \
  '[[ $(crew hand-over 1 2>&1) == *"evidence ran on other work than the branch has now, here and on origin, in ## Evidence: 1 (on $E1), 2 (on $E1), 3 (on $E1); the PR body: 1 (on $E1), 2 (on $E1), 3 (on $E1). Push the branch, run the evidence again"* && $(crew get 1 status) == needs-input ]]'
record_evidence 1; pr_body 1
# Only the user's checks may be left to them: the brief marks one "User check:", or the
# user makes one theirs later, in a Decision. Criterion 1 is marked; 3 isn't, until then.
cp "$F1" "$SB/f1-before-user-checks"
not_run='Not done: it needs a real terminal. To try: run it.'
sed -i 's/^- z2$/- User check: z2/' "$F1"
sed -i "/^### [13]\. .* (on [0-9a-f]*)\$/a $not_run" "$F1"
sed -i "/^[13]\. /a \\   $not_run" "$HOME/gh-pr-body-12"
out=$(crew hand-over 1 2>&1); st=$?
check "hand-over refuses evidence that leaves a check to the user, in the task and the PR body, unless the brief marks it a user check" \
  '((st == 1)) && [[ $out == *"evidence says its check wasn'"'"'t run, in ## Evidence: 3 ('"'"'Not done'"'"'); the PR body: 3 ('"'"'Not done'"'"'). If a check ran, reword its evidence to say what ran and what it showed. If not, run it the ways the project'"'"'s AGENTS.md, docs and memory give"* && $(crew get 1 status) == needs-input ]]'
sed -i '/^## Decisions$/a - 2026-11-24 — Criterion 3 is a user check: only a real terminal shows it (lead)' "$F1"
check "a Decision that isn't the user's doesn't make a check theirs" '[[ $(crew hand-over 1 2>&1) == *"in ## Evidence: 3 ("* ]]'
sed -i 's/^\(- 2026-11-24 — Criterion 3 is a user check: .*\) (lead)$/\1 (user)/' "$F1"
out=$(crew hand-over 1 2>&1)
check "hand-over accepts evidence that leaves a check to the user once a Decision of theirs makes it a user check" \
  '[[ $out != *"wasn'"'"'t run"* && $out == *"automated review hasn'"'"'t run"* ]]'
cp "$SB/f1-before-user-checks" "$F1"; pr_body 1
echo 'SENSITIVE_PATHS=":(nosuchmagic)db"' >>"$PE"
out=$(crew hand-over 1 2>&1); st=$?
check "hand-over refuses when it can't check SENSITIVE_PATHS, rather than hand over unchecked" \
  '((st == 1)) && [[ $out == *"couldn'"'"'t check SENSITIVE_PATHS (:(nosuchmagic)db): git diff failed"* && $(crew get 1 status) == needs-input ]]'
sed -i 's/^SENSITIVE_PATHS=.*/SENSITIVE_PATHS="db\/migrate :(glob)**\/auth*"/' "$PE"
# Automated review has to have run first, wherever the task's PR is and whatever its tier.
refused=0
for r in low medium; do
  crew set 1 risk "$r"
  [[ $(crew hand-over 1 2>&1) == *"automated review hasn't run for this task: run a pass of automated review, then hand over again"* && $(crew get 1 status) == needs-input ]] && refused=$((refused + 1))
done
check "hand-over refuses a low- and a medium-risk task with a PR until an automated review is recorded, and leaves its status" '((refused == 2))'
# Where no review is due, the same hand-over goes through. Each case then puts the task back to needs-input.
echo AUTO_REVIEWS=0 >>"$PE"; crew hand-over 1 >/dev/null; project_off=$(crew get 1 status); crew set 1 status needs-input
sed -i '/^AUTO_REVIEWS=0$/d' "$PE"
crew set 1 auto_review no; crew hand-over 1 >/dev/null; task_off=$(crew get 1 status); crew set 1 status needs-input
crew set 1 auto_review ""
pr=$(crew get 1 pr); mode=$(crew get 1 pr_mode); crew set 1 pr_mode none; crew set 1 pr ""
crew hand-over 1 >/dev/null; no_pr=$(crew get 1 status); crew set 1 status needs-input
crew set 1 pr_mode "$mode"; crew set 1 pr "$pr"
check "hand-over needs no automated review when the project's AUTO_REVIEWS is 0, the task's is off, or it has no PR" \
  '[[ $project_off == pr-open && $task_off == pr-open && $no_pr == pr-open ]]'
crew set 1 risk low
review "$F1" 1   # what the worker's automated review leaves
check "hand-over sets pr-open from any status in flight, and logs it, once every acceptance criterion has evidence, and leaves the tier of a branch that touches no sensitive path" \
  'crew hand-over 1 >/dev/null && [[ $(crew get 1 status) == pr-open && $(crew get 1 risk) == low && $(section "$F1" "## Log" | tail -1) == *" — handed over" ]]'
check "hand-over refuses a task handed over already, and so does crew set" \
  '[[ $(crew hand-over 1 2>&1) == *"task is '"'"'pr-open'"'"': only a task in flight, and not in pr-open, can be handed over"* && $(crew set 1 status pr-open 2>&1) == *"use crew hand-over"* ]]'
check "status shows a pr-open task's look" '[[ $(crew status | grep "^  001-saved-search-filters ") == *"  low: approve from the evidence"* ]]'
# Someone else pushes to the PR's branch: a suggestion applied on GitHub, say.
git -C "$WT" push -q -u origin HEAD 2>/dev/null
(cd "$SB/other-clone" && git fetch -q origin && git checkout -q -b from-github origin/agent/saved-search-filters && commit_in . config/auth.yml >/dev/null && git push -q origin HEAD:agent/saved-search-filters)
echo OPEN >"$HOME/gh-pr-state"; out=$(crew sync 2>&1); rm "$HOME/gh-pr-state"
check "sync fetches and raises the risk of a task whose branch on origin gained a sensitive path after its hand-over, and logs it" \
  '[[ $out == *"001-saved-search-filters: risk → high: touches config/auth.yml"* && $(crew get 1 risk) == high && $(tail -1 "$F1") == *" — risk → high: touches config/auth.yml" ]]'
check "sync records an open PR's state with its review decision, its base, its checks and that it merges cleanly" \
  '[[ $(crew get 1 pr_state) == "open, approved" && $(crew get 1 pr_base) == "" && $(crew get 1 ci_state) == "" && $(crew get 1 mergeable) == "" ]]'
echo OPEN >"$HOME/gh-pr-state"; row=$(crew status | grep "^  001-saved-search-filters "); rm "$HOME/gh-pr-state"
check "status shows a pr-open task raised after its hand-over as waiting on its security review, once on its row, and in to review" \
  '[[ $row == *"not yet: "*"no security review yet"*"  high" && $(grep -o "security review" <<<"$row" | wc -l) == 1 && $(crew status | tail -1) == *"#12 high (no security review yet)"* ]]'
# A review round's own commits: its return to pr-open is a hand-over, so they're checked too.
crew set 1 status addressing-feedback; crew set 1 risk medium; crew set 1 auto_review no
git -C "$WT" pull -q --ff-only; commit_in "$WT" db/migrate/001_add_filters.sql app/auth.rb; git -C "$WT" push -q origin HEAD; record_evidence 1; pr_body 1
out=$(crew hand-over 1 2>&1); st=$?
check "hand-over after a review round raises the risk of a branch whose own commits and origin's both touch sensitive paths, naming the first, then refuses it without a security review" \
  '((st == 1)) && [[ $out == *"risk → high: touches app/auth.rb (+2 more)"* && $out == *"high risk: run /security-review, act on its findings, record them under ## Evidence as '"'"'### Security review'"'"', then hand over again"* && $(crew get 1 risk) == high && $(crew get 1 status) == addressing-feedback ]]'
check "a raise at the hand-over turns the task's automated review back on, and logs why" \
  '[[ $(crew config AUTO_REVIEWS --task 1) == 3 && $(section "$F1" "## Log" | grep -c " — risk → high: touches app/auth.rb (+2 more); automated review back on$") == 1 ]]'
sed -i '/^### 3. z (on /i ### Security review\nNo findings.' "$F1"
git -C "$SB/webapp" remote set-url origin "$SB/nowhere.git"; out=$(crew hand-over 1 2>&1); st=$?
git -C "$SB/webapp" remote set-url origin "$SB/remote.git"
check "hand-over refuses when it can't fetch, since origin may have commits it hasn't seen" \
  '((st == 1)) && [[ $out == *"couldn'"'"'t check the branch as origin has it: git fetch origin failed"* && $(crew get 1 status) == addressing-feedback ]]'
clear_review "$F1"   # the review recorded before the raise
out=$(crew hand-over 1 2>&1); st=$?
check "hand-over refuses a task whose automated review was turned back on until a review is recorded" \
  '((st == 1)) && [[ $out == *"automated review hasn'"'"'t run for this task: run a pass of automated review, then hand over again"* && $(crew get 1 status) == addressing-feedback ]]'
review "$F1" 1
check "hand-over hands a high-risk task over once its security review is under Evidence and its automated review recorded" \
  'crew hand-over 1 >/dev/null && [[ $(crew get 1 status) == pr-open && $(crew status | grep "^  001-saved-search-filters ") == *"  high: read the diff; security review ran" ]]'
# Whether a pr-open task's PR can merge now: crew status reads the PR on GitHub as it
# runs. The ledger's last sync says the PR is open and clear throughout.
merge_note() { crew status | grep "^  001-saved-search-filters "; }
checks() { echo "[{\"__typename\": \"CheckRun\", \"name\": \"test\", $1}]" >"$HOME/gh-pr-checks"; }   # the PR's one check run
passing='"status": "COMPLETED", "conclusion": "SUCCESS"'
cp "$HOME/agents.json" "$HOME/agents.before"; setstate "done" null   # the worker is idle
echo OPEN >"$HOME/gh-pr-state"; echo main >"$HOME/gh-pr-base-12"; checks "$passing"
check "status says merge now for an open PR that merges cleanly, with its checks passing and its automated review recorded" \
  '[[ $(merge_note) == *" #12    merge now  high: read the diff; security review ran" ]]'
echo CONFLICTING >"$HOME/gh-pr-mergeable"; a=$(merge_note); rm "$HOME/gh-pr-mergeable"
touch "$HOME/gh-pr-draft"; b=$(merge_note); rm "$HOME/gh-pr-draft"
checks '"status": "COMPLETED", "conclusion": "FAILURE"'; c=$(merge_note); checks '"status": "IN_PROGRESS"'; d=$(merge_note); checks "$passing"
echo agent/elsewhere >"$HOME/gh-pr-base-12"; e=$(merge_note); echo main >"$HOME/gh-pr-base-12"
check "status reads the PR on GitHub, so one the last sync found clear isn't while it conflicts, is a draft, its CI fails or runs, or it targets another branch" \
  '[[ $a == *" not yet: conflicts with main  high"* && $b == *" not yet: draft  high"* && $c == *" not yet: CI failing  high"* && $d == *" not yet: CI running  high"* && $e == *" not yet: PR targets agent/elsewhere, not main  high"* ]]'
echo MERGED >"$HOME/gh-pr-state"; a=$(merge_note); echo CLOSED >"$HOME/gh-pr-state"; b=$(merge_note); echo OPEN >"$HOME/gh-pr-state"
check "a PR merged or closed on GitHub never says merge now, though crew sync hasn't caught up" \
  '[[ $a == *" merged on GitHub  high"* && $b == *" closed on GitHub  high"* && $(crew get 1 status) == pr-open ]]'
check "only pr-open rows have a merge check" '[[ $(crew status | grep -v " pr-open " | grep -cE "merge now|not yet:|on GitHub") == 0 ]]'
before=$(git -C "$L" status --porcelain; git -C "$L" rev-parse HEAD); crew status --all >/dev/null; crew status --done >/dev/null
check "status writes nothing" '[[ $(git -C "$L" status --porcelain; git -C "$L" rev-parse HEAD) == "$before" ]]'
plain=$(script -qec "crew status --plain" /dev/null </dev/null 2>&1)
check "in a terminal, status --plain prints the text and exits" '[[ $plain == "workers running:"* ]]'
(sleep 3; printf q) | timeout 30 script -qec "crew status" /dev/null >"$HOME/live-status.out" 2>&1; live=$?
check "in a terminal, status opens the live view, which q quits, and it writes nothing" \
  '[[ $live == 0 && $(<"$HOME/live-status.out") == *"q quit"* && $(git -C "$L" status --porcelain; git -C "$L" rev-parse HEAD) == "$before" ]]'
check "on a machine that doesn't run the crew, the merge check says the worker can't be seen there" \
  '[[ $(CREW_HOST=laptop crew status | grep "^  001-saved-search-filters ") == *" not yet: worker can'"'"'t be seen from this machine  high"* ]]'
touch "$HOME/agents-fail"; a=$(merge_note); rm "$HOME/agents-fail"
check "when claude agents fails, the merge check doesn't take the worker for idle" '[[ $a == *" not yet: workers couldn'"'"'t be listed  high"* ]]'
touch "$HOME/gh-pr-view-fails"; a=$(merge_note); rm "$HOME/gh-pr-view-fails"
echo UNKNOWN >"$HOME/gh-pr-mergeable"; crew sync >/dev/null 2>&1; rm "$HOME/gh-pr-mergeable"
touch "$HOME/gh-pr-view-fails"; b=$(merge_note); rm "$HOME/gh-pr-view-fails"
check "when GitHub can't be read, status goes by the last sync and says so, and one that found GitHub still checking for conflicts isn't merge now" \
  '[[ $(crew get 1 mergeable) == unknown && $a == *" merge now (couldn'"'"'t read GitHub; as of the last sync)  high"* && $b == *" not yet: GitHub still checking for conflicts (couldn'"'"'t read GitHub; as of the last sync)  high"* ]]'
# Back as it was: the worker busy, the last sync found the PR clear, and GitHub has it merged.
mv "$HOME/agents.before" "$HOME/agents.json"; sed -i '/^mergeable: unknown$/d' "$F1"; rm "$HOME/gh-pr-state" "$HOME/gh-pr-base-12" "$HOME/gh-pr-checks"
returned=0
for st in needs-input working; do
  crew set 1 status "$st"; crew hand-over 1 >/dev/null && [[ $(crew get 1 status) == pr-open ]] && returned=$((returned + 1))
done
check "hand-over takes a task back to pr-open from needs-input and working too, as after a question or a restack" '((returned == 2))'
# Back to the project's automated review setting.
crew set 1 auto_review ""
sed -i '/^SENSITIVE_PATHS=/d' "$PE"; git -C "$SB/other-clone" checkout -q main
# A merge of the base leaves the branch's own work as it was, so its evidence stays
# current; a merge whose conflict the worker resolved changes it.
main_moves() { (cd "$SB/webapp" && mkdir -p "$(dirname "$1")" && echo "from main" >"$1" && git add "$1" && git commit -qm "main: $1" && git push -q); }
merge_main() { (cd "$WT" && git fetch -q origin && { git merge -q --no-edit origin/main >/dev/null 2>&1 || { echo resolved >src/search.ts && git commit -qam "Merge main"; }; } && git push -q origin HEAD); }
E1=$(evidence_commit 1)
main_moves docs/guide.md; merge_main; crew set 1 status addressing-feedback
check "evidence stays current through a merge of the base, though it ran before the merge" \
  'crew hand-over 1 >/dev/null && [[ $(crew get 1 status) == pr-open && $(git -C "$WT" rev-list --merges "$E1..HEAD") != "" ]]'
main_moves src/search.ts; merge_main; crew set 1 status addressing-feedback
check "a merge of the base whose conflict the worker resolved makes the evidence stale" \
  '[[ $(crew hand-over 1 2>&1) == *"evidence ran on other work than the branch has now, here and on origin, in ## Evidence: 1 (on $E1)"* && $(crew get 1 status) == addressing-feedback ]]'
record_evidence 1; hand_over 1
# Evidence that ran on a commit origin doesn't have yet isn't what the user would merge.
crew set 1 status addressing-feedback; commit_in "$WT" src/unpushed.ts; record_evidence 1; pr_body 1; E1=$(evidence_commit 1)
check "hand-over refuses evidence that ran on a commit the branch on origin lacks, and says a push is all that's missing" \
  '[[ $(crew hand-over 1 2>&1) == *"evidence ran on work the branch on origin doesn'"'"'t have yet, in ## Evidence: 1 (on $E1)"*". Push the branch, then hand over again" && $(crew get 1 status) == addressing-feedback ]]'
git -C "$WT" push -q origin HEAD; hand_over 1
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
check "note-only followup doesn't resend the old round" 'crew followup 1 --note "use defaults" >/dev/null && grep -- "--resume" "$HOME/claude-calls.log" | tail -1 | grep -v "round 1 under" | grep -q "use defaults"'
check "note-only followup leaves a task in its review round addressing-feedback" '[[ $(crew get 1 status) == addressing-feedback ]]'
crew set 1 status working; crew set 1 status needs-input; setstate "done" null
check "note-only followup moves a needs-input task asked while working back to working" 'crew followup 1 --note "over to you" >/dev/null && [[ $(crew get 1 status) == working ]]'
crew set 1 status addressing-feedback; crew set 1 status needs-input; setstate "done" null
check "note-only followup moves a needs-input task asked in a review round back to addressing-feedback" 'crew followup 1 --note "over to you" >/dev/null && [[ $(crew get 1 status) == addressing-feedback ]]'
sed -i "s/^status: .*/status: pr-open/" "$F1"; setstate "done" null  # crew set refuses pr-open
check "note-only followup moves a pr-open task back to working" 'crew followup 1 --note "over to you" >/dev/null && [[ $(crew get 1 status) == working ]]'
setstate "done" null
check "note-only followup keeps a working task working and logs no status line" 'n=$(grep -c "status → " "$F1"); crew followup 1 --note "carry on" >/dev/null && [[ $(crew get 1 status) == working && $(grep -c "status → " "$F1") == "$n" ]]'
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
setstate "done" 4242; touch "$HOME/stop-noop"; n=$(resumes)
out=$(crew followup 1 --note "stop didn't take" 2>&1); code=$?
check "a followup to an idle worker whose process won't close sends nothing, starts no copy, and says so, naming the job" \
  '((code == 1)) && [[ $out == *"job $job is still running, so crew sent it nothing"* && $(resumes) == "$n" && $(crew get 1 worker_job) == "$job" && $(crew get 1 worker_session) == "$sess" && $(jq length "$HOME/agents.json") == "$rows" && $(tail -1 "$F1") != *"stop didn'"'"'t take"* ]]'
rm -f "$HOME/stop-noop"
setstate "done" null; touch "$HOME/reopen-on-resume"
out=$(crew followup 1 --note "reopened" 2>&1)
copy=$(jq -r '.[-1].sessionId' "$HOME/agents.json")
check "a copy started anyway, by a process that opened again before the resume, is recorded, with a warning" '[[ $out == *"started a copy of session $sess"* && $(crew get 1 worker_session) == "$copy" && $copy != "$sess" && $(crew get 1 worker_job) == "$(jq -r ".[-1].id" "$HOME/agents.json")" ]]'
setstate "done" null; touch "$HOME/placeholder-session"; sess=$(crew get 1 worker_session)
out=$(crew followup 1 --note "placeholder" 2>&1)
check "a resume whose process first reports a new session ID keeps the recorded session" '[[ $(crew get 1 worker_session) == "$sess" && $out != *copy* ]]'
rm -f "$HOME/placeholder-session"
setstate "done" null; touch "$HOME/fail-resume-late"; job=$(crew get 1 worker_job)
out=$(crew followup 1 --note "late failure" 2>&1)
check "a resumed job that fails straight away falls back to a fresh worker" '[[ $out == *"failed straight away"* && $out == *"(fresh)"* && $(crew get 1 worker_job) != "$job" ]]'
rm -f "$HOME/fail-resume-late"
# A followup starts the worker fresh when what a fresh worker loads has changed
# since this one started, and resumes it otherwise.
followup1() { setstate "done" null; crew followup 1 --note "$1" 2>&1; }
resumed() { [[ $(last_launch) == "claude --resume $(crew get 1 worker_session) --bg New input"* ]]; }
version=$(cat "$L/VERSION"); echo 99.0.0 >"$L/VERSION"
out=$(CREW_VERSION=$version followup1 "a new version")
git -C "$L" checkout -q -- VERSION
check "with nothing a worker loads changed, a followup resumes it, even after the home's VERSION changes" '[[ $out == *"(resume)"* ]] && resumed'
echo "# changed" >>"$L/agents/lead.md"; install_agents; echo "# changed" >>"$L/.claude/skills/pr-review/SKILL.md"
out=$(followup1 "the lead and a skill changed")
check "a change to the lead's agent file or a skill doesn't start a worker fresh" '[[ $out == *"(resume)"* ]] && resumed'
# starts_fresh WHAT: a followup after WHAT changed starts the worker fresh, records
# the new fingerprint, and its log line says why.
starts_fresh() {
  local before; before=$(crew get 1 worker_fingerprint)
  out=$(followup1 "$1 changed")
  check "a followup after $1 changes starts the worker fresh, records the new fingerprint, and logs why" \
    '[[ $out == *"(fresh: its instructions or settings changed)"* && $(crew get 1 worker_fingerprint) =~ ^[0-9a-f]{12}$ && $(crew get 1 worker_fingerprint) != "$before" && $(tail -1 "$F1") == *"followup (fresh: its instructions or settings changed): worker $(crew get 1 worker_job)"* ]] && ! resumed && [[ $(last_launch) == *"--agent implementer "*"You are picking up task"* ]]'
}
echo "# changed" >>"$L/agents/implementer.md"; out=$(followup1 "the home's implementer changed, not installed yet")
check "a change to an agent in the home that crew install hasn't copied yet doesn't start a worker fresh" '[[ $out == *"(resume)"* ]] && resumed'
install_agents; starts_fresh agents/implementer.md
echo "# changed" >>"$L/agents/reviewer.md"; install_agents; starts_fresh "a subagent's file, agents/reviewer.md,"
jq '.permissions.allow += ["Bash(make test)"]' "$L/config/worker-settings.json" >"$HOME/ws" && mv "$HOME/ws" "$L/config/worker-settings.json"
starts_fresh config/worker-settings.json
echo 'WORKER_PERMISSION_MODE=acceptEdits' >>"$PE"; starts_fresh WORKER_PERMISSION_MODE
sed -i '/^WORKER_PERMISSION_MODE=acceptEdits$/d' "$PE"; git -C "$L" checkout -q -- agents config .claude; install_agents
followup1 "back as it was" >/dev/null; out=$(followup1 "nothing changed since")
check "the next followup resumes the worker it started fresh" '[[ $out == *"(resume)"* ]] && resumed'
sed -i 's/^worker_fingerprint: .*/worker_fingerprint:/' "$F1"; setstate "done" 4242; job=$(crew get 1 worker_job)
out=$(crew followup 1 --note "dispatched before fingerprints" 2>&1)
check "a worker started before crew recorded fingerprints starts fresh on its next followup, and says why" \
  '[[ $out == *"(fresh: its instructions or settings changed)"* && $(crew get 1 worker_fingerprint) =~ ^[0-9a-f]{12}$ ]] && ! resumed'
check "a fresh start takes the session it replaces out of agent view" \
  'grep -qx "claude rm $job" "$HOME/claude-calls.log" && [[ -z $(jq -r --arg id "$job" ".[] | select(.id == \$id) | .id" "$HOME/agents.json") && $(crew get 1 worker_job) != "$job" ]]'
# A task's model: a fresh worker runs on it, and a resume keeps the model its session
# started with, so a change of model starts the worker fresh at its next followup.
check "no launch so far, for a task without a model, passes --model" '! grep -q -- "--model" "$HOME/claude-calls.log" && [[ -z $(crew get 1 worker_model) ]]'
check "set refuses a model that isn't opus, sonnet or haiku, naming them" \
  '[[ $(crew set 1 model gpt-5 2>&1) == *"model must be opus, sonnet or haiku, not '"'"'gpt-5'"'"'"* && -z $(crew get 1 model) ]]'
fp=$(crew get 1 worker_fingerprint); crew set 1 model sonnet; out=$(followup1 "now on sonnet")
check "a followup after the task's model changes starts the worker fresh on it, says so, and records the model but no new fingerprint" \
  '[[ $out == *"(fresh: its model changed from the default to sonnet)"* && $(last_launch) == *" --model sonnet You are picking up task"* && $(crew get 1 worker_model) == sonnet && $(crew get 1 worker_fingerprint) == "$fp" && $(tail -1 "$F1") == *"followup (fresh: its model changed from the default to sonnet): worker "* ]]'
out=$(followup1 "still on sonnet")
check "the next followup resumes it in place, passing no --model" '[[ $out == *"(resume)"* && $(last_launch) != *--model* ]] && resumed'
out=$(setstate "done" null; crew followup 1 --fresh --note "fresh on sonnet" 2>&1)
check "a fresh followup starts it on the task's model" '[[ $out == *"(fresh)"* && $(last_launch) == *" --model sonnet "* ]]'
sess=$(crew get 1 worker_session); transcript "$sess" ended
for _ in {1..40}; do echo '{"type":"system","subtype":"stop_hook_summary"}' >>"$HOME/.claude/projects/stub/$sess.jsonl"; done
touch "$HOME/fresh-transcript"; SECONDS=0
out=$(setstate "done" null; crew followup 1 --fresh --note "fresh over a long transcript" 2>&1)
check "a fresh followup is confirmed once its new session takes its prompt, however long the replaced session's transcript" \
  '((SECONDS < 10)) && [[ $out == *"(fresh)"* && $(crew get 1 worker_session) != "$sess" ]]'
rm -f "$HOME/.claude/projects/stub/$sess.jsonl" "$HOME/.claude/projects/stub/$(crew get 1 worker_session).jsonl"
crew set 1 model haiku; echo "# changed" >>"$L/agents/implementer.md"; install_agents; out=$(followup1 "both changed")
check "a followup after the model and the instructions both change says both" \
  '[[ $out == *"(fresh: its model changed from sonnet to haiku, and its instructions or settings changed)"* && $(last_launch) == *" --model haiku "* && $(crew get 1 worker_model) == haiku ]]'
git -C "$L" checkout -q -- agents; install_agents; crew set 1 model ""; out=$(followup1 "back to the default")
check "a followup after the model is cleared starts the worker fresh on the default, without --model" \
  '[[ $out == *"(fresh: its model changed from haiku to the default, and its instructions or settings changed)"* && $(last_launch) != *--model* && -z $(crew get 1 worker_model) ]]'
# A task's effort works the same way: a fresh worker runs at it, and a change of effort, in
# either direction, starts the worker fresh.
check "set refuses an effort that isn't one of Claude Code's levels, naming them" \
  '[[ $(crew set 1 effort extreme 2>&1) == *"effort must be low, medium, high, xhigh or max, not '"'"'extreme'"'"'"* && -z $(crew get 1 effort) ]]'
check "no launch so far, for a task without an effort, passes --effort" '! grep -q -- "--effort" "$HOME/claude-calls.log" && [[ -z $(crew get 1 worker_effort) ]]'
crew set 1 effort medium; out=$(followup1 "now at medium")
check "a followup after an effort is set starts the worker fresh at it, says so, and records the effort" \
  '[[ $out == *"(fresh: its effort changed from the default to medium)"* && $(last_launch) == *" --effort medium You are picking up task"* && $(crew get 1 worker_effort) == medium ]]'
out=$(followup1 "still at medium")
check "the next followup resumes it in place, passing no --effort" '[[ $out == *"(resume)"* && $(last_launch) != *--effort* ]] && resumed'
crew set 1 effort xhigh; out=$(followup1 "now at xhigh")
check "a followup after the effort changes starts the worker fresh at the new one" \
  '[[ $out == *"(fresh: its effort changed from medium to xhigh)"* && $(last_launch) == *" --effort xhigh "* && $(crew get 1 worker_effort) == xhigh ]]'
crew set 1 effort ""; out=$(followup1 "back to the default")
check "a followup after the effort is cleared starts the worker fresh without --effort" \
  '[[ $out == *"(fresh: its effort changed from xhigh to the default)"* && $(last_launch) != *--effort* && -z $(crew get 1 worker_effort) ]]'
sed -i '/^worker_effort:/d' "$F1"; out=$(followup1 "recorded before efforts")
check "a worker recorded before crew kept efforts resumes when its task names none" '[[ $out == *"(resume)"* ]] && resumed'
row1() { crew status | grep "^  001-saved-search-filters "; }
# A task's auto_review: the lead turns its automated review off, and the worker reads
# how many reviews it may run from crew config --task.
check "crew config --task gives a task without auto_review the project's AUTO_REVIEWS" '[[ $(crew config AUTO_REVIEWS --task 1) == 3 ]]'
check "set refuses an auto_review that isn't no" \
  '[[ $(crew set 1 auto_review yes 2>&1) == *"auto_review must be no, not '"'"'yes'"'"'"* && -z $(crew get 1 auto_review) ]]'
crew set 1 auto_review no
check "a high-risk task gets the project's AUTO_REVIEWS though its automated review is off" \
  '[[ $(crew get 1 risk) == high && $(crew config AUTO_REVIEWS --task 1) == 3 && $(row1) != *"automated review"* ]]'
crew set 1 risk medium
check "a task whose automated review is off gets none, and crew config --task lists it so" '[[ $(crew config AUTO_REVIEWS --task 1) == 0 ]] && crew config --task 1 | grep -qx AUTO_REVIEWS=0'
crew set 1 auto_review ""
check "status shows neither a model nor automated review on the row of a task that sets neither" 'r=$(row1); [[ $r != *model* && $r != *"automated review"* ]]'
crew set 1 model sonnet; crew set 1 auto_review no
check "status shows the model, and that automated review is off, on the row of a task that sets them" '[[ $(row1) == *"  model sonnet  automated review off"* ]]'
crew set 1 model ""; crew set 1 auto_review ""; crew set 1 risk high
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
clear_review "$F1"   # hand_over recorded one
printf 'Automated review\n\nNothing found.\n' >"$HOME/summary.md"
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

# crew evidence: a task's screenshots go to its project's evidence repo, one folder per task.
# The repo is named the way it is on GitHub, and git takes that URL to a bare repo here.
T1=$(basename "$F1" .md); T2=$(basename "$F2" .md); EV=$SB/evidence.git
printf 'PNG' >"$SB/shot.png"; echo "what I ran" >"$SB/notes.txt"
out=$(crew evidence 1 "$SB/shot.png" 2>&1); st=$?
check "without EVIDENCE_REPO, crew evidence refuses, naming the setting, and makes no clone" \
  '((st == 1)) && [[ $out == *"set EVIDENCE_REPO in its project.env"* && ! -e $HOME/agent-work/$P/_evidence ]]'
git init -q --bare "$EV" && git clone -q "$EV" "$SB/evidence-seed" 2>/dev/null
(cd "$SB/evidence-seed" && echo "# Evidence" >README.md && git add -A && git commit -qm init && git push -q origin main)
git config --global url."$EV".insteadOf git@github.com:owner/name.git
echo 'EVIDENCE_REPO="git@github.com:owner/name.git"' >>"$PE"
out=$(crew evidence 1 "$SB/shot.png" "$SB/missing.png" 2>&1); st=$?
check "crew evidence refuses a file that isn't there, and publishes nothing" '((st == 1)) && [[ $out == *"$SB/missing.png isn'"'"'t a file"* && $(git --git-dir="$EV" rev-list --count main) == 1 ]]'
out=$(crew evidence 1 "$SB/shot.png" "$SB/notes.txt"); st=$?; sha=$(git --git-dir="$EV" rev-parse main)
check "crew evidence commits the files under the task's folder on the evidence repo's default branch, in one commit, and pushes it" \
  '((st == 0)) && [[ $(git --git-dir="$EV" rev-list --count main) == 2 && $(git --git-dir="$EV" log -1 --format=%s main) == "$T1: shot.png, notes.txt" &&
    $(git --git-dir="$EV" show --format= --name-only main | sort | paste -sd " ") == "$T1/notes.txt $T1/shot.png" ]]'
want="![shot.png](https://github.com/owner/name/blob/$sha/$T1/shot.png?raw=true)"$'\n'"[notes.txt](https://github.com/owner/name/blob/$sha/$T1/notes.txt)"
check "and prints a Markdown line for each, pinned to that commit: an image shown inline, another file linked" '[[ $out == "$want" ]]'
touch "$HOME/agent-work/$P/_evidence/.git/kept"   # marks this clone, to tell it from a fresh one
out=$(crew evidence 1 "$SB/shot.png")
check "a second publish to the same EVIDENCE_REPO fetches into the clone it has, rather than cloning again" '[[ -e $HOME/agent-work/$P/_evidence/.git/kept ]]'
check "publishing the same file again makes no commit, and links to the commit that has it" \
  '[[ $(git --git-dir="$EV" rev-list --count main) == 2 && $out == "![shot.png](https://github.com/owner/name/blob/$sha/$T1/shot.png?raw=true)" ]]'
printf 'A' >"$SB/a.png"; printf 'B' >"$SB/b.png"
crew evidence 1 "$SB/a.png" >"$SB/ev1.out" 2>&1 & one=$!; crew evidence 2 "$SB/b.png" >"$SB/ev2.out" 2>&1 & two=$!; wait "$one" "$two"
sha_of() { sed -E 's#.*/blob/([0-9a-f]+)/.*#\1#' "$1"; }   # the commit a printed link names
sha1=$(sha_of "$SB/ev1.out"); sha2=$(sha_of "$SB/ev2.out")
check "two tasks' crew evidence started at once both land on the default branch, and each prints its own commit" \
  '[[ $(git --git-dir="$EV" rev-list --count main) == 4 && $sha1 != "$sha2" ]] &&
   git --git-dir="$EV" merge-base --is-ancestor "$sha1" main && git --git-dir="$EV" merge-base --is-ancestor "$sha2" main &&
   [[ $(git --git-dir="$EV" show --format= --name-only "$sha1") == "$T1/a.png" && $(git --git-dir="$EV" show --format= --name-only "$sha2") == "$T2/b.png" ]]'
# EVIDENCE_REPO moves to another repo, whose default branch is master: the old clone's branches must never reach it.
EV2=$SB/evidence2.git
git init -q --bare -b master "$EV2" && git clone -q "$EV2" "$SB/evidence2-seed" 2>/dev/null
(cd "$SB/evidence2-seed" && echo '*.log' >.gitignore && git add -A && git commit -qm init && git push -q origin master)
git config --global url."$EV2".insteadOf git@github.com:owner/other.git
sed -i 's#^EVIDENCE_REPO=.*#EVIDENCE_REPO="git@github.com:owner/other.git"#' "$PE"
out=$(crew evidence 1 "$SB/shot.png"); sha=$(git --git-dir="$EV2" rev-parse master)
check "a new EVIDENCE_REPO gets a fresh clone: the files land on its own default branch, and nothing of the old repo reaches it" \
  '[[ $(git --git-dir="$EV2" for-each-ref --format="%(refname)") == refs/heads/master && $(git --git-dir="$EV2" ls-tree -r --name-only master | paste -sd " ") == ".gitignore $T1/shot.png" &&
    $out == "![shot.png](https://github.com/owner/other/blob/$sha/$T1/shot.png?raw=true)" ]]'
git --git-dir="$EV2" branch trunk master && git --git-dir="$EV2" symbolic-ref HEAD refs/heads/trunk
crew evidence 1 "$SB/a.png" >/dev/null
check "when the evidence repo's default branch changes, the next publish goes to the new one" \
  '[[ $(git --git-dir="$EV2" log -1 --format=%s trunk) == "$T1: a.png" && $(git --git-dir="$EV2" rev-parse master) == "$sha" ]]'
echo "build output" >"$SB/build.log"
out=$(crew evidence 1 "$SB/notes.txt" "$SB/build.log"); sha=$(git --git-dir="$EV2" rev-parse trunk)
check "a file the evidence repo ignores is published all the same, so every printed link names a committed file" \
  '[[ $(git --git-dir="$EV2" show --format= --name-only trunk | sort | paste -sd " ") == "$T1/build.log $T1/notes.txt" && $out == *"[build.log](https://github.com/owner/other/blob/$sha/$T1/build.log)" ]]'
git --git-dir="$EV2" symbolic-ref HEAD refs/heads/gone   # a default branch the clone can't find
out=$(crew evidence 1 "$SB/b.png" 2>&1); st=$?
git --git-dir="$EV2" symbolic-ref HEAD refs/heads/trunk
check "when crew can't find the evidence repo's default branch, it publishes nothing, rather than to the branch it knew" \
  '((st == 1)) && [[ $out == *"couldn'"'"'t find the default branch of the evidence repo"* && $(git --git-dir="$EV2" rev-parse trunk) == "$sha" && $(git --git-dir="$EV2" for-each-ref refs/heads | wc -l) == 2 ]]'
# The same on a fresh clone, of a repo with a main crew could guess at.
git --git-dir="$EV2" branch main trunk && git --git-dir="$EV2" symbolic-ref HEAD refs/heads/gone
rm -rf "$HOME/agent-work/$P/_evidence"
out=$(crew evidence 1 "$SB/b.png" 2>&1); st=$?
git --git-dir="$EV2" symbolic-ref HEAD refs/heads/trunk
check "a fresh clone of an evidence repo whose default branch crew can't find publishes nothing either, not even to main" \
  '((st == 1)) && [[ $out == *"couldn'"'"'t find the default branch of the evidence repo"* && $(git --git-dir="$EV2" rev-parse main) == "$sha" ]]'
git --git-dir="$EV2" branch -q -D main
git init -q --bare "$SB/evidence3.git" && git config --global url."$SB/evidence3.git".insteadOf git@github.com:owner/empty.git
sed -i 's#^EVIDENCE_REPO=.*#EVIDENCE_REPO="git@github.com:owner/empty.git"#' "$PE"
crew evidence 1 "$SB/shot.png" >/dev/null 2>&1; out=$(crew evidence 1 "$SB/shot.png" 2>&1); st=$?
check "an evidence repo with no commits yet is refused, asking for a first commit, once crew has a clone of it too" \
  '((st == 1)) && [[ $out == *"has no commit on main yet; push a first commit to it" ]]'
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
check "a half-set-up worktree isn't reused, and the refusal names the crew command that clears it" \
  '[[ $out == *"left over"* && $out == *"crew cleanup $(basename "$F4" .md) --force"* && $out != *"wt "* && $(crew get 4 status) == ready ]]'
(cd "$SB/other-clone" && git fetch -q origin && git checkout -q -B main origin/main && echo moved >>AGENTS.md && git commit -qam "origin moves on" && git push -q origin main)
crew cleanup 4 --force >/dev/null 2>&1; : >"$HOME/wt-hooks.log"
check "cleanup --force removes the worktree a failed dispatch left, and the branch it started" \
  '[[ ! -d $WT4 ]] && ! git show-ref --verify --quiet refs/heads/agent/instance-per-worktree && grep -q "cleaned up worktree" "$F4"'
echo 'flock -n "$CREW_HOME/.crew.lock" true && echo ledger-unlocked' >"$HOME/wt-hook-cmd"
out=$(crew dispatch 4 2>&1)
check "worktrunk dispatch starts a worker" '[[ $(crew get 4 status) == working && -n $(crew get 4 worker_job) ]]'
check "the dispatch after that cleanup starts the branch at origin's current tip, and records it" '[[ $(git -C "$WT4" rev-parse HEAD) == $(git rev-parse origin/main) && $(crew get 4 base_sha) == $(git rev-parse origin/main) ]]'
git -C "$SB/webapp" merge -q --ff-only origin/main   # later steps push from main
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
  local cmd; read -ra cmd <<<"$(last_launch | grep -o 'git push --force-with-lease=[^`]*' | tail -1)"
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
work "$WT9" model.txt; crew set 9 pr https://github.com/isaac/webapp/pull/21; hand_over 9
check "a pushed, unmerged parent is enough to dispatch" 'crew dispatch 10 >/dev/null 2>&1'
WT10=$(crew get 10 worktree); T9=$(tip season-model)
check "the stacked worktree starts on the parent's tip, which is recorded" '[[ $(git -C "$WT10" rev-parse HEAD) == "$T9" && $(crew get 10 base_sha) == "$T9" ]]'
check "the worker is told which task and PR it is stacked on" 'grep -q "stacked on task 009-season-model (PR #21): agent/season-model" "$HOME/claude-calls.log"'
work "$WT10" admin.txt; crew set 10 pr https://github.com/isaac/webapp/pull/22; hand_over 10
out=$(crew status)
check "status lists a stacked task under its parent, columns aligned" '[[ $(grep -A1 "^  009-season-model " <<<"$out" | tail -1) == "    └ 010-season-admin                    pr-open "* ]]'
# The PRs waiting on the user, by the look they need: 9 and 10 are pr-open, and 2
# and 5 join them for a moment.
crew set 2 pr https://github.com/isaac/webapp/pull/41; hand_over 2
crew set 5 pr https://github.com/isaac/webapp/pull/38; crew set 5 risk medium; hand_over 5; crew set 10 risk high
check "status ends a project's block with its PRs to review, cheapest look first, then by PR number" \
  '[[ $(crew status | tail -1) == "  to review: #21 low, #41 low, #38 medium, #22 high (no security review yet)" ]]'
crew set 5 risk high; sed -i 's/^risk: high$/risk:/' "$F10"; out=$(crew status)
check "a pr-open task without a risk tier needs the diff read, and comes last in to review" \
  '[[ $(grep "└ 010-season-admin" <<<"$out") == *"  no risk tier: read the diff" && $(tail -1 <<<"$out") == "  to review: #21 low, #41 low, #38 high (no security review yet), #22 no risk tier" ]]'
crew set 10 risk low; crew set 2 status working; crew set 2 pr ""; crew set 5 status working; crew set 5 pr ""; crew set 5 risk low
out=$(crew sync)
check "sync leaves a stack alone while the parent hasn't moved" '[[ $out != *restack* && $(crew get 9 status) == pr-open ]]'

work "$WT9" model2.txt; T9b=$(tip season-model)
out=$(crew sync)
check "sync reports a child whose parent's branch moved, without restacking it" '[[ $out == *"010-season-admin: needs a restack, 009-season-model moved"* && $(crew get 10 base_sha) == "$T9" && $(git rev-parse "$(tip season-admin)~1") == "$T9" ]]'
check "status shows the restack that's needed, once on the row" '[[ $(crew status | grep -c "needs restack: 009-season-model moved") == 1 ]]'
crew set 10 effort medium
setstate "done" null; L10=$(git rev-parse origin/agent/season-admin); s10=$(crew get 10 worker_session); crew restack 10 >/dev/null
check "restack starts a fresh worker on Sonnet for the rebase, never resuming the worker's conversation" 'restacked 10 && [[ $(crew get 10 worker_session) != "$s10" ]]'
check "a restack on a task with an effort starts as it would without, and records none" '[[ $(last_launch) != *--effort* && -z $(crew get 10 worker_effort) ]]'
check "restack gives the worker a push with the branch's origin commit as the lease" '[[ $(last_launch) == *"\`git push --force-with-lease=agent/season-admin:$L10 origin agent/season-admin\`"* ]]'
check "restack sends the worker the rebase, cut at the parent's old tip" '[[ $(last_launch) == *"git rebase --onto $T9b $T9\`"* && $(last_launch) != *"gh pr edit --base"* && $(crew get 10 status) == working ]]'
check "a restack session is told to read only what its steps need, not the whole task file" \
  '[[ $(last_launch) == *"Don'"'"'t read the whole task file or review the branch first; read only what a step of your restack workflow needs."* && $(last_launch) != *"Read the whole task file"* ]]'
check "status shows the restack a working task needs, and no merge check" \
  '[[ $(crew status | grep "└ 010-season-admin") == *" working "*"  needs restack: 009-season-model moved  effort medium" && $(crew status | grep "└ 010-season-admin") != *"not yet"* ]]'
setstate "done" null; n=$(launches); out=$(crew restack 10 --again 2>&1); st=$?
check "restack by hand waits until the task is pr-open, and starts nothing" \
  '((st == 4)) && [[ $out == *"010-season-admin is working; a restack waits until it'"'"'s pr-open"* && $(launches) == "$n" ]]'
s10=$(crew get 10 worker_session); out=$(crew followup 10 --note "carry on")
check "the next followup after a restack starts afresh on the task's model, the default here, and at its effort, and says why" \
  '[[ $(last_launch) == "claude --name "*"--agent implementer"* && $(last_launch) != *--model* && $(last_launch) == *" --effort medium "* && $(crew get 10 worker_effort) == medium && -z $(crew get 10 worker_model) && $(crew get 10 worker_session) != "$s10" && $out == *"(fresh: its last session only ran a restack)" ]]'
setstate "done" null; hand_over 10; n=$(launches)
check "restack won't send the same restack twice" '[[ $(crew restack 10 2>&1) == *"already asked"* && $(launches) == "$n" && $(crew get 10 restack_sent) == "$T9b" ]]'
setstate working 4242; out=$(crew restack 10 --again 2>&1)
check "restack starts nothing while the task's worker is busy" '[[ $out == *"is busy (working)"* && $(launches) == "$n" && $(crew get 10 status) == pr-open ]]'
setstate "done" null
check "restack --again sends it anyway" 'crew restack 10 --again >/dev/null && [[ $(launches) == $((n + 1)) ]] && restacked 10'
(cd "$WT10" && git rebase -q --onto "$T9b" "$T9") && restack_push "$WT10"; hand_over 10
check "evidence stays current through a restack, though the rebased branch no longer holds the commit it ran on" \
  '[[ $(crew get 10 status) == pr-open ]] && ! git merge-base --is-ancestor "$(evidence_commit 10)" "$(tip season-admin)"'
out=$(crew sync)
check "sync sees the restack, moves base_sha to the parent's new tip and keeps the cut it moved from" \
  '[[ $out != *restack* && $(crew get 10 base_sha) == "$T9b" && $(crew get 10 earlier_base_shas) == "$T9" ]]'
setstate "done" null
check "restack has nothing to do for a task that's up to date" '[[ $(crew restack 10) == *"nothing to restack"* ]]'
sed -i 's/^status: pr-open$/status: working/' "$F9"; out=$(crew restack 9 2>&1); st=$?; sed -i "s/^status: working$/status: pr-open/" "$F9"
check "restack refuses a task that isn't stacked, before waiting for pr-open" '((st == 1)) && [[ $out == *"009-season-model has nothing to restack"* ]]'

F11=$(crew new "Season picker" --branch season-picker --on 10); fill "$F11"; crew ready 11 >/dev/null
crew set 11 model sonnet; crew dispatch 11 >/dev/null; WT11=$(crew get 11 worktree); T10=$(tip season-admin)
work "$WT11" picker.txt; crew set 11 pr https://github.com/isaac/webapp/pull/23; hand_over 11
check "status nests a three-deep stack in order" '[[ $(crew status | grep -A2 "^  009-season-model " | tail -1) == "      └ 011-season-picker "* ]]'
# The user squash-merges 9, and GitHub deletes its branch.
git merge -q --squash origin/agent/season-model >/dev/null && git commit -qm "Season model (#21)" && git push -q origin main
git push -q origin --delete agent/season-model
crew set 9 status merged
out=$(crew sync)
check "sync reports the child of a merged parent" '[[ $out == *"010-season-admin: needs a restack, 009-season-model merged"* ]]'
check "cleanup of a merged parent leaves its stack workable" 'crew cleanup 9 >/dev/null && ! git show-ref --verify --quiet refs/heads/agent/season-model'
crew observe 10 >/dev/null
check "observe falls back to base_sha when the parent's branch is gone" 'grep -q "Review the change $T9b...HEAD" "$HOME/claude-calls.log"'
# GitHub moved 10's PR to main itself when it deleted 9's branch, and this clone, fetching without
# pruning, still has that branch.
git update-ref refs/remotes/origin/agent/season-model "$T9b"; echo main >"$HOME/gh-pr-base-22"
out=$(crew sync)
check "after a merge, a PR GitHub already moved to what the parent merged into isn't sent back" \
  '[[ $out == *"010-season-admin: needs a restack, 009-season-model merged"* && $out != *"PR targets"* && $(crew status) != *"PR targets"* ]]'
git update-ref -d refs/remotes/origin/agent/season-model; rm -f "$HOME/gh-pr-base-22"
crew set 10 auto_review no
echo 'SENSITIVE_PATHS="admin.txt"' >>"$PE"; out=$(crew sync 2>&1)
check "sync checks a stacked task from base_sha when its merged parent's branch is gone" \
  '[[ $(crew get 10 risk) == high && $out == *"010-season-admin: risk → high: touches admin.txt"* && $out != *"couldn'"'"'t check"* ]]'
check "a raise by sync turns the task's automated review back on, and logs why" \
  '[[ $out == *"010-season-admin: risk → high: touches admin.txt; automated review back on"* && $(crew config AUTO_REVIEWS --task 10) == 3 && $(tail -1 "$F10") == *" — risk → high: touches admin.txt; automated review back on" ]]'
sed -i '/^SENSITIVE_PATHS=/d' "$PE"; crew set 10 risk low; crew set 10 auto_review ""
setstate "done" null
check "restack goes bottom-up" '[[ $(crew restack 11 2>&1) == *"restack it first"* ]]'
crew set 10 model opus; crew restack 10 >/dev/null; M=$(git rev-parse origin/main)
check "after a merge, restack targets the default branch and has the worker retarget the PR" '[[ $(last_launch) == *"git rebase --onto $M $T9b\`"* && $(last_launch) == *"gh pr edit --base main"* && $(last_launch) == *"crew set 010-season-admin base main"* ]]'
check "a restack runs on Sonnet whatever the task's own model" 'restacked 10'
check "crew leaves base alone until the worker has restacked" '[[ $(crew get 10 base) == agent/season-model ]]'
(cd "$WT10" && git rebase -q --onto "$M" "$T9b") && restack_push "$WT10"; crew set 10 base main
setstate "done" null; crew followup 10 --note "go on" >/dev/null
check "the next followup after a restack starts afresh on the task's own model" '[[ $(last_launch) == *" --model opus "* && $(last_launch) != *--resume* && $(crew get 10 worker_model) == opus ]]'
crew set 10 model ""; hand_over 10
check "and through a restack onto the default branch, after its parent was squashed into it" \
  '[[ $(crew get 10 status) == pr-open ]] && ! git merge-base --is-ancestor "$(evidence_commit 10)" "$(tip season-admin)"'
T10b=$(tip season-admin)
echo 'SENSITIVE_PATHS="admin.txt"' >>"$PE"
out=$(crew sync)
check "sync raises a stacked task for its own changes, not its rebased parent's: 11 still holds 10's old commits" \
  '[[ $(crew get 10 risk) == high && $(crew get 11 risk) == low && $out != *"011-season-picker: risk"* ]]'
sed -i '/^SENSITIVE_PATHS=/d' "$PE"; crew set 10 risk low
check "a restacked parent is done, and its own child is next" '[[ $out != *"010-season-admin: needs"* && $out == *"011-season-picker: needs a restack, 010-season-admin moved"* ]]'
setstate "done" null; crew restack 11 >/dev/null
check "restack cuts at the parent's old tip after the parent was rebased" '[[ $(last_launch) == *"git rebase --onto $T10b $T10\`"* ]] && restacked 11'
(cd "$WT11" && git rebase -q --onto "$T10b" "$T10") && restack_push "$WT11"
s11=$(crew get 11 worker_session); setstate "done" null; out=$(crew followup 11 --note "go on")
check "the next followup after a restack starts afresh even when the task's own model is Sonnet too" \
  '[[ $(last_launch) == *" --model sonnet You are picking up task "* && $(last_launch) != *--resume* && $(crew get 11 worker_session) != "$s11" && $out == *"(fresh: its last session only ran a restack)" ]]'
hand_over 11
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
prompt every SessionStart >/dev/null; prompt held >/dev/null   # leads that hear what the ticks below do, and hold back
# The lead for every project hears the news after each crew command that adds some, and keeps it in
# $HOME/news-heard, so the news a prompt carries never nears the hook's cap however much the ticks record.
news_size=0
hear_news() {
  local size; size=$(cat "$L"/news/*.log 2>/dev/null | wc -c)
  [[ $size != "$news_size" ]] || return 0
  news_size=$size
  # A worker's command runs with an empty fingerprint, and the lead's hook says nothing to a worker.
  (unset CREW_LEAD_FINGERPRINT; prompt every) >>"$HOME/news-heard"; echo >>"$HOME/news-heard"
}
# From here to the end of the file, every crew command is followed by hear_news. The lead's own hook is
# left alone: prompt runs it, and it would recurse.
crew() {
  local st; command crew "$@"; st=$?
  [[ $1 == lead ]] || hear_news
  return $st
}
PJ=$(basename "$PD")   # the project, which a lead for every project hears before each line
crew cleanup --merged >/dev/null 2>&1
echo OPEN >"$HOME/gh-pr-state"
F13=$(crew new "Tick routing" --branch tick-routing); fill "$F13"; crew ready 13 >/dev/null
crew set 13 model sonnet; crew dispatch 13 >/dev/null
check "dispatch starts a sonnet task's worker on Sonnet" '[[ $(last_launch) == *" --model sonnet "* && $(crew get 13 worker_model) == sonnet ]]'
crew set 13 pr https://github.com/isaac/webapp/pull/12; hand_over 13
setstate working 4242; n=$(resumes)
out=$(crew tick)
check "tick records a new round but leaves a busy worker alone" '[[ $out == *"013-tick-routing: review round 1 recorded"* && $out == *"013-tick-routing: round 1 waiting for its worker (working)"* && $out == *"2 comment(s) from collaborators, for you"* && $(crew get 13 last_round_sent) == 0 && $(resumes) == "$n" && $(crew get 13 status) == pr-open ]]'
check "a lead hears a round held back for a busy worker, and why" '[[ $(prompt held) == *"$HELD$NL- $PJ: 013-tick-routing: round 1 waiting for its worker (working)"* ]]'
setstate "done" null
out=$(crew tick)
check "tick sends the round once the worker is idle, without recording it again" '[[ $out == "013-tick-routing: round 1 sent to its worker" && $(crew get 13 last_round_sent) == 1 && $(crew get 13 review_rounds) == 1 && $(resumes) == $((n + 1)) && $(crew get 13 status) == addressing-feedback ]]'
check "a review round goes to a sonnet task's worker on Sonnet" 'runs_on 13 sonnet'
hand_over 13; setstate working 4242
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
setstate "done" null; hand_over 13
s13=$(crew get 13 worker_session); crew set 13 feedback_since 1970-01-01T00:00:00Z; setstate working 4242 busy; transcript "$s13" ended; n=$(resumes)
check "status shows a worker whose turn ended as idle, though a shell it left running keeps it working" '[[ $(crew status) == *"worker: idle, background task running"* ]]'
out=$(crew tick)
check "tick sends a round to a worker whose turn ended, though a shell it left running keeps it working" '[[ $out == *"013-tick-routing: round 3 sent to its worker"* && $(crew get 13 last_round_sent) == 3 && $(resumes) == $((n + 1)) ]]'
rm -f "$HOME/.claude/projects/stub/$s13.jsonl"; setstate working 4242; hand_over 13
crew set 13 feedback_since 1970-01-01T00:00:00Z; crew tick >/dev/null; crew set 13 feedback_since 1970-01-01T00:00:00Z; waiting=$(crew tick 2>/dev/null)
setstate "done" null
out=$(crew tick)
check "rounds that piled up behind a busy worker are all named while they wait, and in its prompt" '[[ $waiting == *"013-tick-routing: rounds 4 to 5 waiting for its worker (working)"* && $out == *"013-tick-routing: rounds 4 to 5 sent to its worker"* && $(crew get 13 last_round_sent) == 5 && $(last_prompt) == *"New review feedback is in rounds 4 to 5 under"* ]]'
# Another automated review, asked for on the PR (a `crew review` comment) or with crew review.
round() { awk -v h="### Round $2 " 'index($0, h) == 1 {on=1; next} on && /^##/ {exit} on && NF' "$1"; }
comment() { jq -cn --arg id "$1" --arg who "$2" --arg as "$3" --arg b "$4" '{id: ($id | tonumber), user: {login: $who, type: "User"}, author_association: $as, body: $b, html_url: ("u" + $id), created_at: "2026-09-25T21:40:00Z", issue_url: "https://api.github.com/repos/isaac/webapp/issues/12"}'; }
{ comment 130 isaac OWNER $'  Crew Review\r\n'; comment 131 bob COLLABORATOR "crew review"; comment 132 isaac OWNER "Once that's in, crew review it again."; comment 133 isaac OWNER "crew review"; } >"$HOME/gh-more-comments"
hand_over 13; crew set 13 feedback_since 2026-09-25T21:35:00Z; setstate working 4242; n=$(resumes)
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
hand_over 13; setstate "done" null; crew set 13 auto_review no
check "crew review sends the request to an idle worker straight away, though the task's automated review is off" 'crew review 13 >/dev/null 2>&1 && [[ $(crew get 13 last_round_sent) == 8 && $(resumes) == $((n + 2)) && $(crew get 13 status) == addressing-feedback && $(last_prompt) == *"New review feedback is in round 8 under"* ]]'
crew set 13 auto_review ""
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
hand_over 13; ci aaaa1111ffff test:FAILURE lint:-
out=$(crew tick 2>/dev/null)
check "red CI while another check on the head is still running is left alone" '[[ $(crew get 13 ci_state) == running && $(crew get 13 review_rounds) == 8 && -z $out ]]'
ci aaaa1111ffff test:FAILURE lint:SUCCESS docs:SKIPPED build:CANCELLED
out=$(crew tick 2>/dev/null)
check "once every check has finished, a tick records the red CI as a round and sends it to the idle worker" '[[ $out == $'"'"'013-tick-routing: CI round 9 recorded: red on aaaa111\n013-tick-routing: round 9 sent to its worker'"'"' && $(round "$F13" 9) == "- [ ] CI failed on aaaa111: test" && $(crew get 13 last_round_sent) == 9 && $(crew get 13 status) == addressing-feedback && $(resumes) == $((n + 1)) ]]'
check "a CI round goes to a sonnet task's worker on Sonnet" 'runs_on 13 sonnet'
setstate "done" null; hand_over 13
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
crew set 13 worktree "$WT13"; crew set 13 status working; hand_over 13
# Only crew ci-fix asks for headRefOid, so the hook leaves crew sync's read alone.
printf '#!/bin/sh\ncase "$*" in *headRefOid*) crew set 13 status abandoned ;; esac\n' >"$HOME/gh-pr-view-hook"; chmod +x "$HOME/gh-pr-view-hook"
sed -i '/^## Open questions$/a - [ ] Euros or pounds?' "$F13"
crew tick >/dev/null 2>&1
check "a task abandoned during a tick isn't marked needs-input by it" '[[ $(crew get 13 status) == abandoned ]]'
rm "$HOME/gh-pr-view-hook"; sed -i '/^- \[ \] Euros or pounds?$/d' "$F13"; crew set 13 status working; hand_over 13
echo CLOSED >"$HOME/gh-pr-state"
out=$(crew tick 2>/dev/null); byhand=$(crew ci-fix 13)
check "red CI on a PR closed without merging is left alone, by tick and by hand" '[[ -z $out && $byhand == *"PR #12 is closed"* && $(crew get 13 pr_state) == closed && $(crew get 13 status) == pr-open && $(crew get 13 review_rounds) == 9 ]]'
echo OPEN >"$HOME/gh-pr-state"
crew tick >/dev/null 2>&1; setstate "done" null; hand_over 13; ci cccc3333ffff test:FAILURE lint:FAILURE
out=$(crew tick 2>/dev/null)
check "past MAX_CI_FIXES CI rounds, red CI on a new head is a question for you, not a round" '[[ $out == *"013-tick-routing: CI still red after 2 CI rounds the branch caused, for you"* && $out == *"waiting on you (1 open question(s))"* && $(crew get 13 review_rounds) == 10 && $(crew get 13 status) == needs-input ]] && [[ $(section "$F13" "## Open questions") == *"- [ ] CI is still red on cccc333 after 2 CI rounds the branch caused (MAX_CI_FIXES=2): test, lint. Look at it yourself, or tell the worker to try again."* ]]'
hand_over 13; sed -i 's/^- \[ \] CI is still red/- [x] CI is still red/' "$F13"
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
echo 'MAX_CI_FIXES=3' >>"$PE"; ci dddd4444ffff test:FAILURE; crew ci-fix 13 >/dev/null; sed -i '/^MAX_CI_FIXES=3$/d' "$PE"
printf 'CI was red\n\nOlder round, written last.\n' >"$HOME/ci.md"
out=$(crew ci-summary 13 bbbb222 "$HOME/ci.md" 2>&1)
check "with a newer CI round recorded, a comment for an older one still goes to its own commit" '[[ $out == "updated the CI summary for bbbb222 on PR #12" && $(cat "$HOME/gh-summaries/120") == *"written last."* && ! -e $HOME/gh-summaries/122 ]]'
rm -f "$HOME/gh-last-body"
check "ci-summary refuses a commit the task reports no red CI on" '[[ $(crew ci-summary 13 eeee555 "$HOME/ci.md" 2>&1) == *"reports no red CI on eeee555"* && ! -e $HOME/gh-last-body ]]'
out=$(crew feedback 13 --since 1970-01-01T00:00:00Z)
check "crew feedback doesn't read the CI summaries back" '[[ $out == *"comment 115"* && $out != *"comment 120"* && $out != *"comment 121"* ]]'
rm -rf "$HOME/gh-summaries" "$HOME/gh-pr-head" "$HOME/gh-pr-checks"; crew set 13 last_round_sent "$(crew get 13 review_rounds)"
setstate "done" null
# A PR that conflicts with its base: crew tick sends the worker a round once it has handed the
# PR over (pr-open), once for each commit of the base on origin, and never for a stacked task.
base_moves() {  # another PR merges into main on origin; prints main's new short SHA
  git -C "$SB/webapp" commit -q --allow-empty -m "Another PR" && git -C "$SB/webapp" push -q && git -C "$SB/webapp" rev-parse --short=7 HEAD
}
sed -i '/^## Open questions$/,/^## /s/^- \[ \]/- [x]/' "$F13"   # the user has answered them
crew set 13 status working; n=$(resumes); r=$(crew get 13 review_rounds)
echo CONFLICTING >"$HOME/gh-pr-mergeable"; echo main >"$HOME/gh-pr-base-12"; B1=$(base_moves)
out=$(crew tick 2>/dev/null); byhand=$(crew catch-up 13); code=$?
check "a conflict before the worker hands its PR over is recorded by sync, and left to the worker, by tick and by hand" '[[ $(crew get 13 mergeable) == conflicting && $out != *onflict* && $byhand == *"is working, and its conflicts are its worker'"'"'s own business"* && $code == 3 && $(crew get 13 review_rounds) == "$r" && $(resumes) == "$n" ]]'
hand_over 13
printf '#!/bin/sh\ncrew set 13 status abandoned\n' >"$HOME/gh-pr-view-hook"; chmod +x "$HOME/gh-pr-view-hook"
out=$(crew catch-up 13)
check "a task abandoned while crew catch-up reads GitHub is sent nothing" '[[ $out == "nothing to do: 013-tick-routing is abandoned" && $(crew get 13 review_rounds) == "$r" ]]'
rm "$HOME/gh-pr-view-hook"; crew set 13 status working; hand_over 13
out=$(crew tick 2>/dev/null)
check "once the PR is handed over, a tick records the conflict with main's tip on origin as a round of its own, and sends it to the idle worker" '[[ $out == *"013-tick-routing: catch-up round $((r + 1)) recorded: conflicts with main"* && $out == *"013-tick-routing: round $((r + 1)) sent to its worker"* && $(round "$F13" $((r + 1))) == "- [ ] Conflicts with main at $B1" && $(crew get 13 status) == addressing-feedback && $(resumes) == $((n + 1)) ]]'
check "a catch-up round resumes the worker's conversation, as any review round does" 'runs_on 13 sonnet && [[ $(last_launch) == "claude --resume $(crew get 13 worker_session) --bg New input for task 013-tick-routing ("* ]]'
setstate "done" null; hand_over 13
out=$(crew tick 2>/dev/null); byhand=$(crew catch-up 13); code=$?
check "a second tick on the same base commit records nothing, and crew catch-up by hand says why and exits 3" '[[ -z $out && $byhand == "nothing to do: the conflict with main at $B1 is reported already" && $code == 3 && $(crew get 13 review_rounds) == $((r + 1)) ]]'
echo UNKNOWN >"$HOME/gh-pr-mergeable"; B2=$(base_moves)
out=$(crew tick 2>/dev/null); byhand=$(crew catch-up 13)
check "while GitHub is still working out whether the PR conflicts, sync records it and crew leaves it alone" '[[ $(crew get 13 mergeable) == unknown && -z $out && $byhand == "nothing to do: GitHub hasn'"'"'t worked out yet whether PR #12 conflicts with main" && $(crew get 13 review_rounds) == $((r + 1)) ]]'
echo CONFLICTING >"$HOME/gh-pr-mergeable"; echo agent/elsewhere >"$HOME/gh-pr-base-12"; setstate working 4242   # busy, so no retarget goes out
out=$(crew tick 2>/dev/null); byhand=$(crew catch-up 13)
check "a PR whose GitHub base isn't the task's base gets no conflict round, only its retarget, which waits for the busy worker" '[[ $(crew get 13 mergeable) == conflicting && $out == "013-tick-routing: restack (PR targets agent/elsewhere, not main) waiting for its worker (working)" && $byhand == "nothing to do: PR #12 targets agent/elsewhere, not the task'"'"'s base main" && $(crew get 13 review_rounds) == $((r + 1)) ]] && ! grep -q "at $B2" "$F13"'
crew set 13 base agent/season-model; echo agent/season-model >"$HOME/gh-pr-base-12"
out=$(crew tick 2>/dev/null); byhand=$(crew catch-up 13)
check "a stacked task's conflict is left to a restack, by tick and by hand" '[[ $out != *onflict* && $byhand == "nothing to do: 013-tick-routing is stacked on 009-season-model, so its conflicts are a restack'"'"'s (crew restack)" && $(crew get 13 review_rounds) == $((r + 1)) ]] && ! grep -q "at $B2" "$F13"'
crew set 13 base main; echo main >"$HOME/gh-pr-base-12"; echo CLOSED >"$HOME/gh-pr-state"
out=$(crew tick 2>/dev/null); byhand=$(crew catch-up 13)
check "a conflict on a PR closed without merging is left alone" '[[ $out != *onflict* && $byhand == "nothing to do: PR #12 is closed" && $(crew get 13 review_rounds) == $((r + 1)) ]]'
echo OPEN >"$HOME/gh-pr-state"; setstate "done" null; n=$(resumes)
out=$(crew tick 2>/dev/null)
check "a new commit on the base that still conflicts gets a round of its own" '[[ $out == *"013-tick-routing: catch-up round $((r + 2)) recorded: conflicts with main"* && $(round "$F13" $((r + 2))) == "- [ ] Conflicts with main at $B2" && $(resumes) == $((n + 1)) ]]'
rm "$HOME/gh-pr-mergeable" "$HOME/gh-pr-base-12"; crew sync >/dev/null 2>&1
check "sync clears mergeable once the PR merges cleanly again" '[[ -z $(crew get 13 mergeable) ]]'
setstate "done" null; hand_over 13
out=$(flock "$L/.crew-tick.lock" crew tick)
check "a second tick doesn't run while one is running" '[[ $out == "another crew tick is running" ]]'
check "launched sessions never inherit crew's lock fds" '! grep -q "LEAK LOCK FD" "$HOME/claude-calls.log"'
mkdir -p "$L/projects/zz-other"; printf 'REMOTE="%s"\nDEFAULT_BRANCH="main"\n' "$SB/other.git" >"$L/projects/zz-other/project.env"
out=$(crew tick 2>&1 >/dev/null)
check "tick skips a project with no clone here, rather than using the previous project's" '[[ $out == *"zz-other: skipped"* && $out == *"no clone of zz-other on this machine"* ]]'
rm -r "$L/projects/zz-other"
# Two followups back to back: the second comes while the worker's resume from the first
# is under way, its process up but its prompt not taken yet (see the stub).
s13=$(crew get 13 worker_session); j13=$(crew get 13 worker_job); r=$(crew get 13 review_rounds)
setstate "done" null; transcript "$s13" ended; n=$(resumes)
crew followup 13 --note "first" >/dev/null 2>&1
out=$(crew followup 13 --note "second" 2>&1)
check "a followup note sent while the worker's resume is under way is refused as busy, without a copy" \
  '[[ $out == *"worker $j13 is busy (working). Wait for it"* && $(resumes) == $((n + 1)) && $(crew get 13 worker_job) == "$j13" && $(crew get 13 worker_session) == "$s13" ]]'
hand_over 13; crew set 13 feedback_since 1970-01-01T00:00:00Z
out=$(crew tick 2>&1)
check "a review round recorded while it's under way waits for crew tick" \
  '[[ $out == *"013-tick-routing: round $((r + 1)) waiting for its worker (working)"* && $(crew get 13 last_round_sent) == "$r" && $(resumes) == $((n + 1)) ]]'
transcript "$s13" ended; setstate "done" 4242
out=$(crew tick 2>&1)
check "crew tick sends that round once the worker's turn has ended, to the same session" \
  '[[ $out == *"013-tick-routing: round $((r + 1)) sent to its worker"* && $(crew get 13 last_round_sent) == $((r + 1)) && $(resumes) == $((n + 2)) && $(crew get 13 worker_job) == "$j13" && $(crew get 13 worker_session) == "$s13" ]]'
# An idle worker whose process won't close gets nothing, and no copy, until it does.
hand_over 13; crew set 13 feedback_since 1970-01-01T00:00:00Z; transcript "$s13" ended; setstate "done" 4242; touch "$HOME/stop-noop"
out=$(crew tick 2>&1)
check "crew tick leaves a round unsent for an idle worker whose process won't close, starting no copy, and names the job" \
  '[[ $out == *"013-tick-routing: review round $((r + 2)) recorded"* && $out == *"job $j13 is still running, so crew sent it nothing"* && $(crew get 13 last_round_sent) == $((r + 1)) && $(resumes) == $((n + 2)) && $(crew get 13 worker_session) == "$s13" ]]'
rm -f "$HOME/stop-noop"
out=$(crew tick 2>&1)
check "a later crew tick sends the round once the process closes" \
  '[[ $out == *"013-tick-routing: round $((r + 2)) sent to its worker"* && $(crew get 13 last_round_sent) == $((r + 2)) && $(resumes) == $((n + 3)) && $(crew get 13 worker_session) == "$s13" ]]'
rm -f "$HOME/.claude/projects/stub/$s13.jsonl"

# Nudges: crew tick resumes, once, a worker whose turn ended while its task still says
# it's being worked on, and asks the lead when that doesn't move the task. Task 13's
# review rounds warn on every tick, so these checks read only what tick prints.
WT13=$(crew get 13 worktree); STUB=$HOME/.claude/projects/stub
NUDGE="Your last turn ended while this task's status still says you're working on it. If you're waiting on an answer, record it under Open questions and set the task's status to needs-input. If you started a sub-agent or a background command, check whether it has finished before you rely on its result. Then carry on from your last step."
head13() { git -C "$WT13" rev-parse HEAD | cut -c1-7; }
answer13() { sed -i '/^## Open questions$/,/^## /s/^- \[ \]/- [x]/' "$F13"; }
asked13() { section "$F13" "## Open questions" | grep '^- \[ \]'; }
quiet() { touch -d '20 minutes ago' "$STUB/$(crew get "$1" worker_session).jsonl"; }  # task: its transcript, 20 minutes ago
stall() {  # task state pid [status]: the task's worker so, its last turn ended 20 minutes ago
  local t=$1; shift; setstate "$@"; transcript "$(crew get "$t" worker_session)" ended; quiet "$t"; }
stall13() { stall 13 "$@"; }
nudged13() { [[ $(tail -1 "$F13") == *" — nudged: turn ended mid-task at $(head13)" ]]; }
NUDGED="013-tick-routing: its worker's turn ended mid-task; nudged it"
ASKED="013-tick-routing: its worker stalled and can't be nudged, for the lead"
answer13; crew set 13 status working; stall13 "done" null; n=$(resumes)
out=$(crew tick 2>/dev/null)
check "tick nudges a working task whose worker's turn ended with no question asked, on the task's model, and logs the branch's head" \
  '[[ $out == "$NUDGED" && $(resumes) == $((n + 1)) && $(last_prompt) == *"$NUDGE"* && $(crew get 13 status) == working ]] && runs_on 13 sonnet && nudged13'
check "the nudge is crew's own word, not a note from the lead, and the Log doesn't repeat it" \
  '[[ $(last_prompt) != *"Note from the lead"* && $(tail -2 "$F13" | head -1) == *"followup (resume): worker "*", as its turn ended mid-task" ]]'
crew log 13 "Still on it."; crew set 13 status working; stall13 "done" null
out=$(crew tick 2>/dev/null); q=$(asked13)
check "a second stall with no new commit or change of status since the nudge resumes no one, though the worker logged and set the status it had, and asks the lead" \
  '[[ $out == "$ASKED$NL""013-tick-routing: waiting on you (1 open question(s))" && $(resumes) == $((n + 1)) && $(crew get 13 status) == needs-input && $(wc -l <<<"$q") == 1 ]]'
check "the question names the branch's head, and the transcript, which holds the last turn after its process exits" '[[ $q == *"still at $(head13)"* && $q == *"$STUB/$(crew get 13 worker_session).jsonl"* ]]'
out=$(crew tick 2>/dev/null)
check "a third tick does nothing more" '[[ -z $out && $(resumes) == $((n + 1)) && $(asked13 | wc -l) == 1 ]]'
answer13; crew set 13 status working; stall13 "done" null
out=$(crew tick 2>/dev/null)
check "a change of status since the nudge lets the next stall be nudged again" '[[ $out == "$NUDGED" && $(resumes) == $((n + 2)) ]] && nudged13'
work "$WT13" nudge1.txt; stall13 blocked 4242 busy
out=$(crew tick 2>/dev/null)
check "so does a new commit; and a blocked worker is stalled too" '[[ $out == "$NUDGED" && $(resumes) == $((n + 3)) ]] && nudged13'
work "$WT13" nudge2.txt; stall13 working 4242 busy
out=$(crew tick 2>/dev/null)
check "a working worker whose turn ended is stalled, with a background task running" '[[ $out == "$NUDGED" && $(resumes) == $((n + 4)) ]] && nudged13'
work "$WT13" nudge3.txt; stall13 working null
out=$(crew tick 2>/dev/null)
check "and with its process gone" '[[ $out == "$NUDGED" && $(resumes) == $((n + 5)) ]] && nudged13'
work "$WT13" nudge4.txt; crew set 13 status addressing-feedback; stall13 "done" null
out=$(crew tick 2>/dev/null)
check "a nudged addressing-feedback task stays addressing-feedback" '[[ $out == "$NUDGED" && $(resumes) == $((n + 6)) && $(crew get 13 status) == addressing-feedback ]]'
work "$WT13" nudge5.txt; crew set 13 status working; n=$(resumes)
stall13 "done" null; touch "$STUB/$(crew get 13 worker_session).jsonl"
out=$(crew tick 2>/dev/null)
check "nothing is nudged while the worker's transcript changed in the last 8 minutes" '[[ -z $out && $(resumes) == "$n" ]]'
touch -d '7 minutes ago' "$STUB/$(crew get 13 worker_session).jsonl"
out=$(crew tick 2>/dev/null)
check "nothing is nudged after 7 quiet minutes" '[[ -z $out && $(resumes) == "$n" ]]'
touch -d '9 minutes ago' "$STUB/$(crew get 13 worker_session).jsonl"
out=$(crew tick 2>/dev/null)
check "a worker quiet for 9 minutes is nudged" '[[ $out == "$NUDGED" && $(resumes) == $((n + 1)) ]] && nudged13'
n=$(resumes); work "$WT13" nudge6.txt  # a new commit, so the nudge above no longer blocks the checks below
stall13 working 4242 busy; transcript "$(crew get 13 worker_session)" running; quiet 13
out=$(crew tick 2>/dev/null)
check "or while its turn is still running" '[[ -z $out && $(resumes) == "$n" ]]'
for state in stopped gone; do
  stall13 "done" null
  if [[ $state == stopped ]]; then setstate stopped null; else cp "$HOME/agents.json" "$HOME/agents.kept"; jq --arg j "$(crew get 13 worker_job)" 'map(select(.id != $j))' "$HOME/agents.kept" >"$HOME/agents.json"; fi
  out=$(crew tick 2>/dev/null)
  check "nothing is nudged when the worker is ${state/gone/not listed}" '[[ -z $out && $(resumes) == "$n" ]]'
done
mv "$HOME/agents.kept" "$HOME/agents.json"
crew pause --project "$(basename "$PD")" >/dev/null 2>&1; stall13 "done" null
out=$(crew tick 2>/dev/null); byhand=$(crew nudge 13 2>&1); st=$?
check "nothing is nudged while the project is paused, by tick or by hand; tick holds it back, saying why" '[[ $out == "013-tick-routing: a nudge waits: $(basename "$PD") is paused" && $byhand == *"crew resume --project $(basename "$PD")"* && $st == 4 && $(resumes) == "$n" ]]'
check "a lead hears the nudge a pause holds back" '[[ $(prompt held) == *"$HELD"*"$NL- $PJ: 013-tick-routing: a nudge waits: $PJ is paused"* ]]'
crew resume --project "$(basename "$PD")" >/dev/null
sed -i '/^## Open questions$/a - [ ] Which way? A or B.' "$F13"
out=$(crew tick 2>/dev/null)
check "nothing is nudged when the task has an unchecked Open question: it waits on you" '[[ $out == "013-tick-routing: waiting on you (1 open question(s))" && $(resumes) == "$n" ]]'
answer13; out=$(crew tick 2>/dev/null)
check "nor while it's needs-input" '[[ -z $out && $(resumes) == "$n" ]]'
record_evidence 13; hand_over 13; out=$(crew tick 2>/dev/null)
check "nor once it's pr-open" '[[ -z $out && $(resumes) == "$n" ]]'
# The user takes a worker's split: the lead relays it, and the worker leaves the task to
# the lead, as implementer.md's "split as proposed" says.
crew set 13 status working; crew followup 13 --note "Split as proposed." >/dev/null 2>&1; n=$(resumes)
sed -i '/^## Open questions$/a - [ ] Split as proposed: open the pieces as tasks and abandon this one.' "$F13"; crew set 13 status needs-input; stall13 "done" null
out=$(crew tick 2>/dev/null)
check "a worker whose split the user took isn't told to carry on" '[[ -z $out && $(resumes) == "$n" && $(crew get 13 status) == needs-input ]]'
answer13; crew set 13 status working; work "$WT13" nudge6.txt; stall13 "done" null
jq 'map(.state="working" | .pid=4242 | .status="waiting")' "$HOME/agents.json" >"$HOME/agents-next"
out=$(crew tick 2>/dev/null); byhand=$(crew nudge 13 2>&1); st=$?
check "no nudge reaches a worker that's busy again by the time it would be sent, from tick or by hand" \
  '[[ -z $out && $byhand == "nothing to do: 013-tick-routing hasn'"'"'t stalled" && $st == 3 && $(resumes) == "$n" ]] && ! nudged13'
rm -f "$HOME/agents-next"; stall13 "done" null; mv "$WT13" "$WT13.away"
out=$(crew tick 2>/dev/null); q=$(asked13)
check "a stalled task whose worktree isn't on this machine isn't nudged, gets no worktree, and the lead is asked how to bring a fresh worker" \
  '[[ $out == "$ASKED$NL""013-tick-routing: waiting on you (1 open question(s))" && $(resumes) == "$n" && ! -e $WT13 && $q == *"$WT13, isn'"'"'t on this machine"*"crew followup 013-tick-routing --fresh"* && $(crew get 13 status) == needs-input ]]'
mv "$WT13.away" "$WT13"; answer13; rm -f "$STUB/$(crew get 13 worker_session).jsonl"

# Restacks: 15 is stacked on 14; 14's branch moves.
F14=$(crew new "Tick parent" --branch tick-parent); fill "$F14"; crew ready 14 >/dev/null
crew set 13 status abandoned; setstate "done" null; crew dispatch 14 >/dev/null; WT14=$(crew get 14 worktree)
work "$WT14" p1.txt; crew set 14 pr https://github.com/isaac/webapp/pull/31; hand_over 14
F15=$(crew new "Tick child" --branch tick-child --on 14); fill "$F15"; crew ready 15 >/dev/null
crew dispatch 15 >/dev/null; WT15=$(crew get 15 worktree)
work "$WT15" c1.txt; crew set 15 pr https://github.com/isaac/webapp/pull/32; hand_over 15
work "$WT14" p2.txt; setstate "done" null; n=$(launches)
out=$(crew tick)
check "tick restacks a stacked task whose parent moved, in a fresh worker on Sonnet" '[[ $out == "015-tick-child: restack sent to its worker (014-tick-parent moved)" && $(last_launch) == *"git rebase --onto $(tip tick-parent)"* && $(launches) == $((n + 1)) ]] && restacked 15'
hand_over 15; setstate "done" null
out=$(crew tick 2>&1)
check "tick doesn't send the same restack again when the worker didn't manage it" '[[ -z $out && $(launches) == $((n + 1)) ]]'
# A restack by hand sends the restack alone: a round, or word of a pause, waits for the next tick.
r15=$(crew get 15 review_rounds); crew set 15 review_rounds $((r15 + 1)); n=$(launches); crew restack 15 --again >/dev/null
check "a restack by hand holds back a review round its worker hasn't been sent" \
  '[[ $(launches) == $((n + 1)) && $(last_launch) == *"Restack (014-tick-parent moved)"* && $(last_launch) != *"review feedback"* && $(crew get 15 last_round_sent) == "$r15" ]] && restacked 15'
setstate "done" null; out=$(crew tick)
check "and the next tick sends that round to the worker, afresh on the task's model" \
  '[[ $out == "015-tick-child: round $((r15 + 1)) sent to its worker" && $(last_launch) == *"New review feedback is in round $((r15 + 1))"* && $(last_launch) != *--resume* && $(last_launch) != *--model* && $(crew get 15 last_round_sent) == $((r15 + 1)) ]]'
hand_over 15; crew set 15 interrupted 2026-11-20T10:00:00Z; setstate "done" null; n=$(launches); crew restack 15 --again >/dev/null
check "a restack by hand holds back word of a pause" \
  '[[ $(launches) == $((n + 1)) && $(last_launch) == *"Restack (014-tick-parent moved)"* && $(last_launch) != *"crew was paused"* && -n $(crew get 15 interrupted) ]] && restacked 15'
setstate "done" null; out=$(crew tick)
check "and the next tick tells the worker to carry on after the pause" \
  '[[ $out == "015-tick-child: its worker carries on after the pause" && $(last_launch) == *"crew was paused while you were working"* && -z $(crew get 15 interrupted) ]]'
hand_over 15; n=$(launches)
# 14 is abandoned after 15's restack onto it was sent, and its branch stays where it was.
crew set 14 status abandoned >/dev/null; setstate "done" null
out=$(crew tick 2>&1)
check "tick holds back the restack of a task whose parent was abandoned since its last restack, saying why" \
  '[[ $out == *"015-tick-child: restack held back: 014-tick-parent was abandoned; give it another base with '"'"'crew set 015-tick-child base <branch>'"'"'"* && $(launches) == "$n" ]]'
crew set 14 status working >/dev/null; record_evidence 14; hand_over 14

# Retargets: 15 moves onto 16, a new task stacked on 14 that holds all of 14's work, as a split's last
# piece can. 15 restacks onto 14 first, so its branch already holds 16's.
(cd "$WT15" && git rebase -q --onto "$(tip tick-parent)" "$(crew get 15 base_sha)") && restack_push "$WT15"
FR=$(crew new "Tick replacement" --branch tick-replacement --on 14); fill "$FR"; crew ready "$FR" >/dev/null
crew set 15 base agent/tick-replacement; rs15=$(crew get 15 restack_sent)
echo agent/tick-parent >"$HOME/gh-pr-base-32"
out=$(crew sync)
check "sync records the branch each PR targets on GitHub" '[[ $(crew get 15 pr_base) == agent/tick-parent ]]'
check "a PR isn't reported for a retarget while its task's base isn't on origin" '[[ $out != *"PR targets"* ]]'
setstate "done" null; crew dispatch "$FR" >/dev/null; (cd "$(crew get "$FR" worktree)" && git push -q -u origin HEAD 2>/dev/null)
crew set "$FR" pr https://github.com/isaac/webapp/pull/33; hand_over "$FR"
echo agent/tick-old >"$HOME/gh-pr-base-31"
out=$(crew sync)
check "sync reports each PR that targets a branch other than its task's base, stacked or not" \
  '[[ $out == *"015-tick-child: PR targets agent/tick-parent, not agent/tick-replacement (run '"'"'crew restack 015-tick-child'"'"')"* && $out == *"014-tick-parent: PR targets agent/tick-old, not main"* && $out != *"needs a restack"* ]]'
check "status shows it" '[[ $(crew status) == *"PR targets agent/tick-parent, not agent/tick-replacement"* ]]'
setstate "done" null; n=$(launches)
out=$(crew tick)
check "with nothing to rebase, tick sends a retarget alone, to a stacked task and an unstacked one, each in a fresh worker on Sonnet" \
  '[[ $out == *"015-tick-child: restack sent to its worker (PR targets agent/tick-parent, not agent/tick-replacement)"* && $out == *"014-tick-parent: restack sent to its worker (PR targets agent/tick-old, not main)"* && $(launches) == $((n + 2)) ]] && restacked 14 && restacked 15'
p15=$(prompt_of 015-tick-child)
check "a retarget note asks for gh pr edit --base and no rebase or push, and is recorded apart from restacks" \
  '[[ $p15 == *"Retarget (PR targets agent/tick-parent, not agent/tick-replacement)"*"\`gh pr edit --base agent/tick-replacement\`"* && $p15 != *"git rebase"* && $p15 != *"git push"* && $(crew get 15 retarget_sent) == agent/tick-replacement && $(crew get 15 restack_sent) == "$rs15" ]]'
record_evidence 14; hand_over 14; hand_over 15; setstate "done" null
out=$(crew tick 2>&1)
check "tick doesn't send the same retarget again when the worker didn't manage it" '[[ -z $out && $(launches) == $((n + 2)) ]]'
check "and crew restack won't without --again" '[[ $(crew restack 15 2>&1) == *"already asked to retarget its PR to agent/tick-replacement"* && $(launches) == $((n + 2)) ]]'
setstate "done" null; crew restack 15 --again >/dev/null
check "a retarget by hand starts a fresh worker on Sonnet too" '[[ $(launches) == $((n + 3)) && $(last_launch) == *"Retarget (PR targets agent/tick-parent"* ]] && restacked 15'
hand_over 15
# 14's worker retargeted its PR. 16 moves, and its own PR targets the wrong branch.
echo main >"$HOME/gh-pr-base-31"; echo main >"$HOME/gh-pr-base-33"; work "$(crew get "$FR" worktree)" r1.txt
setstate "done" null; out=$(crew tick)
check "a parent whose PR targets the wrong branch doesn't hold back its child's rebase" \
  '[[ $out == *"016-tick-replacement: restack sent to its worker (PR targets main, not agent/tick-parent)"* && $out == *"015-tick-child: restack sent to its worker (016-tick-replacement moved)"* && $out != *014-tick-parent* ]]'
p15=$(prompt_of 015-tick-child)
check "a rebase note asks for the retarget still due, after the push" \
  '[[ $p15 == *"git rebase --onto $(tip tick-replacement) "*"Your PR targets agent/tick-parent, not your base: after pushing, run \`gh pr edit --base agent/tick-replacement\`."* ]]'
echo agent/tick-replacement >"$HOME/gh-pr-base-32"; echo agent/tick-parent >"$HOME/gh-pr-base-33"
check "once a PR targets its task's base, nothing says otherwise" 'crew sync >/dev/null && [[ $(crew status) != *"PR targets"* ]]'
rm -f "$HOME"/gh-pr-base-*

# 14 and 16 move while 16's worker is still at work on it: 16's restack waits for it to be
# pr-open, and 15's waits for 16's.
work "$WT14" p3.txt; work "$(crew get "$FR" worktree)" r2.txt; crew set "$FR" status working; crew sync >/dev/null; setstate "done" null; n=$(launches)
out=$(crew tick 2>&1)
check "tick holds back the restack of a task that isn't pr-open, saying why" \
  '[[ $out == *"016-tick-replacement: restack (014-tick-parent moved) waits until the task is pr-open; it'"'"'s working"* && $(launches) == "$n" ]]'
check "and a child's restack until its parent's, saying why" '[[ $out == *"015-tick-child: restack (016-tick-replacement moved) waits for 016-tick-replacement'"'"'s own restack"* ]]'
out=$(prompt held)
check "a lead hears each restack held back, and why" \
  '[[ $out == *"$HELD"*"$NL- $PJ: 016-tick-replacement: restack (014-tick-parent moved) waits until the task is pr-open; it'"'"'s working"* && $out == *"$NL- $PJ: 015-tick-child: restack (016-tick-replacement moved) waits for 016-tick-replacement'"'"'s own restack"* ]]'
crew tick >/dev/null 2>&1
check "and isn't told again while the same reason holds them back" '[[ $(prompt held) != *"$HELD"* ]]'
echo MERGED >"$HOME/gh-pr-state"; setstate "done" null; touch "$HOME/rm-fail"
out=$(crew tick 2>&1)
check "tick cleans up merged tasks" '[[ $out == *"014-tick-parent: merged; worktree cleaned up"* && ! -d $WT14 && $(crew get 14 status) == merged ]]'
check "a worker that won't leave agent view is reported, not a reason to keep the worktree" '[[ $out == *"couldn'"'"'t remove job $(crew get 14 worker_job) from agent view"* ]]'
head=$(git -C "$L" rev-parse @); out=$(crew restack 14 2>&1); st=$?
check "crew restack refuses a merged task before bringing back its worktree" '((st == 1)) && [[ $out == *"014-tick-parent is merged"* && ! -d $WT14 && -z $(crew get 14 worktree) && $(git -C "$L" rev-parse @) == "$head" ]]'
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
# A forced cleanup reports a worktree it leaves, and changes nothing for a task with none.
LEFT=$(crew new "Leftover directory" --branch leftover-dir); fill "$LEFT"; crew ready "$LEFT" >/dev/null
WLEFT="$HOME/agent-work/$(basename "$PD")/$(basename "$LEFT" .md)"
snapshot() { cksum <"$LEFT"; ls -R "$HOME/agent-work/$(basename "$PD")" | cksum; git -C "$SB/webapp" branch --list | cksum; }
before=$(snapshot)
out=$(crew cleanup "$LEFT" --force 2>&1); st=$?
check "a forced cleanup of a ready task with no worktree anywhere changes nothing and logs nothing" \
  '((st == 0)) && [[ $out != *"cleaned up"* && $before == $(snapshot) ]]'
mkdir -p "$WLEFT"
out=$(crew cleanup "$LEFT" --force 2>&1); st=$?
check "a forced cleanup doesn't report cleaning up while something that isn't a worktree is at the task's path" \
  '((st == 4)) && [[ $out == *"$(basename "$LEFT" .md): worktree stays: "* && $out != *"cleaned up"* && -d $WLEFT ]] && ! grep -q "cleaned up worktree" "$LEFT"'
rmdir "$WLEFT"
# A leftover whose branch has commits of its own keeps the branch, and one reached by two spellings of its path is removed once.
KEEP=$(crew new "Leftover with work" --branch leftover-work); fill "$KEEP"; crew ready "$KEEP" >/dev/null; setstate "done" null; crew dispatch "$KEEP" >/dev/null
WKEEP=$(crew get "$KEEP" worktree); (cd "$WKEEP" && echo x >w.txt && git add w.txt && git commit -qm "work")
crew set "$KEEP" worktree "" >/dev/null; crew set "$KEEP" status abandoned >/dev/null
out=$(crew cleanup "$KEEP" --force 2>&1)
check "a forced cleanup of a leftover removes it but keeps a branch that has commits of its own" \
  '[[ $out == *"cleaned up"* && ! -d $WKEEP ]] && git -C "$SB/webapp" show-ref --verify --quiet refs/heads/agent/leftover-work'
ALIAS=$(crew new "Aliased path" --branch aliased-path); fill "$ALIAS"; crew ready "$ALIAS" >/dev/null; setstate "done" null; crew dispatch "$ALIAS" >/dev/null
WALIAS=$(crew get "$ALIAS" worktree); ln -s "$WALIAS" "$SB/alias-link"
crew set "$ALIAS" worktree "$SB/alias-link" >/dev/null; crew set "$ALIAS" status abandoned >/dev/null
out=$(crew cleanup "$ALIAS" --force 2>&1); st=$?
check "a forced cleanup reached by two spellings of one worktree path removes it once and succeeds" \
  '((st == 0)) && [[ $out == *"cleaned up"* && ! -d $WALIAS && -z $(crew get "$ALIAS" worktree) ]]'
rm -f "$SB/alias-link"
# A PR's new base that only another clone has pushed, with no stack in flight for sync to fetch
# for (12 steps aside): sync fetches for the PR itself.
crew set 12 status abandoned >/dev/null
FB=$(crew new "Moved base" --branch moved-base); fill "$FB"; crew ready "$FB" >/dev/null; setstate "done" null; crew dispatch "$FB" >/dev/null
work "$(crew get "$FB" worktree)" b.txt; crew set "$FB" pr https://github.com/isaac/webapp/pull/34; hand_over "$FB"
(cd "$SB/other-clone" && git fetch -q origin && git push -q origin origin/main:refs/heads/release/next)
crew set "$FB" base release/next; echo main >"$HOME/gh-pr-base-34"
out=$(crew sync)
check "sync fetches a new base that a PR doesn't target yet, for a task that isn't stacked" '[[ $out == *"$(basename "$FB" .md): PR targets main, not release/next"* ]]'
# Moved onto a branch cut before it started, as when a task leaves an abandoned parent for main,
# its branch still holds commits that aren't on the new base: no retarget alone.
(cd "$SB/other-clone" && git push -q origin origin/main~1:refs/heads/release/old)
crew set "$FB" base release/old; crew sync >/dev/null; setstate "done" null; n=$(resumes)
out=$(crew tick 2>&1)
check "tick sends no retarget alone to an unstacked task whose branch doesn't start on its new base" '[[ $out != *"$(basename "$FB" .md): restack sent"* && $(resumes) == "$n" ]]'
out=$(crew restack "$FB" 2>&1); st=$?
check "and crew restack refuses it, saying why" '((st == 1)) && [[ $out == *"branch doesn'"'"'t start on release/old"*"crew followup $(basename "$FB" .md) --note"* && $(resumes) == "$n" ]]'
rm -f "$HOME/gh-pr-base-34"; crew set "$FB" status abandoned >/dev/null
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
crew set "$F19" pr https://github.com/isaac/webapp/pull/12; hand_over "$F19"; crew set "$F19" feedback_since 2099-01-01T00:00:00Z
moved_away "$F19"; setstate "done" null; n=$(fresh_launches)
out=$(crew tick 2>&1)
check "tick leaves a moved task with an open PR alone while nothing waits for its worker" '[[ $out != *"$(basename "$F19" .md)"* && ! -d $WT19 && $(fresh_launches) == "$n" ]]'
crew set "$F19" feedback_since 1970-01-01T00:00:00Z
out=$(crew tick 2>&1)
check "when a round arrives, its followup makes the worktree here" '[[ $out == *"$(basename "$F19" .md): round 1 sent to its worker"* && -d $WT19 && $(git -C "$WT19" branch --show-current) == agent/pr-moved ]]'
crew set "$F19" status abandoned >/dev/null
rm -f "$HOME/gh-pr-state"

# First runs whose parent moves: crew hand-over refuses a stacked task's first hand-over off its
# parent's latest commit and gives the task's own worker the rebase, which the worker runs. Each
# test worker below does only what implementer.md's first-run steps and crew's messages say.
echo OPEN >"$HOME/gh-pr-state"
tid() { basename "$1" .md; }
own() { git rev-list "$1..$(tip "$2")"; }  # parent-tip branch: the commits the branch adds to it
run_given() {  # worktree message: what a worker does with commands crew gives it: runs each, in order, as written
  local cmd
  while IFS= read -r cmd; do (cd "$1" && bash -c "$cmd" >/dev/null 2>&1) || return 1; done < <(grep -o '`[^`]*`' <<<"$2" | tr -d '`')
}
ranged_review() {  # task n: an automated review as the pr-review skill records it, from the fork point on the base on origin
  local b base fork head; b=$(crew get "$1" branch); base=$(crew get "$1" base)
  fork=$(git merge-base "origin/$base" "refs/heads/$b"); head=$(git rev-parse "refs/heads/$b")
  review "$(crew path "$1")" "$2" "- Range: \`$fork..$head\`, $(git rev-list --count "$fork..$head") commits on \`$b\` since \`$base\` (fork point \`$fork\`)"
}
first_run() {  # task pr: a first run up to its hand-over: work pushed, a PR, evidence, and a pass of automated review
  work "$(crew get "$1" worktree)" "$(tid "$1").txt"
  [[ -z $2 ]] || crew set "$1" pr "https://github.com/isaac/webapp/pull/$2"
  record_evidence "$1"; [[ -z $2 ]] || pr_body "$1"; ranged_review "$1" 1
}
restacks_of() { grep -c "You are restacking task $(tid "$1") " "$HOME/claude-calls.log"; }
resumes_of() { grep -c -- "--resume $(crew get "$1" worker_session)" "$HOME/claude-calls.log"; }
STALE="no longer covers your branch"
FP=$(crew new "First-run parent" --branch first-parent); fill "$FP"; crew ready "$FP" >/dev/null; setstate "done" null; crew dispatch "$FP" >/dev/null
WTP=$(crew get "$FP" worktree); work "$WTP" p1.txt; crew set "$FP" pr https://github.com/isaac/webapp/pull/61; hand_over "$FP"

# The parent moves while the child's worker is idle before its hand-over, with a file it hasn't committed.
FA=$(crew new "Idle when its parent moved" --branch idle-child --on "$(tid "$FP")"); fill "$FA"; crew ready "$FA" >/dev/null
crew set "$FA" model opus; setstate "done" null; crew dispatch "$FA" >/dev/null; WTA=$(crew get "$FA" worktree); sA=$(crew get "$FA" worker_session)
first_run "$FA" 62; echo draft >"$WTA/notes.txt"
work "$WTP" p2.txt; out=$(crew sync)
check "sync says a child's worker rebases before its first hand-over" '[[ $out == *"$(tid "$FA"): needs a restack, $(tid "$FP") moved (its own worker moves its branch and PR before its first hand-over; crew check-base and crew hand-over tell it how)"* ]]'
stall "$FA" "done" null; n=$(fresh_launches)
out=$(crew tick 2>&1)
check "tick nudges a first run that went idle after its parent moved, in its own session, and sends no restack" \
  '[[ $out == *"$(tid "$FA"): its worker'"'"'s turn ended mid-task; nudged it"* && $(last_prompt) == *"$NUDGE"* && $(fresh_launches) == "$n" && $(restacks_of "$FA") == 0 ]] && runs_on "$FA" opus'
out=$(crew hand-over "$FA" 2>&1); st=$?
check "a first hand-over off the parent's latest commit is refused with the rebase onto it, the cut and the lease" \
  '((st == 1)) && [[ $out == *"your branch has to move before its first hand-over ($(tid "$FP") moved): rebase your branch with \`git fetch origin && git rebase --onto $(tip first-parent) $(crew get "$FA" base_sha)\`, then follow \"Stacked tasks\" in your instructions."*"\`git push --force-with-lease=agent/idle-child:$(git rev-parse origin/agent/idle-child) origin agent/idle-child\`"* && $out != *"base main"* && $(crew get "$FA" status) == working ]]'
bs=$(crew get "$FA" base_sha); run_given "$WTA" "$out"; out=$(crew hand-over "$FA" 2>&1); st=$?
check "after the rebase, hand-over refuses the automated review that read the parent's old commits" \
  '((st == 1)) && [[ $out == *"automated review 1 $STALE: it read from $(cut -c1-12 <<<"$bs"), and your branch now leaves origin/agent/first-parent at $(tip first-parent | cut -c1-12). Run a pass of automated review, then hand over again"* && $(crew get "$FA" status) == working ]]'
ranged_review "$FA" 2; out=$(crew hand-over "$FA" 2>&1); st=$?
check "and hands over once a review has read the rebased branch: on the parent's tip, with only the child's commits" \
  '((st == 0)) && [[ $(crew get "$FA" status) == pr-open && $(git rev-parse origin/agent/idle-child) == $(tip idle-child) && $(git log --format=%s "$(own "$(tip first-parent)" idle-child)" -1) == "add $(tid "$FA").txt" && $(own "$(tip first-parent)" idle-child | wc -l) == 1 ]]'
check "the first run carried on in its own session: no restack, no followup, no question, and its uncommitted file is still in its worktree" \
  '[[ $(crew get "$FA" worker_session) == "$sA" && $(restacks_of "$FA") == 0 && $(resumes_of "$FA") == 1 && -z $(crew get "$FA" restack_sent) && $(git -C "$WTA" status --porcelain) == "?? notes.txt" ]] && ! section "$FA" "## Open questions" | grep -q "^- \["'
setstate "done" null; out=$(crew tick 2>&1)
check "and once it's handed over on the parent's tip, tick has no restack to send it" '[[ $out != *"$(tid "$FA"):"*restack* && $(restacks_of "$FA") == 0 ]]'
crew set "$FA" status abandoned >/dev/null

# The parent moves while one child's turn runs straight to its hand-over, and while another waits on an answer.
FB=$(crew new "Busy when its parent moved" --branch busy-child --on "$(tid "$FP")"); fill "$FB"; crew ready "$FB" >/dev/null; setstate "done" null; crew dispatch "$FB" >/dev/null
FE=$(crew new "Asking when its parent moved" --branch asking-child --on "$(tid "$FP")"); fill "$FE"; crew ready "$FE" >/dev/null; setstate "done" null; crew dispatch "$FE" >/dev/null
first_run "$FB" 63; first_run "$FE" 66
sed -i '/^## Open questions$/a - [ ] Which way? A or B.' "$FE"; crew set "$FE" status needs-input
work "$WTP" p3.txt; setstate working 4242 busy; n=$(launches)
out=$(crew tick 2>&1)
check "tick sends a busy first run nothing when its parent moves, and says its worker rebases it" \
  '[[ $out == *"$(tid "$FB"): restack ($(tid "$FP") moved) isn'"'"'t crew'"'"'s to send: its own worker moves its branch and PR before its first hand-over; crew check-base and crew hand-over tell it how"* && $(launches) == "$n" ]]'
refused=0
for _ in 1 2; do [[ $(crew hand-over "$FB" 2>&1) == *"your branch has to move before its first hand-over"* && $(crew get "$FB" status) == working ]] && refused=$((refused + 1)); done
check "a worker that hands over again without acting on the move is refused again, and the task stays off pr-open" '((refused == 2)) && ! git merge-base --is-ancestor "$(tip first-parent)" "$(tip busy-child)"'
out=$(crew hand-over "$FB" 2>&1); run_given "$(crew get "$FB" worktree)" "$out"
check "a hand-over refused for a stale review hands over after a new one" \
  '[[ $(crew hand-over "$FB" 2>&1) == *"automated review 1 $STALE"* ]] && ranged_review "$FB" 2 && crew hand-over "$FB" >/dev/null && [[ $(crew get "$FB" status) == pr-open && $(own "$(tip first-parent)" busy-child | wc -l) == 1 && $(restacks_of "$FB") == 0 && $(launches) == "$n" ]]'
setstate "done" null; out=$(crew tick 2>&1); byhand=$(crew restack "$FE" 2>&1); st=$?
check "a needs-input first run gets no restack from tick or by hand, which say who rebases it" \
  '[[ $out == *"$(tid "$FE"): restack ($(tid "$FP") moved) isn'"'"'t crew'"'"'s to send"* && $(restacks_of "$FE") == 0 && $st == 4 && $byhand == *"$(tid "$FE") hasn'"'"'t been handed over: its own worker moves its branch and PR before its first hand-over; crew check-base and crew hand-over tell it how"* ]]'
sed -i '/^## Open questions$/,/^## /s/^- \[ \]/- [x]/' "$FE"; n=$(fresh_launches); r=$(resumes_of "$FE")
crew followup "$FE" --note "A." >/dev/null 2>&1; crew set "$FE" status working
out=$(crew hand-over "$FE" 2>&1); run_given "$(crew get "$FE" worktree)" "$out"; ranged_review "$FE" 2
check "once answered, it reaches pr-open on the parent's tip with the answer's followup the only one sent" \
  'crew hand-over "$FE" >/dev/null && [[ $(crew get "$FE" status) == pr-open && $(own "$(tip first-parent)" asking-child | wc -l) == 1 && $(resumes_of "$FE") == $((r + 1)) && $(fresh_launches) == "$n" && $(restacks_of "$FE") == 0 ]]'
crew set "$FB" status abandoned >/dev/null; crew set "$FE" status abandoned >/dev/null

# The parent is rewritten, and then gets a new commit, with no crew sync in between.
FG=$(crew new "Moved twice before a sync" --branch twice-child --on "$(tid "$FP")"); fill "$FG"; crew ready "$FG" >/dev/null; setstate "done" null; crew dispatch "$FG" >/dev/null
first_run "$FG" 67; WTG=$(crew get "$FG" worktree)
(cd "$WTP" && git commit -q --amend -m "add p3.txt, reworded" && git push -q -f origin HEAD 2>/dev/null)
run_given "$WTG" "$(crew hand-over "$FG" 2>&1)"; work "$WTP" p4.txt; out=$(crew hand-over "$FG" 2>&1); run_given "$WTG" "$out"
check "a second rebase before a sync cuts where the first one left the branch, so the branch keeps only its own commits" \
  '[[ $out == *"git rebase --onto $(tip first-parent) $(git -C "$WTP" rev-parse HEAD~1)"* ]] && ranged_review "$FG" 2 && crew hand-over "$FG" >/dev/null && [[ $(crew get "$FG" status) == pr-open && $(own "$(tip first-parent)" twice-child | wc -l) == 1 ]]'
crew set "$FG" status abandoned >/dev/null

# A worker that checks its base before each costly step of its first run, as implementer.md says:
# the parent moves before its evidence and its review, and one review reads the branch it hands over.
FL=$(crew new "Checks its base" --branch checking-child --on "$(tid "$FP")"); fill "$FL"; crew ready "$FL" >/dev/null; setstate "done" null; crew dispatch "$FL" >/dev/null
WTL=$(crew get "$FL" worktree); work "$WTL" "$(tid "$FL").txt"; out=$(crew check-base "$FL" 2>&1); st=$?
check "check-base lets a first run whose branch holds its parent's latest commit carry on" '((st == 0)) && [[ $out == "nothing to move: carry on" ]]'
work "$WTP" p5.txt; out=$(crew check-base "$FL" 2>&1); st=$?
check "check-base gives a first run whose parent moved the rebase hand-over would give, and changes no status" \
  '((st == 1)) && [[ $out == *"your branch has to move before its first hand-over ($(tid "$FP") moved): rebase your branch with \`git fetch origin && git rebase --onto $(tip first-parent) $(crew get "$FL" base_sha)\`"*"\`git push --force-with-lease=agent/checking-child:$(git rev-parse origin/agent/checking-child) origin agent/checking-child\`"* && $(crew get "$FL" status) == working ]]'
run_given "$WTL" "$out"; crew check-base "$FL" >/dev/null && record_evidence "$FL"; crew set "$FL" pr https://github.com/isaac/webapp/pull/72; pr_body "$FL"
work "$WTP" p6.txt; out=$(crew check-base "$FL" 2>&1); run_given "$WTL" "$out"; crew check-base "$FL" >/dev/null && ranged_review "$FL" 1
check "with a check before its evidence and its review, it hands over after one review, on the parent's tip, with only its own commits" \
  'crew hand-over "$FL" >/dev/null && [[ $(crew get "$FL" status) == pr-open && $(grep -c "^### Automated review" "$FL") == 1 && $(own "$(tip first-parent)" checking-child | wc -l) == 1 && $(restacks_of "$FL") == 0 ]]'
out=$(crew check-base "$FL" 2>&1); st=$?
check "check-base leaves a task that has been handed over to crew's restacks" '((st == 1)) && [[ $out == *"$(tid "$FL") has been handed over: crew restacks it when its base moves"* ]]'
crew set "$FL" status abandoned >/dev/null

# The lead moves a first run onto another parent, a worker opens its PR on the wrong branch, and the
# lead moves a stacked first run to the default branch. crew sync reads none of their PRs.
edits() { grep -c "^gh pr edit --base $1\$" "$HOME/gh-calls.log"; }  # base: how many times a worker retargeted a PR to it
FR=$(crew new "Replacement parent" --branch replacement-parent --on "$(tid "$FP")"); fill "$FR"; crew ready "$FR" >/dev/null; setstate "done" null; crew dispatch "$FR" >/dev/null
FH=$(crew new "Moved to another parent" --branch moved-child --on "$(tid "$FP")"); fill "$FH"; crew ready "$FH" >/dev/null; setstate "done" null; crew dispatch "$FH" >/dev/null
first_run "$FH" 68; echo agent/first-parent >"$HOME/gh-pr-base-68"; work "$(crew get "$FR" worktree)" r1.txt
crew set "$FH" base agent/replacement-parent >/dev/null; e=$(edits agent/replacement-parent)
out=$(crew hand-over "$FH" 2>&1); run_given "$(crew get "$FH" worktree)" "$out"
(($(edits agent/replacement-parent) > e)) && echo agent/replacement-parent >"$HOME/gh-pr-base-68"  # GitHub moves the PR
check "a first run moved onto another parent gets the rebase onto it and its PR's retarget" \
  '[[ $out == *"your branch has to move before its first hand-over ($(tid "$FR") moved): rebase your branch with \`git fetch origin && git rebase --onto $(tip replacement-parent) "*"Your PR targets agent/first-parent, not your base: after pushing, run \`gh pr edit --base agent/replacement-parent\`."* && $(cat "$HOME/gh-pr-base-68") == agent/replacement-parent ]]'
check "and hands over on the new parent once a review has read it there, with only its own commits" \
  '[[ $(crew hand-over "$FH" 2>&1) == *"automated review 1 $STALE"* ]] && ranged_review "$FH" 2 && crew hand-over "$FH" >/dev/null && [[ $(crew get "$FH" status) == pr-open && $(own "$(tip replacement-parent)" moved-child | wc -l) == 1 ]]'
FI=$(crew new "PR on the wrong branch" --branch wrong-pr-base); fill "$FI"; crew ready "$FI" >/dev/null; setstate "done" null; crew dispatch "$FI" >/dev/null
first_run "$FI" 69; echo agent/first-parent >"$HOME/gh-pr-base-69"; e=$(edits main)
check "check-base gives a first run whose PR targets another branch the retarget, then has it carry on" \
  '[[ $(crew check-base "$FI" 2>&1) == *"your PR has to move before its first hand-over (PR targets agent/first-parent, not main): run \`gh pr edit --base main\`, then carry on" ]]'
out=$(crew hand-over "$FI" 2>&1); st=$?; run_given "$(crew get "$FI" worktree)" "$out"
(($(edits main) > e)) && echo main >"$HOME/gh-pr-base-69"
check "a first run whose PR targets another branch than its base gets the retarget alone" \
  '((st == 1)) && [[ $out == *"your PR has to move before its first hand-over (PR targets agent/first-parent, not main): run \`gh pr edit --base main\`, then hand over again" && $(cat "$HOME/gh-pr-base-69") == main ]] && crew hand-over "$FI" >/dev/null && [[ $(crew get "$FI" status) == pr-open ]]'
FJ=$(crew new "Moved off its parent" --branch unstacked-child --on "$(tid "$FP")"); fill "$FJ"; crew ready "$FJ" >/dev/null; setstate "done" null; crew dispatch "$FJ" >/dev/null
first_run "$FJ" 70; echo agent/first-parent >"$HOME/gh-pr-base-70"; crew set "$FJ" base main >/dev/null; out=$(crew hand-over "$FJ" 2>&1); st=$?
check "a first run moved to a base its branch doesn't start on is told to ask, and stays off pr-open" \
  '((st == 1)) && [[ $out == *"your PR has to move before its first hand-over (PR targets agent/first-parent, not main), but your branch doesn'"'"'t start on main"*"ask what to do"* && $(crew get "$FJ" status) == working ]]'
FK=$(crew new "Moved while handing over" --branch moving-child --on "$(tid "$FP")"); fill "$FK"; crew ready "$FK" >/dev/null; setstate "done" null; crew dispatch "$FK" >/dev/null
first_run "$FK" 71
# The lead moves the child to another parent while crew hand-over reads the first parent's PR, which GitHub calls merged.
cat >"$HOME/gh-pr-view-hook" <<HOOK
#!/bin/sh
case "\$*" in *"/pull/61 --json state"*) ;; *) exit 0 ;; esac
crew set "$FK" base agent/replacement-parent >/dev/null; echo MERGED >"$HOME/gh-pr-state-61"; rm "\$0"
HOOK
chmod +x "$HOME/gh-pr-view-hook"; before="$(crew get "$FR" status) $(crew get "$FR" pr_state)"
crew hand-over "$FK" >/dev/null 2>&1; rm -f "$HOME/gh-pr-state-61"
check "a parent's PR read before the child moved to another parent isn't recorded on the new one" \
  '[[ $(crew get "$FK" base) == agent/replacement-parent && "$(crew get "$FR" status) $(crew get "$FR" pr_state)" == "$before" && $(crew get "$FP" status) != merged ]]'
for t in "$FR" "$FH" "$FI" "$FJ" "$FK"; do crew set "$t" status abandoned >/dev/null; done

# The parent merges while one child has its PR open and another has none yet.
FC=$(crew new "PR open when its parent merged" --branch merged-child --on "$(tid "$FP")"); fill "$FC"; crew ready "$FC" >/dev/null; setstate "done" null; crew dispatch "$FC" >/dev/null
FD=$(crew new "No PR when its parent merged" --branch merged-child-no-pr --on "$(tid "$FP")"); fill "$FD"; crew ready "$FD" >/dev/null; setstate "done" null; crew dispatch "$FD" >/dev/null
first_run "$FC" 64; first_run "$FD" ""
echo agent/first-parent >"$HOME/gh-pr-base-64"
# The parent merges on GitHub while crew hand-over reads its PR, after the hand-over's first fetch.
cat >"$HOME/gh-pr-view-hook" <<HOOK
#!/bin/sh
case "\$*" in *"/pull/61 --json state"*) ;; *) exit 0 ;; esac
cd "$SB/other-clone" && { git fetch -q origin && git checkout -q -B main origin/main && git merge -q --squash origin/agent/first-parent &&
  git commit -qm "First-run parent (#61)" && git push -q origin main && git push -q origin --delete agent/first-parent; } >/dev/null 2>&1
echo MERGED >"$HOME/gh-pr-state-61"; rm "\$0"
HOOK
chmod +x "$HOME/gh-pr-view-hook"
e=$(edits main); out=$(crew hand-over "$FC" 2>&1); run_given "$(crew get "$FC" worktree)" "$out"; git pull -q --ff-only
check "a first hand-over notes the merge of a parent that crew sync hasn't seen" \
  '[[ $(crew get "$FP" status) == merged && $(crew get "$FP" pr_state) == merged ]] && section "$FP" "## Log" | grep -q "PR merged"'
(($(edits main) > e)) && echo main >"$HOME/gh-pr-base-64"  # GitHub moves the PR
check "after a merge, the refusal has the worker rebase onto the default branch as merged, retarget its PR and record its base" \
  '[[ $out == *"($(tid "$FP") merged)"*"git rebase --onto $(git rev-parse origin/main) "*"Your base is now main: after pushing, run \`gh pr edit --base main\` and \`crew set $(tid "$FC") base main\`."* && $(crew get "$FC" base) == main && $(cat "$HOME/gh-pr-base-64") == main && $(git log -1 --format=%s origin/main) == "First-run parent (#61)" ]]'
ranged_review "$FC" 2; crew hand-over "$FC" >/dev/null
check "and it hands over on the default branch, with only its own commits" '[[ $(crew get "$FC" status) == pr-open && $(git log --format=%s origin/main..origin/agent/merged-child) == "add $(tid "$FC").txt" ]]'
out=$(crew check-base "$FD" 2>&1); st=$?
check "a child with no PR yet gets the move from check-base, and the same from hand-over, which needs no PR first" \
  '((st == 1)) && [[ $out == *"Your base is now main: after pushing, run \`crew set $(tid "$FD") base main\`."* && $out != *"gh pr edit"* && $(crew hand-over "$FD" 2>&1) == "$out" ]]'
run_given "$(crew get "$FD" worktree)" "$out"; crew set "$FD" pr https://github.com/isaac/webapp/pull/65; echo main >"$HOME/gh-pr-base-65"; pr_body "$FD"; ranged_review "$FD" 2
check "then opens its PR on the default branch and hands over, with only its own commits" \
  'crew hand-over "$FD" >/dev/null && [[ $(crew get "$FD" status) == pr-open && $(crew get "$FD" base) == main && $(git log --format=%s origin/main..origin/agent/merged-child-no-pr) == "add $(tid "$FD").txt" ]]'
crew set "$FC" status abandoned >/dev/null; crew set "$FD" status abandoned >/dev/null
crew cleanup "$FP" >/dev/null; rm -f "$HOME/gh-pr-state" "$HOME/gh-pr-state-61" "$HOME"/gh-pr-base-6?

# Goals: a goal lead plans every item; crew tick dispatches the ready tasks, passes
# the goal lead its input, and closes the goal once every task is done.
gget() { sed -n "s/^$2: //p" "$1" | head -n 1; }  # goal-file key
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
check "the goal lead gets the lead's settings, all but their agent, with its project, the home and the pause hook" \
  '[[ $(given_settings) == "$(jq -c "del(.agent)" "$L/lead/.claude/settings.json")" ]] && has_pause_hook && jq -e ".hooks.PreToolUse and (has(\"agent\") | not)" "$HOME/claude-settings.last" >/dev/null && session_env "$(gget "$GF" project)"'
check "the goal lead works in a detached checkout of the default branch" '[[ $(jq -r --arg id "$GJ" ".[] | select(.id == \$id) | .cwd" "$HOME/agents.json") == "$CO" && -z $(git -C "$CO" branch --show-current) && $(git -C "$CO" rev-parse HEAD) == $(git -C "$SB/webapp" rev-parse origin/main) ]]'
check "goal start records the fingerprint of what the goal lead loaded" '[[ $(gget "$GF" lead_fingerprint) =~ ^[0-9a-f]{12}$ ]]'
GT1=$(crew new "Season header" --goal 1); T1=$(basename "$GT1" .md)
GT2=$(crew new "Season standings" --goal 1); T2=$(basename "$GT2" .md)
NG=$(crew new "Not in a goal"); fill "$NG"; crew ready "$NG" >/dev/null
check "new --goal makes the task one of the goal's" '[[ $(crew get "$GT1" goal) == "$G" && -z $(crew get "$NG" goal) ]]'
check "new --goal refuses a goal that doesn't exist" '! crew new "Stray" --goal 9 2>/dev/null'
sed -i '/^## Open questions$/a - [ ] Add a standings table? It needs a migration. I recommend yes.' "$GT2"
check "status lists a goal's drafts with open questions under parked, and the rest under planning" '[[ $(crew status) == *"goal $G"*"running"*"0 of 2 task(s) merged, 0/3 in flight$NL    parked$NL         $T2"*"draft"*"1 open question(s)$NL    planning$NL         $T1"* ]]'
fill "$GT1"; crew ready "$GT1" >/dev/null
crew goal cap 1 0 >/dev/null; prompt held >/dev/null
out=$(crew tick 2>&1)
check "goal cap sets max_in_flight, and tick says a goal task over it waits for the cap" '[[ $(gget "$GF" max_in_flight) == 0 && $out == "$T1: dispatch held back: goal $G already has 0 task(s) in flight (max_in_flight 0); leaving $T1 ready" && $(crew get "$GT1" status) == ready ]]'
check "goal cap refuses a value that isn't a whole number" '! crew goal cap 1 -1 2>/dev/null && ! crew goal cap 1 two 2>/dev/null && [[ $(gget "$GF" max_in_flight) == 0 ]]'
out=$(prompt held)
check "a lead hears what the cap holds back, and why" '[[ $out == "$HELD$NL- $PJ: $T1: dispatch held back: goal $G already has 0 task(s) in flight (max_in_flight 0); leaving $T1 ready" ]]'
crew tick >/dev/null 2>&1
check "and isn't told it again while the cap holds the task back" '[[ -z $(prompt held) ]]'
crew goal cap 1 1 >/dev/null
echo "MAX_WORKERS=0" >>"$L/config/crew.env"
out=$(crew tick 2>&1)
check "tick says when MAX_WORKERS holds a goal task back" '[[ $out == "$T1: dispatch held back: 0 workers already running (MAX_WORKERS=0); leaving $T1 ready" && $(crew get "$GT1" status) == ready ]]'
check "and hears it again when another cap holds the task back" '[[ $(prompt held) == "$HELD$NL- $PJ: $T1: dispatch held back: 0 workers already running (MAX_WORKERS=0); leaving $T1 ready" ]]'
sed -i '/^MAX_WORKERS=0$/d' "$L/config/crew.env"
sed -i '/^## Plan$/a 1. Style the header.' "$GT1"
out=$(crew tick 2>&1)
check "tick dispatches no goal task whose brief still says how to meet its objective, and says why as dispatch does" \
  '[[ $out == *"$T1: dispatch failed:"*"brief still says how to meet its objective, under Plan"* && $(crew get "$GT1" status) == ready && -z $(crew get "$GT1" worker_job) ]]'
sed -i '/^1\. Style the header\.$/d' "$GT1"
out=$(crew tick 2>&1)
check "tick dispatches a running goal's ready task, and no other ready task" '[[ $out == *"$T1: dispatched for goal $G"* && $(crew get "$GT1" status) == working && $(crew get "$NG" status) == ready ]]'
GS=$(crew new "Season header styles" --goal "$GF" --on "$GT1"); fill "$GS"; crew ready "$GS" >/dev/null
out=$(crew dispatch "$GS" 2>&1); code=$?
check "a task stacked on one of its goal's counts against the goal's max_in_flight" '[[ $code == 4 && $out == *"goal $G already has 1 task(s) in flight (max_in_flight 1)"* && $(crew get "$GS" status) == ready ]]'
check "status shows a goal's tasks in flight against its max_in_flight" '[[ $(crew status) == *"goal $G"*"0 of 3 task(s) merged, 1/1 in flight"* ]]'
crew set "$GS" status abandoned >/dev/null; crew goal cap 1 3 >/dev/null
check "status leaves a goal's abandoned task out of its task count" '[[ $(crew status) == *"goal $G"*"0 of 2 task(s) merged, 1/3 in flight"* ]]'
# The stub's PR has comments of its own; none of these tests are about them.
crew set "$GT1" feedback_since 2099-01-01T00:00:00Z
stall "$GT1" "done" null; n=$(resumes)
out=$(crew tick 2>&1)
check "tick nudges a goal task's worker whose turn ended mid-task, as any other" '[[ $out == *"$T1: its worker'"'"'s turn ended mid-task; nudged it"* && $(resumes) == $((n + 1)) && $(crew get "$GT1" status) == working ]]'
rm -f "$STUB/$(crew get "$GT1" worker_session).jsonl"
crew set "$GT1" status needs-input
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
# Two notes back to back: the second comes while the goal lead's resume from the
# first is under way, its process up but its prompt not taken yet (see the stub).
gs=$(gget "$GF" lead_session); gj=$(gget "$GF" lead_job)
setstate "done" 4242; transcript "$gs" ended; n=$(resumes)
crew goal note 1 "Use style C for the footer" >/dev/null 2>&1
out=$(crew goal note 1 "And style D for the nav" 2>&1)
check "a note sent while the goal lead's resume is under way is recorded and counted, and waits, without a copy" \
  '[[ $out == *"goal lead $gj is busy (working)"*"crew tick passes it to the goal lead"* && $(section "$GF" "## Decisions") == *"from the user: And style D for the nav"* && $(gget "$GF" unsent_notes) == 1 && $(gget "$GF" lead_job) == "$gj" && $(gget "$GF" lead_session) == "$gs" && $(resumes) == $((n + 1)) ]]'
transcript "$gs" ended; setstate "done" 4242
out=$(crew tick 2>&1)
check "crew tick sends that note once the goal lead's turn has ended, to the same session" \
  '[[ $out == *"goal $G: input sent to its goal lead"* && $(gget "$GF" unsent_notes) == 0 && $(gget "$GF" lead_job) == "$gj" && $(gget "$GF" lead_session) == "$gs" && $(resumes) == $((n + 2)) ]]'
transcript "$gs" ended; setstate "done" 4242; touch "$HOME/stop-noop"
out=$(crew goal note 1 "Use style E for the footer" 2>&1)
check "a note to an idle goal lead whose process won't close is counted and not sent, starting no copy" \
  '[[ $out == *"job $gj is still running, so crew sent it nothing"*"crew tick passes it to the goal lead"* && $(gget "$GF" unsent_notes) == 1 && $(gget "$GF" lead_session) == "$gs" && $(resumes) == $((n + 2)) ]]'
rm -f "$HOME/stop-noop"
out=$(crew tick 2>&1)
check "a later crew tick sends it once the process closes" \
  '[[ $out == *"goal $G: input sent to its goal lead"* && $(gget "$GF" unsent_notes) == 0 && $(gget "$GF" lead_session) == "$gs" && $(resumes) == $((n + 3)) ]]'
rm -f "$STUB/$gs.jsonl"
rm -rf "$CO"; setstate "done" null   # as on a machine the goal moved to
crew goal note 1 "Keep the header small" >/dev/null 2>&1
check "a goal followup brings back a goal lead's checkout that isn't here" '[[ -d $CO && $(git -C "$CO" rev-parse HEAD) == $(git -C "$SB/webapp" rev-parse origin/main) && $(last_launch) == "claude --resume "*"New input for goal $G"* ]]'
before=$(gget "$GF" lead_fingerprint); echo "# changed" >>"$L/agents/goal-lead.md"; install_agents; setstate "done" null
out=$(crew goal note 1 "Keep the footer small" 2>&1)
check "a goal followup after agents/goal-lead.md changes starts the goal lead fresh, records the new fingerprint, and logs why" \
  '[[ $out == *"input sent to its goal lead (fresh: its instructions or settings changed)"* && $(gget "$GF" lead_fingerprint) != "$before" && $(tail -1 "$GF") == *"followup (fresh: its instructions or settings changed): goal lead "* && $(last_launch) == *"--agent goal-lead "*"is already under way"* ]]'
git -C "$L" checkout -q -- agents; install_agents; setstate "done" null
crew goal note 1 "Back as it was" >/dev/null 2>&1; setstate "done" null
echo "# changed" >>"$L/agents/lead.md"; install_agents
out=$(crew goal note 1 "Keep the logo small" 2>&1)
check "a goal followup after agents/lead.md changes starts the goal lead fresh: it follows the lead's intake" \
  '[[ $out == *"input sent to its goal lead (fresh: its instructions or settings changed)"* ]]'
git -C "$L" checkout -q -- agents; install_agents; GJ=$(gget "$GF" lead_job)
crew set "$GT1" status merged; crew set "$GT2" status abandoned; setstate "done" null
out=$(crew tick 2>&1)
check "tick closes a goal once every task is finished, removing its goal lead and checkout" '[[ $out == *"goal $G: done"* && $(gget "$GF" status) == done && ! -d $CO && -z $(gget "$GF" checkout) ]] && grep -qx "claude rm $GJ" "$HOME/claude-calls.log"'
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
echo "WORKER_AUTOCOMPACT_WINDOW=bad" >>"$PE"   # so every dispatch tick runs fails, and says so
out=$(crew tick 2>&1)
check "tick runs crew dispatch for the ready tasks that can go, and not for one waiting on another, which it says waits" '[[ $out == *"$TB: dispatch failed:"*"WORKER_AUTOCOMPACT_WINDOW"* && $out == *"$TH: dispatch waits: it comes after "* && $out != *"$TH: dispatch failed"* ]]'
check "a lead hears a dispatch held back by a dependency, and why" '[[ $(prompt held) == *"$HELD"*"$NL- $PJ: $TH: dispatch waits: it comes after "* ]]'
sed -i '/^WORKER_AUTOCOMPACT_WINDOW=bad$/d' "$PE"
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
# A goal abandoned while the tick sets up one of its own tasks' worktrees gets
# no worker for it.
PS=$(goal_task "$GF4" "Promo popup"); TS=$(basename "$PS" .md)
printf 'crew goal abandon %s >/dev/null 2>&1; rm -f "$HOME/wt-hook-cmd"\n' "$G4" >"$HOME/wt-hook-cmd"
setstate "done" null
out=$(crew tick 2>&1)
check "a goal abandoned while tick sets up its task's worktree starts no worker for it, and says why" \
  '[[ $out == *"$TS: dispatch held back: goal $G4 is abandoned now; leaving $TS ready"* && $(gget "$GF4" status) == abandoned && $(crew get "$PS" status) == ready ]]'
sed -i '/^WORKTREE_TOOL=/d' "$PE"; rm -f "$HOME/wt-hook-cmd"
for f in "$PA" "$PB" "$PC" "$PH" "$PQ" "$PR" "$PS" "$NG"; do crew set "$f" status abandoned; done
# crew status groups a goal's tasks by where they stand, in the order crew tick
# dispatches them, and says what each one not yet dispatched waits for.
num() { basename "$1" .md | cut -d- -f1; }
row() { grep -- "$1" <<<"$st"; }   # the status line of a task, from $st
heading() { awk -v h="    $2" '$0 == h {on = 1; next} on && /^(    [a-z]|  [^ ])/ {exit} on' <<<"$1"; }   # a group's rows
GF5=$(crew goal new "Status groups" --max-in-flight 1); G5=$(basename "$GF5" .md)
sed -i '/^## Goal$/a "Group the tasks."' "$GF5"; crew goal start "$GF5" >/dev/null
SI=$(goal_task "$GF5" "Groups in flight"); crew set "$SI" status working
SM=$(goal_task "$GF5" "Groups merged"); crew set "$SM" status merged
SH=$(goal_task "$GF5" "Groups held"); crew set "$SH" priority 2; crew set "$SH" depends_on "$(basename "$SI" .md)"
SN=$(goal_task "$GF5" "Groups next"); crew set "$SN" depends_on "$(basename "$SM" .md)"
SK=$(crew new "Groups stacked" --goal "$GF5" --on "$SN"); fill "$SK"; crew ready "$SK" >/dev/null
st=$(crew status)
check "status says a goal at its max_in_flight is full" '[[ $st == *"goal $G5"*"1/1 in flight: full"$NL* ]]'
check "status lists a goal's in-flight tasks under their own heading" '[[ $(heading "$st" "in flight") == *"$(basename "$SI" .md)"*working* ]]'
check "status numbers the next task to dispatch, though a higher priority one waits on another task" '[[ $(heading "$st" "next, in dispatch order") == "      1  $(basename "$SN" .md)"* ]]'
check "status lists a held task under waiting, saying what it waits for" '[[ $(heading "$st" "waiting on other tasks") == "         $(basename "$SH" .md)"*"priority 2  after $(num "$SI")" ]]'
check "status leaves a merged dependency out of a task's after" '[[ $(heading "$st" "next, in dispatch order" | head -1) == *"$(basename "$SN" .md)"* && $(row "$(basename "$SN" .md)") != *after* ]]'
check "status lists a task stacked on a ready one once, under its parent, and not after it" '[[ $(grep -c "$(basename "$SK" .md)" <<<"$st") == 1 && $(grep -A1 "1  $(basename "$SN" .md)" <<<"$st" | tail -1) == "        └ $(basename "$SK" .md)"* && $(row "$(basename "$SK" .md)") != *after* ]]'
crew goal cap "$GF5" 3 >/dev/null; crew set "$SN" status working
for i in 1 2 3 4 5; do SL=$(goal_task "$GF5" "Groups more $i"); done
st=$(crew status)
check "status doesn't call a goal under its max_in_flight full" '[[ $st == *"goal $G5"*"2/3 in flight"$'"'"'\n'"'"'"    in flight"* && $st != *"in flight: full"* ]]'
check "status numbers a ready task stacked on one in flight in next, on its parent and not after it" '[[ $(grep -c "$(basename "$SK" .md)" <<<"$st") == 1 && $(heading "$st" "next, in dispatch order" | head -1) == "      1  $(basename "$SK" .md)"*"on $(num "$SN")" ]]'
check "status shows the first 5 tasks that can go next, and how many more there are" '[[ $(heading "$st" "next, in dispatch order" | grep -c "^      [1-5]  ") == 5 && $(heading "$st" "next, in dispatch order" | tail -1) == "      … (1 more)" ]]'
SQ=$(crew new "Groups question on the sixth" --goal "$GF5" --on "$SL"); sed -i '/^## Open questions$/a - [ ] Which way? I recommend this one.' "$SQ"
st=$(crew status)
check "status still lists a task past the first 5 that has a question in its stack, with its number" '[[ $(heading "$st" "next, in dispatch order" | grep -A1 "^      6  $(basename "$SL" .md)" | tail -1) == "        └ $(num "$SQ")"*"1 open question(s)" && $(heading "$st" "next, in dispatch order") != *"more)"* ]]'
crew set "$SQ" status abandoned
SA=$(goal_task "$GF5" "Groups abandoned parent"); SC=$(crew new "Groups orphan" --goal "$GF5" --on "$SA"); fill "$SC"; crew ready "$SC" >/dev/null
crew set "$SA" status abandoned
st=$(crew status)
check "status lists a task stacked on an abandoned one as waiting, after it" '[[ $(heading "$st" "waiting on other tasks") == *"$(basename "$SC" .md)"*"after $(num "$SA") (abandoned)"* ]]'
crew set "$SC" status abandoned
SR=$(goal_task "$GF5" "Groups parent by base"); SY=$(goal_task "$GF5" "Groups child by base"); crew set "$SY" base "$(crew get "$SR" branch)"
crew set "$SR" status abandoned
st=$(crew status)
check "status lists a task stacked through its base on an abandoned one as waiting, after it" '[[ $(heading "$st" "waiting on other tasks") == *"$(num "$SY")-"*"after $(num "$SR") (abandoned)"* ]]'
crew set "$SY" status abandoned
SZ=$(goal_task "$GF5" "Groups after many"); crew set "$SZ" depends_on "$(num "$SI") $(num "$SH") $(num "$SA") $(num "$SL") $(num "$SK")"
st=$(crew status)
check "status names an abandoned dependency even where it only counts the rest" '[[ $(row "$(num "$SZ")-") == *"after 5 tasks, $(num "$SA") abandoned" ]]'
crew set "$SZ" status abandoned
# Dispatch moves a task whose parent merged onto the first task down the stack
# that hasn't, so that one needn't merge first, even when depends_on names it.
SG=$(goal_task "$GF5" "Groups grandparent"); crew set "$SG" status working
SP=$(goal_task "$GF5" "Groups merged middle"); crew set "$SP" base "$(crew get "$SG" branch)"; crew set "$SP" status merged
SU=$(goal_task "$GF5" "Groups on a merged middle"); crew set "$SU" base "$(crew get "$SP" branch)"; crew set "$SU" depends_on "$(num "$SG") $(num "$SP")"; crew set "$SU" priority 9
st=$(crew status)
check "status lists a task whose parent merged as next, on the first task down its stack that hasn't, and not after it" '[[ $(heading "$st" "next, in dispatch order") == *"$(num "$SU")-"*"on $(num "$SG")"* && $(row "$(num "$SU")-") != *after* ]]'
for f in "$SG" "$SP" "$SU"; do crew set "$f" status abandoned; done
GF7=$(crew goal new "Status abandoned cap" --max-in-flight 1); G7=$(basename "$GF7" .md)
SW=$(crew new "Abandoned cap working" --goal "$GF7"); crew set "$SW" status working
SV=$(crew new "Abandoned cap ready" --goal "$GF7"); fill "$SV"; crew ready "$SV" >/dev/null
crew goal abandon "$GF7" >/dev/null 2>&1
st=$(crew status)
check "status says an abandoned goal's cap still holds a task to dispatch by hand" '[[ $(row "$(num "$SV")-") == *"goal $G7 full  dispatch by hand" ]]'
sed -i 's/^max_in_flight: .*/max_in_flight: x/' "$GF7"
st=$(crew status)
check "status reports an abandoned goal's bad cap on its ready task's row, since the goal isn't listed" '[[ $(row "$(num "$SV")-") == *"goal $G7: max_in_flight must be a whole number"*"  dispatch by hand" ]]'
crew set "$SW" status abandoned; crew set "$SV" status abandoned
SX=$(goal_task "$GF5" "Groups on a merged parent"); crew set "$SX" base "$(crew get "$SM" branch)"; crew set "$SX" priority 9
st=$(crew status --done)
check "with --done, a task whose parent merged is listed in its own group, not under the parent" '[[ $(row "$(num "$SX")-") == "      1  $(num "$SX")-"* ]]'
crew set "$SX" status abandoned
GF6=$(crew goal new "Status draft goal"); SD=$(goal_task "$GF6" "Draft goal task")
st=$(crew status)
check "status lists a draft goal's ready task under ready, unnumbered" '[[ $(heading "$st" "ready") == "         $(basename "$SD" .md)"*ready* ]]'
mv "$PD/local.env" "$HOME/local.env.saved"
st=$(crew status --project "$(basename "$PD")")
check "status lists a goal's tasks in their groups for a project with no clone here" '[[ $(heading "$st" "waiting on other tasks") == *"$(basename "$SH" .md)"*"after $(num "$SI")" && $(heading "$st" "next, in dispatch order") == *"1  $(basename "$SK" .md)"* ]]'
mv "$HOME/local.env.saved" "$PD/local.env"
echo "MAX_WORKERS=x" >>"$PE"; sed -i 's/^max_in_flight: .*/max_in_flight: x/' "$GF5"
st=$(crew status)
check "status reports a cap that isn't a number once, on the project's or the goal's line, and groups the tasks as before" \
  '[[ $(grep -c "MAX_WORKERS must be a whole number" <<<"$st") == 1 && $st == *"$NL$(basename "$PD")  MAX_WORKERS must be a whole number, not '"'"'x'"'"'$NL"* && $(grep -c "max_in_flight must be a whole number" <<<"$st") == 1 && $(row "goal $G5") == *"/? in flight  goal $G5: max_in_flight must be a whole number"* && $(heading "$st" "next, in dispatch order") == *"1  $(basename "$SK" .md)"* && $(heading "$st" "waiting on other tasks") == *"$(basename "$SH" .md)"*"after $(num "$SI")" ]]'
sed -i '/^MAX_WORKERS=x$/d' "$PE"; crew goal cap "$GF5" 3 >/dev/null
# Outside a goal: a ready task waits for someone to dispatch it, and drafts with
# nothing to ask fold into a count.
OR=$(crew new "Outside ready"); fill "$OR"; crew ready "$OR" >/dev/null
crew new "Outside draft one" >/dev/null; crew new "Outside draft two" >/dev/null
OQ=$(crew new "Outside question"); sed -i '/^## Open questions$/a - [ ] Which way? I recommend this one.' "$OQ"
st=$(crew status)
check "status says a ready task outside a goal is dispatched by hand" '[[ $(row "$(basename "$OR" .md)") == *"ready"*"dispatch by hand" ]]'
check "status folds drafts outside a goal into a count, and lists one with an open question" '[[ $st == *$'"'"'\n'"'"'"  2 draft(s)  (crew list --project $(basename "$PD") --status draft)"* && $st != *"outside-draft"* && $(row "$(basename "$OQ" .md)") == *"draft"*"1 open question(s)"* ]]'
check "status says when the workers running have reached MAX_WORKERS" 'echo "MAX_WORKERS=0" >>"$L/config/crew.env" && st=$(crew status) && [[ $(head -1 <<<"$st") == "workers running: "*"/0: full, nothing dispatches" && $st == *"$NL$(basename "$PD")  workers full (MAX_WORKERS 0 here)$NL"* ]]'
sed -i '/^MAX_WORKERS=0$/d' "$L/config/crew.env"
crew goal abandon "$GF5" >/dev/null 2>&1; crew goal abandon "$GF6" >/dev/null 2>&1
for f in "$PD"/tasks/*.md; do case $(crew get "$f" status) in merged|abandoned) ;; *) crew set "$f" status abandoned ;; esac; done
rm -f "$HOME/gh-pr-state"
# A go-ahead that waits: the user's word to dispatch a ready task outside a goal
# once another task reaches a status. crew tick keeps it, so nobody has to watch.
echo OPEN >"$HOME/gh-pr-state"; setstate "done" null
# A dispatched task, titled $1, with an open PR whose comments crew has read.
pr_task() {
  local f; f=$(crew new "$1"); fill "$f"; crew ready "$f" >/dev/null; setstate "done" null; crew dispatch "$f" >/dev/null 2>&1
  crew set "$f" pr https://github.com/isaac/webapp/pull/12; crew set "$f" feedback_since 2099-01-01T00:00:00Z; echo "$f"; }
ready_task() { local f; f=$(crew new "$1"); fill "$f"; crew ready "$f" >/dev/null; echo "$f"; }
WA=$(ready_task "Waited on"); TWA=$(basename "$WA" .md)
WB=$(ready_task "Waits for pr-open"); TWB=$(basename "$WB" .md)
crew set "$WB" go_ahead "$(num "$WA") pr-open"
check "set go_ahead records the task it waits for by its ID, with the status" '[[ $(crew get "$WB" go_ahead) == "$TWA pr-open" ]]'
crew set "$WB" priority 2; crew set "$WA" priority 2
st=$(crew status)
check "status says what a ready task's go-ahead waits for, where it said dispatch by hand" '[[ $(row "$TWB") == *"ready"*"  go-ahead once $(num "$WA") is pr-open" && $(row "$TWB") != *"by hand"* ]]'
check "status shows the priority of a task with a go-ahead, which orders tick's dispatch, and not of one dispatched by hand" '[[ $(row "$TWB") == *"  priority 2"* && $(row "$TWA") != *priority* ]]'
out=$(crew tick 2>&1)
check "tick doesn't dispatch a task whose go-ahead isn't met yet" '[[ $out != *"$TWB: dispatched"* && $(crew get "$WB" status) == ready && $(crew get "$WA" status) == ready ]]'
setstate "done" null; crew dispatch "$WA" >/dev/null 2>&1
crew set "$WA" pr https://github.com/isaac/webapp/pull/12; crew set "$WA" feedback_since 2099-01-01T00:00:00Z; hand_over "$WA"
echo "MAX_WORKERS=0" >>"$L/config/crew.env"; setstate "done" null
out=$(crew tick 2>&1)
check "once the task it waits for is pr-open, tick dispatches it as crew dispatch's waits allow: MAX_WORKERS holds it back" \
  '[[ $out == *"$TWB: dispatch held back: 0 workers already running (MAX_WORKERS=0)"* && $(crew get "$WB" status) == ready && $(crew get "$WB" go_ahead) == "$TWA pr-open" ]]'
st=$(crew status)
check "status says a go-ahead is met" '[[ $(row "$TWB") == *"ready"*"  go-ahead met: $(num "$WA") reached pr-open" ]]'
sed -i '/^MAX_WORKERS=0$/d' "$L/config/crew.env"
crew set "$WA" status merged; setstate "done" null
prompt go-ahead >/dev/null   # a lead's session, which hears what the tick below does
out=$(crew tick 2>&1)
check "tick dispatches it once the task it waits for has been pr-open, though it's merged by now, saying why" \
  '[[ $out == *"$TWB: dispatched on its go-ahead: $TWA reached pr-open"* && $(crew get "$WB" status) == working ]]'
check "dispatching ends the go-ahead, and the Log says what it was" '[[ -z $(crew get "$WB" go_ahead) && $(section "$WB" "## Log") == *"go-ahead ($TWA pr-open) ended: the task is working now"* ]]'
check "a lead hears that tick dispatched a task on its go-ahead" '[[ $(prompt go-ahead) == *"$DID"*"$PJ: $TWB: dispatched on its go-ahead: $TWA reached pr-open"* ]]'
# A task tick dispatches here has no review rounds to pull, which would crowd a
# lead's news (see "with its time, under one heading" below).
no_feedback() { crew set "$1" feedback_since 2099-01-01T00:00:00Z; }
no_feedback "$WB"
# A go-ahead for a status the other task has reached and moved on from is met at once.
WC=$(pr_task "Reviewed already"); TWC=$(basename "$WC" .md); hand_over "$WC"; crew set "$WC" status addressing-feedback
WD=$(ready_task "Waits for a pr-open that's past"); TWD=$(basename "$WD" .md); crew set "$WD" go_ahead "$(num "$WC") pr-open"
# Leaving ready ends a go-ahead, and so does withdrawing it.
WE=$(ready_task "Back to draft"); crew set "$WE" go_ahead "$(num "$WC") pr-open"; crew set "$WE" status draft; crew ready "$WE" >/dev/null
WF=$(ready_task "Withdrawn"); crew set "$WF" go_ahead "$(num "$WC") pr-open"; crew set "$WF" go_ahead
WG=$(ready_task "Abandoned with a go-ahead"); crew set "$WG" go_ahead "$(num "$WC") pr-open"; crew set "$WG" status abandoned
check "leaving ready ends a go-ahead, with a Log line, and making the task ready again doesn't bring it back" \
  '[[ -z $(crew get "$WE" go_ahead) && $(section "$WE" "## Log") == *"go-ahead ($TWC pr-open) ended: the task is draft now"* && -z $(crew get "$WG" go_ahead) ]]'
setstate "done" null; out=$(crew tick 2>&1)
check "tick dispatches a task whose go-ahead was met before it was recorded" '[[ $out == *"$TWD: dispatched on its go-ahead: $TWC reached pr-open"* && $(crew get "$WD" status) == working ]]'
no_feedback "$WD"
check "and not one made ready again after its go-ahead ended, or one whose go-ahead was withdrawn, or an abandoned one" \
  '[[ $(crew get "$WE" status) == ready && $(crew get "$WF" status) == ready && $(crew get "$WG" status) == abandoned && $out != *"$(basename "$WE" .md): dispatched"* && $out != *"$(basename "$WF" .md): dispatched"* ]]'
st=$(crew status)
check "status says they're dispatched by hand" '[[ $(row "$(basename "$WE" .md)") == *"  dispatch by hand" && $(row "$(basename "$WF" .md)") == *"  dispatch by hand" ]]'
# A go-ahead waiting on a task that's since been abandoned is never met.
WH=$(ready_task "Will be abandoned"); WI=$(ready_task "Waits on an abandoned task"); TWI=$(basename "$WI" .md)
crew set "$WI" go_ahead "$(num "$WH") merged"; crew set "$WH" status abandoned
setstate "done" null; out=$(crew tick 2>&1); st=$(crew status)
check "a go-ahead waiting on an abandoned task never fires, and status says so" \
  '[[ $out != *"$TWI: dispatched"* && $(crew get "$WI" status) == ready && $(row "$TWI") == *"  go-ahead never met: $(num "$WH") abandoned  dispatch by hand" ]]'
# A paused project's go-aheads wait for crew resume.
WJ=$(ready_task "Waits out a pause"); TWJ=$(basename "$WJ" .md); crew set "$WJ" go_ahead "$(num "$WC") pr-open"
crew pause --project "$PJ" >/dev/null 2>&1; setstate "done" null
out=$(crew tick 2>&1)
check "a paused project's met go-ahead waits, saying why" '[[ $out == *"$TWJ: dispatch waits: $PJ is paused"* && $(crew get "$WJ" status) == ready ]]'
crew resume --project "$PJ" >/dev/null; setstate "done" null
out=$(crew tick 2>&1)
check "and tick dispatches it after crew resume" '[[ $out == *"$TWJ: dispatched on its go-ahead: $TWC reached pr-open"* && $(crew get "$WJ" status) == working ]]'
no_feedback "$WJ"
# A go-ahead withdrawn while the tick sets up the task's worktree (its Worktrunk
# hooks running) starts no worker.
WN=$(ready_task "Withdrawn during setup"); TWN=$(basename "$WN" .md); crew set "$WN" go_ahead "$(num "$WC") pr-open"
echo 'WORKTREE_TOOL="worktrunk"' >>"$PE"
printf 'crew set %q go_ahead >/dev/null 2>&1; rm -f "$HOME/wt-hook-cmd"\n' "$WN" >"$HOME/wt-hook-cmd"
setstate "done" null
out=$(crew tick 2>&1)
check "a go-ahead withdrawn while tick sets up the task's worktree starts no worker, and says why" \
  '[[ $out == *"$TWN: dispatch held back: its go-ahead was withdrawn, or isn'"'"'t met any more; leaving $TWN ready"* && $(crew get "$WN" status) == ready && -z $(crew get "$WN" go_ahead) ]]'
sed -i '/^WORKTREE_TOOL=/d' "$PE"; rm -f "$HOME/wt-hook-cmd"
# What crew set refuses: a wait that can't end, or one for a task tick doesn't dispatch.
WK=$(crew new "Not ready yet"); WS=$(crew new "A spike" --kind spike)
GFG=$(crew goal new "Go-ahead goal"); WL=$(crew new "In a goal" --goal "$GFG"); fill "$WL"; crew ready "$WL" >/dev/null
WM=$(ready_task "Refusals")
check "set go_ahead refuses a task that isn't ready, or is in a goal" \
  '[[ $(crew set "$WK" go_ahead "$(num "$WC") pr-open" 2>&1) == *"a go-ahead is for a ready task"* && $(crew set "$WL" go_ahead "$(num "$WC") pr-open" 2>&1) == *"a go-ahead is for a task outside a goal"* && -z $(crew get "$WK" go_ahead) && -z $(crew get "$WL" go_ahead) ]]'
# A go-ahead waiting for the ledger lock checks the task as it is once it has it
# (-o: crew set mustn't inherit the lock flock holds).
flock -o "$L/.crew.lock" bash -c 'crew set "$1" go_ahead "$2 pr-open" >"$HOME/set.out" 2>&1 & sleep 0.5; sed -i "s/^status: .*/status: draft/" "$1"' _ "$WM" "$(num "$WC")"
check "a go-ahead waiting on the lock refuses a task moved back to draft meanwhile" \
  'wait_for "grep -q \"a go-ahead is for a ready task\" \"\$HOME/set.out\"" && [[ -z $(crew get "$WM" go_ahead) ]]'
crew ready "$WM" >/dev/null
check "set go_ahead refuses the task itself, an abandoned task, and a task that doesn't exist" \
  '[[ $(crew set "$WM" go_ahead "$(num "$WM") pr-open" 2>&1) == *"not $(basename "$WM" .md) itself"* && $(crew set "$WM" go_ahead "$(num "$WH") pr-open" 2>&1) == *"is abandoned"* && $(crew set "$WM" go_ahead "9999 pr-open" 2>&1) == *"doesn'"'"'t match exactly one task"* ]]'
check "set go_ahead refuses a status a go-ahead can't wait for, or the other task's kind never reaches, and a value without one" \
  '[[ $(crew set "$WM" go_ahead "$(num "$WC") working" 2>&1) == *"to be pr-open or merged, not '"'"'working'"'"'"* && $(crew set "$WM" go_ahead "$(num "$WS") merged" 2>&1) == *"to be done, not '"'"'merged'"'"'"* && $(crew set "$WM" go_ahead "$(num "$WC")" 2>&1) == *"go_ahead must be a task and the status it waits for"* && -z $(crew get "$WM" go_ahead) ]]'
crew goal abandon "$GFG" >/dev/null 2>&1
for f in "$PD"/tasks/*.md; do case $(crew get "$f" status) in merged|abandoned) ;; *) crew set "$f" status abandoned ;; esac; done
rm -f "$HOME/gh-pr-state"

# Spikes: a draft lists what it still has to learn; a spike tries a path on a
# branch nobody merges, and its report goes to whoever owns the draft.
echo OPEN >"$HOME/gh-pr-state"; setstate "done" null
GF8=$(crew goal new "Season search" --max-in-flight 1); G8=$(basename "$GF8" .md)
sed -i '/^## Goal$/a "Add search to the season page."' "$GF8"; crew goal start "$GF8" >/dev/null
KD=$(crew new "Season search box" --goal "$GF8"); KDI=$(basename "$KD" .md); fill "$KD"
sed -i '/^## Still to learn$/a - [ ] Does the API page by cursor?\n- [x] Which index? Settled: the existing one.\n- [ ] Is a full scan fast enough?' "$KD"
check "ready refuses a task with open items under Still to learn, counting them and naming the spike that can answer them" \
  '[[ $(crew ready "$KD" 2>&1) == *"2 item(s) under Still to learn are open"*"crew new --kind spike --informs $KDI"* && $(crew get "$KD" status) == draft ]]'
n=$(ls "$PD/tasks" | wc -l)
check "new refuses a kind crew doesn't know, and --informs on a kind that takes none" \
  '[[ $(crew new "Drill" --kind drill 2>&1) == *"--kind must be build, spike, fix or refine, not '"'"'drill'"'"'"* && $(crew new "Built" --informs "$KD" 2>&1) == *"a build task doesn'"'"'t take --informs"* && $(ls "$PD/tasks" | wc -l) == "$n" ]]'
check "new refuses to stack a spike: its branch stays local" \
  '[[ $(crew new "Stacked spike" --kind spike --on "$KD" 2>&1) == *"a spike'"'"'s branch stays local, so it can'"'"'t be stacked"* && $(ls "$PD/tasks" | wc -l) == "$n" ]]'
check "new --informs refuses a task that isn't a draft" \
  '[[ $(crew new "Late spike" --kind spike --informs "$OR" 2>&1) == *"informs must be a draft task in $(basename "$PD"); $(basename "$OR" .md) is '"'"'abandoned'"'"'"* && $(ls "$PD/tasks" | wc -l) == "$n" ]]'
KS=$(crew new "Try cursor paging" --kind spike --informs "$KD" --goal "$GF8"); KSI=$(basename "$KS" .md)
KT=$(crew new "Try a full scan" --kind spike --informs "$KD" --goal "$GF8")
check "new --kind spike --informs makes a spike tied to the draft, and logs it" \
  '[[ $(crew get "$KS" kind) == spike && $(crew get "$KS" informs) == "$KDI" && $(crew get "$KD" kind) == build && $(section "$KS" "## Log") == *" — created as a spike, informs $KDI, for goal $G8" ]]'
check "set checks a spike's informs as new does, and records the draft's ID" \
  '! crew set "$KS" informs "$OR" 2>/dev/null && crew set "$KS" informs "$(num "$KD")" && [[ $(crew get "$KS" informs) == "$KDI" ]]'
KU=$(crew new "Try nothing" --kind spike --informs "$KD")
check "set clears a spike's informs with an empty value" 'crew set "$KU" informs "" && [[ -z $(crew get "$KU" informs) ]]'
crew set "$KU" status abandoned
check "set refuses a kind crew doesn't know" '[[ $(crew set "$KS" kind drill 2>&1) == *"kind must be build, spike, fix or refine, not '"'"'drill'"'"'"* && $(crew get "$KS" kind) == spike ]]'
sed -i -e '/^## Goal$/a Does the API page by cursor?' -e '/^## Acceptance criteria$/a - A page of results by cursor, or the error that says why not.' "$KS"
check "ready refuses a spike without a plan" '[[ $(crew ready "$KS" 2>&1) == *"fill in: Plan"* ]]'
sed -i '/^## Plan$/a 1. Page through the API.' "$KS"
check "ready accepts a spike with Goal, Plan and Acceptance criteria, and no Size or risk" \
  'crew ready "$KS" >/dev/null && [[ $(crew get "$KS" status) == ready && -z $(crew get "$KS" risk) ]]'
crew dispatch "$KS" >/dev/null; KWT=$(crew get "$KS" worktree); KB=$(crew get "$KS" branch)
n=$(ls "$PD/tasks" | wc -l)
check "new refuses to stack a task on a spike, whose branch is never pushed" \
  '[[ $(crew new "On a spike" --on "$KS" 2>&1) == *"a spike'"'"'s branch is never pushed, so nothing can stack on $KSI"* && $(ls "$PD/tasks" | wc -l) == "$n" ]]'
KA=$(crew new "Search results page"); KAI=$(basename "$KA" .md); fill "$KA"; crew set "$KA" depends_on "$KSI"; crew ready "$KA" >/dev/null
KC=$(crew new "On the draft" --on "$KD"); KDB=$(crew get "$KD" branch); KAB=$(crew get "$KA" base)
check "set refuses a base or kind that would put a spike in a stack, either way up" \
  '[[ $(crew set "$KA" base "$KB" 2>&1) == *"nothing can stack on $KSI"* && $(crew set "$KS" base "$KDB" 2>&1) == *"a spike'"'"'s branch stays local, so it can'"'"'t be stacked"* && $(crew set "$KC" kind spike 2>&1) == *"can'"'"'t be stacked"* && $(crew set "$KD" kind spike 2>&1) == *"nothing can stack on $KDI"* && $(crew get "$KA" base) != "$KB" && $(crew get "$KS" base) != "$KDB" && $(crew get "$KD" kind) == build && $(crew get "$KC" kind) == build ]]'
check "set takes a base that stacks one build on another" 'crew set "$KA" base "$KDB" && [[ $(crew get "$KA" base) == "$KDB" ]] && crew set "$KA" base "$KAB"'
check "a task that depends on a running spike waits for it" \
  '[[ $(crew dispatch "$KA" 2>&1) == *"depends on $KSI, which is '"'"'working'"'"' (must be merged or done)"* && $(crew get "$KA" status) == ready ]]'
st=$(crew status)
check "status shows a draft's open items and its spikes, and a spike the draft it informs" \
  '[[ $(row "$KDI") == *"draft"*"2 to learn; $(num "$KS") working, $(num "$KT") draft" && $(row "$KSI") == *"working"*"informs $(num "$KD")" ]]'
check "status shows a task waiting on a running spike" '[[ $(row "$KAI") == *"after $(num "$KS")"* ]]'
check "a running spike holds a slot of its goal's max_in_flight" '[[ $(row "goal $G8") == *"0 of 1 task(s) merged, 1/1 in flight: full" ]]'
commit_in "$KWT" spike/paging.ts
echo 'SENSITIVE_PATHS="spike"' >>"$PE"; out=$(crew sync 2>&1); sed -i '/^SENSITIVE_PATHS=/d' "$PE"
check "sync leaves a spike that touches a sensitive path without a risk tier: it has none to raise" '[[ $out != *"risk →"* && -z $(crew get "$KS" risk) ]]'
check "sync looks for no PR for a spike: one under its branch's name is someone else's" '[[ $out != *"$KSI: found PR"* && -z $(crew get "$KS" pr) ]]'   # the gh stub finds a PR for any branch
check "set refuses done: only hand-over sets it, once the report is in" \
  '[[ $(crew set "$KS" status done 2>&1) == *"only crew hand-over sets done, once the report is in: crew hand-over $KSI"* && $(crew get "$KS" status) == working ]]'
check "hand-over refuses a spike without a report" '[[ $(crew hand-over "$KS" 2>&1) == *"## Report must start with a line '"'"'Answer: <the answer, in a line>'"'"'"* && $(crew get "$KS" status) == working ]]'
sed -i '/^## Automated reviews$/i ## Report\nTried: paging by cursor.\nAnswer: yes\n' "$KS"
check "hand-over refuses a spike whose report doesn't start with its answer" '! crew hand-over "$KS" 2>/dev/null && [[ $(crew get "$KS" status) == working ]]'
sed -i -e '/^Tried: paging by cursor.$/d' -e 's/^Answer: yes$/Answer: yes, 100 rows a page by cursor\nTried: paging by cursor./' "$KS"
touch "$KWT/scratch.log"
check "hand-over refuses a spike whose worktree has changes it didn't commit, which cleanup would leave behind" \
  '[[ $(crew hand-over "$KS" 2>&1) == *"$KWT has changes that aren'"'"'t committed: commit or delete them"* && $(crew get "$KS" status) == working ]]'
rm "$KWT/scratch.log"
check "hand-over moves a spike whose report starts with its answer to done, without a PR or evidence, and logs it" \
  '[[ $(crew hand-over "$KS") == "handed over: $KSI (done)" && $(crew get "$KS" status) == done && $(section "$KS" "## Log" | tail -1) == *" — handed over" ]]'
st=$(crew status)
check "a done spike doesn't hold its goal's slot, and is listed only with --done" \
  '[[ $(row "goal $G8") == *"0/1 in flight" && $(row "$KDI") == *"2 to learn; $(num "$KS") done, $(num "$KT") draft" && $st != *"$KSI"* && $(crew status --done) == *"$KSI"*done* ]]'
check "a task that depends on a done spike no longer waits for it" '[[ $(row "$KAI") != *after* ]]'
setstate "done" null; n=$(resumes)
out=$(crew tick 2>&1)
check "tick cleans up a done spike's worktree and local branch" \
  '[[ $out == *"$KSI: done; worktree cleaned up"* && ! -d $KWT && -z $(crew get "$KS" worktree) ]] && ! git -C "$SB/webapp" show-ref --verify --quiet "refs/heads/$KB"'
check "tick sends a goal's done spike's report to its goal lead, with its answer and the draft it informs" \
  '[[ $out == *"goal $G8: input sent to its goal lead"* && $(last_launch) == *"- $KSI reported: yes, 100 rows a page by cursor; it informs $KDI (task file: $KS)"* && $(crew get "$KS" report_sent) == yes && $(resumes) == $((n + 1)) ]]'
setstate "done" null
out=$(crew tick 2>&1)
check "a report the goal lead has been sent isn't sent again" '[[ $out != *"goal $G8"* && $(resumes) == $((n + 1)) ]]'
KO=$(crew new "Outside learning"); sed -i '/^## Still to learn$/a - [ ] Will it fit?' "$KO"
check "status lists a draft outside a goal that has something to learn, rather than fold it into the count" '[[ $(crew status | grep "$(basename "$KO" .md)") == *"draft"*"1 to learn" ]]'
crew goal abandon "$GF8" >/dev/null 2>&1
for f in "$KC" "$KD" "$KT" "$KO" "$KA"; do crew set "$f" status abandoned; done
rm -f "$HOME/gh-pr-state"

# Fixes: a fix mends a defect live in production, and names the merged task that
# introduced it and the check that missed it.
FC=$(crew new "Invoice totals"); FCI=$(basename "$FC" .md); crew set "$FC" status merged
n=$(ls "$PD/tasks" | wc -l)
check "new refuses --fixes on a kind that takes none, and a task that isn't merged" \
  '[[ $(crew new "Tidy totals" --kind refine --fixes "$FC" 2>&1) == *"a refine task doesn'"'"'t take --fixes"* && $(crew new "Early fix" --kind fix --fixes "$OR" 2>&1) == *"fixes must be a merged task in $(basename "$PD"), or unknown; $(basename "$OR" .md) is '"'"'abandoned'"'"'"* && $(ls "$PD/tasks" | wc -l) == "$n" ]]'
FX=$(crew new "Round invoice totals to the cent" --kind fix --fixes "$(num "$FC")"); FXI=$(basename "$FX" .md); fill "$FX"
check "new --kind fix --fixes records the fixed task's ID, and logs it" \
  '[[ $(crew get "$FX" kind) == fix && $(crew get "$FX" fixes) == "$FCI" && $(section "$FX" "## Log") == *" — created as a fix, fixes $FCI" ]]'
check "ready refuses a fix that doesn't name the check that missed the defect" \
  '[[ $(crew ready "$FX" 2>&1) == *"missed_by must be brief-review, tests, pre-pr-review, security-review, automated-review, ci or user-review: crew set $FXI missed_by <value>"* && $(crew get "$FX" status) == draft ]]'
check "set refuses a missed_by that isn't one of the checks" \
  '[[ $(crew set "$FX" missed_by bogus 2>&1) == *"missed_by must be brief-review, "*", not '"'"'bogus'"'"'"* && -z $(crew get "$FX" missed_by) ]]'
check "set refuses plan-review, the brief review's old name, with a list that names brief-review" \
  '[[ $(crew set "$FX" missed_by plan-review 2>&1) == *"missed_by must be brief-review, "*", not '"'"'plan-review'"'"'"* && -z $(crew get "$FX" missed_by) ]]'
check "set takes brief-review, the review of the brief before the task was marked ready" \
  'crew set "$FX" missed_by brief-review && [[ $(crew get "$FX" missed_by) == brief-review ]]'
check "ready accepts a fix that names the task it fixes and the check that missed it" 'crew ready "$FX" >/dev/null && [[ $(crew get "$FX" status) == ready ]]'
FU=$(crew new "Fix the old export" --kind fix); FUI=$(basename "$FU" .md); fill "$FU"; crew set "$FU" missed_by ci
check "ready refuses a fix that doesn't name the task it fixes" \
  '[[ $(crew ready "$FU" 2>&1) == *"fixes must be a merged task in $(basename "$PD"), or unknown: crew set $FUI fixes <value>"* && $(crew get "$FU" status) == draft ]]'
check "set refuses a fixes that isn't a merged task, and records the ID of one that is" \
  '! crew set "$FU" fixes "$FXI" 2>/dev/null && [[ -z $(crew get "$FU" fixes) ]] && crew set "$FU" fixes "$(num "$FC")" && [[ $(crew get "$FU" fixes) == "$FCI" ]]'
crew set "$FU" fixes unknown
check "ready accepts a fix whose cause is unknown" 'crew ready "$FU" >/dev/null && [[ $(crew get "$FU" status) == ready && $(crew get "$FU" fixes) == unknown ]]'
check "set takes fix and refine as kinds" 'crew set "$FU" kind refine && crew set "$FX" kind fix && [[ $(crew get "$FU" kind) == refine ]]'
check "set clears a fix's fixes and missed_by with an empty value, and ready then refuses it" \
  'crew set "$FX" status draft && crew set "$FX" fixes "" && crew set "$FX" missed_by "" && [[ -z $(crew get "$FX" fixes) && -z $(crew get "$FX" missed_by) && $(crew ready "$FX" 2>&1) == *"fixes must be a merged task in"* ]]'
for f in "$FX" "$FU"; do crew set "$f" status abandoned; done

# Proposals from anyone: a title and a paragraph, kept out of the task list until promoted.
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
check "a worker closes its own proposal: links its task, accepts it and logs it" \
  'crew set "$WP" task 007-contestant-type-on-update && crew set "$WP" status accepted && crew log "$WP" "Done in 007 (#7, abc1234)" &&
   [[ $(crew get "$WP" status) == accepted && $(crew get "$WP" task) == 007-contestant-type-on-update && $(section "$WP" "## Log") == *"status → accepted""$NL"*"Done in 007"* ]]'
TP=$(crew propose "Ask why in the rubric" --project "$PJ" --source misses)
check "a proposal with no task, such as a Target: one, can be set accepted" 'crew set "$TP" status accepted && [[ $(crew get "$TP" status) == accepted && -z $(crew get "$TP" task) ]]'
# An accepted proposal keeps the task it became: crew set won't move it, and says why.
set_refused() {  # proposal key value: crew set refuses, names the proposal's task, and changes nothing
  local sum head err
  sum=$(cksum <"$1") head=$(git -C "$L" rev-parse HEAD)
  ! err=$(crew set "$1" "$2" "$3" 2>&1) &&
    [[ $err == *"accepted for task $(crew get "$1" task), which took up its work"*"Leave it as it is, and raise it with the lead."* &&
       $(cksum <"$1") == "$sum" && $(git -C "$L" rev-parse HEAD) == "$head" ]]
}
check "set refuses to move a promoted proposal to another task, and changes nothing" 'set_refused "$SP" task 007-contestant-type-on-update'
check "set refuses to clear an accepted proposal's task" 'set_refused "$SP" task ""'
check "set refuses to change an accepted proposal's status while a task is linked" 'set_refused "$SP" status open'
check "set runs no task checks on a proposal: status done gets the proposal's refusal, not crew hand-over" \
  'set_refused "$SP" status done && [[ $(crew set "$SP" status done 2>&1) != *"hand-over"* ]]'

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
# With two projects, no worker slot is free for the crew only when none is for either.
echo "MAX_WORKERS=0" >>"$PE"
out=$(crew status --all)
check "status says when only a project's own MAX_WORKERS is reached, on that project's line" '[[ $(head -1 <<<"$out") == "workers running: "*"/3" && $out == *"$NL$PJ  workers full (MAX_WORKERS 0 here)$NL"* && $out == *"$NL$P2$NL"* ]]'
sed -i '/^MAX_WORKERS=0$/d' "$PE"; echo "MAX_WORKERS=0" >>"$L/config/crew.env"; echo "MAX_WORKERS=5" >>"$PE"
out=$(crew status --all)
check "status doesn't say nothing dispatches while a project's own higher MAX_WORKERS isn't reached" '[[ $(head -1 <<<"$out") == "workers running: "*"/0" && $out == *"$NL$P2  workers full (MAX_WORKERS 0 here)$NL"* && $out == *"$NL$PJ$NL"* ]]'
sed -i '/^MAX_WORKERS=0$/d' "$L/config/crew.env"; sed -i '/^MAX_WORKERS=5$/d' "$PE"
echo "MAX_WORKERS=0" >>"$PE"; echo "MAX_WORKERS=0" >>"$L/projects/$P2/project.env"
out=$(crew status --all)
check "status names each project's own MAX_WORKERS when every project is full at a limit that isn't the crew's" '[[ $(head -1 <<<"$out") == "workers running: "*"/3: full, nothing dispatches" && $out == *"$NL$PJ  workers full (MAX_WORKERS 0 here)$NL"* && $out == *"$NL$P2  workers full (MAX_WORKERS 0 here)$NL"* ]]'
sed -i '/^MAX_WORKERS=0$/d' "$PE" "$L/projects/$P2/project.env"
out=$(CREW_PROJECT=nope crew status 2>&1)
check "an unknown CREW_PROJECT fails clearly" '[[ $out == *"CREW_PROJECT is '"'"'nope'"'"', which isn'"'"'t a project registered with crew"* ]]'
out=$(cd "$HOME" && crew list --project "$P2")
check "list takes --project" '[[ $out == *001-shop-checkout* && $out != *saved-search* ]]'
PP=$(crew propose "Ship to Canada" --project "$P2")
check "proposals --all is every project" '[[ $(crew proposals --all) == *"$(basename "$PP" .md)"* && $(crew proposals) != *"$(basename "$PP" .md)"* ]]'
check "status points each project's counts at that project, not the default one" '[[ $(crew status --all) == *"$NL$P2$NL"*"  open proposals: 1  (crew proposals --project $P2)"* ]]'
check "sync and cleanup --merged take --project" '(cd "$HOME" && crew sync --project "$P2" >/dev/null && [[ $(crew cleanup --merged --project "$P2") == "Nothing to clean up." ]])'
fill "$S1"; crew ready "$S1" >/dev/null; setstate "done" null
(cd "$HOME" && CREW_PROJECT=$PJ crew dispatch "$S1" >/dev/null 2>&1)
check "a worker's settings name its task's project, not the caller's" 'session_env "$P2"'
check "a worker's launch sets no CREW_PROJECT, which its session wouldn't get" '[[ $(grep -- "--name crew-$P2-001-shop-checkout" "$HOME/claude-env.log" | tail -1) == "unset claude "* ]]'
(cd "$HOME" && CREW_PROJECT=$PJ crew gardener --project "$P2" >/dev/null 2>&1)
check "the gardener runs with its own settings, which load project memory, and its project, but no pause hook" \
  '[[ $(given_settings) == "$(jq -c . "$L/config/gardener-settings.json")" ]] && session_env "$P2" && ! has_pause_hook'
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
# A project with no clone on this machine holds up no other project's sync, whether
# it's run by hand or by a tick. Its name sorts first, so it's met before the others.
NC=$L/projects/0-no-clone; mkdir "$NC"; echo "REMOTE=\"$SB/nowhere.git\"" >"$NC/project.env"
merged_task() {  # title: a dispatched task with a PR, which GitHub reads as merged
  local f; f=$(crew new "$1" --project "$PJ"); fill "$f"; crew ready "$f" >/dev/null; setstate "done" null; crew dispatch "$f" >/dev/null 2>&1
  crew set "$f" pr https://github.com/isaac/webapp/pull/12; echo "$f"; }
FS=$(merged_task "Merged beside a broken project")
out=$(crew sync --all 2>&1); st=$?
check "sync --all syncs the other projects when one can't be loaded, names the one it skipped and why, and exits 1" \
  '((st == 1)) && [[ $(crew get "$FS" status) == merged && $out == *"crew: no clone of 0-no-clone on this machine"* && $out == *"crew: 0-no-clone: skipped"* ]]'
FT=$(merged_task "Merged beside a broken project, by a tick")
err=$(crew tick 2>&1 >/dev/null)
check "a tick records another project's merge while one can't be loaded, naming the one it skipped once, and not as a failed sync" \
  '[[ $(crew get "$FT" status) == merged && $(grep -c "0-no-clone: skipped" <<<"$err") == 1 && $err != *"sync failed"* ]]'
rm -r "$NC"; crew cleanup --merged --project "$PJ" >/dev/null 2>&1


# Pausing: crew pause stops a project's work without losing any. Sessions mid-turn
# are told to wrap up, and stopped once WRAP_UP_SECONDS run out; crew resume lets
# the work go on, and the next tick tells them to carry on.
git -C "$L" rm -q "$M2"; git -C "$L" commit -qm "no mcp.json for $P2 again"
for f in "$L"/projects/*/tasks/*.md; do
  case $(crew get "$f" status) in merged | abandoned) ;; *) crew set "$f" status abandoned ;; esac
done
jobstate() {  # job state pid [status]: setstate, for one job
  jq --arg id "$1" --arg s "$2" --argjson pid "$3" --arg st "${4:-}" \
    'map(if .id == $id then .state=$s | .pid=$pid | (if $st == "" then del(.status) else .status=$st end) else . end)' \
    "$HOME/agents.json" >"$HOME/a" && mv "$HOME/a" "$HOME/agents.json"; }
calls() { wc -l <"$HOME/claude-calls.log"; }
since() { tail -n "+$(($1 + 1))" "$HOME/claude-calls.log"; }  # the claude calls after the first $1
MARK=$L/projects/$PJ/paused
CARRY="crew was paused while you were working. Carry on from where your Log says you stopped."
GPF=$(crew goal new "Paused goal" --project "$PJ"); TGP=$(basename "$GPF" .md); sed -i '/^## Goal$/a "Keep going."' "$GPF"
setstate "done" null; crew goal start "$GPF" >/dev/null
# A dispatched task in project $1, titled $2, with an open PR whose comments crew has read.
paused_task() {
  local f; f=$(crew new "$2" --project "$1"); fill "$f"; crew ready "$f" >/dev/null; setstate "done" null; crew dispatch "$f" >/dev/null 2>&1
  crew set "$f" pr https://github.com/isaac/webapp/pull/12; crew set "$f" feedback_since 2099-01-01T00:00:00Z; echo "$f"; }
echo OPEN >"$HOME/gh-pr-state"
PT=$(paused_task "$PJ" "Busy when paused"); TPT=$(basename "$PT" .md)
Q2=$(paused_task "$P2" "Shop goes on")
PI=$(paused_task "$PJ" "Idle when paused"); TPI=$(basename "$PI" .md)
check "lead/.claude/settings.json has no pause hook, so the user's own leads never wrap up" '! grep -q "pause --hook" "$L/lead/.claude/settings.json"'
hook=$(jq -r '.hooks.PostToolUse[-1].hooks[0].command' "$HOME/claude-settings.last")
setstate "done" null
jobstate "$(crew get "$PT" worker_job)" working 4242 busy; jobstate "$(gget "$GPF" lead_job)" working 4242 busy; jobstate "$(crew get "$PI" worker_job)" idle 4242
echo "WRAP_UP_SECONDS=60" >>"$L/config/crew.env"
n=$(calls)
out=$(timeout 3 crew pause --project "$PJ" 2>&1)
check "crew pause marks the project paused by crew pause, on this host, and commits the mark" \
  '[[ $(sed -n "s/^by: //p" "$MARK") == "crew pause" && $(sed -n "s/^host: //p" "$MARK") == tower && -z $(git -C "$L" status --porcelain -- projects) && $(git -C "$L" log -1 --format=%s -- "$MARK") == "pause $PJ" ]]'
check "it waits for the sessions mid-turn to wrap up, naming them" '[[ $out == *"paused $PJ"*"waiting up to 1 minute for 2 session(s) to wrap up: $TPT, goal $TGP"* ]]'
check "a pause cut short in its wait has already marked the busy sessions interrupted, and stopped none" \
  '[[ -n $(crew get "$PT" interrupted) && -n $(gget "$GPF" interrupted) && -z $(crew get "$PI" interrupted) && $(since "$n") != *"claude stop"* ]]'
out=$(cd "$HOME" && CREW_PROJECT=$PJ bash -c "$hook" </dev/null); st=$?
check "the pause hook, run as a worker's settings give it, tells a session of a paused project to wrap up, as PostToolUse context" \
  '((st == 0)) && [[ $(jq -r .hookSpecificOutput.hookEventName <<<"$out") == PostToolUse && $(jq -r .hookSpecificOutput.additionalContext <<<"$out") == "crew is pausing project $PJ. "*"Log"*"end your turn now." ]]'
out=$(CREW_PROJECT=$P2 crew pause --hook); st=$?
check "the pause hook says nothing to a session of a project that isn't paused, or of every project, and exits 0" \
  '((st == 0)) && [[ -z $out && -z $(CREW_PROJECT=all crew pause --hook) ]] && CREW_PROJECT=nope crew pause --hook'
out=$(crew status --project "$PJ")
check "status shows the project paused, and the task a pause interrupted" \
  '[[ $out == *"$NL$PJ  paused (crew resume --project $PJ)$NL"* && $(grep "$TPT" <<<"$out") == *"interrupted by a pause"* && $(grep "$TPI" <<<"$out") != *interrupted* ]]'
sed -i 's/^WRAP_UP_SECONDS=60$/WRAP_UP_SECONDS=0/' "$L/config/crew.env"
n=$(calls)
out=$(crew pause --project "$PJ" 2>&1)
check "when the wait runs out, crew pause stops the sessions still mid-turn, and logs it on their files" \
  '[[ $out == *"$PJ was already paused"*"stopped 2 session(s): $TPT, goal $TGP"* && $(since "$n") == *"claude stop $(crew get "$PT" worker_job)"* && $(since "$n") == *"claude stop $(gget "$GPF" lead_job)"* ]] &&
   [[ $(tail -1 "$PT") == *"stopped by crew pause before it ended its turn" && $(tail -1 "$GPF") == *"stopped by crew pause before it ended its turn" ]]'
check "an idle session isn't stopped or marked interrupted" '[[ $(since "$n") != *"claude stop $(crew get "$PI" worker_job)"* && -z $(crew get "$PI" interrupted) ]]'
check "crew pause says how to undo it" '[[ $out == *"'"'"'crew resume --project $PJ'"'"' lets the work go on" ]]'
out=$(jq -n --arg s "$(crew get "$PI" worker_session)" '{session_id: $s}' | (cd "$HOME" && CREW_PROJECT=$PJ bash -c "$hook"))
check "the pause hook marks the work of a session it tells to stop interrupted, though it was idle when the pause began" \
  '[[ $out == *"end your turn now"* && -n $(crew get "$PI" interrupted) && -z $(git -C "$L" status --porcelain -- projects) && $(git -C "$L" log -1 --format=%s) == "pause: $TPI interrupted" ]]'
crew set "$PI" interrupted ""
out=$(crew pause --project "$PJ" 2>&1); st=$?
check "pausing a paused project with no session mid-turn says so, and exits 0" '((st == 0)) && [[ $out == "$PJ was already paused"* && $out != *waiting* ]]'
sed -i 's/^by: .*/by: crew upgrade/' "$MARK"; git -C "$L" commit -qm "paused by an upgrade" -- projects
crew pause --project "$PJ" >/dev/null 2>&1
check "crew pause takes over a pause crew upgrade made, so it outlasts the upgrade" '[[ $(sed -n "s/^by: //p" "$MARK") == "crew pause" ]]'
# Nothing starts a session for a paused project.
GPT=$(crew new "Paused goal task" --goal "$GPF" --project "$PJ"); fill "$GPT"; crew ready "$GPT" >/dev/null
GDF=$(crew goal new "Not started" --project "$PJ"); sed -i '/^## Goal$/a "Later."' "$GDF"; crew commit >/dev/null
PS=$(crew new "Stacked while paused" --project "$PJ" --on "$TPI"); crew set "$PS" worker_job s7ack3d0; sed -i "s/^status: .*/status: pr-open/" "$PS"  # crew set refuses pr-open
PM=$(crew new "Merged parent" --project "$PJ")
PSM=$(crew new "Stacked on a merged task" --project "$PJ" --on "$(basename "$PM" .md)"); fill "$PSM"; crew ready "$PSM" >/dev/null
crew set "$PM" status merged; psm_base=$(crew get "$PSM" base)
head=$(git -C "$L" rev-parse @); n=$(launches)
out=$(crew followup "$PI" --note "go on" 2>&1); st=$?
check "while paused, crew followup holds back with exit 4, naming crew resume" '((st == 4)) && [[ $out == "crew: $PJ is paused; '"'"'crew resume --project $PJ'"'"' lets its work go on" ]]'
out=$(crew dispatch "$GPT" 2>&1); st=$?
check "while paused, crew dispatch holds back with exit 4 and makes no worktree" \
  '((st == 4)) && [[ $out == *"crew resume --project $PJ"* && ! -d $HOME/agent-work/$PJ/$(basename "$GPT" .md) && $(crew get "$GPT" status) == ready ]]'
out=$(crew goal start "$GDF" 2>&1); st=$?
check "while paused, crew goal start holds back with exit 4 and makes no checkout" \
  '((st == 4)) && [[ $out == *"crew resume --project $PJ"* && ! -d $HOME/agent-work/$PJ/_goal-$(basename "$GDF" .md) && $(gget "$GDF" status) == draft ]]'
out=$(crew dispatch "$PSM" 2>&1); st=$?
check "while paused, crew dispatch holds back before it rebases a task whose parent merged" '((st == 4)) && [[ $(crew get "$PSM" base) == "$psm_base" ]]'
out=$(crew restack "$PS" 2>&1); st=$?
check "while paused, crew restack holds back with exit 4" '((st == 4)) && [[ $out == *"crew resume --project $PJ"* ]]'
out=$(crew goal followup "$GPF" 2>&1); st=$?
check "while paused, crew goal followup holds back with exit 4, keeping interrupted for later" \
  '((st == 4)) && [[ $out == *"crew resume --project $PJ"* && -n $(gget "$GPF" interrupted) ]]'
check "and they change nothing in the ledger and start no session" '[[ $(git -C "$L" rev-parse @) == "$head" && -z $(git -C "$L" status --porcelain -- projects) && $(launches) == "$n" ]]'
rounds=$(crew get "$PI" review_rounds)
out=$(crew review "$PI" 2>&1); st=$?
check "while paused, crew review records the round and says it goes out after crew resume" \
  '((st == 0)) && [[ $out == *"is paused, so crew tick sends it once '"'"'crew resume --project $PJ'"'"'"* && $(crew get "$PI" review_rounds) == $((rounds + 1)) && $(launches) == "$n" ]]'
out=$(crew gardener --project "$PJ" 2>&1)
check "crew gardener skips a paused project" '[[ $out == "$PJ: paused; skipped" && $(launches) == "$n" ]]'
crew set "$PT" review_rounds $(($(crew get "$PT" review_rounds) + 1)); crew set "$Q2" review_rounds $(($(crew get "$Q2" review_rounds) + 1))
setstate "done" null; n=$(launches)
out=$(crew tick 2>&1)
check "while paused, crew tick sends the project's workers and goal leads nothing and dispatches none of its tasks, saying why, but serves another project" \
  '[[ $out == *"$(basename "$Q2" .md): round"*"sent to its worker"* && $out == *"$TPT: round 1 waits: $PJ is paused"* && $out == *"goal $TGP: input waits: $PJ is paused"* && $out == *"$(basename "$GPT" .md): dispatch waits: $PJ is paused"* && $(launches) == $((n + 1)) && $(crew get "$GPT" status) == ready && -n $(crew get "$PT" interrupted) && -n $(gget "$GPF" interrupted) ]]'
out=$(prompt held)
check "a lead hears what a paused project's work waits for" \
  '[[ $out == *"$HELD"*"$NL- $PJ: $TPT: round 1 waits: $PJ is paused"* && $out == *"$NL- $PJ: goal $TGP: input waits: $PJ is paused"* && $out == *"$NL- $PJ: $(basename "$GPT" .md): dispatch waits: $PJ is paused"* ]]'
out=$(crew resume --project "$PJ")
check "crew resume removes the mark, commits, and says the next tick follows up" \
  '[[ ! -e $MARK && $out == "resumed $PJ$NL""the next crew tick follows up the work the pause interrupted" && -z $(git -C "$L" status --porcelain -- projects) && $(git -C "$L" log -1 --format=%s) == "resume $PJ" ]]'
setstate "done" null; decisions=$(section "$GPF" "## Decisions"); n=$(calls)
out=$(crew tick 2>&1); new=$(since "$n"); ptline=$(grep -F "New input for task $TPT (" <<<"$new")
check "after crew resume, the next tick follows up the interrupted worker, telling it to carry on, and clears interrupted" \
  '[[ $out == *"$TPT: round"*"sent to its worker"* && $ptline == *"$CARRY"* && -z $(crew get "$PT" interrupted) ]]'
check "it follows up the interrupted goal lead the same way, writing nothing under the goal's Decisions" \
  '[[ $out == *"goal $TGP: input sent to its goal lead"* && $new == *"New input for goal $TGP (goal file: $GPF):$NL- $CARRY$NL"* && -z $(gget "$GPF" interrupted) && $(section "$GPF" "## Decisions") == "$decisions" ]]'
crew set "$PI" interrupted "$(date -Iseconds)"; setstate "done" null; n=$(calls)
crew followup "$PI" --note "check the docs" >/dev/null 2>&1
check "a hand-run crew followup --note also tells the worker to carry on, and clears interrupted" \
  '[[ $(since "$n") == *"New input for task $TPI ("*"$CARRY Note from the lead"*"check the docs"* && -z $(crew get "$PI" interrupted) ]]'
CREW_LEAD_FINGERPRINT='' hand_over "$PI"; crew set "$PI" interrupted "$(date -Iseconds)"; setstate "done" null; n=$(calls)
crew followup "$PI" >/dev/null 2>&1
check "a followup whose only news is the pause leaves the status as the worker last set it" \
  '[[ $(since "$n") == *"New input for task $TPI ("*"$CARRY"* && $(crew get "$PI" status) == pr-open && -z $(crew get "$PI" interrupted) ]]'
crew set "$PI" interrupted "$(date -Iseconds)"; setstate "done" null; n=$(calls)
out=$(crew tick 2>&1)
check "tick tells an idle worker whose turn a pause cut short to carry on" '[[ $out == *"$TPI: its worker carries on after the pause"* && $(since "$n") == *"New input for task $TPI ("*"$CARRY"* ]]'
as_worker set "$PI" status needs-input
# Every kind of thing crew tick did since the lead's previous prompt, and the statuses a worker set.
out=$(<"$HOME/news-heard")
for heard in "merged; worktree cleaned up" "review round 1 recorded" "comment(s) from collaborators, for you" "recorded: red on" \
  " CI rounds the branch caused, for you" "recorded: conflicts with main" "a fresh worker picks it up" "round 1 sent to its worker" \
  "restack sent to its worker (014-tick-parent moved)" "restack sent to its worker (PR targets agent/tick-old, not main)" "waiting on you (1 open question(s))" \
  "dispatched for goal" "goal $G: input sent to its goal lead" "goal $G: done, every task finished" "$TPI: its worker carries on after the pause" \
  "nudged it" "can't be nudged, for the lead" "$TPI: handed over (pr-open)" "$TPI: status → needs-input"; do
  check "a lead hears what crew did since its previous prompt: $heard" '[[ $out == *"$NL- "????-??-??" "??:??" "*$heard* ]]'
done
# Each time it heard anything, the lead got one paragraph: crew's lines under the heading, each with its time.
check "each time a lead hears news, it's under one heading, with each line timed, and nothing a lead set itself" \
  '[[ $out != *"status → abandoned"* ]] && awk -v did="$DID" -v held="$HELD" '"'"'BEGIN { RS = ""; FS = "\n" }
    index($0, did "\n") != 1 && index($0, held "\n") != 1 { exit 1 }
    index($0, did) == 1 { for (i = 2; i <= NF && $i != held; i++) if ($i !~ /^- [0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2} /) exit 1 }'"'"' "$HOME/news-heard"'
setstate "done" null; jobstate "$(crew get "$PT" worker_job)" working 4242 busy; touch "$HOME/stop-noop"; log=$(section "$PT" "## Log")
out=$(crew pause --project "$PJ" 2>&1); st=$?
check "crew pause fails when a session doesn't stop, names it, and logs nothing on its file" \
  '((st == 1)) && [[ $out == *"1 session(s) didn'"'"'t stop: $TPT"* && $out != *"stopped 1"* && $(section "$PT" "## Log") == "$log" ]]'
rm -f "$HOME/stop-noop"; crew resume --project "$PJ" >/dev/null
sed -i 's/^WRAP_UP_SECONDS=0$/WRAP_UP_SECONDS=60/' "$L/config/crew.env"
setstate "done" null; jobstate "$(crew get "$PT" worker_job)" working 4242 busy; n=$(calls)
timeout 20 crew pause --project "$PJ" >"$HOME/pause.out" 2>&1 &
pid=$!
for _ in {1..50}; do [[ -e $MARK ]] && break; sleep 0.1; done
crew resume --project "$PJ" >/dev/null; wait "$pid"; st=$?
check "a pause still waiting when its project is resumed stops nothing, and says the session goes on" \
  '((st == 0)) && [[ $(<"$HOME/pause.out") == *"$PJ was resumed; $TPT goes on"* && $(since "$n") != *"claude stop"* ]]'
sed -i 's/^WRAP_UP_SECONDS=60$/WRAP_UP_SECONDS=0/' "$L/config/crew.env"
PF=$(paused_task "$PJ" "Abandoned but running"); crew set "$PF" status abandoned
setstate "done" null; jobstate "$(crew get "$PT" worker_job)" working 4242 busy; jobstate "$(crew get "$PI" worker_job)" idle 4242
jobstate "$(crew get "$PF" worker_job)" working 4242 busy; n=$(calls)
(cd "$(crew get "$PT" worktree)" && exec setsid sleep 300) & IN_PT=$!
(cd "$(gget "$GPF" checkout)" && exec setsid sleep 300) & IN_GPF=$!
out=$(crew pause --project "$PJ" --force 2>&1)
check "crew pause --force stops the project's sessions at once, busy or idle, and logs it" \
  '[[ $(sed -n "s/^by: //p" "$MARK") == "crew pause --force" && $out != *waiting* && $(since "$n") == *"claude stop $(crew get "$PT" worker_job)"* && $(since "$n") == *"claude stop $(crew get "$PI" worker_job)"* ]] &&
   [[ $(tail -1 "$PT") == *"stopped by crew pause --force" && $(tail -1 "$PI") == *"stopped by crew pause --force" && $(since "$n") != *"claude stop $(crew get "$Q2" worker_job)"* ]]'
check "stopping a session also ends what it left running in its worktree" '[[ $out == *"stopping 1 process(es) still running in $(crew get "$PT" worktree)"* && ! -e /proc/$IN_PT/cwd ]]'
check "--force also ends what a session that had already closed left running in its checkout" \
  '[[ $out == *"stopping 1 process(es) still running in $(gget "$GPF" checkout)"* && ! -e /proc/$IN_GPF/cwd && $(tail -1 "$GPF") != *"stopped by crew pause --force" ]]'
kill "$IN_PT" "$IN_GPF" 2>/dev/null || true
check "--force marks the work of every session it stops interrupted, idle ones too" '[[ -n $(crew get "$PT" interrupted) && -n $(crew get "$PI" interrupted) ]]'
check "--force also stops a worker still running for an abandoned task, without marking its work interrupted" \
  '[[ $(since "$n") == *"claude stop $(crew get "$PF" worker_job)"* && -z $(crew get "$PF" interrupted) ]]'
crew resume --project "$PJ" >/dev/null
touch "$HOME/agents-fail"
out=$(crew pause --project "$PJ" 2>&1); st=$?
check "crew pause fails, changing nothing, when claude agents can't say which sessions are mid-turn" \
  '((st == 1)) && [[ $out == *"couldn'"'"'t list the sessions with claude agents"* && ! -e $MARK && -z $(git -C "$L" status --porcelain -- projects) ]]'
rm -f "$HOME/agents-fail"
mv "$L/projects/$P2/local.env" "$HOME/local.env.saved"
out=$(crew pause --all 2>&1); st=$?
check "crew pause --all also pauses a project with no clone on this machine" '((st == 0)) && [[ -e $L/projects/$P2/paused && -e $MARK ]]'
check "crew status shows the pause of a project with no clone here" '[[ $(crew status --project "$P2") == *"$P2  paused (crew resume --project $P2)"* ]]'
out=$(crew resume --all 2>&1); st=$?
check "and crew resume --all lifts its pause" '((st == 0)) && [[ ! -e $L/projects/$P2/paused && ! -e $MARK ]]'
mv "$HOME/local.env.saved" "$L/projects/$P2/local.env"

# crew stats, over a project of its own: three tasks merged since the date asked
# for, and two before it. The first, low risk, had a review round with one
# comment, and came back clean on its second automated review; the second,
# medium risk, had its question wait 2h, and its PR (#35) was reverted in the
# project's clone. 005 is a fix of 004, a low-risk task merged 60 days before.
SP=$L/projects/stats; mkdir -p "$SP/tasks"; echo "DEFAULT_BRANCH=main" >"$SP/project.env"
for t in 001-stats-one 002-stats-two 003-stats-old 004-stats-cause 005-stats-fix; do
  sed -e 's/^status: .*/status: merged/' -e 's/^kind: .*/kind: build/' "$L/templates/task.md" >"$SP/tasks/$t.md"
done
sed -i -e '/^## Automated reviews$/a ### Automated review 1 — 2026-11-06 11:00\n- Reviewers: Claude reviewer\n#### 1. [should fix] a — `a.go:1`\n- Agreed severity: should fix\n- Outcome: fixed in abc1234\n### Automated review 2 — 2026-11-06 11:30\n- Reviewers: Claude reviewer\nNo findings.' \
  -e '/^## Review rounds$/a ### Round 1 — 2026-11-06 13:00 (PR #3)\n**Conversation comments**\n- [x] comment 5 — [link](u5)\n  > rename it' "$SP/tasks/001-stats-one.md"
sed -i 's/^risk:$/risk: low/' "$SP/tasks/001-stats-one.md" "$SP/tasks/004-stats-cause.md" "$SP/tasks/005-stats-fix.md"
sed -i -e 's/^risk:$/risk: medium/' -e 's|^pr:$|pr: https://github.com/o/stats/pull/35|' "$SP/tasks/002-stats-two.md"
sed -i -e 's/^kind: .*/kind: fix/' -e 's/^fixes:$/fixes: 004-stats-cause/' -e 's/^missed_by:$/missed_by: tests/' "$SP/tasks/005-stats-fix.md"
printf -- '- 2026-11-06 09:00 — status → working\n- 2026-11-06 12:00 — status → pr-open\n- 2026-11-06 13:00 — status → addressing-feedback\n- 2026-11-06 14:00 — status → pr-open\n- 2026-11-06 16:00 — status → merged\n' >>"$SP/tasks/001-stats-one.md"
printf -- '- 2026-11-07 09:00 — status → needs-input\n- 2026-11-07 11:00 — status → working\n- 2026-11-07 12:00 — status → pr-open\n- 2026-11-08 12:00 — status → merged\n' >>"$SP/tasks/002-stats-two.md"
printf -- '- 2026-10-20 12:00 — status → merged\n' >>"$SP/tasks/003-stats-old.md"
printf -- '- 2026-09-06 12:00 — status → merged\n' >>"$SP/tasks/004-stats-cause.md"
printf -- '- 2026-11-09 12:00 — status → merged\n' >>"$SP/tasks/005-stats-fix.md"
crew commit "stats fixtures" >/dev/null
git init -q --bare "$SB/stats.git" && git clone -q "$SB/stats.git" "$SB/stats" 2>/dev/null
(cd "$SB/stats" && echo two >two && git add two && git commit -qm "Merge pull request #35 from me/two" &&
  GIT_COMMITTER_DATE=2026-11-10T12:00:00 git revert --no-edit HEAD >/dev/null && git push -q origin HEAD:main)
echo "REPO=\"$SB/stats\"" >"$SP/local.env"
head=$(git -C "$L" rev-parse @); dirty=$(git -C "$L" status --porcelain)
out=$(cd "$HOME" && crew stats --project stats --since 2026-11-05); rows=$(tr -s ' ' <<<"$out")
check "stats has a row for each task merged since the date: kind, rounds, comments, PR and question waits, reviews, clean after, findings, later fixes, reverts" \
  '[[ $rows == *" 001-stats-one build 1 1 3h - 2 2 1 0 0 0 0"* && $rows == *" 002-stats-two build 0 0 24h 2h 0 - 0 0 0 0 1"* && $rows == *" 005-stats-fix fix 0 0 - - 0 - 0 0 0 0 0"* ]]'
check "stats gives the totals and the median waits" '[[ $rows == *" total 1 1 27h 2h 2 1 0 0 0 1"*" median 3h -"* ]]'
check "stats leaves out a task merged before the date" \
  '[[ $out == "stats: 3 task(s) merged since 2026-11-05"* && $out != *003-stats-old* && $(crew stats --project stats --since 2026-10-01) == *003-stats-old* ]]'
check "a task merged before the date shows its later fix in its row" \
  '[[ $(crew stats --project stats --since 2026-09-01 | tr -s " ") == *" 004-stats-cause build 0 0 - - 0 - 0 0 0 1 0"* ]]'
check "stats groups the tasks by risk tier: merged, median rounds, comments per task, and the later fixes and reverts traced to the tier" \
  '[[ $rows == *"By risk tier:"*" low 2 0.5 0.5 1 0"*" medium 1 0 0 0 1"*" high 0 - - 0 0"*" no risk tier 0 - - 0 0"* ]]'
check "stats warns when low-risk PRs drew more comments than medium ones" \
  '[[ $out == *"Low-risk PRs drew more comments than medium ones: the tiers or SENSITIVE_PATHS may be off."* ]]'
later=$'Later fixes and reverts since 2026-11-05:\n  005-stats-fix fixes 004-stats-cause (low risk), missed by tests\n  revert of #35 → 002-stats-two (medium risk)\n  missed by: tests 1'
check "stats traces a fix and a revert since the date to their tasks and tiers, though the fixed task merged earlier, and counts what missed the fixes" \
  '[[ $out == *"$later" ]]'
check "stats changes nothing in the home" '[[ $(git -C "$L" rev-parse @) == "$head" && $(git -C "$L" status --porcelain) == "$dirty" ]]'
check "stats refuses a date it can't read" '[[ $(crew stats --project stats --since 11/05 2>&1) == *"--since takes a date, YYYY-MM-DD, not '"'"'11/05'"'"'"* ]]'
git clone -q --depth 1 "file://$SB/stats.git" "$SB/stats-shallow" 2>/dev/null
echo "REPO=\"$SB/stats-shallow\"" >"$SP/local.env"
out=$(crew stats --project stats --since 2026-11-05 2>&1); st=$?
check "with a shallow clone, stats says it didn't check for reverts, and why" \
  '((st == 0)) && [[ $out == *"  reverts: not checked ($SB/stats-shallow is a shallow clone; git fetch --unshallow there gets its whole history)" && $out != *"revert of"* ]]'
rm "$SP/local.env"
out=$(crew stats --project stats --since 2026-11-05 2>&1); st=$?
check "without the project's clone, stats says it didn't check for reverts, and still reports" \
  '((st == 0)) && [[ $out == *"005-stats-fix fixes 004-stats-cause"*"  reverts: not checked (no clone of the project on this machine)" && $out != *"revert of"* ]]'

# The misses agent, run by crew gardener: it reads the project's tasks merged after
# misses.last that have a comment of the user's in a round after an automated
# review. 090 has one; 091's only round after its review reports red CI; 092 has
# one but merged before the cursor.
MT=$PD/tasks
for t in 090-misses-one 091-misses-ci 092-misses-old; do sed 's/^status: .*/status: merged/' "$L/templates/task.md" >"$MT/$t.md"; done
review_then_round() {  # file day: a review at 11:00 that day, then a round at 13:00 with the lines that follow
  local f=$1 day=$2; shift 2
  sed -i -e "/^## Automated reviews\$/a ### Automated review 1 — $day 11:00\\n- Reviewers: Claude reviewer\\nNo findings." \
    -e "/^## Review rounds\$/a ### Round 1 — $day 13:00 (PR #9)\\n$*" "$f"
}
review_then_round "$MT/090-misses-one.md" 2026-11-06 '**Conversation comments**\n- [x] comment 5 — [link](u5)\n  > name it for the domain'
review_then_round "$MT/091-misses-ci.md" 2026-11-07 '- [x] CI failed on aaa1111: test'
review_then_round "$MT/092-misses-old.md" 2026-11-04 '**Conversation comments**\n- [x] comment 6 — [link](u6)\n  > too late'
printf -- '- 2026-11-06 16:00 — status → merged\n' >>"$MT/090-misses-one.md"
printf -- '- 2026-11-07 16:00 — status → merged\n' >>"$MT/091-misses-ci.md"
printf -- '- 2026-11-04 16:00 — status → merged\n' >>"$MT/092-misses-old.md"
date -d '2026-11-05 00:00' --iso-8601=seconds >"$PD/misses.last"
git -C "$SB/webapp" rev-parse origin/main >"$PD/gardener.last"   # no new commits for the gardener
crew commit "misses fixtures" >/dev/null
n=$(wc -l <"$HOME/claude-calls.log")
out=$(crew gardener --project "$PJ" 2>&1); st=$?
call=$(tail -n +$((n + 1)) "$HOME/claude-calls.log")
check "with no new commits, crew gardener still runs the misses agent, with the gardener's settings" \
  '((st == 0)) && [[ $out == *"$PJ: no new commits since the last run"*"$PJ: sorting your comments on 1 merged task(s)"* && $call == *"--agent misses "* && $call != *"--agent gardener "* && $(given_settings) == "$(jq -c . "$L/config/gardener-settings.json")" ]]'
check "it passes the task merged after misses.last with a comment after a review, with that comment, and no other task" \
  '[[ $call == *"- $MT/090-misses-one.md"$'"'"'\n'"'"'"  - round 1, after automated review 1: comment 5 — [link](u5)"* && $call != *091-misses-ci* && $call != *092-misses-old* ]]'
check "it records the newest merge time it read in misses.last, and commits" \
  '[[ $(date -d "$(cat "$PD/misses.last")" +%s) == $(date -d "2026-11-06 16:00" +%s) && $(git -C "$L" log -1 --format=%s) == "misses $PJ (1 tasks)" && -z $(git -C "$L" status --porcelain -- projects) ]]'
n=$(wc -l <"$HOME/claude-calls.log")
out=$(crew gardener --project "$PJ" 2>&1)
check "a second run with nothing new starts no session" \
  '[[ $out == *"$PJ: no newly merged tasks with your comments after automated review"* && $(wc -l <"$HOME/claude-calls.log") == "$n" ]]'
# The gardener fails on a new commit; the misses agent still runs and records its own progress.
sed 's/^status: .*/status: merged/' "$L/templates/task.md" >"$MT/093-misses-two.md"
review_then_round "$MT/093-misses-two.md" 2026-11-08 '**Conversation comments**\n- [x] comment 7 — [link](u7)\n  > split this'
printf -- '- 2026-11-08 16:00 — status → merged\n' >>"$MT/093-misses-two.md"
crew commit "misses fixture" >/dev/null
(cd "$SB/webapp" && git commit -q --allow-empty -m "more" && git push -q)
glast=$(cat "$PD/gardener.last"); echo gardener >"$HOME/fail-agent"
out=$(crew gardener --project "$PJ" 2>&1); st=$?
rm "$HOME/fail-agent"
check "when the gardener fails, the misses agent still runs and moves its own cursor, and crew gardener exits 1" \
  '((st == 1)) && [[ $out == *"$PJ: gardener exited with 1"*"$PJ: sorting your comments on 1 merged task(s)"* && $(cat "$PD/gardener.last") == "$glast" && $(date -d "$(cat "$PD/misses.last")" +%s) == $(date -d "2026-11-08 16:00" +%s) ]]'
MP=$(crew propose "Name things for the domain" --source misses --project "$PJ")
check "crew propose takes --source misses, and names the misses agent as its author" \
  '[[ $(crew get "$MP" source) == misses && $(section "$MP" "## Log") == *"proposed by the misses agent"* ]]'

# crew review-eval, over a merged task whose automated review read the PR's two
# commits. Round 1, before the review, has a comment of the user's; round 2,
# after it, has two: each is a case, and so is the fix task naming it. The PR's
# commits are only on origin's refs/pull/77/head, as after a squash merge.
git clone -q "$SB/remote.git" "$SB/pr77" 2>/dev/null
(cd "$SB/pr77" && echo a >a.go && git add -A && git commit -qm "Add a" && echo b >b.go && git add -A &&
  git commit -qm $'Add b\n\n## Why\nb is needed' && git push -q origin HEAD:refs/pull/77/head)
EF=$(git -C "$SB/pr77" rev-parse HEAD~2) EH=$(git -C "$SB/pr77" rev-parse HEAD)
ET=$PD/tasks/095-eval-one.md EX=$PD/tasks/096-eval-fix.md
sed -e 's/^status: .*/status: merged/' -e "s/^project: .*/project: $PJ/" -e 's/^kind: .*/kind: build/' -e 's/^base: .*/base: main/' -e 's|^pr:.*|pr: https://github.com/isaac/webapp/pull/77|' \
  -e '/^## Goal$/a > Make it fast' \
  -e "/^## Automated reviews\$/a ### Automated review 1 — 2026-11-10 10:00\\n\\n- Range: \`$EF..$EH\`, 2 commits on \`b\` since \`main\` (fork point \`$EF\`)\\n- Reviewers: Claude reviewer\\n\\nNo findings." \
  -e '/^## Review rounds$/a ### Round 1 — 2026-11-10 09:00 (PR #77)\n\n**Conversation comments**\n- [x] comment 3 — [link](u3)\n  > an earlier wish\n\n### Round 2 — 2026-11-10 13:00 (PR #77)\n\n**Conversation comments**\n- [x] comment 5 — [link](u5)\n  > b.go has no test\n\n**Inline comments**\n- [x] `a.go:1` — comment 6 — [link](u6)\n  > why is a.go here?' \
  "$L/templates/task.md" >"$ET"
sed -e 's/^status: .*/status: merged/' -e "s/^project: .*/project: $PJ/" -e 's/^kind: .*/kind: fix/' -e 's/^fixes:.*/fixes: 095-eval-one/' -e 's/^title: .*/title: Fix the b race/' "$L/templates/task.md" >"$EX"
crew commit "review eval fixtures" >/dev/null
EW=$(crew config WORK_ROOT --project "$PJ")/$PJ/_eval
judged() { grep -c -- '^claude -p --tools  --permission-prompts none ' <<<"$1"; }
echo "b.go has no test" >"$HOME/judge-finds"
n=$(calls)
out=$(crew review-eval --project "$PJ" 2>&1); st=$?
call=$(since "$n"); EV=$(ls "$PD"/review-eval/*.md); EB=$(ls "$L"/logs/review-eval-"$PJ"-*/095-eval-one-review-1.briefing.md)
check "crew review-eval reruns the reviewer once for the review's three cases, on its range, in its own checkout of the PR's head fetched from origin" \
  '((st == 0)) && [[ $(grep -c -- "--agent reviewer " <<<"$call") == 1 && $call == *"Review \`$EF..HEAD\` in \`$EW\`"* && $call == *"--settings $L/config/readonly-settings.json "* && $(git -C "$EW" rev-parse HEAD) == "$EH" ]]'
check "the briefing has the skill's headings and the user's words from before the review, and none of the misses" \
  '[[ $(head -1 "$EB") == "Code review" && $(grep "^## " "$EB" | tr "\n" ,) == "## Scope,## How to verify,## Hard constraints,## Stated intent," ]] &&
   grep -qx "> Make it fast" "$EB" && grep -qx "> an earlier wish" "$EB" && grep -qx "#### Why" "$EB" && ! grep -q "b.go has no test\|why is a.go here\|b race" "$EB"'
check "a judge weighs each case's miss, with an inline comment's place and the user's earlier words, against the report" \
  '[[ $(judged "$call") == 3 && $call == *"## The miss"*"b.go has no test"*"## The brief, and what the user had said before the review"*"> an earlier wish"*"## The report"*"A stub finding"* && $call == *"On \`a.go:1\`: why is a.go here?"* && $call == *"Fix the b race"* ]]'
check "the report gives each case's verdict and range, the totals and what was tested, and is committed" \
  '[[ $(grep -c "^- \`095-eval-one/" "$EV") == 3 ]] && grep -qF -- "- \`095-eval-one/round-2/comment-5\` **found** — \`${EF:0:7}..${EH:0:7}\` — b.go has no test — #### 1. [should fix] A stub finding" "$EV" &&
   grep -qF -- "- \`095-eval-one/round-2/comment-6\` **missed** — \`${EF:0:7}..${EH:0:7}\` — On \`a.go:1\`: why is a.go here?" "$EV" && grep -qF -- "- \`095-eval-one/fix-096-eval-fix\` **missed**" "$EV" && grep -qx "Found 1 of 3 misses." "$EV" &&
   grep -qF -- "- Rubric: \`$L/.claude/skills/pr-review/rubric.md\` (" "$EV" && grep -qF -- "- Reviewer: \`$HOME/.claude/agents/reviewer.md\` (" "$EV" && grep -qx -- "- Claude Code: 2.1.296" "$EV" && grep -qx -- "- Cases: all" "$EV" &&
   [[ $out == *"Found 1 of 3 misses. Report: $EV"* && $(git -C "$L" log -1 --format=%s) == "review eval $PJ (3 cases)" && -z $(git -C "$L" status --porcelain -- projects) ]]'
# A second run, against another rubric, finds the other comment instead.
mv "$EV" "$PD/review-eval/2026-11-14-090000.md" && crew commit "an earlier eval" >/dev/null
echo "why is a.go here?" >"$HOME/judge-finds"; echo "Look harder." >"$SB/rubric.md"
n=$(calls)
out=$(crew review-eval --project "$PJ" --rubric "$SB/rubric.md" 2>&1); st=$?
call=$(since "$n"); EV=$(ls "$PD"/review-eval/*.md | tail -1); EV2=$(basename "$EV")
check "a second run compares verdicts on the cases both runs have, and says the rubric changed" \
  '((st == 0)) && grep -q "^Compared with the last full run, \`2026-11-14-090000.md\`: 1 now found, 1 now missed, of 3 shared case(s); 0 new case(s). Since then: the rubric changed (.* → .*); the reviewer'"'"'s prompt is the same; Claude Code is the same.$" "$EV" &&
   [[ $out == *"1 now found, 1 now missed"* ]]'
check "--rubric has the reviewer follow that rubric, and the report names it" \
  '[[ $call == *"Follow the rubric at \`$SB/rubric.md\`"* ]] && grep -qF -- "- Rubric: \`$SB/rubric.md\` (" "$EV"'
sleep 1; n=$(calls)   # a report is named for the second its run started
out=$(crew review-eval --project "$PJ" --limit 1 2>&1); st=$?
call=$(since "$n")
check "--limit 1 runs only the first case, and its report says so" \
  '((st == 0)) && [[ $(grep -c -- "--agent reviewer " <<<"$call") == 1 && $(judged "$call") == 1 && $out == *"Found 0 of 1 misses."* ]] && grep -qx -- "- Cases: --limit 1" "$(ls "$PD"/review-eval/*.md | tail -1)"'
# A pause, or a new rubric or reviewer from crew upgrade, during a review stops
# the run: it starts no more sessions, and its report is never a baseline.
sleep 1; echo "$PD/paused" >"$HOME/reviewer-writes"; n=$(calls)
out=$(crew review-eval --project "$PJ" 2>&1); st=$?
rm "$PD/paused"; call=$(since "$n"); EV=$(ls "$PD"/review-eval/*.md | tail -1)
check "a pause while the reviewer runs stops the run: no judge starts, and the report says it was cut short" \
  '((st == 0)) && [[ $(grep -c -- "--agent reviewer " <<<"$call") == 1 && $(judged "$call") == 0 ]] && grep -qx -- "- Cases: all, cut short: the project was paused" "$EV" &&
   grep -qx "Found 0 of 0 misses. Not run: 3." "$EV" && grep -qF -- "- \`095-eval-one/fix-096-eval-fix\` **not run** — \`${EF:0:7}..${EH:0:7}\` — Fix the b race — the project was paused" "$EV"'
sleep 1; echo "$SB/rubric.md" >"$HOME/reviewer-writes"; n=$(calls)
out=$(crew review-eval --project "$PJ" --rubric "$SB/rubric.md" 2>&1); st=$?
call=$(since "$n")
check "a rubric that changes while the reviewer runs stops the run the same way" \
  '((st == 0)) && [[ $(judged "$call") == 0 ]] && grep -qx -- "- Cases: all, cut short: the rubric changed" "$(ls "$PD"/review-eval/*.md | tail -1)"'
sleep 1; echo reviewer >"$HOME/fail-agent"; n=$(calls)
out=$(crew review-eval --task 095-eval-one 2>&1); st=$?
rm "$HOME/fail-agent"; call=$(since "$n"); EV=$(ls "$PD"/review-eval/*.md | tail -1)
check "when the reviewer fails, its cases aren't run or judged, and the report says so and compares with the last full run, not a cut-short one" \
  '((st == 0)) && [[ $(judged "$call") == 0 ]] && grep -q "^Compared with the last full run, \`$EV2\`" "$EV" && grep -qF -- "- \`095-eval-one/round-2/comment-5\` **not run** — \`${EF:0:7}..${EH:0:7}\` — b.go has no test — the reviewer exited with 1; see 095-eval-one-review-1.report.log" "$EV" && grep -qx "Found 0 of 0 misses. Not run: 3." "$EV"'
n=$(calls)
out=$(crew review-eval --project "$P2" --task 095-eval-one 2>&1); st=$?
check "--task looks for the task in the project --project names" \
  '((st == 1)) && [[ $out == *"--task '"'"'095-eval-one'"'"' doesn'"'"'t match exactly one task in $P2"* && $(calls) == "$n" ]]'
printf -- '---\nby: crew pause\n---\n' >"$PD/paused"
out=$(crew review-eval --project "$PJ" 2>&1); st=$?
rm "$PD/paused"
check "a paused project's review eval waits, starting nothing" '((st == 4)) && [[ $out == *"$PJ is paused"* && $(calls) == "$n" ]]'
out=$(flock "$L/.crew-eval-$PJ.lock" crew review-eval --project "$PJ" 2>&1); st=$?
check "a second review eval of a project refuses while one runs" '((st == 1)) && [[ $out == *"a review eval of $PJ is running already"* && $(calls) == "$n" ]]'
out=$(crew review-eval --project "$PJ" --task 090-misses-one 2>&1); st=$?
check "with no cases, crew review-eval says so, starts no session and writes no report" \
  '((st == 0)) && [[ $out == "$PJ: no cases yet: none of its merged tasks has a comment of yours after an automated review, or a fix naming it" && $(calls) == "$n" && $(ls "$PD"/review-eval | wc -l) == 6 ]]'

# What the lead's hook tells each session (news.go), with lines of news from workers'
# statuses: NA is in one project, NB in the other.
NA=$(crew new "News here, for a lead that has been away a while" --project "$PJ"); TNA=$(basename "$NA" .md)
NB=$(crew new "News there" --project "$P2"); TNB=$(basename "$NB" .md)
as_worker set "$NA" status needs-input
prompt turns-1 SessionStart >/dev/null
check "a new session hears nothing from before it started, at its start or its first prompt" '[[ -z $(prompt turns-1) && -z $(prompt turns-2) ]]'
CREW_PROJECT=$PJ prompt here >/dev/null; CREW_PROJECT=$P2 prompt there >/dev/null
crew lead hello >/dev/null 2>&1; fp=$(tail -1 "$HOME/claude-fingerprint.log" | cut -d" " -f1)
CREW_LEAD_FINGERPRINT=$fp prompt lead >/dev/null; CREW_LEAD_FINGERPRINT='' prompt goal-lead SessionStart >/dev/null
check "a prompt with nothing new since the previous one gets nothing added" '[[ -z $(prompt turns-1) ]]'
as_worker set "$NA" status draft; crew set "$NB" status needs-input
out=$(prompt turns-1)
check "a prompt hears the status a worker set, naming its task, and not one the lead set" '[[ $out == "$DID$NL- "????-??-??" "??:??" $PJ: $TNA: status → draft" ]]'
as_worker set "$NB" status draft
check "two sessions taking turns each hear what's new since their own previous prompt" \
  '[[ $(prompt turns-1) == "$DID$NL- "*" $TNB: status → draft" && $(prompt turns-2) == "$DID$NL- "*" $TNA: status → draft$NL- "*" $TNB: status → draft" ]]'
check "and nothing twice" '[[ -z $(prompt turns-1) && -z $(prompt turns-2) ]]'
out=$(CREW_PROJECT=$PJ prompt here); out2=$(CREW_PROJECT=$P2 prompt there)
check "a lead for one project hears only that project, without its name before each line" '[[ $out == "$DID$NL- "????-??-??" "??:??" $TNA: status → draft" && $out2 == "$DID$NL- "????-??-??" "??:??" $TNB: status → draft" ]]'
out=$(CREW_LEAD_FINGERPRINT=$fp prompt lead)
check "crew lead's session hears every project, each line's name before it, as the phone lead does" '[[ $out == "$DID$NL- "*" $PJ: $TNA: status → draft$NL- "*" $P2: $TNB: status → draft" ]]'
check "a goal lead's session hears nothing, and keeps no record" '[[ -z $(CREW_LEAD_FINGERPRINT='' prompt goal-lead) && ! -e $L/news/sessions/goal-lead ]]'
echo "# changed" >>"$L/agents/lead.md"; install_agents
as_worker set "$NA" status needs-input; out=$(CREW_LEAD_FINGERPRINT=$fp prompt lead)
check "a lead whose instructions changed hears that first, then the news" '[[ $out == "crew'"'"'s lead instructions or settings changed"*"start a new lead to get them.$NL$DID$NL- "*" $TNA: status → needs-input" ]]'
git -C "$L" checkout -q -- agents; install_agents
# A long gap: more news than Claude Code passes on from a hook (10,000 characters).
for i in {1..90}; do as_worker set "$NA" status draft; as_worker set "$NA" status needs-input; done
out=$(prompt turns-1)
check "after a long gap, the prompt says how many earlier lines it left out and where to read them, and keeps to the cap" \
  '((${#out} <= 10000)) && [[ $out == [0-9]*" earlier lines left out: this machine'"'"'s news is in $L/news, a file a day, and what crew tick holds back is in $L/news/held-back.$NL$DID$NL"* ]]'
check "and tells the newest lines, up to the last" '[[ $out == *" $TNA: status → draft$NL- "*" $TNA: status → needs-input" ]]'
left=${out%% earlier lines left out*}; told=$(grep -c "^- " <<<"$out")
check "and leaves out only what it doesn't tell" '[[ $left =~ ^[0-9]+$ ]] && ((left + told == 181))'

# When crew tick's cleanup leaves a delivered task's worktree, the leads hear it stays and why,
# once, and nothing says it was cleaned up. When the cause is gone, the next tick cleans up.
stays_case() {  # task-file label reason: tick twice; what the output, the news and the held-back file say
  local f=$1 label=$2 why=$3 id out1 out2 told again
  id=$(basename "$f" .md); prompt "stays-$id" SessionStart >/dev/null
  out1=$(crew tick 2>&1); told=$(prompt "stays-$id")
  out2=$(crew tick 2>&1); again=$(prompt "stays-$id")
  check "$label: tick says the worktree stays, and never that it was cleaned up" \
    '[[ $out1 == *"$id: worktree stays: $why"* && $(grep -cF "$id: worktree stays: $why" <<<"$out1") == 1 && $out1 != *"$id: "*"worktree cleaned up"* && $out2 != *"$id: "*"worktree cleaned up"* && -d $(crew get "$f" worktree) ]] && ! grep -rq "$id: .*worktree cleaned up" "$L/news"'
  check "$label: held-back keeps one item to a line, and a lead hears the reason after the first tick but not the second" \
    '! grep -vP "^[^\t]+\t[^\t]+\$" "$L/news/held-back" && grep -qF "$PJ"$'"'"'\t'"'"'"$id: worktree stays: $why" "$L/news/held-back" && [[ $told == *"$HELD$NL- $PJ: $id: worktree stays: $why" && -z $again ]]'
}
SC1=$(crew new "Stays dirty" --branch stays-dirty --project "$PJ"); fill "$SC1"; crew ready "$SC1" >/dev/null; setstate "done" null; crew dispatch "$SC1" >/dev/null
SW1=$(crew get "$SC1" worktree); echo x >"$SW1/uncommitted.txt"; crew set "$SC1" status merged >/dev/null
stays_case "$SC1" "a merged task with uncommitted changes" "$SW1 has uncommitted changes (crew cleanup --force removes it)"
# A failing ledger commit warns too; the reason must still be cleanup's last line
touch "$L/.git/index.lock"
out=$(crew cleanup "$SC1" 2>&1 >/dev/null); rc=$?; rm -f "$L/.git/index.lock"
check "cleanup's reason is its last line of stderr even when the ledger commit warns" \
  '[[ $rc == 4 && $out == *"ledger commit failed"*"worktree stays"* && ${out##*$NL} == *"$(basename "$SC1" .md): worktree stays: $SW1 has uncommitted changes"* ]]'
rm "$SW1/uncommitted.txt"; setstate "done" null; prompt stays-fixed SessionStart >/dev/null
out=$(crew tick 2>&1)
check "once the changes are gone, tick cleans the worktree up and says so, to the leads too" \
  '[[ $out == *"$(basename "$SC1" .md): merged; worktree cleaned up"* && ! -d $SW1 && -z $(crew get "$SC1" worktree) && $(prompt stays-fixed) == *"$(basename "$SC1" .md): merged; worktree cleaned up"* ]]'
SC2=$(crew new "Stays running" --branch stays-running --project "$PJ"); fill "$SC2"; crew ready "$SC2" >/dev/null; setstate "done" null; crew dispatch "$SC2" >/dev/null
SJ2=$(crew get "$SC2" worker_job); crew set "$SC2" status merged >/dev/null
setstate working 4242; touch "$HOME/stop-noop"
stays_case "$SC2" "a merged task whose worker won't stop" "worker $SJ2 is still running (stop it in claude agents)"
rm -f "$HOME/stop-noop"; setstate "done" null
echo 'WORKTREE_TOOL="worktrunk"' >>"$PD/project.env"
SC3=$(crew new "Stays locked" --branch stays-locked --project "$PJ"); fill "$SC3"; crew ready "$SC3" >/dev/null; setstate "done" null; crew dispatch "$SC3" >/dev/null
SW3=$(crew get "$SC3" worktree); git -C "$SB/webapp" worktree lock "$SW3"; crew set "$SC3" status merged >/dev/null
stays_case "$SC3" "a worktrunk task whose worktree won't remove" "the worktree tool couldn't remove $SW3"
git -C "$SB/webapp" worktree unlock "$SW3"; sed -i '/^WORKTREE_TOOL=/d' "$PD/project.env"

# Red CI the worker finds its branch didn't cause (a flaky test, the CI service, a red base), and
# hands back without asking, doesn't count toward MAX_CI_FIXES; red CI its branch caused does.
# Each case is a task of its own; they come last, so the tasks they add renumber nothing above.
echo OPEN >"$HOME/gh-pr-state"
ci_round() { ci "$1" test:FAILURE; setstate "done" null; crew tick 2>/dev/null; }   # head: CI goes red on it, and a tick acts
# What the worker does with a CI round, as implementer.md "Workflow: review rounds" step 4 says:
# it posts its comment for the head, with --not-the-branch when its branch didn't cause the red
# CI, ticks the line, and hands the PR over. A refused comment fails the suite.
answer_ci() {  # task short-sha [--not-the-branch]
  local f; f=$(crew path "$1"); printf 'CI was red\n' >"$HOME/ci.md"
  crew ci-summary ${3:+"$3"} "$1" "$2" "$HOME/ci.md" >/dev/null || { FAIL=$((FAIL + 1)); printf '  FAIL: ci-summary %s %s refused at line %s\n' "$1" "$2" "${BASH_LINENO[0]}"; }
  sed -i "s/^- \[ \] CI failed on $2:/- [x] CI failed on $2:/" "$f"; hand_over "$1"
}
CO=$(pr_task "Red CI outside the branch"); TCO=$(basename "$CO" .md); hand_over "$CO"
o1=$(ci_round 1111aaaffff0); answer_ci "$CO" 1111aaa --not-the-branch
o2=$(ci_round 2222bbbffff0); answer_ci "$CO" 2222bbb --not-the-branch
o3=$(ci_round 3333cccffff0); answer_ci "$CO" 3333ccc --not-the-branch
check "three red heads the worker hands back as not its branch's each get a round and no question, and leave the task pr-open" '[[ $o1 == *"$TCO: CI round 1 recorded: red on 1111aaa"* && $o2 == *"$TCO: CI round 2 recorded: red on 2222bbb"* && $o3 == *"$TCO: CI round 3 recorded: red on 3333ccc"* && $o3 != *"for you"* && $(crew get "$CO" status) == pr-open ]] && ! grep -q "CI is still red" "$CO"'
check "ci-summary --not-the-branch marks the head's round line, which the worker then ticks" '[[ $(round "$CO" 3) == "- [x] CI failed on 3333ccc: test (the branch didn'"'"'t cause it)" && $(cat "$HOME/gh-summaries/"*) == *"<!-- crew ci-summary: 3333ccc -->"* ]]'
o4=$(setstate "done" null; crew tick 2>/dev/null); o5=$(crew tick 2>/dev/null)
check "a handed-back red head still gets exactly one round across repeated ticks" '[[ $o4 != *"$TCO"* && $o5 != *"$TCO"* && $(crew get "$CO" review_rounds) == 3 && $(grep -c "^- \[.\] CI failed on 3333ccc" "$CO") == 1 && $(crew get "$CO" status) == pr-open ]]'
crew set "$CO" status abandoned
CB=$(pr_task "Red CI from the branch"); TCB=$(basename "$CB" .md); hand_over "$CB"
ci_round 4444dddffff0 >/dev/null; answer_ci "$CB" 4444ddd
ci_round 5555eeeffff0 >/dev/null; answer_ci "$CB" 5555eee
out=$(ci_round 6666fffaaaa0)
check "three red heads the branch caused: the third is a question for the user, as before" '[[ $out == *"$TCB: CI still red after 2 CI rounds the branch caused, for you"* && $(crew get "$CB" status) == needs-input && $(crew get "$CB" review_rounds) == 2 ]] && [[ $(section "$CB" "## Open questions") == *"- [ ] CI is still red on 6666fff after 2 CI rounds the branch caused (MAX_CI_FIXES=2): test."* ]]'
crew set "$CB" status abandoned
CM=$(pr_task "Red CI from both"); TCM=$(basename "$CM" .md); hand_over "$CM"
ci_round 7777aaaffff0 >/dev/null; answer_ci "$CM" 7777aaa
ci_round 8888bbbffff0 >/dev/null; answer_ci "$CM" 8888bbb --not-the-branch
ci_round 9999cccffff0 >/dev/null; answer_ci "$CM" 9999ccc
out=$(ci_round 0000dddffff0)
check "branch caused, handed back, branch caused: a new red head is a question, after 2 CI rounds" '[[ $out == *"$TCM: CI still red after 2 CI rounds the branch caused, for you"* && $(crew get "$CM" status) == needs-input && $(crew get "$CM" review_rounds) == 3 ]] && [[ $(section "$CM" "## Open questions") == *"- [ ] CI is still red on 0000ddd after 2 CI rounds the branch caused (MAX_CI_FIXES=2): test."* ]]'
crew set "$CM" status abandoned
rm -rf "$HOME/gh-summaries" "$HOME/gh-pr-head" "$HOME/gh-pr-checks" "$HOME/gh-pr-state"

summary crew-e2e
