package main

import (
	"context"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

		ctx := cmd.Context()
		p, err := newProviders(ctx, cfg)
		if err != nil {
			return err
		}

		opts := deployOpts{
			Services: services,
			Env:      env,
			Build:    build,
			Yes:      yes,
		}

		return runDeploy(ctx, cfg, p, opts)
	}
}

func newProviders(ctx context.Context, cfg config) (providers, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return providers{}, fmt.Errorf("loading AWS config: %w", err)
	}
	s3Client := s3.NewFromConfig(awsCfg)
	ecrClient := ecr.NewFromConfig(awsCfg)
	cfClient := cloudfront.NewFromConfig(awsCfg)

	builds := make(map[string]buildsProvider, len(cfg.Services))
	for name, svc := range cfg.Services {
		switch svc.Type {
		case "server":
			builds[name] = &serverBuildsProvider{ecr: ecrClient, repoName: parseECRRepo(svc.Image)}
		case "static":
			for _, ec := range svc.Env {
				builds[name] = &staticBuildsProvider{s3: s3Client, bucket: ec.Bucket}
				break
			}
		}
	}

	return providers{
		builds: builds,
		deployers: map[string]deployer{
			"server": &serverDeployer{
				cfg:  cfg,
				dial: func(addr string) (sshRunner, error) { return sshDial(addr) },
			},
			"static": &staticDeployer{cfg: cfg, s3: s3Client, cloudfront: cfClient},
		},
		history: map[string]historyProvider{
			"server": &serverHistoryProvider{cfg: cfg, run: sshRun},
			"static": &staticHistoryProvider{cfg: cfg, s3: s3Client},
		},
		logs: map[string]logsProvider{
			"server": &serverLogsProvider{
				cfg:  cfg,
				dial: func(addr string) (sshRunner, error) { return sshDial(addr) },
			},
			"static": &staticLogsProvider{},
		},
	}, nil
}
