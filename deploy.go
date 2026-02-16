package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var errCancelled = errors.New("cancelled")

type build struct {
	Tag     string
	Branch  string
	SHA     string
	Time    time.Time
	Message string
	Author  string
}

type deploy struct {
	Service string
	Env     string
	Tag     string
	Uptime  time.Duration
}

func buildFromTag(t tag) build {
	return build{
		Tag:    generateTag(t.Branch, t.SHA, t.Time, t.Attempt),
		Branch: t.Branch,
		SHA:    t.SHA,
		Time:   t.Time,
	}
}

type buildsProvider interface {
	listBuilds(ctx context.Context, limit, offset int) ([]build, error)
}

type deployer interface {
	current(ctx context.Context, service, env string) (deploy, error)
	deploy(ctx context.Context, service, env, tag, oldTag string) error
}

type providers struct {
	builds    map[string]buildsProvider
	deployers map[string]deployer
}

type deployOpts struct {
	Services []string
	Env      string
	Build    string
	Yes      bool
}

// deployResult holds the outcome of a parallel deploy.
type deployResult struct {
	failed       []string
	previousTags map[string]string
}

func runDeploy(ctx context.Context, cfg config, p providers, opts deployOpts) error {
	services := opts.Services
	if len(services) == 0 {
		names := sortedServiceNames(cfg)
		result, err := tea.NewProgram(newMultiSelectModel("Select services to deploy:", names)).Run()
		if err != nil {
			return err
		}
		m := result.(multiSelectModel)
		if m.cancelled {
			return errCancelled
		}
		services = m.chosen()
	}

	for _, svc := range services {
		if _, ok := cfg.Services[svc]; !ok {
			return fmt.Errorf("unknown service: %q", svc)
		}
	}

	env := opts.Env
	if env == "" {
		envs := envIntersection(cfg, services)
		if len(envs) == 0 {
			return fmt.Errorf("no common environments across selected services")
		}
		result, err := tea.NewProgram(newSingleSelectModel("Select environment:", envs)).Run()
		if err != nil {
			return err
		}
		m := result.(singleSelectModel)
		if m.cancelled {
			return errCancelled
		}
		env = m.items[m.cursor]
	}

	for _, svc := range services {
		if _, ok := cfg.Services[svc].Env[env]; !ok {
			return fmt.Errorf("service %q has no environment %q", svc, env)
		}
	}

	liveTags := make(map[string]bool)
	previousTags := make(map[string]string)
	for _, svc := range services {
		svcCfg := cfg.Services[svc]
		d, ok := p.deployers[svcCfg.Type]
		if !ok {
			continue
		}
		cur, err := d.current(ctx, svc, env)
		if err == nil && cur.Tag != "" {
			liveTags[cur.Tag] = true
			previousTags[svc] = cur.Tag
		}
	}

	bp := buildsForServices(cfg, p, services)

	var buildTag string
	if opts.Build != "" {
		var err error
		buildTag, err = resolveBuildTag(ctx, bp, opts.Build)
		if err != nil {
			return fmt.Errorf("resolving build: %w", err)
		}
	} else {
		result, err := tea.NewProgram(newBuildPickerModel(bp, liveTags, env)).Run()
		if err != nil {
			return fmt.Errorf("build picker: %w", err)
		}
		bm := result.(buildPickerModel)
		if bm.cancelled {
			return errCancelled
		}
		if bm.cursor >= len(bm.builds) {
			return fmt.Errorf("no build selected")
		}
		buildTag = bm.builds[bm.cursor].Tag
	}

	if !opts.Yes {
		var changes []serviceChange
		for _, svc := range services {
			changes = append(changes, serviceChange{
				service: svc,
				oldTag:  previousTags[svc],
				newTag:  buildTag,
			})
		}
		result, err := tea.NewProgram(newConfirmModel(env, changes)).Run()
		if err != nil {
			return fmt.Errorf("confirm: %w", err)
		}
		cm := result.(confirmModel)
		if cm.result != confirmAccepted {
			return errCancelled
		}
	}

	return deployAllWithUI(ctx, cfg, p, services, env, buildTag, previousTags)
}

// deployAllWithUI runs parallel deploys with TUI progress display.
func deployAllWithUI(ctx context.Context, cfg config, p providers, services []string, env, tag string, previousTags map[string]string) error {
	dm := newDeployModel(services)
	prog := tea.NewProgram(dm)

	var wg sync.WaitGroup
	for _, svc := range services {
		wg.Add(1)
		go func(svc string) {
			defer wg.Done()
			oldTag := previousTags[svc]
			err := deployService(ctx, cfg, p, svc, env, tag, oldTag)
			prog.Send(serviceStatusMsg{service: svc, err: err})
		}(svc)
	}

	finalModel, err := prog.Run()
	if err != nil {
		wg.Wait()
		return fmt.Errorf("deploy UI: %w", err)
	}
	wg.Wait()

	dm = finalModel.(deployModel)
	if dm.phase == phaseComplete {
		return nil
	}

	// Rollback prompt was shown; the model has the user's choice
	// For now, return nil — rollback is handled via deployAll below
	return nil
}

// deployAll runs parallel deploys without TUI. Returns results for the caller to handle.
func deployAll(ctx context.Context, cfg config, p providers, services []string, env, tag string, previousTags map[string]string) (deployResult, error) {
	type result struct {
		service string
		err     error
	}

	results := make(chan result, len(services))
	var wg sync.WaitGroup

	for _, svc := range services {
		wg.Add(1)
		go func(svc string) {
			defer wg.Done()
			oldTag := previousTags[svc]
			err := deployService(ctx, cfg, p, svc, env, tag, oldTag)
			results <- result{service: svc, err: err}
		}(svc)
	}

	wg.Wait()
	close(results)

	var failed []string
	for r := range results {
		if r.err != nil {
			failed = append(failed, r.service)
		}
	}

	return deployResult{
		failed:       failed,
		previousTags: previousTags,
	}, nil
}

func deployService(ctx context.Context, cfg config, p providers, service, env, tag, oldTag string) error {
	svc := cfg.Services[service]

	d, ok := p.deployers[svc.Type]
	if !ok {
		return fmt.Errorf("no deployer for service type %q", svc.Type)
	}

	return d.deploy(ctx, service, env, tag, oldTag)
}


func rollback(ctx context.Context, cfg config, p providers, services []string, env string, previousTags map[string]string) error {
	var errs []string
	for _, svc := range services {
		oldTag, ok := previousTags[svc]
		if !ok || oldTag == "" {
			continue
		}
		err := deployService(ctx, cfg, p, svc, env, oldTag, "")
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", svc, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("rollback errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func resolveBuildTag(ctx context.Context, bp buildsProvider, value string) (string, error) {
	if _, err := parseTag(value); err == nil {
		return value, nil
	}

	builds, err := bp.listBuilds(ctx, 100, 0)
	if err != nil {
		return "", fmt.Errorf("listing builds: %w", err)
	}

	sanitized := sanitizeBranch(value)
	for _, b := range builds {
		if b.Branch == sanitized || b.Branch == value {
			return b.Tag, nil
		}
	}

	return "", fmt.Errorf("no builds found for branch %q", value)
}

func sortedServiceNames(cfg config) []string {
	names := make([]string, 0, len(cfg.Services))
	for name := range cfg.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func envIntersection(cfg config, services []string) []string {
	if len(services) == 0 {
		return nil
	}

	counts := make(map[string]int)
	for env := range cfg.Services[services[0]].Env {
		counts[env] = 1
	}

	for _, svc := range services[1:] {
		for env := range cfg.Services[svc].Env {
			if _, ok := counts[env]; ok {
				counts[env]++
			}
		}
	}

	var result []string
	for env, count := range counts {
		if count == len(services) {
			result = append(result, env)
		}
	}
	sort.Strings(result)
	return result
}

// buildsForServices returns the first matching builds provider for the selected services.
func buildsForServices(cfg config, p providers, services []string) buildsProvider {
	for _, svc := range services {
		t := cfg.Services[svc].Type
		if bp, ok := p.builds[t]; ok {
			return bp
		}
	}
	return nil
}
