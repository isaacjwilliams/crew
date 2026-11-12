<!-- Why CI was red on a worker's PR, and what was done about it: one comment for each commit a
     review round's "CI failed on <sha>" line reports. Fill it in once CI is green again, or once
     you've asked the user about it, then post it with `crew ci-summary <task> <file>`. Leave out
     this comment and the <…> placeholders. Keep it short: a sentence or two for each part. It's
     for the user's collaborators too, so it never mentions the ledger, tasks, review rounds or
     agents.

     "What was done" is one of:
     - the branch caused it: "Fixed in <short sha>: <what changed, in a line>."
     - it didn't (a flaky test, the CI service, the base branch already red): "Nothing in this PR
       caused it. A rerun of the failed jobs passed." or "… The rerun failed again, so it's waiting
       on a person."
     - it couldn't be rerun (CI that can't be rerun from here): "Nothing in this PR caused it. It
       needs someone to rerun it." -->
## CI was red

**What failed:** <check>, <check> on <short sha>.

**Why:** <the cause, in a sentence or two: what the log showed>

**What was done:** <from the list above>
