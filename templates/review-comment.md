<!-- The automated-review summary on a worker's PR. Fill it in from the task file's Automated reviews
     record alone, then post it with `crew review-summary <task> <file>`. Leave out this comment, the
     <…> placeholders and any section that would be empty. One line per finding: a reader should take
     it in at a glance. It's for the user's collaborators too, so it never mentions the ledger,
     tasks, proposals or agents. Withdrawn findings aren't listed.

     <N> counts every review, including one whose reviewers failed. The sentence after it names who
     reviewed, from the Reviewers lines:
     - the Claude reviewer only: "Each was by a Claude reviewer." ("It was by …" after a single review)
     - with Codex: "Each was by a Claude reviewer and Codex, which answered each other's findings."
     - when Codex didn't report in some: add "Codex didn't report in review 2, so the Claude reviewer
       did that one alone." (name each review; "in any of them" when it never reported)
     The line after that says how the last review ended:
     - clean: "The last came back clean." ("It came back clean." after a single review)
     - the limit ran out after fixes: "The limit ran out: the fixes for the last review haven't been reviewed again."
     - its Claude reviewer failed: "The last couldn't finish, so the latest fixes haven't been reviewed again."
     - every review's Claude reviewer failed: "None could finish, so this PR hasn't had an automated review." -->
## Automated review

<N> automated reviews ran before this PR was ready for you. <Who reviewed, from the list above.> <How the last one ended, from the list above.>

**Fixed**
- [<severity>] <what was wrong, in a line> (<short sha>)

**Set aside**
- <the finding, in a line> — <one reviewer raised it, the other refuted it, and the evidence sided with the refuter, in a few words>

**Left for later**
- <what could be better, in a line>

**Left for you**
- <the finding, in a line> — <why it's left: disputed, with the evidence in a few words / couldn't be settled / kept as the plan designed it>
