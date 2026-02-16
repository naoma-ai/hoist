package main

import (
	"context"
	"time"
)

type serverDeployer struct{}

func (d *serverDeployer) current(_ context.Context, service, env string) (deploy, error) {
	// TODO: SSH into node, parse docker ps output
	return deploy{
		Service: service,
		Env:     env,
		Tag:     "stub-server-tag",
		Uptime:  3 * time.Hour,
	}, nil
}

func (d *serverDeployer) deploy(_ context.Context, _, _, _, _ string) error {
	// TODO: SSH into node, docker pull/stop/rm/run
	time.Sleep(1 * time.Second)
	return nil
}
