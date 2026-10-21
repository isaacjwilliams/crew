#!/usr/bin/env bash
# Static checks. Uses the REAL claude only for `plugin validate` (no model call).
. "$(dirname "$0")/lib.sh"
cd "$REPO_DIR" || exit 2
launchers=(bin/crew)
scripts=(bin/crew-guard libexec/crew.sh tests/*.sh tests/stubs/*)

for f in "${launchers[@]}"; do check "sh -n $f" "sh -n '$f'"; done
for f in "${scripts[@]}"; do check "bash -n $f" "bash -n '$f'"; done
if command -v shellcheck >/dev/null; then
  check "shellcheck" 'shellcheck -S warning -x "${launchers[@]}" "${scripts[@]}"'
else echo "  (skipped shellcheck: not installed)"; fi
unformatted=$(gofmt -l cmd)
check "gofmt (unformatted: ${unformatted//$'\n'/ })" '[[ -z $unformatted ]]'
check "go vet" 'go vet ./...'
check "go vet for macOS" 'GOOS=darwin go vet ./...'
check "go test" 'go test ./... >/dev/null'
if command -v fish >/dev/null; then
  for f in fish/*/*.fish; do check "fish syntax $f" "fish --no-execute '$f'"; done
else echo "  (skipped fish syntax: fish not installed)"; fi
for f in config/*.json lead/.claude/settings.json; do check "valid JSON $f" "jq -e . '$f' >/dev/null"; done
if command -v claude >/dev/null; then
  check "claude plugin validate agents" 'claude plugin validate agents >/dev/null 2>&1'
else echo "  (skipped agent validation: claude not installed)"; fi
check "executables are executable" '[[ -x bin/crew && -x bin/crew-guard && -x libexec/crew.sh ]]'

summary lint
