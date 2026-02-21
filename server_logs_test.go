package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestServerLogsTailFindsContainer(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "backend-main-abc1234-20250101000000"}, // docker ps
			{output: "some log output"},                    // docker logs (stream)
		},
	}
	var dialAddr string

	p := &serverLogsProvider{
		cfg: cfg,
		dial: func(addr string) (sshRunner, error) {
			dialAddr = addr
			return mock, nil
		},
	}

	err := p.tail(context.Background(), "backend", "staging", 100, "", io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dialAddr != "10.0.0.1" {
		t.Errorf("expected dial addr 10.0.0.1, got %s", dialAddr)
	}

	if len(mock.commands) != 2 {
		t.Fatalf("expected 2 commands, got %d: %v", len(mock.commands), mock.commands)
	}

	if !strings.Contains(mock.commands[0], `docker ps --filter "name=backend-"`) {
		t.Errorf("cmd[0] = %q, want docker ps", mock.commands[0])
	}

	if mock.commands[1] != "docker logs --tail 100 backend-main-abc1234-20250101000000" {
		t.Errorf("cmd[1] = %q, want docker logs --tail 100", mock.commands[1])
	}
}

func TestServerLogsTailFollowMode(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "backend-main-abc1234-20250101000000"},
			{output: ""},
		},
	}

	p := &serverLogsProvider{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	// n=0 and since="" triggers follow mode
	err := p.tail(context.Background(), "backend", "staging", 0, "", io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.commands[1] != "docker logs -f backend-main-abc1234-20250101000000" {
		t.Errorf("cmd[1] = %q, want docker logs -f", mock.commands[1])
	}
}

func TestServerLogsTailWithSince(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "backend-main-abc1234-20250101000000"},
			{output: ""},
		},
	}

	p := &serverLogsProvider{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	err := p.tail(context.Background(), "backend", "staging", 50, "1h", io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.commands[1] != "docker logs --tail 50 --since 1h backend-main-abc1234-20250101000000" {
		t.Errorf("cmd[1] = %q, want docker logs --tail 50 --since 1h", mock.commands[1])
	}
}

func TestServerLogsTailStreamsOutput(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: "backend-main-abc1234-20250101000000"},
			{output: "line1\nline2\nline3\n"},
		},
	}

	p := &serverLogsProvider{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	var buf bytes.Buffer
	err := p.tail(context.Background(), "backend", "staging", 10, "", &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if buf.String() != "line1\nline2\nline3\n" {
		t.Errorf("unexpected output: %q", buf.String())
	}
}

func TestServerLogsTailNoContainer(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""}, // no running containers
		},
	}

	p := &serverLogsProvider{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	err := p.tail(context.Background(), "backend", "staging", 100, "", io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no running container") {
		t.Errorf("expected 'no running container' error, got: %v", err)
	}
}

func TestServerLogsTailDialFailure(t *testing.T) {
	cfg := testConfig()

	p := &serverLogsProvider{
		cfg: cfg,
		dial: func(_ string) (sshRunner, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	err := p.tail(context.Background(), "backend", "staging", 100, "", io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connecting to") {
		t.Errorf("expected 'connecting to' error, got: %v", err)
	}
}

func TestServerLogsTailDockerPsFailure(t *testing.T) {
	cfg := testConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{err: fmt.Errorf("permission denied")},
		},
	}

	p := &serverLogsProvider{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	err := p.tail(context.Background(), "backend", "staging", 100, "", io.Discard)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "listing containers") {
		t.Errorf("expected 'listing containers' error, got: %v", err)
	}
}
