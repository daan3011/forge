package plane

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestFilterEncode(t *testing.T) {
	tests := []struct {
		name string
		f    WorkItemFilter
		want url.Values
	}{
		{"empty", WorkItemFilter{}, url.Values{}},
		{
			"single values use the plain param",
			WorkItemFilter{StateIDs: []string{"s1"}, AssigneeIDs: []string{"u1"}, Priorities: []Priority{PriorityHigh}},
			url.Values{"state_id": {"s1"}, "assignee_id": {"u1"}, "priority": {"high"}},
		},
		{
			"several values use __in",
			WorkItemFilter{StateGroups: []StateGroup{GroupStarted, GroupUnstarted}, LabelIDs: []string{"a", "b"}},
			url.Values{"state_group__in": {"started,unstarted"}, "label_id__in": {"a,b"}},
		},
		{
			"everything else",
			WorkItemFilter{
				CycleID: "c1", Search: "  ember ", UpdatedAfter: time.Date(2026, 11, 1, 10, 0, 0, 0, time.FixedZone("CET", 3600)),
				Archived: ArchivedOnly, OrderBy: "-updated_at", Expand: []string{"state", "assignees"},
				Fields: []string{"id", "name"}, Limit: 5,
			},
			url.Values{
				"cycle_id": {"c1"}, "search": {"ember"}, "updated_at__gte": {"2026-11-01T09:00:00Z"},
				"archived": {"only"}, "order_by": {"-updated_at"}, "expand": {"state,assignees"}, "fields": {"id,name"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.encode(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseRateLimit(t *testing.T) {
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		h      http.Header
		want   RateLimit
		wantOK bool
	}{
		{"none", http.Header{}, RateLimit{Remaining: -1}, false},
		{"retry-after seconds", http.Header{"Retry-After": {"7"}}, RateLimit{Remaining: -1, RetryAfter: 7 * time.Second}, true},
		{
			"retry-after date",
			http.Header{"Retry-After": {now.Add(30 * time.Second).Format(http.TimeFormat)}},
			RateLimit{Remaining: -1, RetryAfter: 30 * time.Second},
			true,
		},
		{
			"v1 headers",
			http.Header{"X-Ratelimit-Remaining": {"12"}, "X-Ratelimit-Reset": {"1795251660"}},
			RateLimit{Remaining: 12, Reset: time.Unix(1795251660, 0)},
			true,
		},
		{"garbage", http.Header{"Retry-After": {"soon"}, "X-Ratelimit-Remaining": {"x"}}, RateLimit{Remaining: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseRateLimit(tt.h, now)
			if ok != tt.wantOK || got.Remaining != tt.want.Remaining || got.RetryAfter != tt.want.RetryAfter || !got.Reset.Equal(tt.want.Reset) {
				t.Errorf("got %+v %v, want %+v %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		h       http.Header
		attempt int
		want    time.Duration
	}{
		{"backoff 0", nil, 0, time.Second},
		{"backoff 3", nil, 3, 8 * time.Second},
		{"backoff capped", nil, 10, 30 * time.Second},
		{"retry-after zero waits a second", http.Header{"Retry-After": {"0"}}, 0, time.Second},
		{"long retry-after honoured", http.Header{"Retry-After": {"90"}}, 0, 90 * time.Second},
		{"huge retry-after capped", http.Header{"Retry-After": {"3600"}}, 0, 2 * time.Minute},
		{"reset in the past backs off", http.Header{"X-Ratelimit-Reset": {"1"}}, 1, 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryDelay(tt.h, now, tt.attempt); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDate(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{`null`, time.Time{}, false},
		{`""`, time.Time{}, false},
		{`"2026-11-20"`, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC), false},
		{`"2026-11-20T08:30:00Z"`, time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC), false},
		{`"2026-11-20T08:30:00.5+01:00"`, time.Date(2026, 11, 20, 7, 30, 0, 5e8, time.UTC), false},
		{`"2026-11-20T08:30:00.123456"`, time.Date(2026, 11, 20, 8, 30, 0, 123456000, time.UTC), false},
		{`"next week"`, time.Time{}, true},
		{`12`, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var d Date
			err := json.Unmarshal([]byte(tt.in), &d)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if !d.Equal(tt.want) {
				t.Errorf("got %v, want %v", d.Time, tt.want)
			}
		})
	}
	if b, _ := json.Marshal(Date{}); string(b) != "null" {
		t.Errorf("zero date marshals to %s", b)
	}
}

func TestCycleIsCurrent(t *testing.T) {
	day := func(d int) Date { return Date{time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC)} }
	now := time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		c    Cycle
		want bool
	}{
		{"inside", Cycle{StartDate: day(9), EndDate: day(30)}, true},
		{"ends today (date only)", Cycle{StartDate: day(9), EndDate: day(22)}, true},
		{"ended", Cycle{StartDate: day(1), EndDate: day(21)}, false},
		{"not started", Cycle{StartDate: day(23), EndDate: day(30)}, false},
		{"draft without dates", Cycle{}, false},
		{"archived", Cycle{StartDate: day(9), EndDate: day(30), ArchivedAt: day(20)}, false},
		{"end with a time", Cycle{StartDate: day(9), EndDate: Date{time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.c.IsCurrent(now); got != tt.want {
				t.Errorf("got %v", got)
			}
		})
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"<p>plain</p>", "plain"},
		{"<p>a<br/>b</p><p></p><p></p><p>c</p>", "a\nb\n\nc"},
		{"<h2>Steps</h2><ol><li><p>one</p></li><li>two</li></ol>", "Steps\n- one\n\n- two"},
		{"<p>x &lt; y &amp;&amp; <code>z</code>&nbsp;</p>", "x < y && z"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := HTMLToText(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"paragraphs", "<p>one</p><p>two<br>three</p>", "one\n\ntwo\nthree"},
		{"heading", "<h2>Steps</h2><p>do it</p>", "## Steps\n\ndo it"},
		{"inline", "<p><strong>bold</strong> <em>it</em> <s>gone</s> <code>x&lt;y</code></p>", "**bold** *it* ~~gone~~ `x<y`"},
		{"link", `<p>see <a href="https://x.dev/a?b=1&amp;c=2" target="_blank">docs</a></p>`, "see [docs](https://x.dev/a?b=1&c=2)"},
		{"image", `<img src="https://x.dev/i.png" alt="shot">`, "![shot](https://x.dev/i.png)"},
		{"bullets", "<ul><li><p>a</p></li><li><p>b</p></li></ul>", "- a\n- b"},
		{"ordered", "<ol><li>a</li><li>b</li></ol>", "1. a\n2. b"},
		{"nested", "<ul><li><p>a</p><ul><li><p>a1</p></li></ul></li><li><p>b</p></li></ul>", "- a\n  - a1\n- b"},
		{"tasks", `<ul data-type="taskList"><li data-checked="true" data-type="taskItem"><label><input type="checkbox" checked="checked"><span></span></label><div><p>done</p></div></li><li data-checked="false" data-type="taskItem"><label><input type="checkbox"></label><div><p>todo</p></div></li></ul>`, "- [x] done\n- [ ] todo"},
		{"code block", `<pre><code class="language-go">func main() {
	fmt.Println("&lt;hi&gt;")
}</code></pre><p>after</p>`, "```go\nfunc main() {\n\tfmt.Println(\"<hi>\")\n}\n```\n\nafter"},
		{"quote", "<blockquote><p>wise</p><p>words</p></blockquote>", "> wise\n>\n> words"},
		{"rule", "<p>a</p><hr><p>b</p>", "a\n\n---\n\nb"},
		{"whitespace", "<p>  lots \n of\tspace&nbsp;</p>", "lots of space"},
		{"unknown tags", `<p>hi <mention-component entity_name="user_mention"></mention-component><span>there</span></p>`, "hi there"},
		{"comment", "<p>a<!-- x --></p>", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HTMLToMarkdown(tt.in); got != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
