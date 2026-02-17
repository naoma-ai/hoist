package main

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestParseContainerTag(t *testing.T) {
	tests := []struct {
		name    string
		service string
		input   string
		want    string
	}{
		{"simple tag", "backend", "backend-main-abc1234-20250101000000", "main-abc1234-20250101000000"},
		{"branch with hyphens", "backend", "backend-feat-login-abc1234-20250101000000", "feat-login-abc1234-20250101000000"},
		{"no prefix match", "backend", "unrelated", ""},
		{"service name only", "backend", "backend-", ""},
		{"different service", "api", "backend-main-abc1234-20250101000000", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseContainerTag(tt.service, tt.input)
			if got != tt.want {
				t.Errorf("parseContainerTag(%q, %q) = %q, want %q", tt.service, tt.input, got, tt.want)
			}
		})
	}
}

func TestParseDockerUptime(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   time.Duration
	}{
		{"hours", "Up 3 hours", 3 * time.Hour},
		{"days", "Up 2 days", 48 * time.Hour},
		{"about a minute", "Up About a minute", time.Minute},
		{"about an hour", "Up About an hour", time.Hour},
		{"seconds", "Up 30 seconds", 30 * time.Second},
		{"minutes", "Up 5 minutes", 5 * time.Minute},
		{"singular day", "Up 1 day", 24 * time.Hour},
		{"singular hour", "Up 1 hour", time.Hour},
		{"less than a second", "Up Less than a second", time.Second},
		{"no Up prefix", "Exited (0) 3 hours ago", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDockerUptime(tt.status)
			if got != tt.want {
				t.Errorf("parseDockerUptime(%q) = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestServerHistoryCurrent(t *testing.T) {
	cfg := testConfig()

	p := &serverHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, addr, cmd string) (string, error) {
			if addr != "10.0.0.1" {
				t.Errorf("unexpected addr: %s", addr)
			}
			return "backend-main-abc1234-20250101000000\tUp 3 hours", nil
		},
	}

	d, err := p.current(context.Background(), "backend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "main-abc1234-20250101000000" {
		t.Errorf("tag = %q, want %q", d.Tag, "main-abc1234-20250101000000")
	}
	if d.Uptime != 3*time.Hour {
		t.Errorf("uptime = %v, want %v", d.Uptime, 3*time.Hour)
	}
	if d.Service != "backend" {
		t.Errorf("service = %q, want %q", d.Service, "backend")
	}
	if d.Env != "staging" {
		t.Errorf("env = %q, want %q", d.Env, "staging")
	}
}

func TestServerHistoryCurrentNoContainer(t *testing.T) {
	cfg := testConfig()

	p := &serverHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, _, _ string) (string, error) {
			return "", nil
		},
	}

	d, err := p.current(context.Background(), "backend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag, got %q", d.Tag)
	}
}

func TestServerHistoryCurrentSSHError(t *testing.T) {
	cfg := testConfig()

	p := &serverHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, _, _ string) (string, error) {
			return "", fmt.Errorf("connection refused")
		},
	}

	_, err := p.current(context.Background(), "backend", "staging")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestServerHistoryPrevious(t *testing.T) {
	cfg := testConfig()

	callCount := 0
	p := &serverHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, _, cmd string) (string, error) {
			callCount++
			if callCount == 1 {
				// docker ps call
				return "backend-main-abc1234-20250101000000", nil
			}
			// docker inspect call
			return "main-old1234-20241231000000", nil
		},
	}

	d, err := p.previous(context.Background(), "backend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "main-old1234-20241231000000" {
		t.Errorf("tag = %q, want %q", d.Tag, "main-old1234-20241231000000")
	}
	if callCount != 2 {
		t.Errorf("expected 2 SSH calls, got %d", callCount)
	}
}

func TestServerHistoryPreviousNoContainer(t *testing.T) {
	cfg := testConfig()

	p := &serverHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, _, _ string) (string, error) {
			return "", nil
		},
	}

	d, err := p.previous(context.Background(), "backend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag, got %q", d.Tag)
	}
}

func TestServerHistoryPreviousNoLabel(t *testing.T) {
	cfg := testConfig()

	callCount := 0
	p := &serverHistoryProvider{
		cfg: cfg,
		run: func(_ context.Context, _, _ string) (string, error) {
			callCount++
			if callCount == 1 {
				return "backend-main-abc1234-20250101000000", nil
			}
			return "", nil
		},
	}

	d, err := p.previous(context.Background(), "backend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag, got %q", d.Tag)
	}
}
