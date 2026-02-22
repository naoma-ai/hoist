package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func cronjobTestConfig() config {
	return config{
		Project: "myapp",
		Nodes: map[string]string{
			"web1": "10.0.0.1",
		},
		Services: map[string]serviceConfig{
			"report": {
				Type:     "cronjob",
				Image:    "myapp/report",
				Schedule: "0 0 * * *",
				Command:  "/run-report",
				Env: map[string]envConfig{
					"prod": {
						Node:     "web1",
						EnvFile:  "/etc/report/prod.env",
						Cronfile: "/etc/cron.d/hoist-report-prod",
					},
				},
			},
		},
	}
}

func TestCronjobDeployHappyPath(t *testing.T) {
	cfg := cronjobTestConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""},                                                   // docker pull
			{output: "# hoist:tag=old-tag\n# hoist:previous=older-tag\n"}, // cat cronfile
			{output: ""},                                                   // write cronfile
		},
	}
	var dialAddr string

	d := &cronjobDeployer{
		cfg: cfg,
		dial: func(addr string) (sshRunner, error) {
			dialAddr = addr
			return mock, nil
		},
	}

	err := d.deploy(context.Background(), "report", "prod", "main-abc1234-20250101000000", "", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dialAddr != "10.0.0.1" {
		t.Errorf("expected dial addr 10.0.0.1, got %s", dialAddr)
	}

	if len(mock.commands) != 3 {
		t.Fatalf("expected 3 commands, got %d: %v", len(mock.commands), mock.commands)
	}

	// 1. docker pull
	if !strings.HasPrefix(mock.commands[0], "docker pull myapp/report:main-abc1234-20250101000000") {
		t.Errorf("cmd[0] = %q, want docker pull", mock.commands[0])
	}

	// 2. cat cronfile (to get previous tag)
	if !strings.Contains(mock.commands[1], "cat /etc/cron.d/hoist-report-prod") {
		t.Errorf("cmd[1] = %q, want cat cronfile", mock.commands[1])
	}

	// 3. write cronfile
	writeCmd := mock.commands[2]
	if !strings.Contains(writeCmd, "hoist:tag=main-abc1234-20250101000000") {
		t.Errorf("cronfile should contain new tag, got: %s", writeCmd)
	}
	if !strings.Contains(writeCmd, "hoist:previous=old-tag") {
		t.Errorf("cronfile should contain previous tag from existing file, got: %s", writeCmd)
	}
}

func TestCronjobDeployWithOldTag(t *testing.T) {
	cfg := cronjobTestConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""}, // docker pull
			{output: ""}, // write cronfile (no cat because oldTag is provided)
		},
	}

	d := &cronjobDeployer{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	err := d.deploy(context.Background(), "report", "prod", "main-abc1234-20250101000000", "main-old1234-20241231000000", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// When oldTag is provided, no cat command needed.
	if len(mock.commands) != 2 {
		t.Fatalf("expected 2 commands, got %d: %v", len(mock.commands), mock.commands)
	}

	writeCmd := mock.commands[1]
	if !strings.Contains(writeCmd, "hoist:previous=main-old1234-20241231000000") {
		t.Errorf("cronfile should use provided oldTag, got: %s", writeCmd)
	}
}

func TestCronjobDeployFirstDeploy(t *testing.T) {
	cfg := cronjobTestConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""},                            // docker pull
			{output: "", err: fmt.Errorf("no file")}, // cat cronfile fails (first deploy)
			{output: ""},                            // write cronfile
		},
	}

	d := &cronjobDeployer{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	err := d.deploy(context.Background(), "report", "prod", "main-abc1234-20250101000000", "", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	writeCmd := mock.commands[2]
	if !strings.Contains(writeCmd, "hoist:previous=") {
		t.Errorf("cronfile should have empty previous, got: %s", writeCmd)
	}
}

func TestCronjobDeployPullFailure(t *testing.T) {
	cfg := cronjobTestConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{err: fmt.Errorf("pull access denied")},
		},
	}

	d := &cronjobDeployer{
		cfg:  cfg,
		dial: func(_ string) (sshRunner, error) { return mock, nil },
	}

	err := d.deploy(context.Background(), "report", "prod", "main-abc1234-20250101000000", "", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "pulling image") {
		t.Errorf("expected 'pulling image' error, got: %v", err)
	}
	if len(mock.commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mock.commands))
	}
}

func TestCronjobDeployDialFailure(t *testing.T) {
	cfg := cronjobTestConfig()
	d := &cronjobDeployer{
		cfg: cfg,
		dial: func(_ string) (sshRunner, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	err := d.deploy(context.Background(), "report", "prod", "main-abc1234-20250101000000", "", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "connecting to") {
		t.Errorf("expected 'connecting to' error, got: %v", err)
	}
}

func TestBuildCronLine(t *testing.T) {
	svc := serviceConfig{
		Image:    "myapp/report",
		Schedule: "0 0 * * *",
		Command:  "/run-report",
	}
	ec := envConfig{
		EnvFile: "/etc/report/prod.env",
	}

	line := buildCronLine("myapp", "report", "prod", "main-abc1234-20250101000000", svc, ec)

	checks := []string{
		"0 0 * * *",
		"root",
		"docker rm -f report-prod 2>/dev/null;",
		"docker run",
		"--name report-prod",
		"--env-file /etc/report/prod.env",
		"--log-driver=awslogs",
		"awslogs-group=/myapp/prod/report",
		"myapp/report:main-abc1234-20250101000000",
		"/run-report",
	}

	for _, check := range checks {
		if !strings.Contains(line, check) {
			t.Errorf("expected cron line to contain %q, got: %s", check, line)
		}
	}
}

func TestBuildCronLineNoCommand(t *testing.T) {
	svc := serviceConfig{
		Image:    "myapp/report",
		Schedule: "0 0 * * *",
	}
	ec := envConfig{
		EnvFile: "/etc/report/prod.env",
	}

	line := buildCronLine("myapp", "report", "prod", "main-abc1234-20250101000000", svc, ec)

	// Image:tag should be the last thing on the line (no command after it).
	if !strings.HasSuffix(line, "myapp/report:main-abc1234-20250101000000") {
		t.Errorf("expected cron line to end with image:tag when no command, got: %s", line)
	}
}

func TestParseCronfileTag(t *testing.T) {
	content := "# hoist:tag=main-abc1234-20250101000000\n# hoist:previous=main-old1234-20241231000000\n0 0 * * * root docker run ...\n"

	tag := parseCronfileTag(content, "tag")
	if tag != "main-abc1234-20250101000000" {
		t.Errorf("expected tag, got %q", tag)
	}

	prev := parseCronfileTag(content, "previous")
	if prev != "main-old1234-20241231000000" {
		t.Errorf("expected previous tag, got %q", prev)
	}

	missing := parseCronfileTag(content, "nonexistent")
	if missing != "" {
		t.Errorf("expected empty string for missing key, got %q", missing)
	}
}
