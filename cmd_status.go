package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	var (
		env     string
		cfgPath string
	)

	cmd := &cobra.Command{
		Use:           "status",
		Short:         "Show current deploy status for all services",
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
			rows, err := getStatus(ctx, cfg, p, env)
			if err != nil {
				return err
			}
			fmt.Print(formatStatusTable(rows))
			return nil
		},
	}

	cmd.Flags().StringVarP(&env, "env", "e", "", "filter by environment")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "hoist.yml", "config file path")

	return cmd
}
