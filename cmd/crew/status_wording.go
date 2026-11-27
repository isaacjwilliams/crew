package main

// How crew status words what a line says twice: in full for the plain text,
// and as the live view shows it in its columns. Beside a PR's number, the
// live view shows facts about the PR on GitHub as marks. In its notes, it
// words only what's the user's to act on, shows where the line stands as a
// sign, and leaves the rest to the strip under the report, which words the
// cursor's notes in full.

import (
	"fmt"
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// wording is something a status line says: text is its full words, as the
// plain text prints them, and live is what the live view shows in its place.
// An empty live shows nothing there, leaving the words to the strip.
type wording struct{ text, live string }

// same is words that read the same in the plain text and the live view.
func same(s string) wording { return wording{s, s} }

// usersWords is how the live view shows what's the user's to act on.
var usersWords = lipgloss.NewStyle().Bold(true)

// forUser is a note on something that's the user's to act on: the live view
// says it in a word or two, in bold.
func forUser(text, words string) wording { return wording{text, usersWords.Render(words)} }

// standing is a note on where a line stands that asks nothing of the user: the
// live view shows its sign.
func standing(text string, s sign) wording { return wording{text, s.String()} }

// stripOnly is a note the live view leaves to the strip: what the PR's marks
// already show, and details that ask nothing of the user.
func stripOnly(text string) wording { return wording{text: text} }

// texts are the full words of each of fields.
func texts(fields []wording) []string {
	var out []string
	for _, f := range fields {
		out = append(out, f.text)
	}
	return out
}

// joinNotes is a line's notes as one field: the text has every note's words,
// and the live view each word or sign once.
func joinNotes(notes []wording) wording {
	var lives []string
	for _, n := range notes {
		if n.live != "" && !slices.Contains(lives, n.live) {
			lives = append(lives, n.live)
		}
	}
	return wording{strings.Join(texts(notes), "  "), strings.Join(lives, " ")}
}

// questionsNote is a note on a task's open questions, which are the user's to
// answer.
func questionsNote(n int) wording {
	return forUser(fmt.Sprintf("%d open question(s)", n), plural(n, "question"))
}

// capNote is a note on a goal whose max_in_flight crew can't read.
func capNote(err error) wording { return forUser(err.Error(), "max_in_flight?") }

// plural is "1 thing" or "n things".
func plural(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// sign is a short symbol the live view shows in a color. Its symbol alone
// tells it from every other sign, so the view reads the same without color.
type sign struct {
	symbol string
	color  color.Color
}

func (s sign) String() string { return lipgloss.NewStyle().Foreground(s.color).Render(s.symbol) }

// The marks for a PR's checks, one for each state crew sync records (ciState),
// and one for a PR crew hasn't read yet. Only passing has a tick.
var (
	ciPassingMark   = sign{"CI✓", lipgloss.Green}
	ciFailingMark   = sign{"CI✗", lipgloss.Red}
	ciRunningMark   = sign{"CI⧗", lipgloss.Yellow}
	ciCancelledMark = sign{"CI⊘", lipgloss.BrightBlack}
	ciNoneMark      = sign{"CI-", lipgloss.BrightBlack}
	ciUnknownMark   = sign{"CI?", lipgloss.BrightBlack}
)

// The marks for the rest of what GitHub says of a PR.
var (
	mergedMark           = sign{"⋈", lipgloss.Magenta}
	closedMark           = sign{"⊗", lipgloss.BrightBlack}
	draftMark            = sign{"◌", lipgloss.BrightBlack}
	conflictsMark        = sign{"↯", lipgloss.Red}
	checkingMark         = sign{"↯?", lipgloss.Yellow} // GitHub is still working out whether it conflicts
	behindMark           = sign{"⇣", lipgloss.Yellow}
	approvedMark         = sign{"✦", lipgloss.Green}
	changesRequestedMark = sign{"✎", lipgloss.Red}
	reviewRequiredMark   = sign{"⦿", lipgloss.Yellow}
	blockedMark          = sign{"⊖", lipgloss.Red} // by a branch rule none of the others names
)

// The signs for where a line stands, in its notes.
var (
	underWaySign         = sign{"⧗", lipgloss.Yellow}  // something else is at work on it: its worker, crew tick or a review
	waitingSign          = sign{"⋯", lipgloss.Blue}    // it waits on other tasks
	goAheadSign          = sign{"»", lipgloss.Green}   // its go-ahead is met, so crew tick dispatches it
	mustMoveSign         = sign{"↻", lipgloss.Cyan}    // its branch or its PR has to move to another base
	learningSign         = sign{"⌕", lipgloss.Blue}    // a spike that informs it is still finding out
	pausedSign           = sign{"ǁ", lipgloss.Magenta} // a pause stopped its worker
	goalFullSign         = sign{"▮", lipgloss.Yellow}  // its goal has as many tasks in flight as it allows
	noSecurityReviewSign = sign{"⚷", lipgloss.Red}     // it's high risk and its security review hasn't run
	lastSyncSign         = sign{"◷", lipgloss.Yellow}  // GitHub couldn't be read, so its merge check goes by the last sync
)

// prMarks are the facts about a PR the live view shows beside its number: its
// checks, then whether it merged or closed, or else what GitHub says stands in
// its way and whether it's approved.
func prMarks(pr gitHubPR) string {
	if pr.State == "" {
		return ciUnknownMark.String()
	}
	marks := []sign{ciMark(pr)}
	switch pr.State {
	case "MERGED":
		marks = append(marks, mergedMark)
	case "CLOSED":
		marks = append(marks, closedMark)
	default:
		marks = append(marks, openPRMarks(pr)...)
	}
	var out []string
	for _, m := range marks {
		out = append(out, m.String())
	}
	return strings.Join(out, " ")
}

// ciMark is the mark for the checks of a PR crew has read.
func ciMark(pr gitHubPR) sign {
	switch pr.CI {
	case ciPassing:
		return ciPassingMark
	case ciFailing:
		return ciFailingMark
	case ciRunning:
		return ciRunningMark
	case ciCancelled:
		return ciCancelledMark
	}
	return ciNoneMark
}

// openPRMarks are what GitHub says of an open PR besides its checks. GitHub
// blocks a PR for most of what's here too, so the blocked mark shows only when
// nothing else explains it.
func openPRMarks(pr gitHubPR) []sign {
	var marks []sign
	for _, m := range []struct {
		holds bool
		mark  sign
	}{
		{pr.Draft, draftMark},
		{pr.Mergeable == conflicting, conflictsMark},
		{pr.Mergeable == mergeableUnknown, checkingMark},
		{pr.MergeState == "BEHIND", behindMark},
		{pr.ReviewDecision == "CHANGES_REQUESTED", changesRequestedMark},
		{pr.ReviewDecision == "REVIEW_REQUIRED", reviewRequiredMark},
	} {
		if m.holds {
			marks = append(marks, m.mark)
		}
	}
	checksInTheWay := pr.CI != ciPassing && pr.CI != ""
	if len(marks) == 0 && !checksInTheWay && pr.MergeState == "BLOCKED" {
		marks = append(marks, blockedMark)
	}
	if pr.ReviewDecision == "APPROVED" {
		marks = append(marks, approvedMark)
	}
	return marks
}
