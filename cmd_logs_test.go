package main

import (
	"strings"
	"testing"
)

func testConfigYAML() string {
	return `
project: myapp
nodes:
  web1: 10.0.0.1
  web2: 10.0.0.2

services:
  backend:
    type: server
    image: myapp/backend
    port: 8080
    healthcheck: /health
    env:
      staging:
        node: web1
        host: api.staging.example.com
        envfile: /etc/backend/staging.env
      production:
        node: web2
        host: api.example.com
        envfile: /etc/backend/production.env
  frontend:
    type: static
    env:
      staging:
        bucket: frontend-staging
        cloudfront: E1234567890
      production:
        bucket: frontend-prod
        cloudfront: E0987654321
`
}

func TestLogsCommandUnknownService(t *testing.T) {
	cfgPath := writeTemp(t, testConfigYAML())
	cmd := newLogsCmd()
	cmd.SetArgs([]string{"-c", cfgPath, "-s", "nonexistent", "-e", "staging"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("expected 'unknown service' error, got: %v", err)
	}
}

func TestLogsCommandEnvNotFound(t *testing.T) {
	cfgPath := writeTemp(t, testConfigYAML())
	cmd := newLogsCmd()
	cmd.SetArgs([]string{"-c", cfgPath, "-s", "backend", "-e", "nonexistent"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "has no environment") {
		t.Errorf("expected 'has no environment' error, got: %v", err)
	}
}

func TestLogsCommandNoCommonEnv(t *testing.T) {
	yaml := `
project: test
nodes:
  n1: 10.0.0.1
services:
  svc1:
    type: server
    image: a
    port: 8080
    healthcheck: /h
    env:
      staging:
        node: n1
        host: a.com
        envfile: .env
  svc2:
    type: server
    image: b
    port: 8080
    healthcheck: /h
    env:
      production:
        node: n1
        host: b.com
        envfile: .env
`
	cfgPath := writeTemp(t, yaml)
	cmd := newLogsCmd()
	cmd.SetArgs([]string{"-c", cfgPath})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no common environments") {
		t.Errorf("expected 'no common environments' error, got: %v", err)
	}
}
