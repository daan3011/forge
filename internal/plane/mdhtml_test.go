package plane

import (
	"slices"
	"strings"
	"testing"
)

func TestMarkdownToHTML(t *testing.T) {
	tests := []struct {
		name, in string
		want     []string // substrings, in order
	}{
		{"blank", "  \n", nil},
		{"heading and paragraph", "## Steps\n\ndo it", []string{"<h2>Steps</h2>", "<p>do it</p>"}},
		{"inline", "**b** *i* ~~s~~ `x<y`", []string{"<strong>b</strong>", "<em>i</em>", "<del>s</del>", "<code>x&lt;y</code>"}},
		{"link", "see [docs](https://x.dev/a?b=1&c=2)", []string{`<a href="https://x.dev/a?b=1&amp;c=2">docs</a>`}},
		{"lists", "- a\n  - a1\n- b\n\n1. one", []string{"<ul>", "<li>a", "<ul>", "<li>a1</li>", "<li>b</li>", "<ol>", "<li>one</li>"}},
		{"code fence", "```go\nfmt.Println(\"<hi>\")\n```", []string{`<pre><code class="language-go">fmt.Println(&quot;&lt;hi&gt;&quot;)`}},
		{"task list", "- [x] done\n- [ ] todo", []string{`<ul data-type="taskList">`, `<li data-type="taskItem" data-checked="true">done</li>`, `<li data-type="taskItem" data-checked="false">todo</li>`}},
		{"table", "| a | b |\n|---|---|\n| 1 | 2 |", []string{"<table>", "<th>a</th>", "<td>2</td>"}},
		{"raw mention", `hi <mention-component entity_identifier="u1" entity_name="user_mention"></mention-component>`, []string{`<p>hi <mention-component entity_identifier="u1" entity_name="user_mention"></mention-component></p>`}},
		{"escaped tag", `a \<b> c`, []string{"<p>a &lt;b&gt; c</p>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MarkdownToHTML(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == nil && got != "" {
				t.Fatalf("got %q, want empty", got)
			}
			rest := got
			for _, w := range tt.want {
				i := strings.Index(rest, w)
				if i < 0 {
					t.Fatalf("missing %q (in order) in\n%s", w, got)
				}
				rest = rest[i+len(w):]
			}
		})
	}
}

func TestHTMLToEditableMarkdown(t *testing.T) {
	const mention = `<mention-component entity_identifier="u1" entity_name="user_mention"></mention-component>`
	const image = `<image-component src="asset-1" width="300px"></image-component>`
	tests := []struct {
		name, in  string
		wantLossy []string
		keep      []string // must survive HTML → Markdown → HTML
	}{
		{"plain", "<h2>Steps</h2><p>do <strong>it</strong></p><ul><li><p>a</p></li></ul>", nil, nil},
		{"tasks", `<ul data-type="taskList"><li data-checked="true" data-type="taskItem"><label><input type="checkbox" checked="checked"><span></span></label><div><p>done</p></div></li></ul>`, nil, []string{`data-checked="true"`}},
		{"mention", "<p>hi " + mention + " there</p>", []string{"mentions"}, []string{mention, "there"}},
		{"image block", "<p>a</p>" + image + "<p>b</p>", []string{"images"}, []string{image, "<p>a</p>", "<p>b</p>"}},
		{"img inline", `<p>see <img src="https://x.dev/i.png" alt="x"></p>`, []string{"images"}, []string{`<img src="https://x.dev/i.png" alt="x">`}},
		{"table", "<table><tr><td>c</td></tr></table>", []string{"tables"}, []string{"c"}},
		{"unknown", "<p><u>u</u> <callout-x>c</callout-x> <wat>w</wat></p>", []string{"underline", "unknown tags: callout-x, wat"}, nil},
		{"literal angle", "<p>x &lt;b&gt; y</p>", nil, []string{"x &lt;b&gt; y"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md, lossy := HTMLToEditableMarkdown(tt.in)
			if !slices.Equal(lossy, tt.wantLossy) {
				t.Errorf("lossy = %q, want %q", lossy, tt.wantLossy)
			}
			back, err := MarkdownToHTML(md)
			if err != nil {
				t.Fatal(err)
			}
			for _, k := range tt.keep {
				if !strings.Contains(back, k) {
					t.Errorf("%q lost: markdown %q gave\n%s", k, md, back)
				}
			}
		})
	}
}

// For content forge fully understands, editing round-trips: converting the
// edited Markdown back and viewing it again gives the same Markdown.
func TestEditRoundTripStable(t *testing.T) {
	ins := []string{
		"<h2>Steps</h2><p>one</p><p>two <em>it</em> <code>c</code></p>",
		"<ul><li><p>a</p><ul><li><p>a1</p></li></ul></li><li><p>b</p></li></ul><ol><li>x</li><li>y</li></ol>",
		`<ul data-type="taskList"><li data-checked="true" data-type="taskItem"><div><p>done</p></div></li><li data-checked="false" data-type="taskItem"><div><p>todo</p></div></li></ul>`,
		`<pre><code class="language-go">x := 1</code></pre><blockquote><p>q</p></blockquote><p><a href="https://x.dev">l</a></p>`,
	}
	for _, in := range ins {
		md, lossy := HTMLToEditableMarkdown(in)
		if len(lossy) > 0 {
			t.Errorf("%s: unexpected lossy %q", in, lossy)
		}
		back, err := MarkdownToHTML(md)
		if err != nil {
			t.Fatal(err)
		}
		if again, _ := HTMLToEditableMarkdown(back); again != md {
			t.Errorf("not stable:\n%q\n%q\nvia %s", md, again, back)
		}
	}
}
