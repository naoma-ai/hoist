package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

type sshRunner interface {
	run(ctx context.Context, cmd string) (string, error)
	stream(ctx context.Context, cmd string, stdout io.Writer) error
	close() error
}

type serverDeployer struct {
	cfg          config
	dial         func(addr string) (sshRunner, error)
	pollInterval time.Duration // 0 means use default (2s)
	pollTimeout  time.Duration // 0 means use default (120s)
}

func (d *serverDeployer) deploy(ctx context.Context, service, env, tag, oldTag string, logf func(string, ...any)) error {
	svc := d.cfg.Services[service]
	ec := svc.Env[env]
	addr := d.cfg.Nodes[ec.Node]

	logf("connecting to %s (%s)", ec.Node, addr)
	client, err := d.dial(addr)
	if err != nil {
		return fmt.Errorf("connecting to %s: %w", addr, err)
	}
	defer client.close()

	// Pull image.
	pullCmd := fmt.Sprintf("docker pull %s:%s", svc.Image, tag)
	logf("$ %s", pullCmd)
	if _, err := client.run(ctx, pullCmd); err != nil {
		return fmt.Errorf("pulling image: %w", err)
	}
	logf("image pulled")

	// If redeploying the same tag, rename the existing container to avoid name conflict.
	if tag == oldTag && oldTag != "" {
		oldName := fmt.Sprintf("%s-%s", service, oldTag)
		tempName := oldName + "-old"
		renameCmd := fmt.Sprintf("docker rename %s %s", oldName, tempName)
		logf("$ %s", renameCmd)
		if _, err := client.run(ctx, renameCmd); err != nil {
			return fmt.Errorf("renaming old container: %w", err)
		}
	}

	// Start new container.
	containerName := service + "-" + tag
	runArgs := buildDockerRunArgs(d.cfg.Project, service, tag, oldTag, svc, ec, env)
	runCmd := "docker run " + shellJoin(runArgs)
	logf("$ docker run --name %s-%s ...", service, tag)
	if _, err := client.run(ctx, runCmd); err != nil {
		// Clean up the stopped container so the name is free for retry.
		client.run(ctx, fmt.Sprintf("docker rm %s", containerName))
		return fmt.Errorf("starting container: %w", err)
	}
	logf("container started")

	// Wait for healthcheck.
	interval := d.pollInterval
	if interval == 0 {
		interval = 2 * time.Second
	}
	timeout := d.pollTimeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}

	logf("waiting for healthcheck (:%d%s, timeout %s)", svc.Port, svc.Healthcheck, timeout)
	if err := pollHealthcheck(ctx, client, containerName, svc.Port, svc.Healthcheck, interval, timeout); err != nil {
		logf("healthcheck failed, cleaning up new container")
		// Clean up failed new container (best-effort).
		client.run(ctx, fmt.Sprintf("docker stop %s", containerName))
		client.run(ctx, fmt.Sprintf("docker rm %s", containerName))
		return fmt.Errorf("healthcheck failed: %w", err)
	}
	logf("healthcheck passed")

	// Stop and remove ALL old containers for this service.
	newName := service + "-" + tag
	oldContainers, err := listServiceContainers(ctx, client, service)
	if err != nil {
		logf("warning: failed to list old containers: %v", err)
	}
	for _, name := range oldContainers {
		if name == newName {
			continue
		}
		logf("$ docker stop %s", name)
		if _, err := client.run(ctx, fmt.Sprintf("docker stop %s", name)); err != nil {
			logf("warning: failed to stop %s: %v", name, err)
			continue
		}
		logf("$ docker rm %s", name)
		if _, err := client.run(ctx, fmt.Sprintf("docker rm %s", name)); err != nil {
			logf("warning: failed to remove %s: %v", name, err)
		}
	}
	if len(oldContainers) > 0 {
		removed := 0
		for _, name := range oldContainers {
			if name != newName {
				removed++
			}
		}
		if removed > 0 {
			logf("removed %d old container(s)", removed)
		}
	}

	return nil
}

// listServiceContainers returns the names of all running containers whose name
// starts with "<service>-". This catches orphaned containers from previous deploys.
func listServiceContainers(ctx context.Context, client sshRunner, service string) ([]string, error) {
	cmd := fmt.Sprintf(`docker ps --filter "name=%s-" --format "{{.Names}}"`, service)
	out, err := client.run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	prefix := service + "-"
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			names = append(names, line)
		}
	}
	return names, nil
}

func buildDockerRunArgs(project, service, tag, oldTag string, svc serviceConfig, ec envConfig, env string) []string {
	args := []string{
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
	if svc.Command != "" {
		args = append(args, svc.Command)
	}
	return args
}

// shellJoin quotes each argument for safe use in a shell command string.
func shellJoin(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}

func pollHealthcheck(ctx context.Context, client sshRunner, container string, port int, path string, interval, timeout time.Duration) error {
	// Get the container's bridge IP to healthcheck it directly,
	// avoiding Traefik routing to the old container during blue-green deploy.
	ipCmd := fmt.Sprintf("docker inspect %s --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}'", container)
	ip, err := client.run(ctx, ipCmd)
	if err != nil {
		return fmt.Errorf("getting container IP: %w", err)
	}
	healthCmd := fmt.Sprintf("curl -sf http://%s:%d%s", ip, port, path)
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
