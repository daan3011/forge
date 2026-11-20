package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestParseDiff(t *testing.T) {
	raw, err := os.ReadFile("testdata/pr.diff")
	if err != nil {
		t.Fatal(err)
	}
	files := ParseDiff(string(raw))
	want := []struct {
		old, new, status string
		add, del         int
		path             string
	}{
		{"internal/a.go", "internal/a.go", FileModified, 2, 1, "internal/a.go"},
		{"", "docs/new.md", FileAdded, 2, 0, "docs/new.md"},
		{"old.txt", "", FileDeleted, 0, 1, "old.txt"},
		{"src/before.go", "src/after.go", FileRenamed, 1, 1, "src/after.go"},
		{"img/logo.png", "img/logo.png", FileBinary, 0, 0, "img/logo.png"},
		{"run.sh", "run.sh", FileModified, 0, 0, "run.sh"}, // mode change only
		{"with space.txt", "with space.txt", FileModified, 1, 1, "with space.txt"},
	}
	if len(files) != len(want) {
		t.Fatalf("got %d files, want %d: %+v", len(files), len(want), files)
	}
	for i, w := range want {
		f := files[i]
		if f.OldPath != w.old || f.NewPath != w.new || f.Status != w.status || f.Additions != w.add || f.Deletions != w.del || f.Path() != w.path {
			t.Errorf("file %d = %+v, want %+v", i, f, w)
		}
		if !strings.HasPrefix(f.Text, "diff --git ") || strings.HasSuffix(f.Text, "\n") {
			t.Errorf("file %d text = %q", i, f.Text)
		}
	}
	if strings.Contains(files[0].Text, "docs/new.md") {
		t.Error("first file's text runs into the next file")
	}
}

func TestParseDiffEdges(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"no files", "hello\nworld\n", 0},
		{"one", "diff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := len(ParseDiff(tt.in)); got != tt.want {
				t.Errorf("got %d files, want %d", got, tt.want)
			}
		})
	}
}

// diffServer answers every request with status and body, recording the
// request.
func diffServer(got **http.Request, gotBody *string, status int, body string) *Client {
	return NewClient(api.ClientOptions{
		Host: "github.com", AuthToken: "t",
		Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			*got = req
			if req.Body != nil {
				b, _ := io.ReadAll(req.Body)
				*gotBody = string(b)
			}
			return respond(req, status, body), nil
		}),
	})
}

func TestClientDiff(t *testing.T) {
	var req *http.Request
	var body string
	c := diffServer(&req, &body, 200, "diff --git a/x b/x\n")
	seen := 0
	c.OnResponse = func(*http.Response) { seen++ }
	d, err := c.Diff(context.Background(), "daan/forge", 12)
	if err != nil {
		t.Fatal(err)
	}
	if d != "diff --git a/x b/x\n" {
		t.Errorf("diff = %q", d)
	}
	if req.Method != http.MethodGet || req.URL.Path != "/repos/daan/forge/pulls/12" {
		t.Errorf("request = %s %s", req.Method, req.URL.Path)
	}
	if a := req.Header.Get("Accept"); a != "application/vnd.github.v3.diff" {
		t.Errorf("Accept = %q", a)
	}
	if seen != 1 {
		t.Errorf("OnResponse saw %d responses", seen)
	}
	// The JSON clients keep their own Accept header.
	if _, err := c.PR(context.Background(), "daan/forge", 12); err == nil || req.Header.Get("Accept") == "application/vnd.github.v3.diff" {
		t.Errorf("JSON client used the diff Accept header (err %v)", err)
	}
}

func TestClientDiffErrors(t *testing.T) {
	var req *http.Request
	var body string
	c := diffServer(&req, &body, 406, `{"message":"Sorry, the diff exceeded the maximum number of lines (20000)","errors":[{"code":"too_large"}]}`)
	if _, err := c.Diff(context.Background(), "daan/forge", 12); !errors.Is(err, ErrDiffTooLarge) {
		t.Errorf("406: err = %v, want ErrDiffTooLarge", err)
	}
	c = diffServer(&req, &body, 404, `{"message":"Not Found"}`)
	if _, err := c.Diff(context.Background(), "daan/forge", 12); err == nil || errors.Is(err, ErrDiffTooLarge) {
		t.Errorf("404: err = %v", err)
	}
	if _, err := c.Diff(context.Background(), "forge", 12); err == nil {
		t.Error("bad repo: no error")
	}
}

func TestClientSubmitReview(t *testing.T) {
	tests := []struct {
		name, event, body string
		wantBody          string
		wantErr           bool
	}{
		{"approve without body", ReviewEventApprove, "", `{"event":"APPROVE"}`, false},
		{"approve with body", ReviewEventApprove, "lgtm", `{"body":"lgtm","event":"APPROVE"}`, false},
		{"request changes", ReviewEventRequestChanges, "fix x", `{"body":"fix x","event":"REQUEST_CHANGES"}`, false},
		{"comment", ReviewEventComment, "nice", `{"body":"nice","event":"COMMENT"}`, false},
		{"comment needs body", ReviewEventComment, "  ", "", true},
		{"unknown event", "LGTM", "x", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req *http.Request
			var body string
			c := diffServer(&req, &body, 200, `{"id":1}`)
			err := c.SubmitReview(context.Background(), "daan/forge", 12, tt.event, tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatal("no error")
				}
				if req != nil {
					t.Error("sent a request anyway")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if req.Method != http.MethodPost || req.URL.Path != "/repos/daan/forge/pulls/12/reviews" {
				t.Errorf("request = %s %s", req.Method, req.URL.Path)
			}
			if body != tt.wantBody {
				t.Errorf("body = %s, want %s", body, tt.wantBody)
			}
		})
	}
}

func TestSubmitReviewOwnPRReason(t *testing.T) {
	var req *http.Request
	var body string
	c := diffServer(&req, &body, 422, `{"message":"Unprocessable Entity","errors":["Review Can not approve your own pull request"]}`)
	err := c.SubmitReview(context.Background(), "daan/forge", 12, ReviewEventApprove, "")
	if err == nil {
		t.Fatal("no error")
	}
	got := ActionError("approve", 12, err).Error()
	if !strings.HasPrefix(got, "approve #12 failed: ") || strings.Contains(got, "422") || strings.Contains(got, "https://") {
		t.Errorf("error = %q", got)
	}
}
