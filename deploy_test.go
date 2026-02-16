package main

import (
	"context"
	"fmt"
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
	mu      sync.Mutex
	delay   time.Duration
	calls   []deployCall
	errors  map[string]error  // keyed by service name
	deploys map[string]deploy // keyed by "service:env"
}

type deployCall struct {
	service string
	env     string
	tag     string
	oldTag  string
}

func (m *mockDeployer) current(_ context.Context, service, env string) (deploy, error) {
	if m.deploys == nil {
		return deploy{}, fmt.Errorf("no deploy found for %s:%s", service, env)
	}
	key := service + ":" + env
	d, ok := m.deploys[key]
	if !ok {
		return deploy{}, fmt.Errorf("no deploy found for %s", key)
	}
	return d, nil
}

func (m *mockDeployer) deploy(_ context.Context, service, env, tag, oldTag string) error {
	time.Sleep(m.delay)
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

func testConfig() config {
	return config{
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
		},
	}
}

func testProviders(builds []build, deploys map[string]deploy) (providers, *mockDeployer) {
	md := &mockDeployer{deploys: deploys}
	bp := &mockBuildsProvider{builds: builds}
	return providers{
		builds: map[string]buildsProvider{
			"server": bp,
			"static": bp,
		},
		deployers: map[string]deployer{
			"server": md,
			"static": md,
		},
	}, md
}

func TestDeployAllHappyPath(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	tag := "main-abc1234-20250101000000"
	previousTags := map[string]string{
		"backend":  "main-old1234-20241231000000",
		"frontend": "main-old1234-20241231000000",
	}

	result, err := deployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tag, previousTags)
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

	result, err := deployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", "main-abc1234-20250101000000", nil)
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

	err := deployService(context.Background(), cfg, p, "backend", "staging", "main-abc1234-20250101000000", "old-tag")
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

	err := deployService(context.Background(), cfg, p, "frontend", "staging", "main-abc1234-20250101000000", "")
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


func TestRollbackAll(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	previousTags := map[string]string{
		"backend":  "main-old1234-20241231000000",
		"frontend": "main-old1234-20241231000000",
	}

	err := rollback(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", previousTags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(md.calls) != 2 {
		t.Fatalf("expected 2 rollback calls, got %d", len(md.calls))
	}
	for _, c := range md.calls {
		if c.tag != "main-old1234-20241231000000" {
			t.Fatalf("expected old tag, got %s", c.tag)
		}
	}
}

func TestRollbackSkipsEmptyPreviousTags(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	previousTags := map[string]string{
		"backend": "main-old1234-20241231000000",
	}

	err := rollback(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", previousTags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(md.calls) != 1 {
		t.Fatalf("expected 1 call (backend only), got %d", len(md.calls))
	}
	if md.calls[0].service != "backend" {
		t.Fatalf("expected backend, got %s", md.calls[0].service)
	}
}

func TestRollbackFailedOnly(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	previousTags := map[string]string{
		"backend": "main-old1234-20241231000000",
	}

	err := rollback(context.Background(), cfg, p, []string{"backend"}, "staging", previousTags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(md.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(md.calls))
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
	if len(names) != 2 || names[0] != "backend" || names[1] != "frontend" {
		t.Fatalf("expected [backend frontend], got %v", names)
	}
}

func TestRunDeployNonInteractive(t *testing.T) {
	cfg := testConfig()

	tag := "main-abc1234-20250101000000"
	builds := []build{{Tag: tag, Branch: "main", SHA: "abc1234", Time: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}}
	deploys := map[string]deploy{
		"backend:staging": {Service: "backend", Env: "staging", Tag: "main-old1234-20241231000000"},
	}
	md := &mockDeployer{deploys: deploys}
	bp := &mockBuildsProvider{builds: builds}
	p := providers{
		builds: map[string]buildsProvider{
			"server": bp,
			"static": bp,
		},
		deployers: map[string]deployer{
			"server": md,
			"static": md,
		},
	}

	previousTags := map[string]string{
		"backend": "main-old1234-20241231000000",
	}
	result, err := deployAll(context.Background(), cfg, p, []string{"backend"}, "staging", tag, previousTags)
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
	result, err := deployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", tag, nil)
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

func TestDeployAllParallelExecution(t *testing.T) {
	cfg := testConfig()
	p, md := testProviders(nil, nil)

	result, err := deployAll(context.Background(), cfg, p, []string{"backend", "frontend"}, "staging", "main-abc1234-20250101000000", nil)
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
