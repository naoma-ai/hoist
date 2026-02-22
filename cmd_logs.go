package main

import (
	"fmt"
	"io"
	"os"
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

			ctx := cmd.Context()
			p, err := newProviders(ctx, cfg)
			if err != nil {
				return err
			}

			// Default to server services (static and cronjob services have no persistent process to tail)
			targets := services
			if len(targets) == 0 {
				for _, name := range sortedServiceNames(cfg) {
					t := cfg.Services[name].Type
					if t != "static" && t != "cronjob" {
						targets = append(targets, name)
					}
				}
				if len(targets) == 0 {
					return fmt.Errorf("no services with tailable logs")
				}
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
			padLen := maxServiceNameLen(targets)
			var wg sync.WaitGroup
			errs := make(chan error, len(targets))
			for _, svc := range targets {
				wg.Add(1)
				go func(svc string) {
					defer wg.Done()
					svcCfg := cfg.Services[svc]
					lp := p.logs[svcCfg.Type]
					w := os.Stdout
					var pw *linePrefixWriter
					if len(targets) > 1 {
						prefix := fmt.Sprintf("[%-*s]", padLen, svc)
						pw = newLinePrefixWriter(w, prefix)
					}
					var dest io.Writer = w
					if pw != nil {
						dest = pw
					}
					if err := lp.tail(ctx, svc, env, n, since, dest); err != nil {
						errs <- fmt.Errorf("tailing logs for %s: %w", svc, err)
					}
					if pw != nil {
						pw.Flush()
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
