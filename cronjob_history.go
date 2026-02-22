package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type cronjobHistoryProvider struct {
	cfg config
	run func(ctx context.Context, addr, cmd string) (string, error)
}

func (p *cronjobHistoryProvider) current(ctx context.Context, service, env string) (deploy, error) {
	svc := p.cfg.Services[service]
	ec := svc.Env[env]
	addr := p.cfg.Nodes[ec.Node]

	// Read crontab and extract block for this service.
	blockID := service + "-" + env
	out, err := p.run(ctx, addr, "crontab -l 2>/dev/null")
	if err != nil || out == "" {
		return deploy{}, nil
	}

	block := extractCrontabBlock(out, blockID)
	if block == "" {
		return deploy{}, nil
	}

	tag := parseCronfileTag(block, "tag")
	if tag == "" {
		return deploy{}, nil
	}

	d := deploy{
		Service: service,
		Env:     env,
		Tag:     tag,
	}

	// Get last run info from docker inspect.
	containerName := service + "-" + env
	inspectCmd := fmt.Sprintf(`docker inspect %s --format '{{.State.FinishedAt}}\t{{.State.ExitCode}}' 2>/dev/null`, containerName)
	inspectOut, err := p.run(ctx, addr, inspectCmd)
	if err == nil && inspectOut != "" {
		d.Uptime, d.ExitCode = parseContainerFinishInfo(inspectOut)
	}

	return d, nil
}

func (p *cronjobHistoryProvider) previous(ctx context.Context, service, env string) (deploy, error) {
	svc := p.cfg.Services[service]
	ec := svc.Env[env]
	addr := p.cfg.Nodes[ec.Node]

	blockID := service + "-" + env
	out, err := p.run(ctx, addr, "crontab -l 2>/dev/null")
	if err != nil || out == "" {
		return deploy{}, nil
	}

	block := extractCrontabBlock(out, blockID)
	if block == "" {
		return deploy{}, nil
	}

	tag := parseCronfileTag(block, "previous")
	if tag == "" {
		return deploy{}, nil
	}

	return deploy{
		Service: service,
		Env:     env,
		Tag:     tag,
	}, nil
}

// parseContainerFinishInfo parses "2025-01-15T10:30:00Z\t0" into
// a duration since finish and the exit code.
func parseContainerFinishInfo(s string) (time.Duration, int) {
	parts := strings.SplitN(strings.TrimSpace(s), "\t", 2)
	if len(parts) != 2 {
		return 0, 0
	}

	finished, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return 0, 0
	}

	exitCode, _ := strconv.Atoi(parts[1])

	return time.Since(finished), exitCode
}
