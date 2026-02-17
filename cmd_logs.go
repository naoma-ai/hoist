package main

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/spf13/cobra"
)

func newLogsCmd() *cobra.Command {
	var (
		services []string
		env      string
		n        int
		since    string
		cfgPath  string
	)

	cmd := &cobra.Command{
		Use:           "logs",
		Short:         "Tail logs from running containers",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cfgPath)
			if err != nil {
				return err
			}

			p := newProviders(cfg)
			ctx := context.Background()

			// Default to all services
			targets := services
			if len(targets) == 0 {
				targets = sortedServiceNames(cfg)
			}

			// Validate services exist
			for _, svc := range targets {
				if _, ok := cfg.Services[svc]; !ok {
					return fmt.Errorf("unknown service: %q", svc)
				}
			}

			// If no env specified, pick the first common env
			if env == "" {
				envs := envIntersection(cfg, targets)
				if len(envs) == 0 {
					return fmt.Errorf("no common environments across selected services")
				}
				sort.Strings(envs)
				env = envs[0]
			}

			// Validate env exists for all targets
			for _, svc := range targets {
				if _, ok := cfg.Services[svc].Env[env]; !ok {
					return fmt.Errorf("service %q has no environment %q", svc, env)
				}
			}

			// Validate all providers exist before starting
			for _, svc := range targets {
				svcCfg := cfg.Services[svc]
				if _, ok := p.logs[svcCfg.Type]; !ok {
					return fmt.Errorf("no logs provider for service type %q", svcCfg.Type)
				}
			}

			// Run log tailing concurrently for all services
			var wg sync.WaitGroup
			errs := make(chan error, len(targets))
			for _, svc := range targets {
				wg.Add(1)
				go func(svc string) {
					defer wg.Done()
					svcCfg := cfg.Services[svc]
					lp := p.logs[svcCfg.Type]
					if err := lp.tail(ctx, svc, env, n, since); err != nil {
						errs <- fmt.Errorf("tailing logs for %s: %w", svc, err)
					}
				}(svc)
			}
			wg.Wait()
			close(errs)

			for err := range errs {
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringSliceVarP(&services, "service", "s", nil, "services to show logs for (comma-separated)")
	cmd.Flags().StringVarP(&env, "env", "e", "", "target environment")
	cmd.Flags().IntVarP(&n, "tail", "n", 0, "number of lines to tail")
	cmd.Flags().StringVar(&since, "since", "", "show logs since duration (e.g. 1h)")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "hoist.yml", "config file path")

	return cmd
}
