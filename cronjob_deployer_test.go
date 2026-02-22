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
						Node:    "web1",
						EnvFile: "/etc/report/prod.env",
					},
				},
			},
		},
	}
}

func TestCronjobDeployHappyPath(t *testing.T) {
	cfg := cronjobTestConfig()

	existingCrontab := "# hoist:begin report-prod\n# hoist:tag=old-tag\n# hoist:previous=older-tag\n0 0 * * * docker run ...\n# hoist:end report-prod\n"

	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""},                // docker pull
			{output: existingCrontab},   // crontab -l
			{output: ""},                // printf | crontab -
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

	// 2. crontab -l
	if !strings.Contains(mock.commands[1], "crontab -l") {
		t.Errorf("cmd[1] = %q, want crontab -l", mock.commands[1])
	}

	// 3. write crontab
	writeCmd := mock.commands[2]
	if !strings.Contains(writeCmd, "crontab -") {
		t.Errorf("write command should pipe to crontab -, got: %s", writeCmd)
	}
	if !strings.Contains(writeCmd, "hoist:tag=main-abc1234-20250101000000") {
		t.Errorf("crontab should contain new tag, got: %s", writeCmd)
	}
	if !strings.Contains(writeCmd, "hoist:previous=old-tag") {
		t.Errorf("crontab should contain previous tag from existing block, got: %s", writeCmd)
	}
	if !strings.Contains(writeCmd, "hoist:begin report-prod") {
		t.Errorf("crontab should contain begin marker, got: %s", writeCmd)
	}
	if !strings.Contains(writeCmd, "hoist:end report-prod") {
		t.Errorf("crontab should contain end marker, got: %s", writeCmd)
	}
}

func TestCronjobDeployWithOldTag(t *testing.T) {
	cfg := cronjobTestConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""},  // docker pull
			{output: ""},  // crontab -l (empty, first deploy but oldTag provided)
			{output: ""},  // printf | crontab -
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

	if len(mock.commands) != 3 {
		t.Fatalf("expected 3 commands, got %d: %v", len(mock.commands), mock.commands)
	}

	writeCmd := mock.commands[2]
	if !strings.Contains(writeCmd, "hoist:previous=main-old1234-20241231000000") {
		t.Errorf("crontab should use provided oldTag, got: %s", writeCmd)
	}
}

func TestCronjobDeployFirstDeploy(t *testing.T) {
	cfg := cronjobTestConfig()
	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""},                             // docker pull
			{output: "", err: fmt.Errorf("no crontab for user")}, // crontab -l fails (first deploy)
			{output: ""},                             // printf | crontab -
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
		t.Errorf("crontab should have empty previous, got: %s", writeCmd)
	}
	if !strings.Contains(writeCmd, "hoist:begin report-prod") {
		t.Errorf("crontab should contain begin marker, got: %s", writeCmd)
	}
}

func TestCronjobDeployAppendsBlock(t *testing.T) {
	cfg := cronjobTestConfig()

	// Existing crontab with a different service's block.
	existingCrontab := "# hoist:begin other-prod\n# hoist:tag=other-tag\n0 * * * * docker run other\n# hoist:end other-prod\n"

	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""},              // docker pull
			{output: existingCrontab}, // crontab -l
			{output: ""},              // printf | crontab -
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
	// Should preserve the other block.
	if !strings.Contains(writeCmd, "hoist:begin other-prod") {
		t.Errorf("crontab should preserve other blocks, got: %s", writeCmd)
	}
	// Should append the new block.
	if !strings.Contains(writeCmd, "hoist:begin report-prod") {
		t.Errorf("crontab should contain new block, got: %s", writeCmd)
	}
}

func TestCronjobDeployReplacesBlock(t *testing.T) {
	cfg := cronjobTestConfig()

	// Existing crontab with both our block and another block.
	existingCrontab := "# hoist:begin other-prod\n# hoist:tag=other-tag\n0 * * * * docker run other\n# hoist:end other-prod\n# hoist:begin report-prod\n# hoist:tag=old-tag\n# hoist:previous=older-tag\n0 0 * * * docker run old\n# hoist:end report-prod\n"

	mock := &mockSSHRunner{
		responses: []mockRunResult{
			{output: ""},              // docker pull
			{output: existingCrontab}, // crontab -l
			{output: ""},              // printf | crontab -
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
	// Should preserve the other block.
	if !strings.Contains(writeCmd, "hoist:begin other-prod") {
		t.Errorf("crontab should preserve other blocks, got: %s", writeCmd)
	}
	// Should have updated tag.
	if !strings.Contains(writeCmd, "hoist:tag=main-abc1234-20250101000000") {
		t.Errorf("crontab should contain new tag, got: %s", writeCmd)
	}
	// Previous should be the old tag.
	if !strings.Contains(writeCmd, "hoist:previous=old-tag") {
		t.Errorf("crontab should contain previous=old-tag, got: %s", writeCmd)
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

	// Should NOT contain "root" user field.
	if strings.Contains(line, " root ") {
		t.Errorf("cron line should not contain root user field, got: %s", line)
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
	content := "# hoist:tag=main-abc1234-20250101000000\n# hoist:previous=main-old1234-20241231000000\n0 0 * * * docker run ...\n"

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

func TestExtractCrontabBlock(t *testing.T) {
	crontab := "# hoist:begin report-prod\n# hoist:tag=abc123\n# hoist:previous=old123\n0 0 * * * docker run report\n# hoist:end report-prod\n# hoist:begin other-prod\n# hoist:tag=xyz\n# hoist:end other-prod\n"

	block := extractCrontabBlock(crontab, "report-prod")
	if !strings.Contains(block, "hoist:tag=abc123") {
		t.Errorf("expected block to contain tag, got: %q", block)
	}
	if strings.Contains(block, "other-prod") {
		t.Errorf("block should not contain other service's content")
	}

	// Non-existent block.
	empty := extractCrontabBlock(crontab, "nonexistent")
	if empty != "" {
		t.Errorf("expected empty string for missing block, got: %q", empty)
	}

	// Empty crontab.
	empty2 := extractCrontabBlock("", "report-prod")
	if empty2 != "" {
		t.Errorf("expected empty string for empty crontab, got: %q", empty2)
	}
}

func TestReplaceCrontabBlock(t *testing.T) {
	newBlock := "# hoist:begin report-prod\n# hoist:tag=new-tag\n# hoist:previous=old-tag\n0 0 * * * docker run new\n# hoist:end report-prod"

	t.Run("replace existing", func(t *testing.T) {
		crontab := "# hoist:begin report-prod\n# hoist:tag=old-tag\n0 0 * * * docker run old\n# hoist:end report-prod\n"
		result := replaceCrontabBlock(crontab, "report-prod", newBlock)
		if !strings.Contains(result, "hoist:tag=new-tag") {
			t.Errorf("expected new tag, got: %s", result)
		}
		if strings.Contains(result, "hoist:tag=old-tag") {
			t.Errorf("should not contain old tag, got: %s", result)
		}
	})

	t.Run("append to empty", func(t *testing.T) {
		result := replaceCrontabBlock("", "report-prod", newBlock)
		if !strings.Contains(result, "hoist:begin report-prod") {
			t.Errorf("expected new block, got: %s", result)
		}
	})

	t.Run("append to existing other", func(t *testing.T) {
		crontab := "# hoist:begin other-prod\n# hoist:tag=other\n0 * * * * docker run other\n# hoist:end other-prod\n"
		result := replaceCrontabBlock(crontab, "report-prod", newBlock)
		if !strings.Contains(result, "hoist:begin other-prod") {
			t.Errorf("should preserve other block, got: %s", result)
		}
		if !strings.Contains(result, "hoist:begin report-prod") {
			t.Errorf("should append new block, got: %s", result)
		}
	})

	t.Run("replace preserves other blocks", func(t *testing.T) {
		crontab := "# hoist:begin other-prod\n# hoist:tag=other\n0 * * * * docker run other\n# hoist:end other-prod\n# hoist:begin report-prod\n# hoist:tag=old-tag\n0 0 * * * docker run old\n# hoist:end report-prod\n"
		result := replaceCrontabBlock(crontab, "report-prod", newBlock)
		if !strings.Contains(result, "hoist:begin other-prod") {
			t.Errorf("should preserve other block, got: %s", result)
		}
		if !strings.Contains(result, "hoist:tag=new-tag") {
			t.Errorf("should have new tag, got: %s", result)
		}
	})
}
