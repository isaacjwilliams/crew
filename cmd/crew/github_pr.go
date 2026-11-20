package main

// A PR as GitHub reports it. crew sync reads it and records it in the task
// file, for crew tick's filters and for crew status.

import (
	"encoding/json"
	"strings"
)

// gitHubPR is a PR as GitHub reported it.
type gitHubPR struct {
	State          string // OPEN, MERGED or CLOSED
	Draft          bool
	ReviewDecision string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, or empty
	UpdatedAt      string
	Base           string  // the branch it targets
	CI             ciState // the checks on its head commit (rollup)
	Mergeable      string  // MERGEABLE, CONFLICTING, or UNKNOWN while GitHub works it out
}

// readPR asks GitHub for the PR at url.
func readPR(repo, url string) (gitHubPR, error) {
	out, err := ghQuiet(repo, "pr", "view", url, "--json", "state,isDraft,reviewDecision,updatedAt,baseRefName,statusCheckRollup,mergeable")
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
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return gitHubPR{}, err
	}
	ci, _ := rollup(v.StatusCheckRollup)
	return gitHubPR{v.State, v.IsDraft, v.ReviewDecision, v.UpdatedAt, v.BaseRefName, ci, v.Mergeable}, nil
}

// record writes the PR into the task's front matter, and says whether that
// changed anything.
func (pr gitHubPR) record(t Task) bool {
	mergeable := ""
	switch pr.Mergeable {
	case "CONFLICTING":
		mergeable = conflicting
	case "UNKNOWN":
		mergeable = mergeableUnknown
	}
	changed := false
	for _, kv := range []struct{ key, value string }{
		{"pr_updated", pr.UpdatedAt}, // for prMayHaveNewComments in crew tick
		{"pr_base", pr.Base},         // for retargetReason
		{"ci_state", string(pr.CI)},  // for crew tick, which runs crew ci-fix only while CI is failing
		{"mergeable", mergeable},     // for crew tick, which runs crew catch-up only while the PR conflicts, and for crew status
		{"pr_state", pr.label()},
	} {
		if t.Get(kv.key) != kv.value {
			t.Set(kv.key, kv.value)
			changed = true
		}
	}
	return changed
}

// label words the PR's state for pr_state: merged, closed, draft or open, and,
// while it's open, GitHub's review decision, as in "open, approved".
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

// syncedPR is the PR as crew sync last recorded it in the task file (record).
// Its State is empty when sync never read it.
func syncedPR(t Task) gitHubPR {
	pr := gitHubPR{UpdatedAt: t.Get("pr_updated"), Base: t.Get("pr_base"), CI: ciState(t.Get("ci_state")), Mergeable: "MERGEABLE"}
	switch t.Get("mergeable") {
	case conflicting:
		pr.Mergeable = "CONFLICTING"
	case mergeableUnknown:
		pr.Mergeable = "UNKNOWN"
	}
	state, decision, _ := strings.Cut(t.Get("pr_state"), ", ")
	switch state {
	case "":
	case "merged", "closed":
		pr.State = strings.ToUpper(state)
	default:
		pr.State, pr.Draft = "OPEN", state == "draft"
		pr.ReviewDecision = strings.ToUpper(strings.ReplaceAll(decision, " ", "_"))
	}
	return pr
}
