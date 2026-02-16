package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "hoist",
		Short:         "Deploy services to remote nodes",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addDeployToRoot(cmd)
	cmd.AddCommand(newTagCmd())
	return cmd
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		if errors.Is(err, errCancelled) {
			fmt.Println("deploy cancelled")
			return
		}
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}
