package main

import (
	"context"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
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

		p, err := newProviders(context.Background(), cfg)
		if err != nil {
			return err
		}

		opts := deployOpts{
			Services: services,
			Env:      env,
			Build:    build,
			Yes:      yes,
		}

		return runDeploy(context.Background(), cfg, p, opts)
	}
}

func newProviders(ctx context.Context, cfg config) (providers, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return providers{}, fmt.Errorf("loading AWS config: %w", err)
	}
	s3Client := s3.NewFromConfig(awsCfg)
	ecrClient := ecr.NewFromConfig(awsCfg)

	var serverRepo string
	for _, svc := range cfg.Services {
		if svc.Type == "server" {
			serverRepo = parseECRRepo(svc.Image)
			break
		}
	}

	var staticBucket string
	for _, svc := range cfg.Services {
		if svc.Type == "static" {
			for _, ec := range svc.Env {
				staticBucket = ec.Bucket
				break
			}
			break
		}
	}

	return providers{
		builds: map[string]buildsProvider{
			"server": &serverBuildsProvider{ecr: ecrClient, repoName: serverRepo},
			"static": &staticBuildsProvider{s3: s3Client, bucket: staticBucket},
		},
		deployers: map[string]deployer{
			"server": &serverDeployer{},
			"static": &staticDeployer{},
		},
		history: map[string]historyProvider{
			"server": &serverHistoryProvider{cfg: cfg, run: sshRun},
			"static": &staticHistoryProvider{cfg: cfg, s3: s3Client},
		},
		logs: map[string]logsProvider{
			"server": &serverLogsProvider{cfg: cfg},
			"static": &staticLogsProvider{cfg: cfg},
		},
	}, nil
}
