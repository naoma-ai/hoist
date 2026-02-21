package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newBuildsCmd() *cobra.Command {
	var (
		limit    int
		cfgPath  string
		services []string
	)

	cmd := &cobra.Command{
		Use:           "builds",
		Short:         "List recent builds",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			p, err := newProviders(ctx, cfg)
			if err != nil {
				return err
			}
			allServices := sortedServiceNames(cfg)
			if len(services) > 0 {
				for _, s := range services {
					if _, ok := cfg.Services[s]; !ok {
						return fmt.Errorf("unknown service %q", s)
					}
				}
				allServices = services
			}
			bp := buildsForServices(cfg, p, allServices)
			if bp == nil {
				return fmt.Errorf("no builds provider available")
			}

			builds, err := bp.listBuilds(ctx, limit+1, 0)
			if err != nil {
				return fmt.Errorf("listing builds: %w", err)
			}

			hasMore := len(builds) > limit
			if hasMore {
				builds = builds[:limit]
			}

			enrichBuilds(builds)

			fmt.Print(formatBuildsTable(builds, limit, hasMore))
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "maximum number of builds to show")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "hoist.yml", "config file path")
	cmd.Flags().StringSliceVarP(&services, "service", "s", nil, "filter by service (comma-separated)")

	return cmd
}

func enrichBuilds(builds []build) {
	for i, b := range builds {
		out, err := gitOutput("git", "log", "-1", "--format=%s\n%an", b.SHA)
		if err != nil {
			continue
		}
		parts := strings.SplitN(out, "\n", 2)
		if len(parts) >= 1 {
			builds[i].Message = parts[0]
		}
		if len(parts) >= 2 {
			builds[i].Author = parts[1]
		}
	}
}

func formatBuildTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("Jan 02 15:04")
}

func formatBuildsTable(builds []build, limit int, hasMore bool) string {
	if len(builds) == 0 {
		return "No builds found.\n"
	}

	// Calculate column widths
	buildW, commitW, authorW, timeW := len("BUILD"), len("COMMIT"), len("AUTHOR"), len("TIME")
	for _, b := range builds {
		if len(b.Tag) > buildW {
			buildW = len(b.Tag)
		}
		if len(b.Message) > commitW {
			commitW = len(b.Message)
		}
		if len(b.Author) > authorW {
			authorW = len(b.Author)
		}
		ts := formatBuildTime(b.Time)
		if len(ts) > timeW {
			timeW = len(ts)
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-*s  %-*s  %-*s  %-*s\n", buildW, "BUILD", commitW, "COMMIT", authorW, "AUTHOR", timeW, "TIME")
	for _, b := range builds {
		fmt.Fprintf(&sb, "%-*s  %-*s  %-*s  %-*s\n", buildW, b.Tag, commitW, b.Message, authorW, b.Author, timeW, formatBuildTime(b.Time))
	}

	if hasMore {
		total := limit + 1 // we know there's at least one more
		fmt.Fprintf(&sb, "\n(showing %d of %d+, load more: --limit %d)\n", limit, total, limit+10)
	}

	return sb.String()
}
