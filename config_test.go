package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "hoist.yml")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigValidFull(t *testing.T) {
	yaml := `
project: myapp

nodes:
  prod1: 10.0.0.1
  staging1: 10.0.0.2

services:
  api:
    type: server
    image: api:latest
    port: 8080
    healthcheck: /health
    env:
      production:
        node: prod1
        host: api.example.com
        envfile: .env.prod
      staging:
        node: staging1
        host: api.staging.example.com
        envfile: .env.staging
  web:
    type: static
    env:
      production:
        bucket: my-bucket-prod
        cloudfront: EPROD123
      staging:
        bucket: my-bucket-staging
        cloudfront: ESTAGING456
`
	cfg, err := loadConfig(writeTemp(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := config{
		Project: "myapp",
		Nodes: map[string]string{
			"prod1":    "10.0.0.1",
			"staging1": "10.0.0.2",
		},
		Services: map[string]serviceConfig{
			"api": {
				Type:        "server",
				Image:       "api:latest",
				Port:        8080,
				Healthcheck: "/health",
				Env: map[string]envConfig{
					"production": {Node: "prod1", Host: "api.example.com", EnvFile: ".env.prod"},
					"staging":    {Node: "staging1", Host: "api.staging.example.com", EnvFile: ".env.staging"},
				},
			},
			"web": {
				Type: "static",
				Env: map[string]envConfig{
					"production": {Bucket: "my-bucket-prod", CloudFront: "EPROD123"},
					"staging":    {Bucket: "my-bucket-staging", CloudFront: "ESTAGING456"},
				},
			},
		},
	}

	if diff := cmp.Diff(want, cfg); diff != "" {
		t.Errorf("config mismatch (-want +got):\n%s", diff)
	}
}

func TestLoadConfigServerMissingFields(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing image",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  api:
    type: server
    port: 8080
    healthcheck: /health
    env:
      prod:
        node: n1
        host: api.com
        envfile: .env
`,
			wantErr: "missing image",
		},
		{
			name: "missing port",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  api:
    type: server
    image: api:latest
    healthcheck: /health
    env:
      prod:
        node: n1
        host: api.com
        envfile: .env
`,
			wantErr: "missing port",
		},
		{
			name: "missing healthcheck",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  api:
    type: server
    image: api:latest
    port: 8080
    env:
      prod:
        node: n1
        host: api.com
        envfile: .env
`,
			wantErr: "missing healthcheck",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(writeTemp(t, tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadConfigServerEnvMissingFields(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing node",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  api:
    type: server
    image: api:latest
    port: 8080
    healthcheck: /health
    env:
      prod:
        host: api.com
        envfile: .env
`,
			wantErr: "missing node",
		},
		{
			name: "missing host",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  api:
    type: server
    image: api:latest
    port: 8080
    healthcheck: /health
    env:
      prod:
        node: n1
        envfile: .env
`,
			wantErr: "missing host",
		},
		{
			name: "missing envfile",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  api:
    type: server
    image: api:latest
    port: 8080
    healthcheck: /health
    env:
      prod:
        node: n1
        host: api.com
`,
			wantErr: "missing envfile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(writeTemp(t, tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadConfigUndefinedNode(t *testing.T) {
	yaml := `
project: test
nodes:
  n1: 10.0.0.1
services:
  api:
    type: server
    image: api:latest
    port: 8080
    healthcheck: /health
    env:
      prod:
        node: nonexistent
        host: api.com
        envfile: .env
`
	_, err := loadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not defined in nodes") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "not defined in nodes")
	}
}

func TestLoadConfigUnknownServiceType(t *testing.T) {
	yaml := `
project: test
services:
  fn:
    type: lambda
    env:
      prod:
        bucket: b
`
	_, err := loadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "unknown type") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "unknown type")
	}
}

func TestLoadConfigEmptyServices(t *testing.T) {
	yaml := `
project: test
services: {}
`
	_, err := loadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no services defined") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "no services defined")
	}
}

func TestLoadConfigServiceNoEnvironments(t *testing.T) {
	yaml := `
project: test
services:
  api:
    type: server
    image: api:latest
    port: 8080
    healthcheck: /health
    env: {}
`
	_, err := loadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no environments defined") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "no environments defined")
	}
}

func TestLoadConfigStaticWithoutServerFields(t *testing.T) {
	yaml := `
project: test
services:
  web:
    type: static
    env:
      prod:
        bucket: my-bucket
        cloudfront: E123
`
	_, err := loadConfig(writeTemp(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadConfigStaticEnvMissingFields(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing bucket",
			yaml: `
project: test
services:
  web:
    type: static
    env:
      prod:
        cloudfront: E123
`,
			wantErr: "missing bucket",
		},
		{
			name: "missing cloudfront",
			yaml: `
project: test
services:
  web:
    type: static
    env:
      prod:
        bucket: my-bucket
`,
			wantErr: "missing cloudfront",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(writeTemp(t, tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadConfigCronjobValid(t *testing.T) {
	yaml := `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    image: myapp/report
    schedule: "0 0 * * *"
    command: /run-report
    env:
      prod:
        node: n1
        envfile: /etc/report/prod.env
        cronfile: /etc/cron.d/hoist-report-prod
`
	cfg, err := loadConfig(writeTemp(t, yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	svc := cfg.Services["report"]
	if svc.Type != "cronjob" {
		t.Errorf("expected type cronjob, got %s", svc.Type)
	}
	if svc.Schedule != "0 0 * * *" {
		t.Errorf("expected schedule '0 0 * * *', got %s", svc.Schedule)
	}
	if svc.Command != "/run-report" {
		t.Errorf("expected command '/run-report', got %s", svc.Command)
	}
	ec := svc.Env["prod"]
	if ec.Cronfile != "/etc/cron.d/hoist-report-prod" {
		t.Errorf("expected cronfile path, got %s", ec.Cronfile)
	}
}

func TestLoadConfigCronjobMissingFields(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing image",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    schedule: "0 0 * * *"
    env:
      prod:
        node: n1
        envfile: .env
        cronfile: /etc/cron.d/report
`,
			wantErr: "missing image",
		},
		{
			name: "missing schedule",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    image: myapp/report
    env:
      prod:
        node: n1
        envfile: .env
        cronfile: /etc/cron.d/report
`,
			wantErr: "missing schedule",
		},
		{
			name: "has port",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    image: myapp/report
    schedule: "0 0 * * *"
    port: 8080
    env:
      prod:
        node: n1
        envfile: .env
        cronfile: /etc/cron.d/report
`,
			wantErr: "must not have port",
		},
		{
			name: "has healthcheck",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    image: myapp/report
    schedule: "0 0 * * *"
    healthcheck: /health
    env:
      prod:
        node: n1
        envfile: .env
        cronfile: /etc/cron.d/report
`,
			wantErr: "must not have healthcheck",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(writeTemp(t, tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadConfigCronjobEnvMissingFields(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "missing node",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    image: myapp/report
    schedule: "0 0 * * *"
    env:
      prod:
        envfile: .env
        cronfile: /etc/cron.d/report
`,
			wantErr: "missing node",
		},
		{
			name: "missing envfile",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    image: myapp/report
    schedule: "0 0 * * *"
    env:
      prod:
        node: n1
        cronfile: /etc/cron.d/report
`,
			wantErr: "missing envfile",
		},
		{
			name: "missing cronfile",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    image: myapp/report
    schedule: "0 0 * * *"
    env:
      prod:
        node: n1
        envfile: .env
`,
			wantErr: "missing cronfile",
		},
		{
			name: "undefined node",
			yaml: `
project: test
nodes:
  n1: 10.0.0.1
services:
  report:
    type: cronjob
    image: myapp/report
    schedule: "0 0 * * *"
    env:
      prod:
        node: nonexistent
        envfile: .env
        cronfile: /etc/cron.d/report
`,
			wantErr: "not defined in nodes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(writeTemp(t, tt.yaml))
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/hoist.yml")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	yaml := `
services:
  - this is not valid
  [broken yaml
`
	_, err := loadConfig(writeTemp(t, yaml))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
