package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCronjobHistoryCurrentWithContainer(t *testing.T) {
	cfg := cronjobTestConfig()

	crontabContent := "# hoist:begin report-prod\n# hoist:tag=main-abc1234-20250101000000\n# hoist:previous=main-old1234-20241231000000\n0 0 * * * docker run ...\n# hoist:end report-prod\n"
	finishedAt := time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)

	calls := 0
	p := &cronjobHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			calls++
			if strings.Contains(cmd, "crontab -l") {
				return crontabContent, nil
			}
			if strings.Contains(cmd, "docker inspect") {
				return fmt.Sprintf("%s\t0", finishedAt), nil
			}
			return "", fmt.Errorf("unexpected command: %s", cmd)
		},
	}

	d, err := p.current(context.Background(), "report", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.Tag != "main-abc1234-20250101000000" {
		t.Errorf("expected tag, got %q", d.Tag)
	}

	// Uptime should be approximately 2 hours.
	if d.Uptime < time.Hour || d.Uptime > 3*time.Hour {
		t.Errorf("expected ~2h uptime, got %v", d.Uptime)
	}

	if d.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", d.ExitCode)
	}
}

func TestCronjobHistoryCurrentNoContainer(t *testing.T) {
	cfg := cronjobTestConfig()

	crontabContent := "# hoist:begin report-prod\n# hoist:tag=main-abc1234-20250101000000\n# hoist:previous=\n0 0 * * * docker run ...\n# hoist:end report-prod\n"

	p := &cronjobHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			if strings.Contains(cmd, "crontab -l") {
				return crontabContent, nil
			}
			if strings.Contains(cmd, "docker inspect") {
				return "", fmt.Errorf("no such container")
			}
			return "", fmt.Errorf("unexpected command: %s", cmd)
		},
	}

	d, err := p.current(context.Background(), "report", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if d.Tag != "main-abc1234-20250101000000" {
		t.Errorf("expected tag, got %q", d.Tag)
	}
	if d.Uptime != 0 {
		t.Errorf("expected zero uptime when no container, got %v", d.Uptime)
	}
}

func TestCronjobHistoryCurrentNoCrontab(t *testing.T) {
	cfg := cronjobTestConfig()

	p := &cronjobHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			return "", fmt.Errorf("no crontab for user")
		},
	}

	d, err := p.current(context.Background(), "report", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag, got %q", d.Tag)
	}
}

func TestCronjobHistoryCurrentNoBlock(t *testing.T) {
	cfg := cronjobTestConfig()

	// Crontab exists but has no block for report-prod.
	crontabContent := "# hoist:begin other-prod\n# hoist:tag=other-tag\n0 * * * * docker run other\n# hoist:end other-prod\n"

	p := &cronjobHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			if strings.Contains(cmd, "crontab -l") {
				return crontabContent, nil
			}
			return "", fmt.Errorf("unexpected command: %s", cmd)
		},
	}

	d, err := p.current(context.Background(), "report", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag when block not found, got %q", d.Tag)
	}
}

func TestCronjobHistoryCurrentNonZeroExit(t *testing.T) {
	cfg := cronjobTestConfig()
	finishedAt := time.Now().Add(-30 * time.Minute).Format(time.RFC3339Nano)

	p := &cronjobHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			if strings.Contains(cmd, "crontab -l") {
				return "# hoist:begin report-prod\n# hoist:tag=main-abc1234-20250101000000\n# hoist:end report-prod\n", nil
			}
			if strings.Contains(cmd, "docker inspect") {
				return fmt.Sprintf("%s\t1", finishedAt), nil
			}
			return "", nil
		},
	}

	d, err := p.current(context.Background(), "report", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", d.ExitCode)
	}
}

func TestCronjobHistoryPrevious(t *testing.T) {
	cfg := cronjobTestConfig()

	p := &cronjobHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			return "# hoist:begin report-prod\n# hoist:tag=main-abc1234-20250101000000\n# hoist:previous=main-old1234-20241231000000\n# hoist:end report-prod\n", nil
		},
	}

	d, err := p.previous(context.Background(), "report", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "main-old1234-20241231000000" {
		t.Errorf("expected previous tag, got %q", d.Tag)
	}
}

func TestCronjobHistoryPreviousNoCrontab(t *testing.T) {
	cfg := cronjobTestConfig()

	p := &cronjobHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			return "", fmt.Errorf("no crontab for user")
		},
	}

	d, err := p.previous(context.Background(), "report", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag, got %q", d.Tag)
	}
}

func TestCronjobHistoryPreviousNoBlock(t *testing.T) {
	cfg := cronjobTestConfig()

	p := &cronjobHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			return "# hoist:begin other-prod\n# hoist:tag=other\n# hoist:end other-prod\n", nil
		},
	}

	d, err := p.previous(context.Background(), "report", "prod")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag when block not found, got %q", d.Tag)
	}
}

func TestParseContainerFinishInfo(t *testing.T) {
	now := time.Now()
	twoHoursAgo := now.Add(-2 * time.Hour).Format(time.RFC3339Nano)

	uptime, exitCode := parseContainerFinishInfo(twoHoursAgo + "\t0")
	if uptime < time.Hour || uptime > 3*time.Hour {
		t.Errorf("expected ~2h, got %v", uptime)
	}
	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	uptime2, exitCode2 := parseContainerFinishInfo(twoHoursAgo + "\t137")
	if uptime2 < time.Hour {
		t.Errorf("expected >1h, got %v", uptime2)
	}
	if exitCode2 != 137 {
		t.Errorf("expected exit code 137, got %d", exitCode2)
	}

	// Bad input.
	uptime3, exitCode3 := parseContainerFinishInfo("garbage")
	if uptime3 != 0 || exitCode3 != 0 {
		t.Errorf("expected zeros for bad input, got %v, %d", uptime3, exitCode3)
	}
}
