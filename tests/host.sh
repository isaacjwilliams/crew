#!/usr/bin/env bash
# A machine running a crew home: `crew install` scenarios, each in a fresh fake HOME.
# shellcheck disable=SC2034  # variables read inside check eval strings
. "$(dirname "$0")/lib.sh"

# 1. A fresh install puts this machine's files in place.
isolate_home "$(new_sandbox host)/home"
H=$HOME/crew; make_home "$H"
out=$("$H/bin/crew" install --yes 2>&1)
check "agents installed" '[[ -f $HOME/.claude/agents/lead.md && -f $HOME/.claude/agents/goal-lead.md && -f $HOME/.claude/agents/implementer.md && -f $HOME/.claude/agents/casual-observer.md && -f $HOME/.claude/agents/gardener.md && -f $HOME/.claude/agents/plan-reviewer.md ]]'
check "agents installed as the home's copies" 'cmp -s "$HOME/.claude/agents/lead.md" "$H/agents/lead.md"'
check "fish files installed" '[[ -f $HOME/.config/fish/conf.d/agent-crew.fish && -f $HOME/.config/fish/functions/lead.fish && -f $HOME/.config/fish/completions/crew.fish ]]'
check "systemd units installed, timers not enabled" '[[ -f $HOME/.config/systemd/user/crew-gardener.timer && -f $HOME/.config/systemd/user/crew-tick.timer ]] && ! grep -q enable "$HOME/systemctl-calls.log"'
check "CREW_HOME set for Claude Code sessions" '[[ $(jq -r .env.CREW_HOME "$HOME/.claude/settings.json") == "$H" ]]'
check "AGENTS.md setting applied with --yes" '[[ $(jq -r ".pluginConfigs[\"agents-md@builtin\"].options.instructionFiles" "$HOME/.claude/settings.json") == claude-md-and-agents-md ]]'
check "work root created" '[[ -d $HOME/agent-work ]]'
check "install reports the home's version" '[[ $out == *"Done (crew $(cat "$H/VERSION"))"* ]]'
check "install changes nothing tracked in the home" '[[ -z $(git -C "$H" status --porcelain) ]]'
LL=$H/lead/.claude/settings.local.json
check "the lead's settings for this machine add the home and work root, and leave out the home's CLAUDE.md" '[[ $(jq -c . "$LL") == "{\"permissions\":{\"additionalDirectories\":[\"$H\",\"$HOME/agent-work\"]},\"claudeMdExcludes\":[\"$H/CLAUDE.md\"]}" ]]'
check "and they aren't committed" 'git -C "$H" check-ignore -q "$LL"'
doctor_out=$(PATH="$H/bin:$PATH" crew doctor 2>&1 || true)
check "crew doctor shows the home and its version" '[[ $doctor_out == *"crew $(cat "$H/VERSION") at $H"* ]]'
check "crew doctor finds the guard hook in the settings" '[[ $doctor_out == *"ok   lead/.claude/settings.json runs crew-guard"* && $doctor_out == *"ok   config/worker-settings.json runs crew-guard"* ]]'
check "crew doctor finds the commit hook in the settings" '[[ $doctor_out == *"ok   lead/.claude/settings.json commits ledger edits"* && $doctor_out == *"ok   config/worker-settings.json commits ledger edits"* ]]'
check "crew doctor finds the lead's settings for this machine" '[[ $doctor_out == *"ok   lead/.claude/settings.local.json written for this machine"* ]]'
check "crew doctor flags an untrusted lead directory" '[[ $doctor_out == *"FAIL $H/lead trusted"* ]]'
jq -n --arg p "$H/lead" '{projects: {($p): {hasTrustDialogAccepted: true}}}' >"$HOME/.claude.json"
doctor_out=$(PATH="$H/bin:$PATH" crew doctor 2>&1 || true)
check "crew doctor finds the lead directory trusted" '[[ $doctor_out == *"ok   $H/lead trusted"* ]]'

# 2. An unchanged re-run backs nothing up.
backups() { find "$HOME/.local/share/agent-crew/backups" -type f 2>/dev/null | wc -l; }
n=$(backups); out=$("$H/bin/crew" install --yes 2>&1)
check "an unchanged re-run backs nothing up" '[[ $out != *"backed up"* && $(backups) == "$n" ]]'

# 3. An installed agent edited in place is backed up before it's replaced.
echo '# local tweak' >>"$HOME/.claude/agents/lead.md"
"$H/bin/crew" install --yes >/dev/null 2>&1
check "an edited agent is backed up" 'grep -q "local tweak" "$HOME"/.local/share/agent-crew/backups/*/.claude/agents/lead.md'
check "and replaced with the home's copy" 'cmp -s "$HOME/.claude/agents/lead.md" "$H/agents/lead.md"'

# 4. What the home stops shipping is removed; what it starts shipping is installed.
lead_inode=$(stat -c %i "$HOME/.claude/agents/lead.md")
echo '# new upstream' >>"$H/agents/lead.md"
echo 'function crew-extra; end' >"$H/fish/functions/crew-extra.fish"
rm "$H/agents/gardener.md" "$H/systemd/crew-gardener.timer"
"$H/bin/crew" install --yes >/dev/null 2>&1
check "a changed agent is replaced by rename, not rewritten in place" '[[ $(stat -c %i "$HOME/.claude/agents/lead.md") != "$lead_inode" ]] && cmp -s "$H/agents/lead.md" "$HOME/.claude/agents/lead.md"'
check "a new fish function is installed" '[[ -f $HOME/.config/fish/functions/crew-extra.fish ]]'
check "a dropped agent is removed and backed up" '[[ ! -e $HOME/.claude/agents/gardener.md ]] && ls "$HOME"/.local/share/agent-crew/backups/*/.claude/agents/gardener.md >/dev/null 2>&1'
check "a dropped unit is disabled and removed" '[[ ! -e $HOME/.config/systemd/user/crew-gardener.timer ]] && grep -q "disable --now crew-gardener.timer" "$HOME/systemctl-calls.log"'
check "other agents are left alone" '[[ -f $HOME/.claude/agents/implementer.md ]]'

# 5. A skipped component keeps its files.
rm "$H/fish/functions/crew-extra.fish"
"$H/bin/crew" install --yes --no-fish >/dev/null 2>&1
check "--no-fish leaves a dropped fish file alone" '[[ -f $HOME/.config/fish/functions/crew-extra.fish ]]'
"$H/bin/crew" install --yes >/dev/null 2>&1
check "the next run with fish removes it" '[[ ! -e $HOME/.config/fish/functions/crew-extra.fish && -f $HOME/.config/fish/functions/lead.fish ]]'

# 6. The AGENTS.md question is asked once. (Without a terminal, `ask` reads EOF,
#    which counts as the default yes, so a repeated question would apply it.)
out=$("$H/bin/crew" install </dev/null 2>&1)
check "not asked again once the setting is on" '[[ $out != *"AGENTS.md loading"* ]]'
isolate_home "$(new_sandbox host)/home"
H=$HOME/crew; make_home "$H"
echo n | "$H/bin/crew" install >/dev/null 2>&1
check "declining leaves the setting off" '! jq -e ".pluginConfigs" "$HOME/.claude/settings.json" >/dev/null 2>&1'
"$H/bin/crew" install </dev/null >/dev/null 2>&1
check "a declined question isn't asked again" '! jq -e ".pluginConfigs" "$HOME/.claude/settings.json" >/dev/null 2>&1'

# 7. A home somewhere other than ~/crew: the files point at it, and re-runs back nothing up.
isolate_home "$(new_sandbox host)/home"
H=$HOME/elsewhere/crew; make_home "$H"
"$H/bin/crew" install --yes >/dev/null 2>&1
out=$("$H/bin/crew" install --yes 2>&1)
check "a re-run with a custom home backs nothing up" '[[ $out != *"backed up"* ]]'
check "CREW_HOME set to the custom home" '[[ $(jq -r .env.CREW_HOME "$HOME/.claude/settings.json") == "$H" ]]'
check "fish conf.d points at the custom home" 'grep -q "CREW_HOME $H\$" "$HOME/.config/fish/conf.d/agent-crew.fish"'
for s in gardener tick lead; do
  check "$s service points at the custom home" 'grep -q "^Environment=CREW_HOME=$H\$" "$HOME/.config/systemd/user/crew-$s.service" && grep -q "^ExecStart=$H/bin/crew " "$HOME/.config/systemd/user/crew-$s.service"'
done

# 8. crew doctor checks that each enabled unit's service can find crew's tools,
#    and that the settings still run crew-guard.
isolate_home "$(new_sandbox host)/home"
H=$HOME/crew; make_home "$H"
"$H/bin/crew" install --yes --skip-agents-md-setting >/dev/null 2>&1
systemctl --user enable --now crew-tick.timer
doctor_out=$(PATH="$H/bin:$PATH" crew doctor 2>&1 || true)
check "crew doctor checks the tick service's PATH" '[[ $doctor_out == *"ok   crew-tick.service finds git"* ]]'
check "crew doctor flags a tool the tick service can't find" '[[ $doctor_out == *"FAIL crew-tick.service finds claude"* ]]'
mkdir -p "$HOME/.local/share/mise/shims"
for c in gh jq claude; do ln -s "$(command -v "$c")" "$HOME/.local/share/mise/shims/$c"; done
doctor_out=$(PATH="$H/bin:$PATH" crew doctor 2>&1 || true)
check "the tick service finds tools installed with mise" '[[ $doctor_out != *"FAIL crew-tick.service"* ]]'
check "crew doctor skips timers that aren't enabled" '[[ $doctor_out != *"crew-gardener.service"* ]]'
jq 'del(.hooks)' "$H/config/worker-settings.json" >"$HOME/ws" && mv "$HOME/ws" "$H/config/worker-settings.json"
doctor_out=$(PATH="$H/bin:$PATH" crew doctor 2>&1 || true)
check "crew doctor flags settings that lack the guard hook" '[[ $doctor_out == *"FAIL config/worker-settings.json runs crew-guard"* ]]'

# 9. One machine runs the crew at a time. Two machines, each with a HOME of its
#    own, share one private origin.
SB=$(new_sandbox lease)
isolate_home "$SB/laptop"; isolate_home "$SB/tower"
tower()  { HOME=$SB/tower CREW_HOST=tower "$@"; }
laptop() { HOME=$SB/laptop CREW_HOST=laptop "$@"; }
origin_host() { git --git-dir="$SB/crew.git" show main:host 2>/dev/null; }
TH=$SB/tower/crew; LH=$SB/laptop/crew
git init -q --bare "$SB/crew.git"
make_home "$TH"; git -C "$TH" remote add origin "$SB/crew.git"; git -C "$TH" push -q -u origin main
git init -q --bare "$SB/webapp.git"; git clone -q "$SB/webapp.git" "$SB/tower/webapp" 2>/dev/null
(cd "$SB/tower/webapp" && echo "# Conventions" >AGENTS.md && git add -A && git commit -qm init && git push -q -u origin main && git remote set-head origin main)

out=$(cd "$SB/tower/webapp" && tower "$TH/bin/crew" project add 2>&1)
check "nothing is changed before a machine runs the crew" '[[ $out == *"no machine runs the crew"* ]]'
out=$(tower "$TH/bin/crew" host up </dev/null 2>&1)
check "host up claims the lease on origin" '[[ $(origin_host) == tower && $(cat "$TH/host") == tower && $out == *"crew runs on tower"* ]]'
check "host up installs, and enables the timers and the lead service" '[[ -f $SB/tower/.claude/agents/lead.md ]] && grep -qx crew-tick.timer "$SB/tower/systemctl-enabled" && grep -qx crew-gardener.timer "$SB/tower/systemctl-enabled" && grep -qx crew-lead.service "$SB/tower/systemctl-enabled"'
tower "$TH/bin/crew" serve >/dev/null 2>&1
check "on the machine that runs the crew, serve runs the lead's Remote Control server in lead/" '[[ $(tail -n 1 "$SB/tower/claude-calls.log") == "claude remote-control --name crew --spawn same-dir --permission-mode auto" && $(cat "$SB/tower/remote-control.cwd") == "$TH/lead" ]]'
(cd "$SB/tower/webapp" && tower "$TH/bin/crew" project add >/dev/null)
P=$(basename "$(ls -d "$TH"/projects/*/)")
tower "$TH/bin/crew" new "Lease test" --project "$P" >/dev/null
wait_for '[[ $(git -C "$TH" rev-parse @) == "$(git --git-dir="$SB/crew.git" rev-parse main)" ]]'

git clone -q "$SB/crew.git" "$LH"
out=$(laptop "$LH/bin/crew" status 2>&1)
check "a machine that doesn't run the crew reads it" '[[ $out == *"read-only: crew runs on tower"* && $out == *"001-lease-test"* ]]'
out=$(laptop "$LH/bin/crew" log 1 "from the laptop" 2>&1)
check "and is refused a change, told which machine runs the crew" '[[ $out == *"crew runs on tower; this machine can only read (crew host up --take moves it here)"* && $(git -C "$LH" status --porcelain) == "" ]] && ! grep -q "from the laptop" "$LH"/projects/*/tasks/001-*.md'
out=$(laptop "$LH/bin/crew" host 2>&1)
check "crew host says who runs the crew, since when, and this machine's name" '[[ $out == *"crew runs on tower (since "*"ago)"* && $out == *"this machine: laptop"* ]]'
out=$(laptop "$LH/bin/crew" host up </dev/null 2>&1)
check "host up is refused while another machine runs the crew" '[[ $out == *"crew runs on tower (since "*"crew host up --take moves it here"* && $(origin_host) == tower ]]'
laptop "$LH/bin/crew" host up --take </dev/null >/dev/null 2>&1
LREPO=$SB/laptop/agent-work/$P/repo
check "host up --take moves the lease" '[[ $(origin_host) == laptop ]] && grep -qx crew-tick.timer "$SB/laptop/systemctl-enabled"'
check "and clones each project this machine has no clone of" '[[ $(git -C "$LREPO" remote get-url origin) == "$SB/webapp.git" ]] && grep -q "^REPO=\"$LREPO\"" "$LH/projects/$P/local.env"'

# The tower hasn't heard yet. Its next commit is refused by origin, so it learns.
echo '[{"id":"job1","name":"crew-x-001","pid":4242,"state":"working","sessionId":"job1-s"}]' >"$SB/tower/agents.json"
tower "$TH/bin/crew" log 1 "a late decision" >/dev/null 2>&1
check "a background push origin rejects for a lease moved elsewhere marks it lost" 'wait_for "[[ -e $TH/.crew-lease-lost ]]" && grep -q "crew runs on laptop now" "$TH/logs/push.log"'
out=$(tower "$TH/bin/crew" log 1 "another" 2>&1)
check "and the next change there is refused" '[[ $out == *"crew has moved to laptop"* ]]'
out=$(tower "$TH/bin/crew" tick 2>&1)
lost=$(git -C "$TH" branch --list "lost/tower-*" --format="%(refname:short)")
check "the old host's next tick stands down: units off, crew sessions stopped" '[[ $out == *"standing down"* ]] && grep -q "disable --now crew-tick.timer crew-gardener.timer crew-lead.service" "$SB/tower/systemctl-calls.log" && ! grep -qx crew-tick.timer "$SB/tower/systemctl-enabled" && grep -qx "claude stop job1" "$SB/tower/claude-calls.log"'
check "it keeps the ledger commit origin never got on a lost/ branch, and resets to origin" '[[ -n $lost ]] && git -C "$TH" log -1 --format=%s "$lost" | grep -q "log 001-lease-test" && [[ $(git -C "$TH" rev-parse @) == "$(git --git-dir="$SB/crew.git" rev-parse main)" && $(cat "$TH/host") == laptop && ! -e $TH/.crew-lease-lost ]]'
rc=0; tower "$TH/bin/crew" serve >/dev/null 2>&1 || rc=$?
check "on a machine that doesn't run the crew, serve exits 3, so its unit stays stopped" '[[ $rc == 3 ]]'
out=$(tower "$TH/bin/crew" log 1 "from a worker still on the tower" 2>&1)
check "a worker's crew log on the stood-down machine fails with the new holder's name" '[[ $out == *"crew runs on laptop; this machine can only read"* ]]'

# A planned handover from the laptop.
WT=$SB/laptop/agent-work/$P/001-lease-test
git -C "$LREPO" worktree add -q --no-track -b agent/lease-test "$WT" origin/main   # as crew makes them
(cd "$WT" && echo x >x && git add x && git commit -qm "work")
laptop "$LH/bin/crew" set 1 worktree "$WT" >/dev/null
out=$(laptop "$LH/bin/crew" host down 2>&1)
check "host down is refused while a worktree's branch was never pushed" '[[ $out == *"001-lease-test: its branch was never pushed"* && $(origin_host) == laptop ]]'
(cd "$WT" && git push -q -u origin HEAD 2>/dev/null && echo y >y && git add y && git commit -qm "more")
out=$(laptop "$LH/bin/crew" host down 2>&1)
check "or has commits it hasn't pushed" '[[ $out == *"001-lease-test: 1 commit(s) not pushed"* && $(origin_host) == laptop ]]'
(cd "$WT" && git push -q)
# The handover's own push meets a commit origin has from elsewhere, and rebases onto it.
git clone -q "$SB/crew.git" "$SB/third"
(cd "$SB/third" && mkdir -p reports && echo n >reports/third.md && git add reports && git commit -qm "from a third clone" && git push -q)
mkdir -p "$LH/reports"; echo l >"$LH/reports/laptop.md"; git -C "$LH" add reports && git -C "$LH" commit -qm "a laptop note"
start=$SECONDS
out=$(laptop "$LH/bin/crew" host down 2>&1)
check "once everything is pushed, host down releases the lease and turns the units off" '[[ $out == *"crew runs on no machine now"* && -z $(origin_host) ]] && ! grep -qx crew-tick.timer "$SB/laptop/systemctl-enabled"'
origin_log=$(git --git-dir="$SB/crew.git" log --format=%s main)
check "its last push takes in what origin has, without waiting on its own lock" '[[ -f $LH/reports/third.md && $out != *"couldn'"'"'t push every ledger commit"* && $((SECONDS - start)) -lt 20 && $origin_log == *"a laptop note"* ]]'
out=$(laptop "$LH/bin/crew" log 1 "after the handover" 2>&1)
check "after a handover no machine takes changes" '[[ $out == *"no machine runs the crew"* ]]'

# 10. A person's own crew, from a clone of the public repo.
SB=$(new_sandbox setup)
isolate_home "$SB/home"
make_home "$SB/public-tree"; git init -q --bare "$SB/public.git"; git -C "$SB/public-tree" push -q "$SB/public.git" main
H=$HOME/crew; git clone -q "$SB/public.git" "$H"
git init -q --bare "$HOME/remotes/isaac/crew.git"
out=$(CREW_HOST=tower "$H/bin/crew" setup </dev/null 2>&1)
check "setup that can't make the private repo leaves the clone as it was, and suggests --name" '[[ $out == *"pick another with crew setup --name"* && $(git -C "$H" remote) == origin && $(git -C "$H" remote get-url --push origin) == "$SB/public.git" && ! -e $H/config/crew.env ]]'
rm -rf "$HOME/remotes/isaac/crew.git"
out=$(CREW_HOST=tower "$H/bin/crew" setup </dev/null 2>&1)
check "setup makes the private repo origin and the public one upstream" '[[ $(git -C "$H" remote get-url origin) == "$HOME/remotes/isaac/crew.git" && $(git -C "$H" remote get-url upstream) == "$SB/public.git" ]]'
check "and nothing can be pushed to the public repo" '! git -C "$H" push -q upstream main 2>/dev/null && [[ -z $(git --git-dir="$SB/public.git" log --oneline main -- config/crew.env) ]]'
check "it commits the person's settings, from the template, to the private repo" '[[ $(git --git-dir="$HOME/remotes/isaac/crew.git" log -1 --format=%s main -- config/crew.env) == "crew: set up for isaac" ]] && cmp -s "$H/config/crew.env" "$H/templates/crew.env"'
check "and runs the crew here: the lease claimed, the units enabled" '[[ $(git --git-dir="$HOME/remotes/isaac/crew.git" show main:host) == tower ]] && grep -qx crew-lead.service "$HOME/systemctl-enabled" && [[ $out == *"Set BRANCH_PREFIX"* ]]'
out=$(CREW_HOST=tower "$H/bin/crew" setup </dev/null 2>&1)
check "setup refuses a home that's set up already" '[[ $out == *"set up already"* ]]'

# 11. A new version from the public repo, merged into a home that has a ledger.
up() { CREW_HOST=tower "$H/bin/crew" upgrade </dev/null 2>&1; }
mkdir -p "$H/reports"; echo "a note" >"$H/reports/n.md"; CREW_HOST=tower "$H/bin/crew" commit "a ledger note" >/dev/null
echo "# new in v2" >>"$SB/public-tree/agents/lead.md"; echo 9.9.9 >"$SB/public-tree/VERSION"
sed -i 's/say "Done (crew $(crew_version))/say "Done (crew $(crew_version), by the new install)/' "$SB/public-tree/bin/crew"
git -C "$SB/public-tree" commit -qam "v2" && git -C "$SB/public-tree" push -q "$SB/public.git" main
before=$(git -C "$H" rev-parse @)
out=$(up)
check "upgrade merges the public repo's new version into a home with ledger commits, and names it" '[[ $out == *"crew upgraded to 9.9.9"* ]] && git -C "$H" merge-base --is-ancestor "$before" @ && grep -q "# new in v2" "$H/agents/lead.md" && [[ -f $H/reports/n.md ]]'
check "the new version installs itself" '[[ $out == *"by the new install"* ]]'
check "and installs it: the changed agent is copied, the old one backed up" 'grep -q "# new in v2" "$HOME/.claude/agents/lead.md" && compgen -G "$HOME/.local/share/agent-crew/backups/*/.claude/agents/lead.md" >/dev/null && ! grep -q "# new in v2" "$HOME"/.local/share/agent-crew/backups/*/.claude/agents/lead.md'
check "and pushes the merge to the private repo, and restarts the lead's server" '[[ $(git --git-dir="$HOME/remotes/isaac/crew.git" rev-parse main) == $(git -C "$H" rev-parse @) ]] && grep -q "restart crew-lead.service" "$HOME/systemctl-calls.log"'
echo "an edit" >>"$H/README.md"
out=$(up)
check "upgrade refuses a home with uncommitted changes" '[[ $out == *"uncommitted changes"* ]]'
git -C "$H" checkout -q -- README.md
sed -i '1s/.*/# my own first line/' "$H/agents/gardener.md"; git -C "$H" commit -qam "my gardener"
sed -i '1s/.*/# upstream first line/' "$SB/public-tree/agents/gardener.md"
git -C "$SB/public-tree" commit -qam "v3" && git -C "$SB/public-tree" push -q "$SB/public.git" main
out=$(up)
check "a conflict with an edit in the home stops with what to do, and leaves the merge to the person" '[[ $out == *"merge is left in progress"*"merge --abort"* ]] && git -C "$H" rev-parse -q --verify MERGE_HEAD >/dev/null'

# 12. TEMPORARY, removed with crew migrate-ledger: a ledger made by install.sh,
#     shaped like the user's, imported into a fresh home.
SB=$(new_sandbox migrate)
isolate_home "$SB/home"
export CREW_HOST=tower
git init -q --bare "$SB/webapp.git"; git clone -q "$SB/webapp.git" "$SB/webapp" 2>/dev/null
(cd "$SB/webapp" && echo a >a && git add a && git commit -qm init && git push -q -u origin main)
OLD=$HOME/agent-ledger; P=isaac-webapp
mkdir -p "$OLD"/{bin,templates,config,reports,logs} "$OLD/projects/$P"/{tasks,proposals,retros,verify}
cp "$REPO_DIR"/bin/* "$OLD/bin/"; cp "$REPO_DIR"/templates/*.md "$OLD/templates/"; cp "$REPO_DIR/LEDGER.md" "$OLD/README.md"
printf '.crew.lock\n.crew-tick.lock\nlogs/\nconfig/*.dist\n' >"$OLD/.gitignore"; echo v1 >"$OLD/.crew-version"
sed 's/^BRANCH_PREFIX=.*/BRANCH_PREFIX="claude\/"/' "$REPO_DIR/config/defaults.env" >"$OLD/config/crew.env"
for f in worker readonly; do cp "$REPO_DIR/config/$f-settings.json" "$OLD/config/"; cp "$REPO_DIR/config/$f-settings.json" "$OLD/config/$f-settings.json.dist"; done
cp "$REPO_DIR/lead/.claude/settings.json" "$OLD/config/lead-settings.json"; echo '{}' >"$OLD/config/lead-settings.json.dist"
printf 'REPO="%s"\nDEFAULT_BRANCH="main"\nWORKTREE_TOOL="git"\n' "$SB/webapp" >"$OLD/projects/$P/project.env"
sed -e 's/{{ID}}/001-old-task/; s/{{TITLE}}/An old task/; s/{{PROJECT}}/'"$P"'/; s/{{BASE}}/main/; s/{{BRANCH}}/claude\/old-task/; s/{{PRE_PR_REVIEW}}/yes/; s/{{PR_MODE}}/draft/; s/{{NOW}}/2026-10-01T00:00:00Z/' \
  "$REPO_DIR/templates/task.md" | sed 's/^status: draft/status: pr-open/' >"$OLD/projects/$P/tasks/001-old-task.md"
echo "# a proposal" >"$OLD/projects/$P/proposals/2026-10-01-x.md"; echo "# a retro" >"$OLD/projects/$P/retros/r.md"
echo "# checks" >"$OLD/projects/$P/verify/v.md"; echo abc123 >"$OLD/projects/$P/gardener.last"
echo '{"permissions": {"allow": ["Bash(bin/rspec *)"]}}' >"$OLD/projects/$P/worker-settings.json"
echo "# a report" >"$OLD/reports/2026-10-15-x.md"; touch "$OLD/logs/gardener.log"
git -C "$OLD" init -q && git -C "$OLD" add -A && git -C "$OLD" commit -qm "ledger: initial"
echo "- 2026-10-02 — worked on" >>"$OLD/projects/$P/tasks/001-old-task.md"; git -C "$OLD" commit -qam "log 001-old-task"
H=$HOME/crew; make_home "$H"; git init -q --bare "$SB/crew.git"; git -C "$H" remote add origin "$SB/crew.git"; git -C "$H" push -q -u origin main
cp "$H/templates/crew.env" "$H/config/crew.env"; git -C "$H" add config/crew.env; git -C "$H" commit -qm "crew: set up"; git -C "$H" push -q
"$H/bin/crew" host up </dev/null >/dev/null 2>&1
out=$("$H/bin/crew" migrate-ledger "$OLD" 2>&1)
check "migrate-ledger brings the old ledger's files into the home" '[[ -f $H/projects/$P/tasks/001-old-task.md && -f $H/projects/$P/proposals/2026-10-01-x.md && -f $H/projects/$P/retros/r.md && -f $H/projects/$P/verify/v.md && -f $H/projects/$P/worker-settings.json && -f $H/reports/2026-10-15-x.md && $(cat "$H/projects/$P/gardener.last") == abc123 ]]'
check "with their history" 'git -C "$H" log --format=%s | grep -qx "ledger: initial" && git -C "$H" log --format=%s -- "projects/$P/tasks/001-old-task.md" | grep -qx "log 001-old-task"'
check "project.env has REMOTE and no REPO; this machine's local.env has the old REPO" '[[ $(grep "^REMOTE=" "$H/projects/$P/project.env") == "REMOTE=\"$SB/webapp.git\"" ]] && ! grep -q "^REPO=" "$H/projects/$P/project.env" && grep -qx "REPO=\"$SB/webapp\"" "$H/projects/$P/local.env" && [[ -z $(git -C "$H" ls-files "projects/$P/local.env") ]]'
check "the old ledger's own settings are kept in crew.env, and its copy of the defaults isn't" 'grep -qx "BRANCH_PREFIX=\"claude/\"" "$H/config/crew.env" && ! grep -q "^MAX_WORKERS=" "$H/config/crew.env"'
check "install.sh's copies of crew are left behind" '[[ ! -e $H/.crew-version && ! -e $H/config/lead-settings.json && ! -e $H/config/crew.env.dist ]] && cmp -s "$H/bin/crew" "$REPO_DIR/bin/crew" && cmp -s "$H/README.md" "$REPO_DIR/README.md" && git -C "$H" diff --quiet HEAD -- bin templates'
check "an edited settings file of the old ledger is named, not silently dropped" '[[ $out == *"lead-settings.json has edits of yours"* && $out != *"worker-settings.json has edits"* ]]'
check "crew status lists the imported tasks" '[[ $("$H/bin/crew" status 2>&1) == *"001-old-task"*"pr-open"* ]]'
check "the import reaches origin, and says what to do next" '[[ $(git --git-dir="$SB/crew.git" rev-parse main) == $(git -C "$H" rev-parse @) && $out == *"crew followup TASK --fresh"* ]]'
out=$("$H/bin/crew" migrate-ledger "$OLD" 2>&1)
check "migrate-ledger refuses a home that has projects" '[[ $out == *"has projects already"* ]]'
unset CREW_HOST

summary host
