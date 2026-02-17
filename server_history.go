package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type serverHistoryProvider struct {
	cfg config
	run func(ctx context.Context, addr, cmd string) (string, error)
}

func (p *serverHistoryProvider) current(ctx context.Context, service, env string) (deploy, error) {
	svc := p.cfg.Services[service]
	addr := p.cfg.Nodes[svc.Env[env].Node]

	cmd := fmt.Sprintf(`docker ps --filter "name=%s-" --format "{{.Names}}\t{{.Status}}"`, service)
	out, err := p.run(ctx, addr, cmd)
	if err != nil {
		return deploy{}, fmt.Errorf("listing containers: %w", err)
	}

	if out == "" {
		return deploy{}, nil
	}

	// Take first matching line.
	line := strings.SplitN(out, "\n", 2)[0]
	parts := strings.SplitN(line, "\t", 2)
	if len(parts) != 2 {
		return deploy{}, fmt.Errorf("unexpected docker ps output: %q", line)
	}

	name := parts[0]
	status := parts[1]

	tag := parseContainerTag(service, name)
	if tag == "" {
		return deploy{}, nil
	}

	return deploy{
		Service: service,
		Env:     env,
		Tag:     tag,
		Uptime:  parseDockerUptime(status),
	}, nil
}

func (p *serverHistoryProvider) previous(ctx context.Context, service, env string) (deploy, error) {
	svc := p.cfg.Services[service]
	addr := p.cfg.Nodes[svc.Env[env].Node]

	// Find the running container name.
	psCmd := fmt.Sprintf(`docker ps --filter "name=%s-" --format "{{.Names}}"`, service)
	out, err := p.run(ctx, addr, psCmd)
	if err != nil {
		return deploy{}, fmt.Errorf("listing containers: %w", err)
	}

	if out == "" {
		return deploy{}, nil
	}

	containerName := strings.SplitN(out, "\n", 2)[0]

	// Read the hoist.previous label from the running container.
	inspectCmd := fmt.Sprintf(`docker inspect --format "{{index .Config.Labels \"hoist.previous\"}}" %s`, containerName)
	label, err := p.run(ctx, addr, inspectCmd)
	if err != nil {
		return deploy{}, fmt.Errorf("inspecting container: %w", err)
	}

	label = strings.TrimSpace(label)
	if label == "" {
		return deploy{}, nil
	}

	return deploy{
		Service: service,
		Env:     env,
		Tag:     label,
	}, nil
}

// parseContainerTag extracts the tag from a container name like "backend-main-abc1234-20250101000000".
// Returns empty string if the name doesn't start with the service prefix.
func parseContainerTag(service, name string) string {
	prefix := service + "-"
	if !strings.HasPrefix(name, prefix) {
		return ""
	}
	return name[len(prefix):]
}

// parseDockerUptime parses Docker status strings like "Up 3 hours", "Up 2 days",
// "Up About a minute", "Up 30 seconds". Approximate — used for display only.
func parseDockerUptime(status string) time.Duration {
	// Strip leading "Up " prefix.
	s := strings.TrimPrefix(status, "Up ")
	if s == status {
		return 0
	}

	// Handle "About a minute" / "About an hour".
	if strings.HasPrefix(s, "About a") || strings.HasPrefix(s, "About an") {
		if strings.Contains(s, "minute") {
			return time.Minute
		}
		if strings.Contains(s, "hour") {
			return time.Hour
		}
		return 0
	}

	// Handle "Less than a second".
	if strings.HasPrefix(s, "Less than") {
		return time.Second
	}

	// Parse "{N} {unit}" patterns.
	var n int
	var unit string
	if _, err := fmt.Sscanf(s, "%d %s", &n, &unit); err != nil {
		return 0
	}

	switch {
	case strings.HasPrefix(unit, "second"):
		return time.Duration(n) * time.Second
	case strings.HasPrefix(unit, "minute"):
		return time.Duration(n) * time.Minute
	case strings.HasPrefix(unit, "hour"):
		return time.Duration(n) * time.Hour
	case strings.HasPrefix(unit, "day"):
		return time.Duration(n) * 24 * time.Hour
	case strings.HasPrefix(unit, "week"):
		return time.Duration(n) * 7 * 24 * time.Hour
	case strings.HasPrefix(unit, "month"):
		return time.Duration(n) * 30 * 24 * time.Hour
	case strings.HasPrefix(unit, "year"):
		return time.Duration(n) * 365 * 24 * time.Hour
	default:
		return 0
	}
}
