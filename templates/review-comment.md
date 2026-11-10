<!-- The automated-review summary on a worker's PR. Fill it in from the task file's Automated reviews
     record alone, then post it with `crew review-summary <task> <file>`. Leave out this comment, the
     <…> placeholders and any section that would be empty. One line per finding: a reader should take
     it in at a glance. It's for the user's collaborators too, so it never mentions the ledger,
     tasks, proposals or agents.

     <N> counts every review, including one whose reviewer failed. The line after it says how the
     last review ended:
     - clean: "The last came back clean." ("It came back clean." after a single review)
     - the limit ran out after fixes: "The limit ran out: the fixes for the last review haven't been reviewed again."
     - its reviewer failed: "The last couldn't finish, so the latest fixes haven't been reviewed again."
     - every reviewer failed: "None could finish, so this PR hasn't had an automated review." -->
## Automated review

<N> automated reviews ran before this PR was ready for you. <How the last one ended, from the list above.>

**Fixed**
- [<severity>] <what was wrong, in a line> (<short sha>)

**Left for later**
- <what could be better, in a line>

**Left for you**
- <the finding, in a line> — <why it's left: disputed, with the evidence in a few words / couldn't be settled / kept as the plan designed it>
