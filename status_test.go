package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestGetStatusAllServices(t *testing.T) {
	cfg := testConfig()
	deploys := map[string]deploy{
		"backend:staging":      {Service: "backend", Env: "staging", Tag: "main-abc1234-20250101000000", Uptime: 3 * time.Hour},
		"backend:production":   {Service: "backend", Env: "production", Tag: "main-def5678-20241231000000", Uptime: 48 * time.Hour},
		"frontend:staging":     {Service: "frontend", Env: "staging", Tag: "main-abc1234-20250101000000", Uptime: 1 * time.Hour},
		"frontend:production":  {Service: "frontend", Env: "production", Tag: "main-def5678-20241231000000", Uptime: 24 * time.Hour},
		"report:staging":       {Service: "report", Env: "staging", Tag: "main-abc1234-20250101000000"},
		"report:production":    {Service: "report", Env: "production", Tag: "main-def5678-20241231000000"},
	}
	p, _ := testProviders(nil, deploys)

	rows, err := getStatus(context.Background(), cfg, p, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("expected 6 rows, got %d", len(rows))
	}

	// Rows should be sorted by service name, then env
	expected := []struct {
		service, env string
	}{
		{"backend", "production"},
		{"backend", "staging"},
		{"frontend", "production"},
		{"frontend", "staging"},
		{"report", "production"},
		{"report", "staging"},
	}
	for i, e := range expected {
		if rows[i].Service != e.service || rows[i].Env != e.env {
			t.Errorf("row %d: expected %s/%s, got %s/%s", i, e.service, e.env, rows[i].Service, rows[i].Env)
		}
	}
}

func TestGetStatusFilteredByEnv(t *testing.T) {
	cfg := testConfig()
	deploys := map[string]deploy{
		"backend:staging":  {Service: "backend", Env: "staging", Tag: "tag1", Uptime: time.Hour},
		"frontend:staging": {Service: "frontend", Env: "staging", Tag: "tag2", Uptime: time.Hour},
		"report:staging":   {Service: "report", Env: "staging", Tag: "tag3"},
	}
	p, _ := testProviders(nil, deploys)

	rows, err := getStatus(context.Background(), cfg, p, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
	for _, r := range rows {
		if r.Env != "staging" {
			t.Errorf("expected env staging, got %s", r.Env)
		}
	}
}

func TestGetStatusTypeField(t *testing.T) {
	cfg := testConfig()
	deploys := map[string]deploy{
		"backend:staging":  {Service: "backend", Env: "staging", Tag: "tag1", Uptime: time.Hour},
		"frontend:staging": {Service: "frontend", Env: "staging", Tag: "tag2", Uptime: time.Hour},
	}
	p, _ := testProviders(nil, deploys)

	rows, err := getStatus(context.Background(), cfg, p, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, r := range rows {
		switch r.Service {
		case "backend":
			if r.Type != "server" {
				t.Errorf("expected backend type 'server', got %q", r.Type)
			}
			if r.Health != "healthy" {
				t.Errorf("expected backend health 'healthy', got %q", r.Health)
			}
		case "frontend":
			if r.Type != "static" {
				t.Errorf("expected frontend type 'static', got %q", r.Type)
			}
			if r.Health != "" {
				t.Errorf("expected frontend health empty, got %q", r.Health)
			}
		}
	}
}

func TestGetStatusMissingDeploy(t *testing.T) {
	cfg := testConfig()
	// Only backend:staging has a deploy; others return zero deploy (not deployed yet)
	deploys := map[string]deploy{
		"backend:staging": {Service: "backend", Env: "staging", Tag: "tag1", Uptime: time.Hour},
	}
	p, _ := testProviders(nil, deploys)

	rows, err := getStatus(context.Background(), cfg, p, "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	for _, r := range rows {
		if r.Service == "frontend" && r.Tag != "" {
			t.Errorf("expected empty tag for frontend with missing deploy, got %q", r.Tag)
		}
	}
}

func TestGetStatusProviderError(t *testing.T) {
	cfg := testConfig()
	mh := &mockHistoryProvider{
		currentErrors: map[string]error{
			"backend:staging": fmt.Errorf("SSH connection refused"),
		},
	}
	p := providers{
		history: map[string]historyProvider{
			"server": mh,
			"static": mh,
		},
	}

	_, err := getStatus(context.Background(), cfg, p, "staging")
	if err == nil {
		t.Fatal("expected error from history provider")
	}
	if !contains(err.Error(), "SSH connection refused") {
		t.Errorf("expected underlying error, got: %v", err)
	}
}

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{40 * time.Minute, "40m"},
		{2 * time.Hour, "2h"},
		{24 * time.Hour, "1d"},
		{72 * time.Hour, "3d"},
		{30 * time.Minute, "30m"},
		{0, "0m"},
	}
	for _, tt := range tests {
		got := formatUptime(tt.d)
		if got != tt.want {
			t.Errorf("formatUptime(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestFormatStatusTableGroupedByType(t *testing.T) {
	rows := []statusRow{
		{Service: "backend", Env: "staging", Tag: "main-abc1234-20250101000000", Type: "server", Uptime: 3 * time.Hour, Health: "healthy"},
		{Service: "frontend", Env: "staging", Tag: "main-abc1234-20250101000000", Type: "static", Uptime: 1 * time.Hour},
	}
	output := formatStatusTable(rows)

	if !contains(output, "SERVERS") {
		t.Error("expected SERVERS section header")
	}
	if !contains(output, "STATIC") {
		t.Error("expected STATIC section header")
	}
	if !contains(output, "backend") {
		t.Error("expected backend in output")
	}
	if !contains(output, "frontend") {
		t.Error("expected frontend in output")
	}
	// HEALTH should appear in server section.
	if !contains(output, "HEALTH") {
		t.Error("expected HEALTH column in server section")
	}
	if !contains(output, "healthy") {
		t.Error("expected 'healthy' in server section")
	}
}

func TestFormatStatusTableCronjobSection(t *testing.T) {
	rows := []statusRow{
		{Service: "report", Env: "prod", Tag: "main-abc1234-20250101000000", Type: "cronjob", Schedule: "0 0 * * *", LastRun: "2h ago (exit 0)"},
	}
	output := formatStatusTable(rows)

	if !contains(output, "CRONJOBS") {
		t.Error("expected CRONJOBS section header")
	}
	if !contains(output, "SCHEDULE") {
		t.Error("expected SCHEDULE column")
	}
	if !contains(output, "LAST RUN") {
		t.Error("expected LAST RUN column")
	}
	if !contains(output, "0 0 * * *") {
		t.Error("expected schedule value")
	}
	if !contains(output, "2h ago (exit 0)") {
		t.Error("expected last run value")
	}
}

func TestFormatStatusTableSectionOrder(t *testing.T) {
	rows := []statusRow{
		{Service: "report", Env: "prod", Type: "cronjob", Tag: "tag1", Schedule: "0 0 * * *", LastRun: "never"},
		{Service: "backend", Env: "prod", Type: "server", Tag: "tag2", Uptime: time.Hour, Health: "healthy"},
		{Service: "frontend", Env: "prod", Type: "static", Tag: "tag3", Uptime: time.Hour},
	}
	output := formatStatusTable(rows)

	serverIdx := strings.Index(output, "SERVERS")
	staticIdx := strings.Index(output, "STATIC")
	cronjobIdx := strings.Index(output, "CRONJOBS")

	if serverIdx < 0 || staticIdx < 0 || cronjobIdx < 0 {
		t.Fatalf("expected all sections, got:\n%s", output)
	}

	if serverIdx > staticIdx || staticIdx > cronjobIdx {
		t.Errorf("expected section order SERVERS < STATIC < CRONJOBS, got positions %d, %d, %d", serverIdx, staticIdx, cronjobIdx)
	}
}

func TestFormatStatusTableEmpty(t *testing.T) {
	output := formatStatusTable(nil)
	if output != "No services found.\n" {
		t.Errorf("expected 'No services found.' message, got %q", output)
	}
}

func TestFormatStatusTableOnlyServerSection(t *testing.T) {
	rows := []statusRow{
		{Service: "backend", Env: "prod", Tag: "tag1", Type: "server", Uptime: time.Hour, Health: "healthy"},
	}
	output := formatStatusTable(rows)

	if !contains(output, "SERVERS") {
		t.Error("expected SERVERS section")
	}
	// Should NOT have STATIC or CRONJOBS sections.
	if contains(output, "STATIC") {
		t.Error("unexpected STATIC section")
	}
	if contains(output, "CRONJOBS") {
		t.Error("unexpected CRONJOBS section")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
