package github

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGitHub is a small stateful imitation of the GitHub REST API: just the
// endpoints the adapter uses, with the behaviors the adapter relies on
// (pagination through Link headers, 404/422 errors, label events, issue
// dependencies, PR lookup by head). It stands in for the network in unit tests.
type fakeGitHub struct {
	t      *testing.T
	srv    *httptest.Server
	owner  string
	repo   string
	token  string
	actor  string
	pageSz int

	mu         sync.Mutex
	seq        int64 // ids of issues, comments, events… share one counter
	tick       int
	milestones map[int]*ghMilestone
	issues     []*ghIssue
	comments   map[int][]*ghComment
	events     []*ghEvent
	blockedBy  map[int][]int
	branches   map[string]string
	prs        []*ghPR
	statuses   map[string][]ghStatus
	checkRuns  map[string][]ghCheckRun
	requests   []string
	// intercept, when set, may answer a request itself (rate-limit tests).
	intercept func(w http.ResponseWriter, r *http.Request) bool
}

type ghMilestone struct {
	Number       int
	Title, State string
}

type ghIssue struct {
	Number    int
	ID        int64
	Title     string
	Body      string
	State     string
	Labels    []string
	Milestone int
	PR        bool
}

type ghComment struct {
	ID   int64
	Body string
	At   time.Time
}

type ghEvent struct {
	ID    int64
	Kind  string
	Issue int
	Label string
	At    time.Time
}

type ghPR struct {
	Number     int
	Title      string
	Body       string
	Head, Base string
	State      string
	Merged     bool
}

type ghStatus struct{ Context, State, URL string }

type ghCheckRun struct{ Name, Status, Conclusion, URL string }

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{
		t: t, owner: "acme", repo: "sandbox", token: "ghp_" + strings.Repeat("x", 36), actor: "kluzo-bot", pageSz: 2,
		milestones: map[int]*ghMilestone{}, comments: map[int][]*ghComment{}, blockedBy: map[int][]int{},
		branches: map[string]string{"main": strings.Repeat("a", 40)}, statuses: map[string][]ghStatus{}, checkRuns: map[string][]ghCheckRun{},
	}
	mux := http.NewServeMux()
	base := "/repos/acme/sandbox"
	mux.HandleFunc("GET "+base+"/milestones/{n}", f.getMilestone)
	mux.HandleFunc("GET "+base+"/issues", f.listIssues)
	mux.HandleFunc("POST "+base+"/issues", f.createIssue)
	mux.HandleFunc("GET "+base+"/issues/events", f.listEvents)
	mux.HandleFunc("GET "+base+"/issues/{n}", f.getIssue)
	mux.HandleFunc("PATCH "+base+"/issues/{n}", f.editIssue)
	mux.HandleFunc("GET "+base+"/issues/{n}/comments", f.listComments)
	mux.HandleFunc("POST "+base+"/issues/{n}/comments", f.createComment)
	mux.HandleFunc("POST "+base+"/issues/{n}/labels", f.addLabels)
	mux.HandleFunc("PUT "+base+"/issues/{n}/labels", f.replaceLabels)
	mux.HandleFunc("GET "+base+"/issues/{n}/dependencies/blocked_by", f.listBlockers)
	mux.HandleFunc("POST "+base+"/issues/{n}/dependencies/blocked_by", f.addBlocker)
	mux.HandleFunc("DELETE "+base+"/issues/{n}/dependencies/blocked_by/{id}", f.removeBlocker)
	mux.HandleFunc("GET "+base+"/git/ref/{ref...}", f.getRef)
	mux.HandleFunc("POST "+base+"/git/refs", f.createRef)
	mux.HandleFunc("GET "+base+"/pulls", f.listPRs)
	mux.HandleFunc("POST "+base+"/pulls", f.createPR)
	mux.HandleFunc("GET "+base+"/pulls/{n}", f.getPR)
	mux.HandleFunc("PATCH "+base+"/pulls/{n}", f.editPR)
	mux.HandleFunc("GET "+base+"/commits/{sha}/check-runs", f.listCheckRuns)
	mux.HandleFunc("GET "+base+"/commits/{sha}/status", f.combinedStatus)

	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		if f.intercept != nil && f.intercept(w, r) {
			return
		}
		// go-github tokens go as "Bearer"; GitHub App installation tokens as "token".
		if auth := r.Header.Get("Authorization"); auth != "Bearer "+f.token && auth != "token "+f.token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "Bad credentials"})
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) now() time.Time {
	f.tick++
	return time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC).Add(time.Duration(f.tick) * time.Second)
}

func (f *fakeGitHub) id() int64 { f.seq++; return f.seq }

// requestCount returns how many requests so far were exactly "METHOD /path".
func (f *fakeGitHub) requestCount(request string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r == request {
			n++
		}
	}
	return n
}

// ---- seeding helpers used by tests ----

func (f *fakeGitHub) addMilestone(title string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := len(f.milestones) + 1
	f.milestones[n] = &ghMilestone{Number: n, Title: title, State: "open"}
	return n
}

func (f *fakeGitHub) commit(branch string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	sha := fmt.Sprintf("%040x", f.id())
	f.branches[branch] = sha
	return sha
}

func (f *fakeGitHub) merge(pr int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prs[pr-1].State, f.prs[pr-1].Merged = "closed", true
}

func (f *fakeGitHub) setStatus(sha, context, state, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.statuses[sha] = append(f.statuses[sha], ghStatus{context, state, target})
}

func (f *fakeGitHub) setCheckRun(sha string, run ghCheckRun) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkRuns[sha] = append(f.checkRuns[sha], run)
}

func (f *fakeGitHub) issue(n int) *ghIssue {
	for _, is := range f.issues {
		if is.Number == n {
			return is
		}
	}
	return nil
}

// ---- JSON shapes ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
}

func invalid(w http.ResponseWriter, msg string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"message": "Validation Failed: " + msg})
}

func decode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

func (f *fakeGitHub) issueJSON(is *ghIssue) map[string]any {
	labels := []map[string]string{}
	for _, l := range is.Labels {
		labels = append(labels, map[string]string{"name": l})
	}
	out := map[string]any{
		"number": is.Number, "id": is.ID, "title": is.Title, "body": is.Body, "state": is.State, "labels": labels,
		"html_url": fmt.Sprintf("https://example.test/issues/%d", is.Number), "milestone": nil,
	}
	if m := f.milestones[is.Milestone]; m != nil {
		out["milestone"] = map[string]any{"number": m.Number, "title": m.Title}
	}
	if is.PR {
		out["pull_request"] = map[string]any{}
	}
	return out
}

func (f *fakeGitHub) prJSON(p *ghPR) map[string]any {
	return map[string]any{
		"number": p.Number, "title": p.Title, "body": p.Body, "state": p.State, "merged": p.Merged,
		"html_url": fmt.Sprintf("https://example.test/pull/%d", p.Number),
		"head":     map[string]any{"ref": p.Head, "sha": f.branches[p.Head]},
		"base":     map[string]any{"ref": p.Base, "sha": f.branches[p.Base]},
	}
}

// paginate writes items[from:to] of the requested page and the Link header.
func (f *fakeGitHub) paginate(w http.ResponseWriter, r *http.Request, total int, emit func(i int) any) {
	q := r.URL.Query()
	per, _ := strconv.Atoi(q.Get("per_page"))
	if per <= 0 || per > f.pageSz {
		per = f.pageSz
	}
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	items := []any{}
	for i := (page - 1) * per; i < min(page*per, total); i++ {
		items = append(items, emit(i))
	}
	if page*per < total {
		next := url.Values{}
		for k, v := range q {
			next[k] = v
		}
		next.Set("page", strconv.Itoa(page+1))
		next.Set("per_page", strconv.Itoa(per))
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?%s>; rel="next"`, f.srv.URL, r.URL.Path, next.Encode()))
	}
	writeJSON(w, http.StatusOK, items)
}

// ---- handlers ----

func (f *fakeGitHub) getMilestone(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	m := f.milestones[n]
	if m == nil {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"number": m.Number, "title": m.Title, "state": m.State, "description": "about " + m.Title, "html_url": fmt.Sprintf("https://example.test/milestone/%d", n)})
}

func (f *fakeGitHub) listIssues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var matching []*ghIssue
	for _, is := range f.issues { // created order == ascending
		if m := q.Get("milestone"); m != "" && m != strconv.Itoa(is.Milestone) {
			continue
		}
		if s := q.Get("state"); s != "" && s != "all" && s != is.State {
			continue
		}
		matching = append(matching, is)
	}
	f.paginate(w, r, len(matching), func(i int) any { return f.issueJSON(matching[i]) })
}

func (f *fakeGitHub) createIssue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title     string
		Body      string
		Labels    []string
		Milestone int
	}
	if err := decode(r, &in); err != nil {
		invalid(w, err.Error())
		return
	}
	if in.Milestone != 0 && f.milestones[in.Milestone] == nil {
		invalid(w, "milestone does not exist")
		return
	}
	is := &ghIssue{Number: len(f.issues) + 1, ID: f.id(), Title: in.Title, Body: in.Body, State: "open", Milestone: in.Milestone}
	f.issues = append(f.issues, is)
	for _, l := range in.Labels {
		f.label(is, l)
	}
	writeJSON(w, http.StatusCreated, f.issueJSON(is))
}

func (f *fakeGitHub) getIssue(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	if is := f.issue(n); is != nil {
		writeJSON(w, http.StatusOK, f.issueJSON(is))
		return
	}
	notFound(w)
}

func (f *fakeGitHub) editIssue(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	is := f.issue(n)
	if is == nil {
		notFound(w)
		return
	}
	var in struct{ Body *string }
	_ = decode(r, &in)
	if in.Body != nil {
		is.Body = *in.Body
	}
	writeJSON(w, http.StatusOK, f.issueJSON(is))
}

func (f *fakeGitHub) listComments(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	if f.issue(n) == nil {
		notFound(w)
		return
	}
	cs := f.comments[n]
	f.paginate(w, r, len(cs), func(i int) any {
		return map[string]any{"id": cs[i].ID, "body": cs[i].Body, "created_at": cs[i].At, "user": map[string]string{"login": f.actor}, "html_url": fmt.Sprintf("https://example.test/issues/%d#c%d", n, cs[i].ID)}
	})
}

func (f *fakeGitHub) createComment(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	if f.issue(n) == nil {
		notFound(w)
		return
	}
	var in struct{ Body string }
	_ = decode(r, &in)
	c := &ghComment{ID: f.id(), Body: in.Body, At: f.now()}
	f.comments[n] = append(f.comments[n], c)
	writeJSON(w, http.StatusCreated, map[string]any{"id": c.ID, "body": c.Body, "html_url": fmt.Sprintf("https://example.test/issues/%d#c%d", n, c.ID)})
}

// label adds a label and records the event.
func (f *fakeGitHub) label(is *ghIssue, l string) {
	if slices.Contains(is.Labels, l) {
		return
	}
	is.Labels = append(is.Labels, l)
	f.events = append(f.events, &ghEvent{ID: f.id(), Kind: "labeled", Issue: is.Number, Label: l, At: f.now()})
}

func (f *fakeGitHub) addLabels(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	is := f.issue(n)
	if is == nil {
		notFound(w)
		return
	}
	var labels []string
	_ = decode(r, &labels)
	for _, l := range labels {
		f.label(is, l)
	}
	writeJSON(w, http.StatusOK, f.issueJSON(is)["labels"])
}

// replaceLabels sets the labels of an issue, recording an unlabeled event for
// each one dropped and a labeled event for each one new.
func (f *fakeGitHub) replaceLabels(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	is := f.issue(n)
	if is == nil {
		notFound(w)
		return
	}
	var labels []string
	_ = decode(r, &labels)
	for _, old := range slices.Clone(is.Labels) {
		if !slices.Contains(labels, old) {
			is.Labels = slices.DeleteFunc(is.Labels, func(l string) bool { return l == old })
			f.events = append(f.events, &ghEvent{ID: f.id(), Kind: "unlabeled", Issue: n, Label: old, At: f.now()})
		}
	}
	for _, l := range labels {
		f.label(is, l)
	}
	writeJSON(w, http.StatusOK, f.issueJSON(is)["labels"])
}

func (f *fakeGitHub) listEvents(w http.ResponseWriter, r *http.Request) {
	f.paginate(w, r, len(f.events), func(i int) any { // newest first
		e := f.events[len(f.events)-1-i]
		return map[string]any{
			"id": e.ID, "event": e.Kind, "created_at": e.At, "actor": map[string]string{"login": f.actor},
			"label": map[string]string{"name": e.Label}, "issue": f.issueJSON(f.issue(e.Issue)),
		}
	})
}

func (f *fakeGitHub) listBlockers(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	if f.issue(n) == nil {
		notFound(w)
		return
	}
	bs := f.blockedBy[n]
	f.paginate(w, r, len(bs), func(i int) any { return f.issueJSON(f.issue(bs[i])) })
}

func (f *fakeGitHub) addBlocker(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	var in struct {
		IssueID int64 `json:"issue_id"`
	}
	_ = decode(r, &in)
	if f.issue(n) == nil {
		notFound(w)
		return
	}
	for _, is := range f.issues {
		if is.ID == in.IssueID {
			if !slices.Contains(f.blockedBy[n], is.Number) {
				f.blockedBy[n] = append(f.blockedBy[n], is.Number)
			}
			writeJSON(w, http.StatusCreated, f.issueJSON(f.issue(n)))
			return
		}
	}
	invalid(w, "issue_id does not exist")
}

func (f *fakeGitHub) removeBlocker(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.PathValue("n"))
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	for _, is := range f.issues {
		if is.ID == id && slices.Contains(f.blockedBy[n], is.Number) {
			f.blockedBy[n] = slices.DeleteFunc(f.blockedBy[n], func(b int) bool { return b == is.Number })
			writeJSON(w, http.StatusOK, f.issueJSON(f.issue(n)))
			return
		}
	}
	notFound(w)
}

func (f *fakeGitHub) getRef(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutPrefix(r.PathValue("ref"), "heads/")
	sha, found := f.branches[name]
	if !ok || !found {
		notFound(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ref": "refs/heads/" + name, "object": map[string]string{"sha": sha, "type": "commit"}})
}

func (f *fakeGitHub) createRef(w http.ResponseWriter, r *http.Request) {
	var in struct{ Ref, SHA string }
	_ = decode(r, &in)
	name, ok := strings.CutPrefix(in.Ref, "refs/heads/")
	if _, exists := f.branches[name]; !ok || exists {
		invalid(w, "Reference already exists")
		return
	}
	if !slices.Contains(f.branchSHAs(), in.SHA) {
		invalid(w, "Object does not exist")
		return
	}
	f.branches[name] = in.SHA
	writeJSON(w, http.StatusCreated, map[string]any{"ref": in.Ref, "object": map[string]string{"sha": in.SHA}})
}

func (f *fakeGitHub) branchSHAs() []string {
	var out []string
	for _, sha := range f.branches {
		out = append(out, sha)
	}
	return out
}

func (f *fakeGitHub) listPRs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var matching []*ghPR
	for i := len(f.prs) - 1; i >= 0; i-- { // newest first
		p := f.prs[i]
		if head := q.Get("head"); head != "" && head != f.owner+":"+p.Head {
			continue
		}
		if base := q.Get("base"); base != "" && base != p.Base {
			continue
		}
		if s := q.Get("state"); s != "" && s != "all" && s != p.State {
			continue
		}
		matching = append(matching, p)
	}
	f.paginate(w, r, len(matching), func(i int) any { return f.prJSON(matching[i]) })
}

func (f *fakeGitHub) createPR(w http.ResponseWriter, r *http.Request) {
	var in struct{ Title, Body, Head, Base string }
	_ = decode(r, &in)
	head, hok := f.branches[in.Head]
	base, bok := f.branches[in.Base]
	switch {
	case !hok || !bok:
		invalid(w, "head or base does not exist")
		return
	case head == base:
		invalid(w, "No commits between "+in.Base+" and "+in.Head)
		return
	}
	n := len(f.issues) + 1 // a PR is also an issue, sharing the numbers
	f.issues = append(f.issues, &ghIssue{Number: n, ID: f.id(), Title: in.Title, State: "open", PR: true})
	p := &ghPR{Number: n, Title: in.Title, Body: in.Body, Head: in.Head, Base: in.Base, State: "open"}
	f.prs = append(f.prs, p)
	writeJSON(w, http.StatusCreated, f.prJSON(p))
}

func (f *fakeGitHub) pr(r *http.Request) *ghPR {
	n, _ := strconv.Atoi(r.PathValue("n"))
	for _, p := range f.prs {
		if p.Number == n {
			return p
		}
	}
	return nil
}

func (f *fakeGitHub) getPR(w http.ResponseWriter, r *http.Request) {
	if p := f.pr(r); p != nil {
		writeJSON(w, http.StatusOK, f.prJSON(p))
		return
	}
	notFound(w)
}

func (f *fakeGitHub) editPR(w http.ResponseWriter, r *http.Request) {
	p := f.pr(r)
	if p == nil {
		notFound(w)
		return
	}
	var in struct{ Body *string }
	_ = decode(r, &in)
	if in.Body != nil {
		p.Body = *in.Body
	}
	writeJSON(w, http.StatusOK, f.prJSON(p))
}

func (f *fakeGitHub) listCheckRuns(w http.ResponseWriter, r *http.Request) {
	runs := f.checkRuns[r.PathValue("sha")]
	if r.URL.Query().Get("filter") == "latest" { // the last report of each name
		var latest []ghCheckRun
		for i := len(runs) - 1; i >= 0; i-- {
			if !slices.ContainsFunc(latest, func(l ghCheckRun) bool { return l.Name == runs[i].Name }) {
				latest = append([]ghCheckRun{runs[i]}, latest...)
			}
		}
		runs = latest
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	from, to := min((page-1)*f.pageSz, len(runs)), min(page*f.pageSz, len(runs))
	items := []any{}
	for _, run := range runs[from:to] {
		items = append(items, map[string]any{"name": run.Name, "status": run.Status, "conclusion": run.Conclusion, "details_url": run.URL})
	}
	if to < len(runs) {
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d>; rel="next"`, f.srv.URL, r.URL.Path, page+1))
	}
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(runs), "check_runs": items})
}

func (f *fakeGitHub) combinedStatus(w http.ResponseWriter, r *http.Request) {
	var latest []ghStatus // the last report of each context
	all := f.statuses[r.PathValue("sha")]
	for i := len(all) - 1; i >= 0; i-- {
		if !slices.ContainsFunc(latest, func(l ghStatus) bool { return l.Context == all[i].Context }) {
			latest = append([]ghStatus{all[i]}, latest...)
		}
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	from, to := min((page-1)*f.pageSz, len(latest)), min(page*f.pageSz, len(latest))
	items := []any{}
	for _, s := range latest[from:to] {
		items = append(items, map[string]any{"context": s.Context, "state": s.State, "target_url": s.URL})
	}
	if to < len(latest) {
		w.Header().Set("Link", fmt.Sprintf(`<%s%s?page=%d>; rel="next"`, f.srv.URL, r.URL.Path, page+1))
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": "pending", "total_count": len(latest), "statuses": items})
}
