package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockBuildsProvider struct {
	builds []build
}

func (m *mockBuildsProvider) listBuilds(_ context.Context, limit, offset int) ([]build, error) {
	if offset >= len(m.builds) {
		return nil, nil
	}
	end := offset + limit
	if end > len(m.builds) {
		end = len(m.builds)
	}
	return m.builds[offset:end], nil
}

type mockDeployer struct {
	mu     sync.Mutex
	delay  time.Duration
	calls  []deployCall
	errors map[string]error // keyed by service name
}

type mockHistoryProvider struct {
	deploys         map[string]deploy // keyed by "service:env" — used by current()
	currentErrors   map[string]error  // keyed by "service:env"
	previousDeploys map[string]deploy // keyed by "service:env" — used by previous() when set
	previousErrors  map[string]error  // keyed by "service:env"
}

type deployCall struct {
	service string
	env     string
	tag     string
	oldTag  string
}

func (m *mockHistoryProvider) current(_ context.Context, service, env string) (deploy, error) {
	key := service + ":" + env
	if m.currentErrors != nil {
		if err, ok := m.currentErrors[key]; ok {
			return deploy{}, err
		}
	}
	if m.deploys == nil {
		return deploy{}, nil
	}
	d, ok := m.deploys[key]
	if !ok {
		return deploy{}, nil
	}
	return d, nil
}

func (m *mockHistoryProvider) previous(_ context.Context, service, env string) (deploy, error) {
	key := service + ":" + env
	if m.previousErrors != nil {
		if err, ok := m.previousErrors[key]; ok {
			return deploy{}, err
		}
	}
	if m.previousDeploys != nil {
		d, ok := m.previousDeploys[key]
		if !ok {
			return deploy{}, nil
		}
		return d, nil
	}
	// Legacy fallback: use deploys map, error if missing
	if m.deploys == nil {
		return deploy{}, fmt.Errorf("no previous deploy for %s:%s", service, env)
	}
	d, ok := m.deploys[key]
	if !ok {
		return deploy{}, fmt.Errorf("no previous deploy for %s", key)
	}
	return d, nil
}

func (m *mockDeployer) deploy(ctx context.Context, service, env, tag, oldTag string, logf func(string, ...any)) error {
	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.delay):
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, deployCall{service: service, env: env, tag: tag, oldTag: oldTag})
	if m.errors != nil {
		if err, ok := m.errors[service]; ok {
			return err
		}
	}
	return nil
}

func nopLogf(string, ...any) {}

func testConfig() config {
	return config{
		Project: "myapp",
		Nodes: map[string]string{
			"web1": "10.0.0.1",
			"web2": "10.0.0.2",
		},
		Services: map[string]serviceConfig{
			"backend": {
				Type:        "server",
				Image:       "myapp/backend",
				Port:        8080,
				Healthcheck: "/health",
				Env: map[string]envConfig{
					"staging": {
						Node:    "web1",
						Host:    "api.staging.example.com",
						EnvFile: "/etc/backend/staging.env",
					},
					"production": {
						Node:    "web2",
						Host:    "api.example.com",
						EnvFile: "/etc/backend/production.env",
					},
				},
			},
			"frontend": {
				Type: "static",
				Env: map[string]envConfig{
					"staging": {
						Bucket:     "frontend-staging",
						CloudFront: "E1234567890",
					},
					"production": {
						Bucket:     "frontend-prod",
						CloudFront: "E0987654321",
					},
				},
			},
			"report": {
				Type:     "cronjob",
				Image:    "myapp/report",
				Schedule: "0 0 * * *",
				Command:  "/run-report",
				Env: map[string]envConfig{
					"staging": {
						Node:    "web1",
						EnvFile: "/etc/report/staging.env",
					},
					"production": {
						Node:    "web2",
						EnvFile: "/etc/report/production.env",
					},
				},
			},
		},
	}
}

func testProviders(builds []build, deploys map[string]deploy) (providers, *mockDeployer) {
	md := &mockDeployer{}
	bp := &mockBuildsProvider{builds: builds}
	mh := &mockHistoryProvider{deploys: deploys}
	return providers{
		builds: map[string]buildsProvider{
			"backend":  bp,
			"frontend": bp,
			"report":   bp,
		},
		deployers: map[string]deployer{
			"server":  md,
			"static":  md,
			"cronjob": md,
		},
		history: map[string]historyProvider{
			"server":  mh,
			"static":  mh,
			"cronjob": mh,
		},
	}, md
}

// testDeployAll is a helper that passes io.Discard for the writer.
func testDeployAll(ctx context.Context, cfg config, p providers, services []string, env string, tags, previousTags map[string]string) (deployResult, error) {
	var mu sync.Mutex
	padLen := maxServiceNameLen(services)
	return deployAll(ctx, cfg, p, services, env, tags, previousTags, io.Discard, &mu, padLen)
}

func TestDeployAllHappyPath(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	tag := "main-abc1234-20250101000000"
	previousTags := map[string]string{
		"backend":  "main-old1234-20241231000000",
		"frontend": "main-old1234-20241231000000",
	}

	tags := map[string]string{
		"backend":  tag,
		"frontend": tag,
	}
	result, err := testDeployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tags, previousTags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.failed) != 0 {
		t.Fatalf("expected no failures, got %v", result.failed)
	}

	if len(md.calls) != 2 {
		t.Fatalf("expected 2 deploy calls, got %d", len(md.calls))
	}

	var services []string
	for _, c := range md.calls {
		services = append(services, c.service)
	}
	sort.Strings(services)
	if services[0] != "backend" || services[1] != "frontend" {
		t.Fatalf("expected [backend frontend], got %v", services)
	}

	for _, c := range md.calls {
		if c.service == "backend" {
			if c.tag != tag {
				t.Fatalf("expected tag %s, got %s", tag, c.tag)
			}
			if c.oldTag != "main-old1234-20241231000000" {
				t.Fatalf("expected old tag, got %s", c.oldTag)
			}
		}
	}
}

func TestDeployAllPartialFailure(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)
	md.errors = map[string]error{
		"backend": fmt.Errorf("connection refused"),
	}

	partialTag := "main-abc1234-20250101000000"
	tags := map[string]string{"backend": partialTag, "frontend": partialTag}
	result, err := testDeployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tags, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.failed) != 1 {
		t.Fatalf("expected 1 failure, got %d: %v", len(result.failed), result.failed)
	}
	if result.failed[0] != "backend" {
		t.Fatalf("expected backend to fail, got %s", result.failed[0])
	}

	if len(md.calls) != 2 {
		t.Fatalf("expected 2 deploy calls, got %d", len(md.calls))
	}
}

func TestDeployServiceServer(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	err := deployService(context.Background(), cfg, p, "backend", "staging", "main-abc1234-20250101000000", "old-tag", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(md.calls) != 1 {
		t.Fatalf("expected 1 deploy call, got %d", len(md.calls))
	}
	call := md.calls[0]
	if call.service != "backend" {
		t.Fatalf("expected backend, got %s", call.service)
	}
	if call.tag != "main-abc1234-20250101000000" {
		t.Fatalf("expected tag, got %s", call.tag)
	}
	if call.oldTag != "old-tag" {
		t.Fatalf("expected old-tag, got %s", call.oldTag)
	}
}

func TestDeployServiceStatic(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	err := deployService(context.Background(), cfg, p, "frontend", "staging", "main-abc1234-20250101000000", "", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(md.calls) != 1 {
		t.Fatalf("expected 1 deploy call, got %d", len(md.calls))
	}
	if md.calls[0].service != "frontend" {
		t.Fatalf("expected frontend, got %s", md.calls[0].service)
	}
}

func TestDeployAllWithPerServiceTags(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	// Simulate rollback: each service gets a different tag
	tags := map[string]string{
		"backend":  "main-old1111-20241230000000",
		"frontend": "main-old2222-20241229000000",
	}
	currentTags := map[string]string{
		"backend":  "main-cur1111-20250101000000",
		"frontend": "main-cur2222-20250101000000",
	}

	result, err := testDeployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tags, currentTags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.failed) != 0 {
		t.Fatalf("expected no failures, got %v", result.failed)
	}

	if len(md.calls) != 2 {
		t.Fatalf("expected 2 deploy calls, got %d", len(md.calls))
	}
	for _, c := range md.calls {
		if c.tag != tags[c.service] {
			t.Errorf("service %s: expected tag %s, got %s", c.service, tags[c.service], c.tag)
		}
		if c.oldTag != currentTags[c.service] {
			t.Errorf("service %s: expected oldTag %s, got %s", c.service, currentTags[c.service], c.oldTag)
		}
	}
}

func TestResolveBuildTagFullTag(t *testing.T) {
	bp := &mockBuildsProvider{}
	tag := "main-abc1234-20250101000000"

	result, err := resolveBuildTag(context.Background(), bp, tag)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != tag {
		t.Fatalf("expected %s, got %s", tag, result)
	}
}

func TestResolveBuildTagBranchName(t *testing.T) {
	builds := []build{
		{Tag: "main-abc1234-20250102000000", Branch: "main"},
		{Tag: "main-def5678-20250101000000", Branch: "main"},
		{Tag: "feat-xyz-ghi9012-20250101000000", Branch: "feat-xyz"},
	}
	bp := &mockBuildsProvider{builds: builds}

	result, err := resolveBuildTag(context.Background(), bp, "feat-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "feat-xyz-ghi9012-20250101000000" {
		t.Fatalf("expected feat-xyz build, got %s", result)
	}
}

func TestResolveBuildTagUnknownBranch(t *testing.T) {
	bp := &mockBuildsProvider{builds: []build{
		{Tag: "main-abc1234-20250101000000", Branch: "main"},
	}}

	_, err := resolveBuildTag(context.Background(), bp, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown branch")
	}
	if !strings.Contains(err.Error(), "no builds found") {
		t.Fatalf("expected 'no builds found' error, got: %v", err)
	}
}

func TestAllEnvironments(t *testing.T) {
	cfg := testConfig()
	envs := allEnvironments(cfg)
	if len(envs) != 2 {
		t.Fatalf("expected 2 envs, got %d: %v", len(envs), envs)
	}
	if envs[0] != "production" || envs[1] != "staging" {
		t.Fatalf("expected [production staging], got %v", envs)
	}
}

func TestAllEnvironmentsMixed(t *testing.T) {
	cfg := config{
		Services: map[string]serviceConfig{
			"a": {Env: map[string]envConfig{"staging": {}}},
			"b": {Env: map[string]envConfig{"production": {}}},
		},
	}
	envs := allEnvironments(cfg)
	if len(envs) != 2 {
		t.Fatalf("expected 2 envs, got %d: %v", len(envs), envs)
	}
	if envs[0] != "production" || envs[1] != "staging" {
		t.Fatalf("expected [production staging], got %v", envs)
	}
}

func TestServicesWithEnv(t *testing.T) {
	cfg := testConfig()
	services := servicesWithEnv(cfg, "staging")
	if len(services) != 3 {
		t.Fatalf("expected 3 services, got %d: %v", len(services), services)
	}
	if services[0] != "backend" || services[1] != "frontend" || services[2] != "report" {
		t.Fatalf("expected [backend frontend report], got %v", services)
	}
}

func TestServicesWithEnvPartial(t *testing.T) {
	cfg := config{
		Services: map[string]serviceConfig{
			"a": {Env: map[string]envConfig{"staging": {}}},
			"b": {Env: map[string]envConfig{"production": {}}},
			"c": {Env: map[string]envConfig{"staging": {}, "production": {}}},
		},
	}
	services := servicesWithEnv(cfg, "staging")
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d: %v", len(services), services)
	}
	if services[0] != "a" || services[1] != "c" {
		t.Fatalf("expected [a c], got %v", services)
	}
}

func TestServicesWithEnvNone(t *testing.T) {
	cfg := testConfig()
	services := servicesWithEnv(cfg, "nonexistent")
	if len(services) != 0 {
		t.Fatalf("expected 0 services, got %d: %v", len(services), services)
	}
}

func TestEnvIntersection(t *testing.T) {
	cfg := testConfig()

	envs := envIntersection(cfg, []string{"backend", "frontend"})
	if len(envs) != 2 {
		t.Fatalf("expected 2 envs, got %d: %v", len(envs), envs)
	}
	if envs[0] != "production" || envs[1] != "staging" {
		t.Fatalf("expected [production staging], got %v", envs)
	}
}

func TestEnvIntersectionNoOverlap(t *testing.T) {
	cfg := config{
		Services: map[string]serviceConfig{
			"a": {Env: map[string]envConfig{"staging": {}}},
			"b": {Env: map[string]envConfig{"production": {}}},
		},
	}

	envs := envIntersection(cfg, []string{"a", "b"})
	if len(envs) != 0 {
		t.Fatalf("expected 0 envs, got %d", len(envs))
	}
}

func TestSortedServiceNames(t *testing.T) {
	cfg := testConfig()
	names := sortedServiceNames(cfg)
	if len(names) != 3 || names[0] != "backend" || names[1] != "frontend" || names[2] != "report" {
		t.Fatalf("expected [backend frontend report], got %v", names)
	}
}

func TestRunDeployNonInteractive(t *testing.T) {
	cfg := testConfig()

	tag := "main-abc1234-20250101000000"
	builds := []build{{Tag: tag, Branch: "main", SHA: "abc1234", Time: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}}
	deploys := map[string]deploy{
		"backend:staging": {Service: "backend", Env: "staging", Tag: "main-old1234-20241231000000"},
	}
	md := &mockDeployer{}
	bp := &mockBuildsProvider{builds: builds}
	mh := &mockHistoryProvider{deploys: deploys}
	p := providers{
		builds: map[string]buildsProvider{
			"server": bp,
			"static": bp,
		},
		deployers: map[string]deployer{
			"server": md,
			"static": md,
		},
		history: map[string]historyProvider{
			"server": mh,
			"static": mh,
		},
	}

	previousTags := map[string]string{
		"backend": "main-old1234-20241231000000",
	}
	tags := map[string]string{"backend": tag}
	result, err := testDeployAll(context.Background(), cfg, p, []string{"backend"}, "staging", tags, previousTags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.failed) != 0 {
		t.Fatalf("expected no failures, got %v", result.failed)
	}

	if len(md.calls) != 1 {
		t.Fatalf("expected 1 deploy call, got %d", len(md.calls))
	}
	if md.calls[0].tag != tag {
		t.Fatalf("expected tag %s, got %s", tag, md.calls[0].tag)
	}
}

func TestDeployAllMultipleServices(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	tag := "main-abc1234-20250101000000"
	tags := map[string]string{"backend": tag, "frontend": tag}
	result, err := testDeployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tags, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.failed) != 0 {
		t.Fatalf("expected no failures, got %v", result.failed)
	}

	if len(md.calls) != 2 {
		t.Fatalf("expected 2 deploy calls, got %d", len(md.calls))
	}
}

func TestRunDeployUnknownService(t *testing.T) {
	cfg := testConfig()
	p, _ := testProviders(nil, nil)

	err := runDeploy(context.Background(), cfg, p, deployOpts{
		Services: []string{"nonexistent"},
		Env:      "staging",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unknown service") {
		t.Errorf("expected 'unknown service' error, got: %v", err)
	}
}

func TestRunDeployEnvNotFound(t *testing.T) {
	cfg := testConfig()
	p, _ := testProviders(nil, nil)

	err := runDeploy(context.Background(), cfg, p, deployOpts{
		Services: []string{"backend"},
		Env:      "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "has no environment") {
		t.Errorf("expected 'has no environment' error, got: %v", err)
	}
}

func TestRunDeployServiceHasNoEnv(t *testing.T) {
	cfg := testConfig()
	p, _ := testProviders(nil, nil)

	// backend has staging+production, but "nonexistent" doesn't exist
	err := runDeploy(context.Background(), cfg, p, deployOpts{
		Services: []string{"backend"},
		Env:      "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "has no environment") {
		t.Errorf("expected 'has no environment' error, got: %v", err)
	}
}

func TestRunDeployHistoryProviderError(t *testing.T) {
	cfg := testConfig()
	mh := &mockHistoryProvider{
		currentErrors: map[string]error{
			"backend:staging": fmt.Errorf("SSH connection refused"),
		},
	}
	md := &mockDeployer{}
	bp := &mockBuildsProvider{}
	p := providers{
		builds:    map[string]buildsProvider{"server": bp, "static": bp},
		deployers: map[string]deployer{"server": md, "static": md},
		history:   map[string]historyProvider{"server": mh, "static": mh},
	}

	err := runDeploy(context.Background(), cfg, p, deployOpts{
		Services: []string{"backend"},
		Env:      "staging",
		Tags:     map[string]string{"backend": "main-abc1234-20250101000000"},
		Yes:      true,
	})
	if err == nil {
		t.Fatal("expected error from history provider")
	}
	if !strings.Contains(err.Error(), "getting current deploy") {
		t.Errorf("expected 'getting current deploy' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "SSH connection refused") {
		t.Errorf("expected underlying error in message, got: %v", err)
	}
}

func TestDeployAllAllFail(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)
	md.errors = map[string]error{
		"backend":  fmt.Errorf("connection refused"),
		"frontend": fmt.Errorf("S3 access denied"),
	}

	tag := "main-abc1234-20250101000000"
	tags := map[string]string{"backend": tag, "frontend": tag}
	result, err := testDeployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tags, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sort.Strings(result.failed)
	if len(result.failed) != 2 {
		t.Fatalf("expected 2 failures, got %d: %v", len(result.failed), result.failed)
	}
	if result.failed[0] != "backend" || result.failed[1] != "frontend" {
		t.Fatalf("expected [backend frontend], got %v", result.failed)
	}
}

func TestEnvIntersectionSingleEnv(t *testing.T) {
	cfg := config{
		Services: map[string]serviceConfig{
			"a": {Env: map[string]envConfig{"staging": {}}},
			"b": {Env: map[string]envConfig{"staging": {}}},
		},
	}

	envs := envIntersection(cfg, []string{"a", "b"})
	if len(envs) != 1 {
		t.Fatalf("expected 1 env, got %d: %v", len(envs), envs)
	}
	if envs[0] != "staging" {
		t.Fatalf("expected staging, got %s", envs[0])
	}
}

func TestDeployAllContextCancellation(t *testing.T) {
	cfg := testConfig()
	md := &mockDeployer{delay: 5 * time.Second}
	bp := &mockBuildsProvider{}
	mh := &mockHistoryProvider{}
	p := providers{
		builds:    map[string]buildsProvider{"server": bp, "static": bp},
		deployers: map[string]deployer{"server": md, "static": md},
		history:   map[string]historyProvider{"server": mh, "static": mh},
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after a short delay so goroutines see cancellation
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	tag := "main-abc1234-20250101000000"
	tags := map[string]string{"backend": tag, "frontend": tag}
	result, err := testDeployAll(ctx, cfg, p, []string{"backend", "frontend"}, "staging", tags, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both services should fail due to context cancellation
	if len(result.failed) != 2 {
		t.Fatalf("expected 2 failures from context cancellation, got %d", len(result.failed))
	}
}

func TestBuildsForServicesIntersection(t *testing.T) {
	cfg := testConfig()

	shared1 := "main-abc1234-20250101100000"
	shared2 := "main-def5678-20250101090000"
	serverOnly := "main-aaa1111-20250101080000"
	staticOnly := "main-bbb2222-20250101070000"

	serverBuilds := &mockBuildsProvider{builds: []build{
		{Tag: shared1, Branch: "main", SHA: "abc1234", Time: mustParseTag(t, shared1).Time},
		{Tag: shared2, Branch: "main", SHA: "def5678", Time: mustParseTag(t, shared2).Time},
		{Tag: serverOnly, Branch: "main", SHA: "aaa1111", Time: mustParseTag(t, serverOnly).Time},
	}}
	staticBuilds := &mockBuildsProvider{builds: []build{
		{Tag: shared1, Branch: "main", SHA: "abc1234", Time: mustParseTag(t, shared1).Time},
		{Tag: shared2, Branch: "main", SHA: "def5678", Time: mustParseTag(t, shared2).Time},
		{Tag: staticOnly, Branch: "main", SHA: "bbb2222", Time: mustParseTag(t, staticOnly).Time},
	}}

	p := providers{
		builds: map[string]buildsProvider{
			"backend":  serverBuilds,
			"frontend": staticBuilds,
		},
	}

	bp := buildsForServices(cfg, p, []string{"backend", "frontend"})
	builds, err := bp.listBuilds(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(builds) != 2 {
		t.Fatalf("expected 2 builds (intersection), got %d", len(builds))
	}
	if builds[0].Tag != shared1 {
		t.Errorf("builds[0].Tag = %q, want %q", builds[0].Tag, shared1)
	}
	if builds[1].Tag != shared2 {
		t.Errorf("builds[1].Tag = %q, want %q", builds[1].Tag, shared2)
	}
}

func mustParseTag(t *testing.T, s string) tag {
	t.Helper()
	tg, err := parseTag(s)
	if err != nil {
		t.Fatalf("parseTag(%q): %v", s, err)
	}
	return tg
}

func TestBuildsForServicesSingleType(t *testing.T) {
	cfg := config{
		Services: map[string]serviceConfig{
			"api":     {Type: "server"},
			"workers": {Type: "server"},
		},
	}

	bp := &mockBuildsProvider{builds: []build{
		{Tag: "main-abc1234-20250101100000"},
	}}
	p := providers{
		builds: map[string]buildsProvider{
			"api":     bp,
			"workers": bp,
		},
	}

	result := buildsForServices(cfg, p, []string{"api", "workers"})
	// When all services share the same provider instance, no intersection needed — return it directly
	builds, err := result.listBuilds(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(builds) != 1 {
		t.Fatalf("expected 1 build, got %d", len(builds))
	}
}

func TestDeployAllParallelExecution(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	parallelTag := "main-abc1234-20250101000000"
	parallelTags := map[string]string{"backend": parallelTag, "frontend": parallelTag}
	result, err := testDeployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", parallelTags, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.failed) != 0 {
		t.Fatalf("expected no failures, got %v", result.failed)
	}

	var deployedServices []string
	for _, c := range md.calls {
		deployedServices = append(deployedServices, c.service)
	}
	sort.Strings(deployedServices)
	if len(deployedServices) != 2 || deployedServices[0] != "backend" || deployedServices[1] != "frontend" {
		t.Fatalf("expected [backend frontend], got %v", deployedServices)
	}
}

// --- New tests for log output and helpers ---

func TestNewServiceLogf(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	logf := newServiceLogf(&buf, &mu, "backend", 10)
	logf("pulling %s", "image:tag")
	logf("done")

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), buf.String())
	}
	if lines[0] != "[backend   ] pulling image:tag" {
		t.Errorf("unexpected line 0: %q", lines[0])
	}
	if lines[1] != "[backend   ] done" {
		t.Errorf("unexpected line 1: %q", lines[1])
	}
}

func TestNewServiceLogfConcurrent(t *testing.T) {
	var buf bytes.Buffer
	var mu sync.Mutex
	logA := newServiceLogf(&buf, &mu, "a", 3)
	logB := newServiceLogf(&buf, &mu, "bbb", 3)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); logA("msg") }()
		go func() { defer wg.Done(); logB("msg") }()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 100 {
		t.Fatalf("expected 100 lines, got %d", len(lines))
	}
	for _, line := range lines {
		if line != "[a  ] msg" && line != "[bbb] msg" {
			t.Errorf("unexpected line: %q", line)
		}
	}
}

func TestMaxServiceNameLen(t *testing.T) {
	if n := maxServiceNameLen([]string{"a", "bb", "ccc"}); n != 3 {
		t.Errorf("expected 3, got %d", n)
	}
	if n := maxServiceNameLen(nil); n != 0 {
		t.Errorf("expected 0, got %d", n)
	}
}

func TestPromptRollback(t *testing.T) {
	tests := []struct {
		input    string
		expected rollbackChoice
	}{
		{"Y\n", rollbackAll},
		{"y\n", rollbackAll},
		{"\n", rollbackAll},
		{"n\n", rollbackNone},
		{"N\n", rollbackNone},
		{"s\n", rollbackFailed},
		{"S\n", rollbackFailed},
		{"x\n", rollbackNone},
	}
	for _, tc := range tests {
		r := strings.NewReader(tc.input)
		got := promptRollback(r)
		if got != tc.expected {
			t.Errorf("input %q: expected %d, got %d", tc.input, tc.expected, got)
		}
	}
}

func TestPromptRollbackEOF(t *testing.T) {
	r := strings.NewReader("")
	got := promptRollback(r)
	if got != rollbackNone {
		t.Errorf("expected rollbackNone on EOF, got %d", got)
	}
}

func TestDeployAllLogOutput(t *testing.T) {
	cfg := testConfig()
	p, _ := testProviders(nil, nil)

	var buf bytes.Buffer
	var mu sync.Mutex
	tag := "main-abc1234-20250101000000"
	tags := map[string]string{"backend": tag, "frontend": tag}

	_, err := deployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tags, nil, &buf, &mu, 8)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "[backend ]") {
		t.Error("expected [backend ] prefix in output")
	}
	if !strings.Contains(output, "[frontend]") {
		t.Error("expected [frontend] prefix in output")
	}
	if !strings.Contains(output, "done") {
		t.Error("expected 'done' in output")
	}
}

func TestDeployAllErrorsMap(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)
	md.errors = map[string]error{
		"backend": fmt.Errorf("connection refused"),
	}

	tag := "main-abc1234-20250101000000"
	tags := map[string]string{"backend": tag, "frontend": tag}
	result, err := testDeployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tags, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.errors == nil {
		t.Fatal("expected errors map to be populated")
	}
	if result.errors["backend"] == nil {
		t.Fatal("expected error for backend")
	}
	if !strings.Contains(result.errors["backend"].Error(), "connection refused") {
		t.Errorf("expected 'connection refused' in error, got: %v", result.errors["backend"])
	}
	if _, ok := result.errors["frontend"]; ok {
		t.Error("expected no error for frontend")
	}
}
