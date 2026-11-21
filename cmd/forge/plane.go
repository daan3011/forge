package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/daan3011/forge/internal/config"
	"github.com/daan3011/forge/internal/plane"
	"github.com/daan3011/forge/internal/projects"
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
		Long: `List work items of a Plane project, found like the Plane tab finds it:

  1. project is a forge project name (left out: the repo you're in, else
     the only project linked to Plane);
  2. its saved link (picked on the Plane tab, or plane_project_id in config);
  3. else the Plane project whose identifier or name matches the repo name,
     which is then saved as its link;
  4. else project is a Plane project identifier (e.g. FORGE), name or UUID.

Without a match the error lists the workspace's Plane projects.`,
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
			res, err := projects.ResolvePlane(cmd.Context(), arg, here, cfg.Projects, c.Projects)
			if err != nil {
				return err
			}
			if res.Source == projects.PlaneMatched && res.Project != nil {
				// Save it like the Plane tab does, so it is a link from now on.
				what := res.Plane.Identifier
				if what == "" {
					what = res.ID
				}
				if err := st.Projects().SetPlane(cmd.Context(), res.Project.Repo, res.ID); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), "warning: save Plane link:", err)
				} else {
					fmt.Fprintf(cmd.ErrOrStderr(), "linked %s to Plane project %s\n", res.Project.Repo, what)
				}
			}
			return planeLs(cmd.Context(), cmd.OutOrStdout(), c, res.ID, o)
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

func planeLs(ctx context.Context, w io.Writer, c plane.API, projectID string, o planeLsOpts) error {
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
