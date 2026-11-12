<!-- The notes for a crew release. They are the message of the annotated tag v<version>, and the
     release workflow publishes them on the release page, under the title "crew <version>".
     Write them from the changes since the last tag (`git log <last tag>..origin/main` and the
     diff), check every claim against the code, and save them to a file for
     `git tag -a v<version> <sha> -F <file>`.

     The notes are for people who run crew: what changed for them, in plain words and short
     sentences. Leave out this comment, the <…> placeholders and any part that would be empty,
     except Upgrading, which is always there. Don't start a line with "#": git drops it from a tag
     message, so use bold labels, not headings.
     - No version line: the page is already titled with the version. Start with the most
       important change.
     - One paragraph per change that matters to people who run crew, most important first. Open
       each with a bold sentence that says what changed for them. Name any new command or setting,
       with its default.
     - Smaller fixes: one sentence each, in one paragraph.
     - Removed or renamed: one line each, the old name → the new one, or what replaces a removed
       thing.
     - New on your repo: anything crew now does or posts on a project's repo that your
       collaborators will see (comments, reactions, reruns).
     - Upgrading: keep one of the two forms below, either "Nothing to do: `crew upgrade` installs
       everything." or the steps, under "Before you upgrade" and "After upgrading". When an agent's instructions changed
       (`git diff <last tag>..v<version> -- agents/ .claude/`), say that sessions started before
       the upgrade keep their old instructions until they're compacted or started fresh, and what
       that means for this release's change.
     - Skipping versions: only when an earlier release has upgrade steps of its own that someone
       skipping it would miss. -->
**<What changed for people who run crew, in a sentence.>** <What it does, for whom, and any new command or setting with its default.>

**<The next most important change.>** <…>

**Smaller fixes.** <One sentence each.>

**Removed or renamed.**
- <old name> → <new name, or what replaces it>

**New on your repo.** <What your collaborators will now see crew do or post.>

**Upgrading.** Nothing to do: `crew upgrade` installs everything.

**Upgrading.**
- Before you upgrade: <a step>
- After upgrading: <a step>
- <Which agents' instructions changed.> Sessions started before the upgrade keep their old instructions until they're compacted or started fresh (a new `lead`, or `crew followup TASK --fresh` for a worker): <what that means for this release's change>.

**Skipping versions?** Read the v<x.y.z> notes too: <what that release needs you to do>.
