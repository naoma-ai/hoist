package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newTagCmd() *cobra.Command {
	var attempt int
	cmd := &cobra.Command{
		Use:   "tag",
		Short: "Generate a build tag from git state",
		RunE: func(cmd *cobra.Command, args []string) error {
			branch, sha, err := resolveGitInfo()
			if err != nil {
				return err
			}
			t := generateTag(branch, sha, time.Now(), attempt)
			fmt.Fprintln(cmd.OutOrStdout(), t)
			return nil
		},
	}
	cmd.Flags().IntVar(&attempt, "attempt", 0, "build attempt number")
	return cmd
}

func resolveGitInfo() (branch, sha string, err error) {
	branch = os.Getenv("GITHUB_REF_NAME")
	sha = os.Getenv("GITHUB_SHA")
	if branch != "" && sha != "" {
		return branch, sha, nil
	}

	branch, err = gitOutput("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("resolving branch: %w", err)
	}
	sha, err = gitOutput("git", "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("resolving SHA: %w", err)
	}
	return branch, sha, nil
}

func gitOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
