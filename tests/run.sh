#!/usr/bin/env bash
# Run all suites (or the ones named): tests/run.sh [lint] [guard] [crew-e2e] [host]
cd "$(dirname "$0")" || exit 2
suites=("$@"); ((${#suites[@]})) || suites=(lint guard crew-e2e host)
for tool in bash git jq flock; do command -v "$tool" >/dev/null || { echo "missing required tool: $tool" >&2; exit 2; }; done
failed=()
for s in "${suites[@]}"; do
  [[ -f $s.sh ]] || { echo "no such suite: $s" >&2; exit 2; }
  bash "./$s.sh" || failed+=("$s")
done
if ((${#failed[@]})); then echo "FAILED: ${failed[*]}"; exit 1; fi
echo "all suites passed"
