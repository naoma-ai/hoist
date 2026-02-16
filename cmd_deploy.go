package main

import (
	"context"
	"time"

	"github.com/spf13/cobra"
)

func addDeployToRoot(cmd *cobra.Command) {
	var (
		services []string
		env      string
		build    string
		yes      bool
		cfgPath  string
	)

	cmd.Flags().StringSliceVarP(&services, "service", "s", nil, "services to deploy (comma-separated)")
	cmd.Flags().StringVarP(&env, "env", "e", "", "target environment")
	cmd.Flags().StringVarP(&build, "build", "b", "", "build tag or branch name")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip confirmation prompt")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "hoist.yml", "config file path")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			return err
		}

		p := newProviders()

		opts := deployOpts{
			Services: services,
			Env:      env,
			Build:    build,
			Yes:      yes,
		}

		return runDeploy(context.Background(), cfg, p, opts)
	}
}

func newProviders() providers {
	now := time.Now().UTC()
	sampleBuilds := []build{
		buildFromTag(tag{Branch: "main", SHA: "f82bc01", Time: now.Add(-1 * time.Hour)}),
		buildFromTag(tag{Branch: "main", SHA: "a1b2c3d", Time: now.Add(-2 * time.Hour)}),
		buildFromTag(tag{Branch: "add-client-tools", SHA: "b2c4e88", Time: now.Add(-3 * time.Hour)}),
		buildFromTag(tag{Branch: "fix-auth", SHA: "c3d5f99", Time: now.Add(-4 * time.Hour)}),
		buildFromTag(tag{Branch: "main", SHA: "d4e6a00", Time: now.Add(-5 * time.Hour)}),
	}

	return providers{
		builds: map[string]buildsProvider{
			"server": &serverBuildsProvider{builds: sampleBuilds},
			"static": &staticBuildsProvider{builds: sampleBuilds},
		},
		deployers: map[string]deployer{
			"server": &serverDeployer{},
			"static": &staticDeployer{},
		},
	}
}
