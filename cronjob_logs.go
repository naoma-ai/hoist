package main

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type cronjobLogsProvider struct {
	cfg  config
	dial func(addr string) (sshRunner, error)
}

func (p *cronjobLogsProvider) tail(ctx context.Context, service, env string, n int, since string, w io.Writer) error {
	svc := p.cfg.Services[service]
	ec := svc.Env[env]
	addr := p.cfg.Nodes[ec.Node]

	client, err := p.dial(addr)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	defer client.close()

	containerName := service + "-" + env

	// Check container exists (including exited ones).
	psCmd := fmt.Sprintf(`docker ps -a --filter "name=^%s$" --format "{{.Names}}"`, containerName)
	out, err := client.run(ctx, psCmd)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}
	if out == "" {
		return fmt.Errorf("no runs yet for %s in %s", service, env)
	}
	container := strings.SplitN(out, "\n", 2)[0]

	follow := n == 0 && since == ""
	args := dockerLogsArgs(container, since, n, follow)
	cmd := "docker " + strings.Join(args, " ")

	return client.stream(ctx, cmd, w)
}
