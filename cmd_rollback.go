package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

type rollbackResult struct {
	targets []string
	tags    map[string]string
	skipped []string
}

// resolveRollbackTargets determines which services to roll back and their previous tags.
func resolveRollbackTargets(ctx context.Context, cfg config, p providers, services []string, env string, w io.Writer) (rollbackResult, error) {
	targets := services
	if len(targets) == 0 {
		for name, svc := range cfg.Services {
			if _, ok := svc.Env[env]; ok {
				targets = append(targets, name)
			}
		}
	}

	for _, name := range targets {
		svc, ok := cfg.Services[name]
		if !ok {
			return rollbackResult{}, fmt.Errorf("unknown service: %q", name)
		}
		if _, ok := svc.Env[env]; !ok {
			return rollbackResult{}, fmt.Errorf("service %q has no environment %q", name, env)
		}
	}

	tags := make(map[string]string)
	var rollbackTargets, skipped []string
	for _, name := range targets {
		svc := cfg.Services[name]
		hp, ok := p.history[svc.Type]
		if !ok {
			continue
		}
		prev, err := hp.previous(ctx, name, env)
		if err != nil {
			return rollbackResult{}, fmt.Errorf("getting previous deploy for %s: %w", name, err)
		}
		if prev.Tag == "" {
			fmt.Fprintf(w, "skipping %s: no previous deploy\n", name)
			skipped = append(skipped, name)
			continue
		}
		tags[name] = prev.Tag
		rollbackTargets = append(rollbackTargets, name)
	}

	return rollbackResult{targets: rollbackTargets, tags: tags, skipped: skipped}, nil
}

func newRollbackCmd() *cobra.Command {
	var (
		services []string
		yes      bool
		cfgPath  string
	)

	cmd := &cobra.Command{
		Use:           "rollback <environment>",
		Short:         "Redeploy previous build for services in an environment",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env := args[0]

			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}

			ctx := cmd.Context()
			p, err := newProviders(ctx, cfg)
			if err != nil {
				return err
			}

			res, err := resolveRollbackTargets(ctx, cfg, p, services, env, cmd.OutOrStdout())
			if err != nil {
				return err
			}

			if len(res.targets) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "Nothing to roll back.")
				return nil
			}

			return runDeploy(ctx, cfg, p, deployOpts{
				Services: res.targets,
				Env:      env,
				Tags:     res.tags,
				Yes:      yes,
			})
		},
	}

	cmd.Flags().StringSliceVarP(&services, "service", "s", nil, "services to rollback (comma-separated)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompt")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "hoist.yml", "config file path")

	return cmd
}
