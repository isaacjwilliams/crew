<!-- The notes for a crew release: the message of its annotated tag, which the release workflow
     publishes on the release page titled "crew <version>". No tool reads this file: the lead
     fills it in by hand, and CLAUDE.md ("Versions and releases") says when and how to tag.

     Write for people who run crew: what changed for them, in plain words and short sentences.
     Leave out this comment, the <…> placeholders and any empty part. Never start a line with "#":
     git drops such lines from a tag message, so use bold labels, not headings.
     - No version line: the page title already has it. Start with the most important change.
     - One paragraph per change that matters to people who run crew, most important first. Open
       each with a bold sentence that says what changed for them. Name any new command or setting,
       with its default.
     - Smaller fixes: one sentence each, in one paragraph.
     - Removed or renamed: one line each, the old name → the new one, or what replaces a removed
       thing.
     - New on your repo: anything crew now does or posts on a project's repo that your
       collaborators will see (comments, reactions, reruns).
     - Upgrading is always there. When there are no steps, it's just "**Upgrading.** Nothing to do:
       `crew upgrade` installs everything." When an agent's instructions changed
       (`git diff <last tag>..<merge sha> -- agents/`), add the line about sessions started
       before the upgrade, and say what it means for this release's change. A skill's changes
       (`.claude/skills/`) reach running sessions the next time they use it, so they need no line.
     - Skipping versions: only when an earlier release has upgrade steps that someone skipping it
       would miss. -->
**<What changed for people who run crew, in a sentence.>** <What it does, for whom, and any new command or setting with its default.>

**<The next most important change.>** <…>

**Smaller fixes.** <One sentence each.>

**Removed or renamed.**
- <old name> → <new name, or what replaces it>

**New on your repo.** <What your collaborators will now see crew do or post.>

**Upgrading.**
- Before you upgrade: <a step>
- After upgrading: <a step>
- <Which agents' instructions changed.> Sessions started before the upgrade keep their old instructions until they're compacted or started fresh (a new `lead`, or `crew followup TASK --fresh --note "crew was upgraded"` for a worker): <what that means for this release's change>.

**Skipping versions?** Read the v<x.y.z> notes too: <what that release needs you to do>.
