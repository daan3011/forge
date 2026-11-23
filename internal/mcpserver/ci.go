package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daan3011/forge/internal/cifix"
	"github.com/daan3011/forge/internal/github"
	"github.com/daan3011/forge/internal/store"
)

// GitHub is what get_ci_failure needs from GitHub; *github.Client
// satisfies it.
type GitHub interface {
	PRDetail(ctx context.Context, repo string, number int) (github.PRDetail, error)
	RunJobs(ctx context.Context, repo string, runID int64) ([]github.Job, error)
	JobLogs(ctx context.Context, repo string, jobID int64) ([]byte, error)
	ListRuns(ctx context.Context, repo string, f github.RunFilter) ([]github.Run, error)
}

// CIFailureIn is get_ci_failure's input.
type CIFailureIn struct {
	RunURL string `json:"run_url,omitempty" jsonschema:"A GitHub Actions run or job URL (…/actions/runs/123 or …/actions/runs/123/job/456) or a pull request URL (…/pull/12). Leave empty for this session's pull request, else the latest failed run on its branch."`
}

// CIJobOut is one failed job.
type CIJobOut struct {
	Workflow string   `json:"workflow,omitempty"`
	Job      string   `json:"job"`
	URL      string   `json:"url,omitempty"`
	RunURL   string   `json:"run_url,omitempty"`
	Lines    []string `json:"lines,omitempty" jsonschema:"The error lines of the job's log with a little context; the log's tail when no error line matched."`
	Tail     bool     `json:"tail,omitempty" jsonschema:"True when lines are the log's tail because no error line matched."`
	NoLog    string   `json:"no_log,omitempty" jsonschema:"Why there are no log lines, when there aren't."`
}

// CIFailureOut is get_ci_failure's result.
type CIFailureOut struct {
	Source        string     `json:"source" jsonschema:"What was looked at: a run, a pull request or the branch."`
	Repo          string     `json:"repo"`
	Branch        string     `json:"branch,omitempty"`
	HeadSHA       string     `json:"head_sha,omitempty"`
	CommitMessage string     `json:"commit_message,omitempty"`
	Jobs          []CIJobOut `json:"jobs"`
}

var (
	runURLRe = regexp.MustCompile(`^/([^/]+/[^/]+)/actions/runs/(\d+)(?:/job/(\d+))?(?:/attempts/\d+)?/?$`)
	prURLRe  = regexp.MustCompile(`^/([^/]+/[^/]+)/pull/(\d+)(?:/.*)?$`)
)

// ciRef is what a run_url names.
type ciRef struct {
	repo  string
	run   int64
	job   int64
	pr    int
	isRun bool
}

func parseCIURL(raw string) (ciRef, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return ciRef{}, fmt.Errorf("run_url %q is not a URL", raw)
	}
	if m := runURLRe.FindStringSubmatch(u.Path); m != nil {
		r := ciRef{repo: m[1], isRun: true}
		r.run, _ = strconv.ParseInt(m[2], 10, 64)
		if m[3] != "" {
			r.job, _ = strconv.ParseInt(m[3], 10, 64)
		}
		return r, nil
	}
	if m := prURLRe.FindStringSubmatch(u.Path); m != nil {
		n, _ := strconv.Atoi(m[2])
		return ciRef{repo: m[1], pr: n}, nil
	}
	return ciRef{}, fmt.Errorf("run_url %q is neither a GitHub Actions run / job URL nor a pull request URL", raw)
}

func (s *Server) getCIFailure(ctx context.Context, _ *mcp.CallToolRequest, in CIFailureIn) (*mcp.CallToolResult, CIFailureOut, error) {
	if s.d.GitHub == nil {
		why := "GitHub isn't available"
		if s.d.GitHubErr != nil {
			why += ": " + s.d.GitHubErr.Error()
		}
		return nil, CIFailureOut{}, errors.New(why)
	}
	var (
		targets []cifix.Target
		source  string
		err     error
	)
	if strings.TrimSpace(in.RunURL) != "" {
		ref, perr := parseCIURL(in.RunURL)
		if perr != nil {
			return nil, CIFailureOut{}, perr
		}
		if ref.isRun {
			source = "run " + cifix.RunURL(ref.repo, ref.run)
			targets, err = s.runTargets(ctx, ref)
		} else {
			source = fmt.Sprintf("pull request %s#%d", ref.repo, ref.pr)
			targets, err = s.prTargets(ctx, ref.repo, ref.pr, "")
		}
	} else {
		source, targets, err = s.sessionTargets(ctx)
	}
	if err != nil {
		return nil, CIFailureOut{}, err
	}
	if len(targets) == 0 {
		return nil, CIFailureOut{}, fmt.Errorf("%s has no failing checks", source)
	}
	project := ""
	if s.sess != nil {
		project = s.sess.Project
	}
	f := cifix.Summarize(ctx, s.d.GitHub, s.d.Store.Cache(), project, s.d.Now(), targets)
	out := CIFailureOut{Source: source, Repo: f.Repo, Branch: f.Branch, HeadSHA: f.HeadSHA, CommitMessage: f.CommitMessage}
	for _, j := range f.Jobs {
		out.Jobs = append(out.Jobs, CIJobOut{
			Workflow: j.Workflow, Job: j.Job, URL: j.URL, RunURL: j.RunURL, Lines: j.Lines, Tail: j.Tail, NoLog: j.NoLog,
		})
	}
	return nil, out, nil
}

// runTargets are the failed jobs of a run, or the one job a job URL names.
func (s *Server) runTargets(ctx context.Context, ref ciRef) ([]cifix.Target, error) {
	jobs, err := s.d.GitHub.RunJobs(ctx, ref.repo, ref.run)
	if err != nil {
		return nil, fmt.Errorf("couldn't list the jobs of run %d: %s", ref.run, github.Reason(err))
	}
	run := cifix.Run{Repo: ref.repo, ID: ref.run, URL: cifix.RunURL(ref.repo, ref.run)}
	var out []cifix.Target
	for _, j := range jobs {
		if ref.job != 0 && j.ID != ref.job {
			continue
		}
		if ref.job == 0 && j.State != github.CheckFail {
			continue
		}
		out = append(out, cifix.Target{Run: run, JobID: j.ID, Job: j.Name, URL: j.URL})
		if len(out) == cifix.MaxJobs {
			break
		}
	}
	return out, nil
}

func (s *Server) prTargets(ctx context.Context, repo string, n int, branch string) ([]cifix.Target, error) {
	d, err := s.d.GitHub.PRDetail(ctx, repo, n)
	if err != nil {
		return nil, fmt.Errorf("couldn't load the checks of %s#%d: %s", repo, n, github.Reason(err))
	}
	return cifix.PRTargets(repo, branch, d), nil
}

// sessionTargets uses the session's most recently linked pull request,
// else the latest failed run on its branch.
func (s *Server) sessionTargets(ctx context.Context) (string, []cifix.Target, error) {
	x, err := s.session()
	if err != nil {
		return "", nil, fmt.Errorf("%w; pass run_url", err)
	}
	links, err := s.d.Store.Links().For(ctx, store.SessionRef(x.ID))
	if err != nil {
		return "", nil, err
	}
	var pr *store.Link
	for i := range links {
		l := &links[i]
		if l.Other.Kind == store.KindGitHubPR && (pr == nil || !l.CreatedAt.Before(pr.CreatedAt)) {
			pr = l
		}
	}
	if pr != nil {
		repo, num, ok := strings.Cut(pr.Other.ID, "#")
		if n, err := strconv.Atoi(num); ok && err == nil {
			ts, err := s.prTargets(ctx, repo, n, x.Branch)
			return fmt.Sprintf("pull request %s#%d", repo, n), ts, err
		}
	}
	repo := x.Repo
	if repo == "" && s.hasProj {
		repo = s.project.Repo
	}
	if repo == "" || x.Branch == "" {
		return "", nil, errors.New("this session has no pull request or GitHub branch; pass run_url")
	}
	src := "branch " + x.Branch
	runs, err := s.d.GitHub.ListRuns(ctx, repo, github.RunFilter{Branch: x.Branch, Status: "failure"})
	if err != nil {
		return "", nil, fmt.Errorf("couldn't list the runs of %s: %s", src, github.Reason(err))
	}
	if len(runs) == 0 {
		return src, nil, nil
	}
	r := runs[0] // newest first
	ts, err := s.runTargets(ctx, ciRef{repo: repo, run: r.ID, isRun: true})
	run := cifix.RunOf(r)
	for i := range ts {
		ts[i].Run = run
	}
	return fmt.Sprintf("run %s (latest failure on %s)", r.URL, x.Branch), ts, err
}

func (s *Server) addCITools(srv *mcp.Server) {
	mcp.AddTool(srv, &mcp.Tool{
		Name: "get_ci_failure",
		Description: "Get why CI fails: the failed jobs of a GitHub Actions run with the error lines of their logs. " +
			"Without run_url it uses this session's pull request (its failing checks), else the latest failed run on the session's branch. " +
			"Use it before fixing CI.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, s.getCIFailure)
}
