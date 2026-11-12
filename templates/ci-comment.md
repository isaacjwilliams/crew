<!-- Why CI was red on a worker's PR, and what was done about it.
     - One comment for each commit a review round's "CI failed on <sha>" line reports.
     - Fill it in when you're done with that line: CI is green again, or you're about to ask the
       user. Post it with `crew ci-summary <task> <file>`.
     - Leave out this comment and the <…> placeholders. Keep each part to a sentence or two.
     - It's for the user's collaborators too: never mention the ledger, tasks, review rounds or agents.

     "What was done" is one of these:
     - Your branch caused it: "Fixed in <short sha>: <what changed, in a line>."
     - It didn't, and a rerun of the failed jobs passed: "Nothing in this PR caused it. A rerun of
       the failed jobs passed."
     - It didn't, and the rerun failed again: "Nothing in this PR caused it. The rerun failed again,
       so it's waiting on a person."
     - It didn't, and it couldn't be rerun from here: "Nothing in this PR caused it. It needs someone
       to rerun it." -->
## CI was red

**What failed:** <check>, <check> on <short sha>.

**Why:** <the cause, in a sentence or two: what the log showed>

**What was done:** <from the list above>
