package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockSSHRunner struct {
	commands  []string
	responses []mockRunResult
	idx       int
}

type mockRunResult struct {
	output string
	err    error
}

func (m *mockSSHRunner) run(_ context.Context, cmd string) (string, error) {
	m.commands = append(m.commands, cmd)
	if m.idx < len(m.responses) {
		r := m.responses[m.idx]
		m.idx++
		return r.output, r.err
	}
	m.idx++
	return "", nil
}

func (m *mockSSHRunner) stream(_ context.Context, cmd string, stdout io.Writer) error {
	m.commands = append(m.commands, cmd)
	if m.idx < len(m.responses) {
		r := m.responses[m.idx]
		m.idx++
		if r.err != nil {
			return r.err
		}
		if r.output != "" {
			stdout.Write([]byte(r.output))
		}
		return nil
	}
	m.idx++
	return nil
}

func (m *mockSSHRunner) close() error { return nil }

func TestBuildDockerRunArgs(t *testing.T) {
	svc := serviceConfig{Image: "myapp/backend", Port: 8080, Healthcheck: "/health"}
	ec := envConfig{Host: "api.staging.example.com", EnvFile: "/etc/backend/staging.env"}

	args := buildDockerRunArgs("myapp", "backend", "main-abc1234-20250101000000", "main-old1234-20241231000000", svc, ec, "staging")
	joined := strings.Join(args, " ")

	checks := []string{
		"-d",
		"--name backend-main-abc1234-20250101000000",
		"--restart unless-stopped",
		"--env-file /etc/backend/staging.env",
		"--log-driver awslogs",
		"awslogs-group=/myapp/staging/backend",
		"traefik.enable=true",
		"traefik.http.routers.backend.rule=Host(`api.staging.example.com`)",
		"traefik.http.services.backend.loadbalancer.server.port=8080",
		"hoist.previous=main-old1234-20241231000000",
	}

	for _, check := range checks {
		if !strings.Contains(joined, check) {
			t.Errorf("expected args to contain %q, got: %s", check, joined)
		}
	}

	// Image:tag must be the last argument.
	last := args[len(args)-1]
	if last != "myapp/backend:main-abc1234-20250101000000" {
		t.Errorf("expected last arg to be image:tag, got %q", last)
	}
}

func TestBuildDockerRunArgsEmptyOldTag(t *testing.T) {
	svc := serviceConfig{Image: "myapp/backend", Port: 8080, Healthcheck: "/health"}
	ec := envConfig{Host: "api.example.com", EnvFile: "/etc/backend/prod.env"}

	args := buildDockerRunArgs("myapp", "backend", "main-abc1234-20250101000000", "", svc, ec, "production")
	joined := strings.Join(args, " ")

	// Label should still be present with empty value.
	if !strings.Contains(joined, "hoist.previous=") {
		t.Errorf("expected hoist.previous label, got: %s", joined)
	}
}

func TestPollHealthcheckImmediateSuccess(t *testing.T) {
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "172.17.0.2"}, // docker inspect
			{output: "OK"},         // curl
		},
	}
	err := pollHealthcheck(context.Background(), mock, "test-container", 8080, "/health", 10*time.Millisecond, 1*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(mock.commands))
	}
	if !strings.Contains(mock.commands[0], "docker inspect test-container") {
		t.Errorf("unexpected command: %s", mock.commands[0])
	}
	if !strings.Contains(mock.commands[1], "curl -sf http://172.17.0.2:8080/health") {
		t.Errorf("unexpected command: %s", mock.commands[1])
	}
}

func TestPollHealthcheckEventualSuccess(t *testing.T) {
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "172.17.0.2"},        // docker inspect
			{err: fmt.Errorf("unhealthy")}, // curl 1
			{err: fmt.Errorf("unhealthy")}, // curl 2
			{err: fmt.Errorf("unhealthy")}, // curl 3
			{output: "OK"},                 // curl 4
		},
	}
	err := pollHealthcheck(context.Background(), mock, "test-container", 8080, "/health", 10*time.Millisecond, 1*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.commands) != 5 {
		t.Fatalf("expected 5 commands, got %d", len(mock.commands))
	}
}

func TestPollHealthcheckTimeout(t *testing.T) {
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "172.17.0.2"},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
		},
	}
	err := pollHealthcheck(context.Background(), mock, "test-container", 8080, "/health", 10*time.Millisecond, 50*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' error, got: %v", err)
	}
}

func TestPollHealthcheckContextCancelled(t *testing.T) {
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "172.17.0.2"},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
			{err: fmt.Errorf("unhealthy")},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(25 * time.Millisecond)
		cancel()
	}()
	err := pollHealthcheck(ctx, mock, "test-container", 8080, "/health", 10*time.Millisecond, 5*time.Second)
	if err == nil {
		t.Fatal("expected error from context cancellation")
	}
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestServerDeployHappyPath(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{}
	var dialAddr string

	d := &serverDeployer{
		cfg: cfg,
		dial: func(addr string) (sshRunner, error) {
			dialAddr = addr
			return mock, nil
		},
		pollInterval: 10 * time.Millisecond,
		pollTimeout:  1 * time.Second,
	}

	err := d.deploy(context.Background(), "backend", "staging", "main-abc1234-20250101000000", "main-old1234-20241231000000", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dialAddr != "10.0.0.1" {
		t.Errorf("expected dial addr 10.0.0.1, got %s", dialAddr)
	}

	// Expect: pull, run, docker inspect, curl healthcheck, stop old, rm old = 6 commands.
	if len(mock.commands) < 6 {
		t.Fatalf("expected at least 6 commands, got %d: %v", len(mock.commands), mock.commands)
	}

	if !strings.HasPrefix(mock.commands[0], "docker pull myapp/backend:main-abc1234-20250101000000") {
		t.Errorf("cmd[0] = %q, want docker pull", mock.commands[0])
	}
	if !strings.HasPrefix(mock.commands[1], "docker run") {
		t.Errorf("cmd[1] = %q, want docker run", mock.commands[1])
	}
	if !strings.Contains(mock.commands[2], "docker inspect") {
		t.Errorf("cmd[2] = %q, want docker inspect", mock.commands[2])
	}
	if !strings.Contains(mock.commands[3], "curl -sf") {
		t.Errorf("cmd[3] = %q, want curl healthcheck", mock.commands[3])
	}

	// Last two: stop and rm old container.
	n := len(mock.commands)
	if mock.commands[n-2] != "docker stop backend-main-old1234-20241231000000" {
		t.Errorf("cmd[%d] = %q, want docker stop old", n-2, mock.commands[n-2])
	}
	if mock.commands[n-1] != "docker rm backend-main-old1234-20241231000000" {
		t.Errorf("cmd[%d] = %q, want docker rm old", n-1, mock.commands[n-1])
	}
}

func TestServerDeployNoOldTag(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{}

	d := &serverDeployer{
		cfg:          cfg,
		dial:         func(_ string) (sshRunner, error) { return mock, nil },
		pollInterval: 10 * time.Millisecond,
		pollTimeout:  1 * time.Second,
	}

	err := d.deploy(context.Background(), "backend", "staging", "main-abc1234-20250101000000", "", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT have stop/rm old commands.
	for _, cmd := range mock.commands {
		if strings.Contains(cmd, "docker stop") || strings.Contains(cmd, "docker rm") {
			t.Errorf("unexpected cleanup command when oldTag is empty: %s", cmd)
		}
	}
}

func TestServerDeployPullFailure(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{err: fmt.Errorf("pull access denied")},
		},
	}

	d := &serverDeployer{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	err := d.deploy(context.Background(), "backend", "staging", "main-abc1234-20250101000000", "old-tag", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pulling image") {
		t.Errorf("expected 'pulling image' error, got: %v", err)
	}

	// Only the pull command should have been issued.
	if len(mock.commands) != 1 {
		t.Fatalf("expected 1 command, got %d: %v", len(mock.commands), mock.commands)
	}
}

func TestServerDeployHealthcheckFailure(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""},                             // docker pull
			{output: "container-id"},                 // docker run
			{err: fmt.Errorf("unhealthy")},           // healthcheck 1
			{err: fmt.Errorf("unhealthy")},           // healthcheck 2
			{err: fmt.Errorf("unhealthy")},           // healthcheck 3
			{err: fmt.Errorf("unhealthy")},           // healthcheck 4
			{err: fmt.Errorf("unhealthy")},           // healthcheck 5
			{err: fmt.Errorf("unhealthy")},           // healthcheck 6
			{err: fmt.Errorf("unhealthy")},           // healthcheck 7
			{err: fmt.Errorf("unhealthy")},           // healthcheck 8
			{err: fmt.Errorf("unhealthy")},           // healthcheck 9
			{err: fmt.Errorf("unhealthy")},           // healthcheck 10
			{output: ""},                             // docker stop new (cleanup)
			{output: ""},                             // docker rm new (cleanup)
		},
	}

	d := &serverDeployer{
		cfg:          cfg,
		dial:         func(_ string) (sshRunner, error) { return mock, nil },
		pollInterval: 10 * time.Millisecond,
		pollTimeout:  50 * time.Millisecond,
	}

	err := d.deploy(context.Background(), "backend", "staging", "main-abc1234-20250101000000", "main-old1234-20241231000000", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "healthcheck failed") {
		t.Errorf("expected 'healthcheck failed' error, got: %v", err)
	}

	// Verify cleanup of new container happened.
	var hasStopNew, hasRmNew bool
	for _, cmd := range mock.commands {
		if cmd == "docker stop backend-main-abc1234-20250101000000" {
			hasStopNew = true
		}
		if cmd == "docker rm backend-main-abc1234-20250101000000" {
			hasRmNew = true
		}
	}
	if !hasStopNew {
		t.Error("expected docker stop for new container")
	}
	if !hasRmNew {
		t.Error("expected docker rm for new container")
	}

	// Old container should NOT have been stopped or removed.
	for _, cmd := range mock.commands {
		if cmd == "docker stop backend-main-old1234-20241231000000" {
			t.Error("old container should not be stopped on healthcheck failure")
		}
		if cmd == "docker rm backend-main-old1234-20241231000000" {
			t.Error("old container should not be removed on healthcheck failure")
		}
	}
}

func TestServerDeployDialFailure(t *testing.T) {
	cfg := testConfig()

	d := &serverDeployer{
		cfg: cfg,
		dial: func(_ string) (sshRunner, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	err := d.deploy(context.Background(), "backend", "staging", "main-abc1234-20250101000000", "old-tag", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connecting to") {
		t.Errorf("expected 'connecting to' error, got: %v", err)
	}
}

func TestServerDeployLogOutput(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{}

	d := &serverDeployer{
		cfg:          cfg,
		dial:         func(_ string) (sshRunner, error) { return mock, nil },
		pollInterval: 10 * time.Millisecond,
		pollTimeout:  1 * time.Second,
	}

	var buf bytes.Buffer
	var mu sync.Mutex
	logf := newServiceLogf(&buf, &mu, "backend", 8)
	err := d.deploy(context.Background(), "backend", "staging", "main-abc1234-20250101000000", "main-old1234-20241231000000", logf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	expected := []string{
		"connecting to",
		"docker pull",
		"image pulled",
		"docker run",
		"container started",
		"waiting for healthcheck",
		"healthcheck passed",
		"docker stop",
		"docker rm",
		"old container removed",
	}
	for _, e := range expected {
		if !strings.Contains(output, e) {
			t.Errorf("expected %q in output, got:\n%s", e, output)
		}
	}
}
