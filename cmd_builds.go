package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newBuildsCmd() *cobra.Command {
	var (
		limit   int
		cfgPath string
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

			p := newProviders(cfg)
			services := sortedServiceNames(cfg)
			bp := buildsForServices(cfg, p, services)
			if bp == nil {
				return fmt.Errorf("no builds provider available")
			}

			builds, err := bp.listBuilds(context.Background(), limit+1, 0)
			if err != nil {
				return fmt.Errorf("listing builds: %w", err)
			}

			hasMore := len(builds) > limit
			if hasMore {
				builds = builds[:limit]
			}

			fmt.Print(formatBuildsTable(builds, limit, hasMore))
			return nil
		},
	}

	cmd.Flags().IntVar(&limit, "limit", 10, "maximum number of builds to show")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "hoist.yml", "config file path")

	return cmd
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
