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

	// Read existing crontab.
	blockID := service + "-" + env
	crontab, _ := client.run(ctx, "crontab -l 2>/dev/null")

	// Determine previous tag.
	previous := oldTag
	if previous == "" {
		block := extractCrontabBlock(crontab, blockID)
		if block != "" {
			previous = parseCronfileTag(block, "tag")
		}
	}

	// Build the new block.
	cronLine := buildCronLine(d.cfg.Project, service, env, tag, svc, ec)
	newBlock := fmt.Sprintf("# hoist:begin %s\n# hoist:tag=%s\n# hoist:previous=%s\n%s\n# hoist:end %s", blockID, tag, previous, cronLine, blockID)
	crontab = replaceCrontabBlock(crontab, blockID, newBlock)

	// Write crontab.
	writeCmd := fmt.Sprintf("printf '%%s' %s | crontab -", shellQuote(crontab))
	logf("writing crontab entry %s", blockID)
	if _, err := client.run(ctx, writeCmd); err != nil {
		return fmt.Errorf("writing crontab: %w", err)
	}
	logf("crontab updated")

	return nil
}

func buildCronLine(project, service, env, tag string, svc serviceConfig, ec envConfig) string {
	containerName := service + "-" + env

	var parts []string
	parts = append(parts, svc.Schedule)
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

// extractCrontabBlock returns the content between the begin/end markers for blockID,
// or empty string if not found.
func extractCrontabBlock(crontab, blockID string) string {
	beginMarker := "# hoist:begin " + blockID
	endMarker := "# hoist:end " + blockID

	lines := strings.Split(crontab, "\n")
	var block []string
	inside := false
	for _, line := range lines {
		if line == beginMarker {
			inside = true
			continue
		}
		if line == endMarker {
			break
		}
		if inside {
			block = append(block, line)
		}
	}
	if !inside {
		return ""
	}
	return strings.Join(block, "\n")
}

// replaceCrontabBlock replaces the block for blockID in the crontab, or appends it
// if no existing block is found. Returns the updated crontab content.
func replaceCrontabBlock(crontab, blockID, newBlock string) string {
	beginMarker := "# hoist:begin " + blockID
	endMarker := "# hoist:end " + blockID

	lines := strings.Split(crontab, "\n")
	var result []string
	replaced := false
	inside := false
	for _, line := range lines {
		if line == beginMarker {
			inside = true
			result = append(result, strings.Split(newBlock, "\n")...)
			replaced = true
			continue
		}
		if inside && line == endMarker {
			inside = false
			continue
		}
		if !inside {
			result = append(result, line)
		}
	}

	if !replaced {
		// Append to the end. Ensure there's a newline separator.
		trimmed := strings.TrimRight(strings.Join(result, "\n"), "\n")
		if trimmed == "" {
			return newBlock + "\n"
		}
		return trimmed + "\n" + newBlock + "\n"
	}

	return strings.Join(result, "\n")
}
