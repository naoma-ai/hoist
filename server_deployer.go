package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type sshRunner interface {
	run(ctx context.Context, cmd string) (string, error)
	close() error
}

type serverDeployer struct {
	cfg          config
	dial         func(addr string) (sshRunner, error)
	pollInterval time.Duration // 0 means use default (2s)
	pollTimeout  time.Duration // 0 means use default (120s)
}

func (d *serverDeployer) deploy(ctx context.Context, service, env, tag, oldTag string) error {
	svc := d.cfg.Services[service]
	ec := svc.Env[env]
	addr := d.cfg.Nodes[ec.Node]

	client, err := d.dial(addr)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	defer client.close()

	// Pull image.
	pullCmd := fmt.Sprintf("docker pull %s:%s", svc.Image, tag)
	if _, err := client.run(ctx, pullCmd); err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}

	// Start new container.
	runArgs := buildDockerRunArgs(d.cfg.Project, service, tag, oldTag, svc, ec, env)
	runCmd := "docker run " + strings.Join(runArgs, " ")
	if _, err := client.run(ctx, runCmd); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	// Wait for healthcheck.
	interval := d.pollInterval
	if interval == 0 {
		interval = 2 * time.Second
	}
	timeout := d.pollTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	if err := pollHealthcheck(ctx, client, svc.Port, svc.Healthcheck, interval, timeout); err != nil {
		// Clean up failed new container (best-effort).
		client.run(ctx, fmt.Sprintf("docker stop %s-%s", service, tag))
		client.run(ctx, fmt.Sprintf("docker rm %s-%s", service, tag))
		return fmt.Errorf("healthcheck failed: %w", err)
	}

	// Stop and remove old container.
	if oldTag != "" {
		if _, err := client.run(ctx, fmt.Sprintf("docker stop %s-%s", service, oldTag)); err != nil {
			return fmt.Errorf("stopping old container: %w", err)
		}
		if _, err := client.run(ctx, fmt.Sprintf("docker rm %s-%s", service, oldTag)); err != nil {
			return fmt.Errorf("removing old container: %w", err)
		}
	}

	return nil
}

func buildDockerRunArgs(project, service, tag, oldTag string, svc serviceConfig, ec envConfig, env string) []string {
	return []string{
		"-d",
		"--name", service + "-" + tag,
		"--restart", "unless-stopped",
		"--env-file", ec.EnvFile,
		"--log-driver", "awslogs",
		"--log-opt", fmt.Sprintf("awslogs-group=/%s/%s/%s", project, env, service),
		"--label", "traefik.enable=true",
		"--label", fmt.Sprintf("traefik.http.routers.%s.rule=Host(`%s`)", service, ec.Host),
		"--label", fmt.Sprintf("traefik.http.services.%s.loadbalancer.server.port=%d", service, svc.Port),
		"--label", fmt.Sprintf("hoist.previous=%s", oldTag),
		svc.Image + ":" + tag,
	}
}

func pollHealthcheck(ctx context.Context, client sshRunner, port int, path string, interval, timeout time.Duration) error {
	healthCmd := fmt.Sprintf("curl -sf http://localhost:%d%s", port, path)
	deadline := time.After(timeout)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// First attempt immediately.
	if _, err := client.run(ctx, healthCmd); err == nil {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline:
			return fmt.Errorf("timed out after %s", timeout)
		case <-ticker.C:
			if _, err := client.run(ctx, healthCmd); err == nil {
				return nil
			}
		}
	}
}
