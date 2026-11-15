<!-- The body of a worker's PR. Fill it in from the task file when you open the PR, and keep it
     true with `gh pr edit` as the branch changes. Leave out this comment and the <…> placeholders.
     It's for the user's collaborators too, so it never mentions the ledger, tasks, agents or crew.
     From this alone, a reader should be able to tell whether to merge the PR outright, glance at
     the evidence, or read the code closely. Keep it short: the diff shows what changed.

     - A stacked PR starts with "Stacked on #<parent PR>." on a line of its own.
     - The summary: what changed and why, in at most three sentences.
     - Risk: what could break, for whom, and how far it reaches. When it's small, say so in a line.
     - How to revert: whether reverting the merge is enough, and anything a revert alone doesn't
       undo, such as data written, settings changed or a deploy step.
     - Evidence: one item per acceptance criterion, numbered as in the task's Evidence section.
       The criterion in a line and the command that shows it, then its output in a <details>
       block. Where only a look shows it, say what you saw and how; where only a person can
       check it, what to try.
     - Notes for reviewers: only for a trade-off or an open question. Leave it out otherwise. -->
<What changed and why, in at most three sentences.>

## Risk

<What could break, for whom, and how far it reaches.>

## How to revert

<Revert the merge, plus anything a revert alone doesn't undo.>

## Evidence

1. <The criterion, in a line>: `<command>`
   <details><summary>Output</summary>

   ```
   <its output, trimmed to what shows the result>
   ```

   </details>

## Notes for reviewers

<A trade-off or an open question.>
