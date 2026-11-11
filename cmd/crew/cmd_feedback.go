package main

// PR comments: crew feedback reads them into the task, crew review asks for
// another automated review as a review round of its own, crew reply answers
// comments, and crew review-summary posts the summary of automated review.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// replyMarker tags the replies workers post, so crew feedback doesn't read them
// back as new feedback (they're posted under the user's own account).
const replyMarker = "<!-- crew -->"

// gh runs the GitHub CLI in a project's clone, which tells it the repo.
func gh(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir, cmd.Stderr = dir, os.Stderr
	return cmd.Output()
}

// ghQuiet is gh with its errors left out, for lookups that may fail.
func ghQuiet(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	return cmd.Output()
}

// ghLogin is the GitHub login crew acts for.
func (c *Crew) ghLogin() (string, error) {
	if c.login == "" {
		out, err := exec.Command("gh", "api", "user", "--jq", ".login").Output()
		if err != nil || strings.TrimSpace(string(out)) == "" {
			return "", fail("gh is not authenticated (run 'gh auth login')")
		}
		c.login = strings.TrimSpace(string(out))
	}
	return c.login, nil
}

// prOfBranch finds the URL of the PR whose head is branch, or nothing.
func prOfBranch(repo, branch string) string {
	out, _ := ghQuiet(repo, "pr", "list", "--head", branch, "--state", "all", "--json", "url", "--jq", ".[0].url // empty")
	return strings.TrimSpace(string(out))
}

// A review, an inline review comment or a conversation comment, as the GitHub
// REST API returns it.
type ghComment struct {
	ID   json.Number `json:"id"`
	User *struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"user"`
	AuthorAssociation string       `json:"author_association"`
	Body              string       `json:"body"`
	HTMLURL           string       `json:"html_url"`
	CreatedAt         string       `json:"created_at"`
	SubmittedAt       string       `json:"submitted_at"` // reviews
	State             string       `json:"state"`        // reviews
	Path              string       `json:"path"`
	Line              *json.Number `json:"line"`
	OriginalLine      *json.Number `json:"original_line"`
	InReplyTo         *json.Number `json:"in_reply_to_id"`
	ReviewID          *json.Number `json:"pull_request_review_id"`
	PullRequestURL    string       `json:"pull_request_url"`
	IssueURL          string       `json:"issue_url"`

	who string // "me" or "collaborator"
}

// who says whose a comment is: the user's ("me"), a collaborator's (someone with
// write access, not a bot), or anyone else's ("other").
func (cm ghComment) whose(me string) string {
	if cm.User == nil {
		return "other"
	}
	if cm.User.Login == me {
		return "me"
	}
	if cm.User.Type != "Bot" && (cm.AuthorAssociation == "OWNER" || cm.AuthorAssociation == "MEMBER" || cm.AuthorAssociation == "COLLABORATOR") {
		return "collaborator"
	}
	return "other"
}

// where is an inline comment's place in the diff: `path:line`.
func (cm ghComment) where() string {
	line := "?"
	if cm.Line != nil {
		line = cm.Line.String()
	} else if cm.OriginalLine != nil {
		line = cm.OriginalLine.String()
	}
	return "`" + cm.Path + ":" + line + "`"
}

// quoted is a comment's body as a Markdown quote, indented under a list item.
func quoted(body string) string {
	body = strings.ReplaceAll(body, "\r", "")
	if body == "" {
		return ""
	}
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		lines[i] = "  > " + l
	}
	return strings.Join(lines, "\n")
}

// ghList reads every page of a list endpoint: one object per line, as
// `--paginate --jq '.[]'` prints them.
func ghList(repo, endpoint string) ([]ghComment, error) {
	out, err := gh(repo, "api", endpoint, "--paginate", "--jq", ".[]")
	if err != nil {
		return nil, quietly(err)
	}
	var all []ghComment
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	for {
		var cm ghComment
		if err := dec.Decode(&cm); err == io.EOF {
			return all, nil
		} else if err != nil {
			return nil, fmt.Errorf("can't read %s: %v", endpoint, err)
		}
		all = append(all, cm)
	}
}

// relevant keeps the user's and collaborators' comments, without workers' replies.
func relevant(cms []ghComment, me string, keep func(ghComment) bool) []ghComment {
	var out []ghComment
	for _, cm := range cms {
		cm.who = cm.whose(me)
		if cm.who != "other" && !strings.Contains(cm.Body, replyMarker) && keep(cm) {
			out = append(out, cm)
		}
	}
	return out
}

func byWho(cms []ghComment, who string) []ghComment {
	var out []ghComment
	for _, cm := range cms {
		if cm.who == who {
			out = append(out, cm)
		}
	}
	return out
}

// crew feedback TASK [--since ISO]: pulls the PR comments newer than
// feedback_since. The user's own become a review round for the worker; their
// `crew review` comment asks it for another automated review.
// Collaborators' become unchecked items under Open questions: they wait for the
// user to decide what to do, and no agent answers them on its own. Anyone else's
// are left alone. Exits 3 if there was nothing new.
func cmdFeedback(c *Crew, args []string) error {
	if err := need("gh"); err != nil {
		return err
	}
	var arg, since string
	for len(args) > 0 {
		if args[0] == "--since" && len(args) > 1 {
			since, args = args[1], args[2:]
		} else {
			arg, args = args[0], args[1:]
		}
	}
	if arg == "" {
		return usage("crew feedback TASK")
	}
	t, err := c.resolveTask(arg)
	if err != nil {
		return err
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	repo := p.Repo()
	pr := t.PR()
	if pr == "" {
		if pr = prOfBranch(repo, t.Branch()); pr == "" {
			return fail("%s has no PR yet", t.ID())
		}
	}
	_, num := splitLast(pr)
	if since == "" {
		since = t.Get("feedback_since")
	}
	if since == "" {
		since = "1970-01-01T00:00:00Z"
	}
	me, err := c.ghLogin()
	if err != nil {
		return err
	}
	stamp := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	allReviews, err := ghList(repo, "repos/{owner}/{repo}/pulls/"+num+"/reviews")
	if err != nil {
		return err
	}
	reviews := relevant(allReviews, me, func(r ghComment) bool { return r.SubmittedAt > since && r.Body != "" })
	reviewOf := map[string]ghComment{}
	for _, r := range allReviews {
		reviewOf[r.ID.String()] = r
	}
	allInline, err := ghList(repo, "repos/{owner}/{repo}/pulls/"+num+"/comments")
	if err != nil {
		return err
	}
	// An inline comment counts once its review is submitted, from that time; one
	// in a review still pending isn't the user's to send yet.
	inline := relevant(allInline, me, func(cm ghComment) bool {
		var r *ghComment
		if cm.ReviewID != nil {
			if rv, ok := reviewOf[cm.ReviewID.String()]; ok {
				r = &rv
			}
		}
		at := cm.CreatedAt
		if r != nil && r.SubmittedAt != "" {
			at = r.SubmittedAt
		}
		return (r == nil || r.State != "PENDING") && at > since
	})
	allConvo, err := ghList(repo, "repos/{owner}/{repo}/issues/"+num+"/comments")
	if err != nil {
		return err
	}
	convo := relevant(allConvo, me, func(cm ghComment) bool { return cm.CreatedAt > since })

	r := reviewRound{reviews: byWho(reviews, "me"), inline: byWho(inline, "me")}
	for _, cm := range byWho(convo, "me") {
		if isReviewRequest(cm) {
			r.reviewRequest = "comment " + cm.ID.String()
		} else {
			r.convo = append(r.convo, cm)
		}
	}
	theirReviews, theirInline, theirConvo := byWho(reviews, "collaborator"), byWho(inline, "collaborator"), byWho(convo, "collaborator")
	no := len(theirReviews) + len(theirInline) + len(theirConvo)
	if r.empty() && no == 0 {
		fmt.Printf("No new review feedback on PR #%s since %s.\n", num, since)
		return exitQuietly(3)
	}

	var questions strings.Builder
	for _, rv := range theirReviews {
		fmt.Fprintf(&questions, "- [ ] @%s reviewed PR #%s (%s), review %s — [link](%s)\n%s\n", rv.User.Login, num, strings.ToLower(rv.State), rv.ID, rv.HTMLURL, quoted(rv.Body))
	}
	for _, cm := range theirInline {
		fmt.Fprintf(&questions, "- [ ] @%s commented on %s in PR #%s, comment %s — [link](%s)\n%s\n", cm.User.Login, cm.where(), num, cm.ID, cm.HTMLURL, quoted(cm.Body))
	}
	for _, cm := range theirConvo {
		fmt.Fprintf(&questions, "- [ ] @%s commented on PR #%s, comment %s — [link](%s)\n%s\n", cm.User.Login, num, cm.ID, cm.HTMLURL, quoted(cm.Body))
	}

	if err := c.lock(); err != nil {
		return err
	}
	var n int
	var round string
	if !r.empty() {
		n, round = r.add(t, num)
		msg := fmt.Sprintf("review round %d recorded (%d reviews, %d inline, %d conversation)", n, len(r.reviews), len(r.inline), len(r.convo))
		if r.reviewRequest != "" {
			msg += ", with a request for another automated review"
		}
		t.Log(msg)
	}
	if questions.Len() > 0 {
		t.AppendToSection("## Open questions", questions.String())
		t.Log(fmt.Sprintf("%d collaborator comment(s) recorded under Open questions for the user", no))
	}
	t.Set("pr", pr)
	t.Set("feedback_since", stamp)
	t.Touch()
	c.commit("feedback " + p.Name + "/" + t.ID())
	if questions.Len() > 0 {
		fmt.Print("For the user (under Open questions):\n" + questions.String())
	}
	if round == "" {
		return nil
	}
	fmt.Print(round)
	warnManyRounds(p, n)
	return nil
}

// crew review TASK: asks the task's worker for another automated review pass on
// its open PR. The request is a review round, like the one the user's `crew
// review` comment on the PR makes, so it reaches the worker the way every round
// does: now if the worker is idle, or from crew tick once it is.
func cmdReview(c *Crew, args []string) error {
	if err := need("claude"); err != nil {
		return err
	}
	if len(args) != 1 {
		return usage("crew review TASK")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	if t.PR() == "" || !t.Status().InFlight() || t.Get("pr_state") == "closed" {
		return fail("%s has no open PR", t.ID())
	}
	if err := c.lock(); err != nil {
		return err
	}
	n, _ := reviewRound{reviewRequest: "crew review"}.add(t, t.PRNumber())
	t.Log(fmt.Sprintf("review round %d recorded: a request for another automated review", n))
	t.Touch()
	c.commit("review " + p.Name + "/" + t.ID())
	c.unlock()
	warnManyRounds(p, n)
	if as, job := c.agents(), t.WorkerJob(); as.jobBusy(job) {
		fmt.Printf("round %d asks for another automated review; crew tick sends it once worker %s is free (%s)\n", n, job, as.jobState(job))
		return nil
	}
	return c.followup(t, false, "")
}

// isReviewRequest: the comment's whole text is "crew review", in any case. Like
// "@dependabot rebase", it's a command: the user asks for another automated
// review pass, rather than commenting on the change.
func isReviewRequest(cm ghComment) bool {
	return strings.EqualFold(strings.TrimSpace(cm.Body), "crew review")
}

// A review round: the user's new comments on the PR, for the worker that wrote
// it, and their request for another automated review pass, if they made one.
type reviewRound struct {
	reviews, inline, convo []ghComment
	reviewRequest          string // where it came from: "comment <id>" or "crew review"
}

func (r reviewRound) empty() bool {
	return len(r.reviews)+len(r.inline)+len(r.convo) == 0 && r.reviewRequest == ""
}

// add writes the round into the task as its next, before ## Log, and counts it
// in review_rounds. It returns the round's number and text. Run it with the
// ledger lock held.
func (r reviewRound) add(t Task, prNumber string) (int, string) {
	n := atoiOr0(t.Get("review_rounds")) + 1
	var b strings.Builder
	fmt.Fprintf(&b, "### Round %d — %s (PR #%s)\n\n", n, time.Now().Format("2006-01-02 15:04"), prNumber)
	if len(r.reviews) > 0 {
		b.WriteString("**Review summaries**\n")
		for _, rv := range r.reviews {
			fmt.Fprintf(&b, "- [ ] review %s (%s)\n%s\n", rv.ID, strings.ToLower(rv.State), quoted(rv.Body))
		}
		b.WriteString("\n")
	}
	if len(r.inline) > 0 {
		b.WriteString("**Inline comments**\n")
		for _, cm := range r.inline {
			thread := ""
			if cm.InReplyTo != nil {
				thread = " (in thread " + cm.InReplyTo.String() + ")"
			}
			fmt.Fprintf(&b, "- [ ] %s — comment %s%s — [link](%s)\n%s\n", cm.where(), cm.ID, thread, cm.HTMLURL, quoted(cm.Body))
		}
		b.WriteString("\n")
	}
	if len(r.convo) > 0 {
		b.WriteString("**Conversation comments**\n")
		for _, cm := range r.convo {
			fmt.Fprintf(&b, "- [ ] comment %s — [link](%s)\n%s\n", cm.ID, cm.HTMLURL, quoted(cm.Body))
		}
		b.WriteString("\n")
	}
	if r.reviewRequest != "" {
		fmt.Fprintf(&b, "- [ ] Automated review requested (%s)\n\n", r.reviewRequest)
	}
	t.InsertBefore("## Log", b.String())
	t.Set("review_rounds", fmt.Sprint(n))
	return n, b.String()
}

// warnManyRounds warns once a task has had more than MAX_REVIEW_ROUNDS rounds.
func warnManyRounds(p *Project, n int) {
	if max, err := intSetting(p.Settings, "MAX_REVIEW_ROUNDS"); err == nil && n > max {
		warn("this is round %d (MAX_REVIEW_ROUNDS=%d); consider whether the plan needs a rethink", n, max)
	}
}

func atoiOr0(s string) int {
	n := 0
	fmt.Sscan(s, &n)
	return n
}

var commentID = regexp.MustCompile(`^[0-9]+$`)

// crew reply TASK COMMENT_ID TEXT: replies to one of the review comments a round
// lists, on the task's own PR only. An inline comment gets a reply in its thread.
// A conversation comment or a review summary has no thread, so the reply is a
// new PR comment that quotes it first.
func cmdReply(c *Crew, args []string) error {
	if err := need("gh"); err != nil {
		return err
	}
	if len(args) == 0 {
		return usage("crew reply TASK COMMENT_ID TEXT")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	if len(args) < 2 || args[1] == "" {
		return fail("comment id required")
	}
	cid, text := args[1], strings.Join(args[2:], " ")
	if text == "" {
		return fail("reply text required")
	}
	if !commentID.MatchString(cid) {
		return fail("comment id must be a number, not '%s'", cid)
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	if v := p.Settings["REPLY_TO_THREADS"]; v != "yes" {
		return fail("REPLY_TO_THREADS is '%s'; not replying", v)
	}
	if t.PR() == "" {
		return fail("task has no PR")
	}
	repo, num := p.Repo(), t.PRNumber()
	body := text + "\n\n" + replyMarker
	lookup := func(endpoint string) (ghComment, bool) {
		var cm ghComment
		out, err := ghQuiet(repo, "api", endpoint)
		if err != nil {
			return cm, false
		}
		dec := json.NewDecoder(bytes.NewReader(out))
		dec.UseNumber()
		return cm, dec.Decode(&cm) == nil
	}
	if cm, ok := lookup("repos/{owner}/{repo}/pulls/comments/" + cid); ok {
		if !strings.HasSuffix(cm.PullRequestURL, "/pulls/"+num) {
			return fail("comment %s isn't on PR #%s", cid, num)
		}
		// GitHub only accepts replies to a thread's top-level comment.
		root := cm.ID
		if cm.InReplyTo != nil {
			root = *cm.InReplyTo
		}
		if _, err := gh(repo, "api", "--silent", "repos/{owner}/{repo}/pulls/"+num+"/comments/"+root.String()+"/replies", "-f", "body="+body); err != nil {
			return quietly(err)
		}
		fmt.Printf("replied to comment %s in its thread\n", cid)
		return nil
	}
	cm, ok := lookup("repos/{owner}/{repo}/issues/comments/" + cid)
	kind := "Comment"
	if ok {
		if !strings.HasSuffix(cm.IssueURL, "/issues/"+num) {
			return fail("comment %s isn't on PR #%s", cid, num)
		}
	} else if cm, ok = lookup("repos/{owner}/{repo}/pulls/" + num + "/reviews/" + cid); ok {
		kind = "Review"
	} else {
		return fail("%s isn't a review comment, conversation comment or review on PR #%s", cid, num)
	}
	first := ""
	for _, l := range strings.Split(strings.ReplaceAll(cm.Body, "\r", ""), "\n") {
		if strings.TrimSpace(l) != "" {
			first = l
			break
		}
	}
	if r := []rune(first); len(r) > 120 {
		first = string(r[:117]) + "…"
	}
	quote := fmt.Sprintf("> [%s](%s): %s", kind, cm.HTMLURL, first)
	if _, err := gh(repo, "api", "--silent", "repos/{owner}/{repo}/issues/"+num+"/comments", "-f", "body="+quote+"\n\n"+body); err != nil {
		return quietly(err)
	}
	fmt.Printf("replied to %s %s on PR #%s\n", strings.ToLower(kind), cid, num)
	return nil
}

// summaryMarker tags the automated-review summary a worker posts on its PR, so
// crew review-summary finds it again and edits it rather than posting another.
const summaryMarker = "<!-- crew review-summary -->"

// crew review-summary TASK FILE: posts FILE as the automated-review summary on
// the task's own PR. A PR has one: if a summary from this GitHub account is
// there already, it is edited to FILE instead.
func cmdReviewSummary(c *Crew, args []string) error {
	if err := need("gh"); err != nil {
		return err
	}
	if len(args) != 2 {
		return usage("crew review-summary TASK FILE")
	}
	t, err := c.resolveTask(args[0])
	if err != nil {
		return err
	}
	text, err := os.ReadFile(args[1])
	if err != nil {
		return fail("can't read %s: %v", args[1], err)
	}
	if strings.TrimSpace(string(text)) == "" {
		return fail("%s is empty", args[1])
	}
	if t.PR() == "" {
		return fail("task has no PR")
	}
	p, err := c.loadProject(t.Project())
	if err != nil {
		return err
	}
	me, err := c.ghLogin()
	if err != nil {
		return err
	}
	repo, num := p.Repo(), t.PRNumber()
	// summaryMarker finds this comment again; replyMarker keeps crew feedback from
	// reading it back as the user's feedback.
	body := strings.TrimRight(string(text), "\n") + "\n\n" + summaryMarker + "\n" + replyMarker
	comments, err := ghList(repo, "repos/{owner}/{repo}/issues/"+num+"/comments")
	if err != nil {
		return err
	}
	for _, cm := range comments {
		if cm.whose(me) == "me" && strings.Contains(cm.Body, summaryMarker) {
			if _, err := gh(repo, "api", "--silent", "--method", "PATCH", "repos/{owner}/{repo}/issues/comments/"+cm.ID.String(), "-f", "body="+body); err != nil {
				return quietly(err)
			}
			fmt.Printf("updated the automated-review summary on PR #%s\n", num)
			return nil
		}
	}
	if _, err := gh(repo, "api", "--silent", "repos/{owner}/{repo}/issues/"+num+"/comments", "-f", "body="+body); err != nil {
		return quietly(err)
	}
	fmt.Printf("posted the automated-review summary on PR #%s\n", num)
	return nil
}
