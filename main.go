package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// version and buildTime are set via -ldflags for release builds.
// When unset, buildVersion() falls back to VCS info embedded by go install.
var version = ""
var buildTime = ""

func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var revision, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > 7 {
				revision = s.Value[:7]
			} else {
				revision = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if revision == "" {
		return "dev"
	}
	ts := buildTime
	if ts == "" {
		ts = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	return revision + dirty + " (" + ts + ")"
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hoist",
		Short:         "Deploy services to remote nodes",
		Version:       buildVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addDeployToRoot(cmd)
	cmd.AddCommand(newTagCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newBuildsCmd())
	cmd.AddCommand(newRollbackCmd())
	cmd.AddCommand(newLogsCmd())
	return cmd
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cmd := newRootCmd()
	if err := cmd.ExecuteContext(ctx); err != nil {
		if errors.Is(err, errCancelled) {
			fmt.Println("deploy cancelled")
			return
		}
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
