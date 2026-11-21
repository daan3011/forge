package plane

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// md renders GitHub-flavoured Markdown. Raw HTML is let through on purpose:
// HTMLToEditableMarkdown keeps mentions and images as raw tags, and they
// must reach Plane unchanged.
var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

var (
	// taskItemRe matches a GFM task-list item's checkbox.
	taskItemRe = regexp.MustCompile(`<li>\s*<input (checked="" )?disabled="" type="checkbox"\s*/?>\s*`)
	// taskListRe matches the list opening right before a task item.
	taskListRe = regexp.MustCompile(`<ul>(\s*)<li data-type="taskItem"`)
	// splitImageRe matches a block image whose tags HTMLToEditableMarkdown
	// put on separate lines (so Markdown treats it as an HTML block).
	splitImageRe = regexp.MustCompile(`(<image-component\b[^>]*>)\s+(</image-component>)`)
)

// MarkdownToHTML converts Markdown written in forge (descriptions and
// comments) into HTML for Plane. Task lists come out in tiptap's shape
// (data-type="taskList"/"taskItem", data-checked) so Plane shows them as
// checklists. Blank input gives "".
func MarkdownToHTML(src string) (string, error) {
	if strings.TrimSpace(src) == "" {
		return "", nil
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	out := taskItemRe.ReplaceAllStringFunc(buf.String(), func(m string) string {
		checked := "false"
		if strings.Contains(m, "checked=\"\" ") {
			checked = "true"
		}
		return `<li data-type="taskItem" data-checked="` + checked + `">`
	})
	out = splitImageRe.ReplaceAllString(out, "$1$2")
	out = taskListRe.ReplaceAllString(out, `<ul data-type="taskList">$1<li data-type="taskItem"`)
	return strings.TrimSpace(out), nil
}
