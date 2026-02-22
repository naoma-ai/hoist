package main

import (
	"context"
	"fmt"
	"strings"
)

type cronjobDeployer struct {
	cfg  config
	dial func(addr string) (sshRunner, error)
}

func (d *cronjobDeployer) deploy(ctx context.Context, service, env, tag, oldTag string, logf func(string, ...any)) error {
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

	// Read existing cronfile to get current tag (becomes previous).
	previous := oldTag
	if previous == "" {
		catCmd := fmt.Sprintf("cat %s 2>/dev/null", ec.Cronfile)
		out, err := client.run(ctx, catCmd)
		if err == nil && out != "" {
			previous = parseCronfileTag(out, "tag")
		}
	}

	// Build the cron line.
	cronLine := buildCronLine(d.cfg.Project, service, env, tag, svc, ec)

	// Write cronfile.
	content := fmt.Sprintf("# hoist:tag=%s\n# hoist:previous=%s\n%s\n", tag, previous, cronLine)
	writeCmd := fmt.Sprintf("printf '%%s' %s > %s", shellQuote(content), ec.Cronfile)
	logf("writing cronfile %s", ec.Cronfile)
	if _, err := client.run(ctx, writeCmd); err != nil {
		return fmt.Errorf("writing cronfile: %w", err)
	}
	logf("cronfile written")

	return nil
}

func buildCronLine(project, service, env, tag string, svc serviceConfig, ec envConfig) string {
	containerName := service + "-" + env

	var parts []string
	parts = append(parts, svc.Schedule)
	parts = append(parts, "root")
	parts = append(parts, fmt.Sprintf("docker rm -f %s 2>/dev/null;", containerName))

	runArgs := []string{
		"docker", "run",
		"--name", containerName,
		"--env-file", ec.EnvFile,
		"--log-driver=awslogs",
		"--log-opt", fmt.Sprintf("awslogs-region=us-east-1"),
		"--log-opt", fmt.Sprintf("awslogs-group=/%s/%s/%s", project, env, service),
		fmt.Sprintf("%s:%s", svc.Image, tag),
	}

	if svc.Command != "" {
		runArgs = append(runArgs, svc.Command)
	}

	parts = append(parts, strings.Join(runArgs, " "))

	return strings.Join(parts, " ")
}

// parseCronfileTag extracts a value from hoist metadata comments in a cronfile.
// For example, parseCronfileTag(content, "tag") parses "# hoist:tag=some-tag".
func parseCronfileTag(content, key string) string {
	prefix := "# hoist:" + key + "="
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimPrefix(line, prefix)
		}
	}
	return ""
}

// shellQuote wraps a string in single quotes for safe shell use.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
