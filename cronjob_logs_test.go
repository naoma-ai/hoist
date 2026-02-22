package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestCronjobLogsTailHappyPath(t *testing.T) {
	cfg := cronjobTestConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "report-prod"},         // docker ps -a
			{output: "log line 1\nlog line 2\n"}, // docker logs (streamed)
		},
	}

	lp := &cronjobLogsProvider{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	var buf bytes.Buffer
	err := lp.tail(context.Background(), "report", "prod", 100, "", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mock.commands) != 2 {
		t.Fatalf("expected 2 commands, got %d: %v", len(mock.commands), mock.commands)
	}

	// First command should use -a flag.
	if !strings.Contains(mock.commands[0], "docker ps -a") {
		t.Errorf("expected docker ps -a, got: %s", mock.commands[0])
	}

	// Second command should be docker logs with the fixed container name.
	if !strings.Contains(mock.commands[1], "docker logs") {
		t.Errorf("expected docker logs, got: %s", mock.commands[1])
	}
	if !strings.Contains(mock.commands[1], "report-prod") {
		t.Errorf("expected container name report-prod, got: %s", mock.commands[1])
	}

	if buf.String() != "log line 1\nlog line 2\n" {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

func TestCronjobLogsTailNoContainer(t *testing.T) {
	cfg := cronjobTestConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""}, // docker ps -a returns empty
		},
	}

	lp := &cronjobLogsProvider{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	var buf bytes.Buffer
	err := lp.tail(context.Background(), "report", "prod", 100, "", &buf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no runs yet") {
		t.Errorf("expected 'no runs yet' error, got: %v", err)
	}
}

func TestCronjobLogsTailDialFailure(t *testing.T) {
	cfg := cronjobTestConfig()

	lp := &cronjobLogsProvider{
		cfg: cfg,
		dial: func(_ string) (sshRunner, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	var buf bytes.Buffer
	err := lp.tail(context.Background(), "report", "prod", 100, "", &buf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connecting to") {
		t.Errorf("expected 'connecting to' error, got: %v", err)
	}
}
