<!-- The body of a worker's PR. Fill it in from the task file when you open the PR, and keep it
     true with `gh pr edit` as the branch changes. Leave out this comment and the <…> placeholders.
     It's for the user's collaborators too, so it never mentions the ledger, tasks, agents or crew.
     From this alone, a reader should be able to tell whether to merge the PR outright, glance at
     the evidence, or read the code closely. Keep it short: the diff shows what changed.

     - A stacked PR starts with "Stacked on #<parent PR>." on a line of its own.
     - The objective, in a sentence: the outcome this PR reaches, from the task's Goal, and why it
       matters.
     - Risk: first the task's tier and the look it asks for, as one of "Low risk: approve from the
       evidence", "Medium risk: read the diff" or "High risk: read the diff; security review ran".
       Then what could break, for whom, and how far it reaches. When it's small, say so in a line.
     - How to revert: whether reverting the merge is enough, and anything a revert alone doesn't
       undo, such as data written, settings changed or a deploy step.
     - Evidence: one item per gate (acceptance criterion), numbered as `crew show` prints the
       task's evidence. The criterion in a line, the commit it ran on as "(on <sha>)", as its
       recorded evidence has it,
       and the command that shows it, then its output in a <details> block. Where only a look
       shows it, say what you saw and how; for a check the task leaves to the user, why it
       can't run here, then "To try:" and what to try.
       A published screenshot or file goes in the item it shows, as the
       Markdown line printed when it was published; never give a path on someone's disk.
     - Notes for reviewers: only for a trade-off or an open question. Leave it out otherwise. -->
<The objective, in a sentence.>

## Risk

<Low | Medium | High> risk: <the look>.

<What could break, for whom, and how far it reaches.>

## How to revert

<Revert the merge, plus anything a revert alone doesn't undo.>

## Evidence

1. <The criterion, in a line> (on <sha>): `<command>`
   <details><summary>Output</summary>

   ```
   <its output, trimmed to what shows the result>
   ```

   </details>

## Notes for reviewers

<A trade-off or an open question.>
