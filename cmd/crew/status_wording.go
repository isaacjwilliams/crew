package main

// How crew status words what a line says twice: in full for the plain text,
// and as the live view shows it in its columns. Beside a PR's number, the
// live view shows facts about the PR on GitHub as marks.

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// wording is something a status line says: text is its full words, as the
// plain text prints them, and live is what the live view shows in its place.
type wording struct{ text, live string }

// same is words that read the same in the plain text and the live view.
func same(s string) wording { return wording{s, s} }

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
	noChecksMark    = sign{"CI-", lipgloss.BrightBlack}
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
	return noChecksMark
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
