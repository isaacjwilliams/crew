<!-- The automated-review summary on a worker's PR, one for each pass of automated review. Fill it
     in from this pass's reviews in the task file's Automated reviews record alone, then post it
     with `crew review-summary <task> <file>`. Leave out this comment, the <…> placeholders and any
     section that would be empty. One line per finding: a reader should take it in at a glance.
     It's for the user's collaborators too, so it never mentions the ledger, tasks, proposals or
     agents. Withdrawn findings aren't listed. A finding left for the user because it already failed at
     the fork point goes under "Already on <base branch>", not "Left for you".

     <N> counts every review in this pass, including one whose reviewers failed. When the record
     has a "Summary posted" line before this pass's reviews, open instead of the line below with
     "<N> more automated reviews of this PR ran since the last summary." ("1 more automated review
     …" for one).
     The sentences after it say who reviewed, from the Reviewers and Rounds lines:
     - the Claude reviewer only: "Each was by a Claude reviewer." ("It was by …" after a single review)
     - with Codex: "Each was by a Claude reviewer and Codex."
     - when some review's Rounds line is above 0, add "They answered each other's findings in reviews 1
       and 2." (name those reviews; "in each" when every review had a round)
     - for a reviewer that didn't report in some review, add "Codex didn't report in review 2, so the
       Claude reviewer did that one alone." or "The Claude reviewer didn't report in review 2, so Codex
       did that one alone." (name each review; "in any of them" when it never reported)
     The next says what they read. The PR's first review reads all of it. Each later one reads only
     the commits since the review before, or the whole PR again when it can't (after a rebase, say).
     A Range line that says "since review <N>" read the new commits; one that says "since <base
     branch>" read the whole PR:
     - all the whole PR: "Each read the whole PR." ("It read the whole PR." after a single review)
     - all the new commits: "Each read only the commits since the review before it." ("It read only
       the commits since review 2." after a single review)
     - some of each: "Reviews 1 and 3 read the whole PR, and the others only the commits since the
       review before." (name the reviews that read the whole PR)
     The line after that says how the last review ended:
     - clean: "The last came back clean." ("It came back clean." after a single review)
     - the limit ran out after fixes: "The limit ran out: the fixes for the last review haven't been reviewed again."
     - only Codex reported in it: "The Claude reviewer couldn't finish the last, so only Codex has seen the latest fixes."
     - no reviewer reported in it: "The last couldn't finish, so the latest fixes haven't been reviewed again."
     - no reviewer reported in any review: "None could finish, so this PR hasn't had an automated review." -->
## Automated review

<N> automated reviews ran before this PR was ready for you. <Who reviewed, from the list above.> <What they read, from the list above.> <How the last one ended, from the list above.>

**Fixed**
- [<severity>] <what was wrong, in a line> (<short sha>)

**Set aside**
- <the finding, in a line> — <one reviewer raised it, the other refuted it, and the evidence sided with the refuter, in a few words>

**Left for later**
- <what could be better, in a line>

**Already on <base branch>**
- [<severity>] <the finding, in a line> — <the size of its fix, in a few words>

**Left for you**
- <the finding, in a line> — <why it's left: disputed, with the evidence in a few words / couldn't be settled / kept as the plan designed it / only a deploy window would close it, and what that costs in a few words / reviews disagreed about it / still open from an earlier review>
