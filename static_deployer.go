package main

import (
	"context"
	"time"
)

type staticDeployer struct{}

func (d *staticDeployer) current(_ context.Context, service, env string) (deploy, error) {
	// TODO: read version marker from S3 bucket
	return deploy{
		Service: service,
		Env:     env,
		Tag:     "stub-static-tag",
		Uptime:  1 * time.Hour,
	}, nil
}

func (d *staticDeployer) deploy(_ context.Context, _, _, _, _ string) error {
	// TODO: S3 sync + CloudFront invalidation
	return nil
}
