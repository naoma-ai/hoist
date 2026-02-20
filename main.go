package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/spf13/cobra"
)

// version is set via -ldflags "-X main.version=v1.2.3" for release builds.
// When unset, buildVersion() falls back to VCS info embedded by go install.
var version = ""

func buildVersion() string {
	if version != "" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var revision, dirty, vcsTime string
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
		case "vcs.time":
			vcsTime = s.Value
		}
	}
	if revision != "" {
		return revision + dirty + " (" + vcsTime + ")"
	}
	return "dev"
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
