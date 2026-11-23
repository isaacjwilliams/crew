package main

// A PR as GitHub reports it. crew sync reads it and records it in the task
// file, for crew tick's filters and for crew status.

import (
	"encoding/json"
	"strings"
)

// gitHubPR is a PR as GitHub reported it.
type gitHubPR struct {
	URL            string // the PR crew asked for
	State          string // OPEN, MERGED or CLOSED
	Draft          bool
	ReviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, or empty
	UpdatedAt      string
	Base           string  // the branch it targets
	CI             ciState // the checks on its head commit (rollup)
	Mergeable      string  // conflicting, mergeableUnknown, or empty when it merges cleanly
	// MergeState is GitHub's verdict on merging it now: CLEAN, BEHIND (its base
	// moved, and the branch rules want it up to date), BLOCKED (by a branch
	// rule) and so on.
	MergeState string
}

// A PR's Mergeable, as crew sync records it: GitHub says it conflicts with its
// base, or GitHub is still working that out.
const (
	conflicting      = "conflicting"
	mergeableUnknown = "unknown"
)

// readPR asks GitHub for the PR at url.
func readPR(repo, url string) (gitHubPR, error) {
	out, err := ghQuiet(repo, "pr", "view", url, "--json", "state,isDraft,reviewDecision,updatedAt,baseRefName,statusCheckRollup,mergeable,mergeStateStatus")
	if err != nil {
		return gitHubPR{}, err
	}
	var v struct {
		State             string    `json:"state"`
		IsDraft           bool      `json:"isDraft"`
		ReviewDecision    string    `json:"reviewDecision"`
		UpdatedAt         string    `json:"updatedAt"`
		BaseRefName       string    `json:"baseRefName"`
		StatusCheckRollup []ciCheck `json:"statusCheckRollup"`
		Mergeable         string    `json:"mergeable"`
		MergeStateStatus  string    `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return gitHubPR{}, err
	}
	ci, _ := rollup(v.StatusCheckRollup)
	mergeable := ""
	switch v.Mergeable {
	case "CONFLICTING":
		mergeable = conflicting
	case "UNKNOWN":
		mergeable = mergeableUnknown
	}
	return gitHubPR{URL: url, State: v.State, Draft: v.IsDraft, ReviewDecision: v.ReviewDecision, UpdatedAt: v.UpdatedAt,
		Base: v.BaseRefName, CI: ci, Mergeable: mergeable, MergeState: v.MergeStateStatus}, nil
}

// record writes the PR into the front matter of t, the task whose PR it is,
// and says whether that changed anything. It writes nothing into a task whose
// PR is another, as when the ledger changed while crew waited for GitHub.
func (pr gitHubPR) record(t Task) bool {
	if pr.URL != t.PR() {
		return false
	}
	changed := false
	for _, kv := range []struct{ key, value string }{
		{"pr_updated", pr.UpdatedAt},                    // for prMayHaveNewComments in crew tick
		{"pr_base", pr.Base},                            // for retargetReason
		{"ci_state", string(pr.CI)},                     // for crew tick, which runs crew ci-fix only while CI is failing, and for crew status
		{"mergeable", pr.Mergeable},                     // for crew tick, which runs crew catch-up only while the PR conflicts, and for crew status
		{"merge_state", strings.ToLower(pr.MergeState)}, // for crew status
		{"pr_state", pr.label()},
	} {
		if t.Get(kv.key) != kv.value {
			t.Set(kv.key, kv.value)
			changed = true
		}
	}
	return changed
}

// noteMerge marks t merged when this, its PR, is, and says whether it did.
// crew sync does it for every task, and crew hand-over for a first run's
// parent, which may have merged since sync last looked.
func (pr gitHubPR) noteMerge(t Task) bool {
	if pr.URL != t.PR() || pr.State != "MERGED" || t.Status() == Merged {
		return false
	}
	t.SetStatus(Merged)
	t.Touch()
	t.Log("PR merged")
	return true
}

// label words the PR's state for pr_state: merged, closed, draft or open, and,
// while it's open, GitHub's review decision, as in "open, approved". syncedPR
// reads it back, so the two change together.
func (pr gitHubPR) label() string {
	label := "open"
	switch {
	case pr.State == "MERGED":
		return "merged"
	case pr.State == "CLOSED":
		return "closed"
	case pr.Draft:
		label = "draft"
	}
	if pr.ReviewDecision != "" {
		label += ", " + strings.ToLower(strings.ReplaceAll(pr.ReviewDecision, "_", " "))
	}
	return label
}

// syncedPR is the PR as crew sync last recorded it in the task file (record),
// reading pr_state back the way label wrote it. Its State is empty when sync
// never read it.
func syncedPR(t Task) gitHubPR {
	pr := gitHubPR{URL: t.PR(), UpdatedAt: t.Get("pr_updated"), Base: t.Get("pr_base"), CI: ciState(t.Get("ci_state")), Mergeable: t.Get("mergeable"),
		MergeState: strings.ToUpper(t.Get("merge_state"))}
	state, decision, _ := strings.Cut(t.Get("pr_state"), ", ")
	switch state {
	case "": // never synced
	case "merged", "closed":
		pr.State = strings.ToUpper(state)
	default:
		pr.State, pr.Draft = "OPEN", state == "draft"
		pr.ReviewDecision = strings.ToUpper(strings.ReplaceAll(decision, " ", "_"))
	}
	return pr
}
