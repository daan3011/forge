package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/store"
	"github.com/daan3011/forge/internal/templates"
)

// planeClient builds the Plane client for cfg, writing lists through to
// cache (may be nil).
func (d deps) planeClient(cfg config.Plane, cache plane.Cache) (plane.API, error) {
	if d.newPlane != nil {
		return d.newPlane(cfg, cache)
	}
	return newPlaneClient(cfg, cache)
}

func newPlaneClient(cfg config.Plane, cache plane.Cache) (*plane.Client, error) {
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		return nil, err
	}
	var opts []plane.Option
	if cache != nil {
		opts = append(opts, plane.WithCache(cache, cfg.RefreshInterval.Duration))
	}
	return plane.New(cfg, key, opts...)
}

// planeIssueContext resolves {issue_title} / {issue_body} for a Plane
// work item key. The config and key are read on first use, so a missing
// Plane setup only fails templates that need an issue.
func planeIssueContext(ctx context.Context, ref string) (map[string]string, error) {
	cfg, _, _, err := config.LoadOrCreate()
	if err != nil {
		return nil, err
	}
	c, err := newPlaneClient(cfg.Plane, nil)
	if err != nil {
		return nil, err
	}
	return c.IssueContext(ctx, ref)
}

var _ templates.ProviderFunc = planeIssueContext

func newPlaneCmd(d deps) *cobra.Command {
	group := &cobra.Command{
		Use:   "plane",
		Short: "Plane debug commands",
		Args:  cobra.NoArgs,
	}

	var o planeLsOpts
	ls := &cobra.Command{
		Use:   "ls [project]",
		Short: "List a project's Plane work items",
		Long: `List work items of a Plane project. project is a forge project name (its
plane_project_id), a Plane project identifier (e.g. FORGE) or UUID; it can be
left out inside a repo linked to Plane, or when exactly one project is.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, _, err := config.LoadOrCreate()
			if err != nil {
				return err
			}
			st, err := store.Open(cmd.Context(), d.dbPath())
			if err != nil {
				return err
			}
			defer st.Close()
			here := d.resolveProjects(cmd.Context(), cfg, st, cmd.ErrOrStderr())
			c, err := d.planeClient(cfg.Plane, st.Cache())
			if err != nil {
				return err
			}
			arg := ""
			if len(args) == 1 {
				arg = args[0]
			}
			if arg == "" && here != "" && slices.ContainsFunc(cfg.Projects, func(p config.Project) bool {
				return p.Name == here && p.PlaneProjectID != ""
			}) {
				arg = here // the repo you're in, when it's linked to Plane
			}
			return planeLs(cmd.Context(), cmd.OutOrStdout(), c, cfg.Projects, arg, o)
		},
	}
	ls.Flags().StringSliceVar(&o.groups, "state-group", nil, "only these state groups (backlog, unstarted, started, completed, cancelled, triage)")
	ls.Flags().BoolVar(&o.mine, "mine", false, "only items assigned to you")
	ls.Flags().StringVar(&o.search, "search", "", "only items whose name matches")
	ls.Flags().IntVar(&o.limit, "limit", 50, "at most this many items (0 = all)")
	ls.Flags().BoolVar(&o.json, "json", false, "print the items as JSON")

	group.AddCommand(ls)
	return group
}

type planeLsOpts struct {
	groups []string
	mine   bool
	search string
	limit  int
	json   bool
}

func planeLs(ctx context.Context, w io.Writer, c plane.API, projects []config.Project, arg string, o planeLsOpts) error {
	projectID, err := resolvePlaneProject(ctx, c, projects, arg)
	if err != nil {
		return err
	}
	f := plane.WorkItemFilter{Search: o.search, Limit: o.limit, Expand: []string{"state"}}
	for _, g := range o.groups {
		f.StateGroups = append(f.StateGroups, plane.StateGroup(strings.ToLower(strings.TrimSpace(g))))
	}
	if o.mine {
		me, err := c.Me(ctx)
		if err != nil {
			return err
		}
		f.AssigneeIDs = []string{me.ID}
	}
	items, err := c.WorkItems(ctx, projectID, f)
	if err != nil {
		return err
	}
	if o.json {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if items == nil {
			items = []plane.WorkItem{}
		}
		return enc.Encode(items)
	}
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "no work items")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATE\tPRIO\tTITLE")
	for _, it := range items {
		state := it.StateID
		if it.State != nil {
			state = it.State.Name
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it.Key(), state, it.Priority, it.Name)
	}
	return tw.Flush()
}

// resolvePlaneProject turns arg into a Plane project UUID: a forge project
// name, else a Plane project UUID, identifier or name; "" picks the only
// forge project with a plane_project_id.
func resolvePlaneProject(ctx context.Context, c plane.ProjectReader, projects []config.Project, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		var linked []config.Project
		for _, p := range projects {
			if p.PlaneProjectID != "" {
				linked = append(linked, p)
			}
		}
		switch len(linked) {
		case 1:
			return linked[0].PlaneProjectID, nil
		case 0:
			return "", fmt.Errorf("no forge project has a plane_project_id; name a Plane project (identifier or UUID)")
		default:
			names := make([]string, len(linked))
			for i, p := range linked {
				names[i] = p.Name
			}
			return "", fmt.Errorf("several projects use Plane; pick one of: %s", strings.Join(names, ", "))
		}
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, arg) {
			if p.PlaneProjectID == "" {
				return "", fmt.Errorf("project %s has no plane_project_id", p.Name)
			}
			return p.PlaneProjectID, nil
		}
	}
	ps, err := c.Projects(ctx)
	if err != nil {
		return "", err
	}
	for _, p := range ps {
		if p.ID == arg || strings.EqualFold(p.Identifier, arg) || strings.EqualFold(p.Name, arg) {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("no forge or Plane project %q", arg)
}
