package docs

import (
	"embed"
	"strings"
	"time"
)

//go:embed templates/*.md
var templateFS embed.FS

// Templates are the doc templates offered for a new doc, "blank" first.
var Templates = []string{"blank", "spec", "notes", "runbook"}

// Template returns the body of a new doc from the named template, with
// {{title}} and {{date}} filled in. Unknown names and "blank" give just a
// title heading.
func Template(name, title string, now time.Time) string {
	body := "# {{title}}\n"
	if name != "blank" {
		if b, err := templateFS.ReadFile("templates/" + name + ".md"); err == nil {
			body = string(b)
		}
	}
	return strings.NewReplacer("{{title}}", title, "{{date}}", now.Format("2006-01-02")).Replace(body)
}

// TitleFrom turns a doc name ("runbooks/deploy-api.md") into a title
// ("Deploy api").
func TitleFrom(rel string) string {
	base := rel[strings.LastIndex(rel, "/")+1:]
	base = strings.TrimSuffix(base, ".md")
	base = strings.TrimSpace(strings.NewReplacer("-", " ", "_", " ").Replace(base))
	if base == "" {
		return "Untitled"
	}
	return strings.ToUpper(base[:1]) + base[1:]
}
