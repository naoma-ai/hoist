package main

import (
	"context"
	"fmt"
	"io"
	"strings"
)

type serverLogsProvider struct {
	cfg  config
	dial func(addr string) (sshRunner, error)
}

func (p *serverLogsProvider) tail(ctx context.Context, service, env string, n int, since string, w io.Writer) error {
	svc := p.cfg.Services[service]
	ec := svc.Env[env]
	addr := p.cfg.Nodes[ec.Node]

	client, err := p.dial(addr)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	defer client.close()

	// Find running container.
	psCmd := fmt.Sprintf(`docker ps --filter "name=%s-" --format "{{.Names}}"`, service)
	out, err := client.run(ctx, psCmd)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	// Docker's name filter is a substring match, so we must check the prefix ourselves.
	prefix := service + "-"
	var container string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			container = line
			break
		}
	}
	if container == "" {
		return fmt.Errorf("no running container for %s in %s", service, env)
	}

	follow := n == 0 && since == ""
	args := dockerLogsArgs(container, since, n, follow)
	cmd := "docker " + strings.Join(args, " ")

	return client.stream(ctx, cmd, w)
}
