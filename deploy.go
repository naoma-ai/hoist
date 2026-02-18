package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
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
	deploy(ctx context.Context, service, env, tag, oldTag string) error
}

type historyProvider interface {
	current(ctx context.Context, service, env string) (deploy, error)
	previous(ctx context.Context, service, env string) (deploy, error)
}

type logsProvider interface {
	tail(ctx context.Context, service, env string, n int, since string) error
}

type providers struct {
	builds    map[string]buildsProvider
	deployers map[string]deployer
	history   map[string]historyProvider
	logs      map[string]logsProvider
}

type deployOpts struct {
	Services []string
	Env      string
	Build    string
	Tags     map[string]string // pre-resolved per-service tags (skips build select)
	Yes      bool
}

// deployResult holds the outcome of a parallel deploy.
type deployResult struct {
	failed []string
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
		if len(envs) == 1 {
			env = envs[0]
		} else {
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
	}

	for _, svc := range services {
		if _, ok := cfg.Services[svc].Env[env]; !ok {
			return fmt.Errorf("service %q has no environment %q", svc, env)
		}
	}

	fetchHistory := func(ctx context.Context) (map[string]bool, map[string]string, error) {
		liveTags := make(map[string]bool)
		previousTags := make(map[string]string)
		for _, svc := range services {
			svcCfg := cfg.Services[svc]
			hp, ok := p.history[svcCfg.Type]
			if !ok {
				continue
			}
			cur, err := hp.current(ctx, svc, env)
			if err != nil {
				return nil, nil, fmt.Errorf("getting current deploy for %s: %w", svc, err)
			}
			if cur.Tag != "" {
				liveTags[cur.Tag] = true
				previousTags[svc] = cur.Tag
			}
		}
		return liveTags, previousTags, nil
	}

	// Resolve per-service tags: either pre-provided, from --build flag, or interactive
	tags := opts.Tags
	var previousTags map[string]string
	if tags != nil {
		// Pre-resolved tags (e.g. rollback): fetch history synchronously.
		_, prevTags, err := fetchHistory(ctx)
		if err != nil {
			return err
		}
		previousTags = prevTags
	} else {
		bp := buildsForServices(cfg, p, services)

		var buildTag string
		if opts.Build != "" {
			// Non-interactive: need history before resolving build.
			liveTags, prevTags, err := fetchHistory(ctx)
			if err != nil {
				return err
			}
			_ = liveTags
			previousTags = prevTags

			buildTag, err = resolveBuildTag(ctx, bp, opts.Build)
			if err != nil {
				return fmt.Errorf("resolving build: %w", err)
			}
		} else {
			result, err := tea.NewProgram(newBuildPickerModel(bp, env, fetchHistory)).Run()
			if err != nil {
				return fmt.Errorf("build picker: %w", err)
			}
			bm := result.(buildPickerModel)
			if bm.cancelled {
				return errCancelled
			}
			if bm.historyErr != nil {
				return bm.historyErr
			}
			if bm.cursor >= len(bm.builds) {
				return fmt.Errorf("no build selected")
			}
			buildTag = bm.builds[bm.cursor].Tag
			previousTags = bm.previousTags
		}

		tags = make(map[string]string, len(services))
		for _, svc := range services {
			tags[svc] = buildTag
		}
	}

	if !opts.Yes {
		var changes []serviceChange
		for _, svc := range services {
			changes = append(changes, serviceChange{
				service: svc,
				oldTag:  previousTags[svc],
				newTag:  tags[svc],
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

	return deployAllWithUI(ctx, cfg, p, services, env, tags, previousTags)
}

// deployAllWithUI runs parallel deploys with TUI progress display.
// tags maps each service to the tag it should be deployed to.
func deployAllWithUI(ctx context.Context, cfg config, p providers, services []string, env string, tags map[string]string, previousTags map[string]string) error {
	deployCtx, cancelDeploy := context.WithCancel(ctx)
	defer cancelDeploy()

	dm := newDeployModel(services)
	prog := tea.NewProgram(dm)

	var wg sync.WaitGroup
	for _, svc := range services {
		wg.Add(1)
		go func(svc string) {
			defer wg.Done()
			oldTag := previousTags[svc]
			err := deployService(deployCtx, cfg, p, svc, env, tags[svc], oldTag)
			prog.Send(serviceStatusMsg{service: svc, err: err})
		}(svc)
	}

	finalModel, err := prog.Run()
	if err != nil {
		cancelDeploy()
		wg.Wait()
		return fmt.Errorf("deploy UI: %w", err)
	}

	dm = finalModel.(deployModel)

	// ctrl+c during deploying phase: cancel context and wait for goroutines
	if dm.phase == phaseDeploying {
		cancelDeploy()
		wg.Wait()
		return errCancelled
	}

	wg.Wait()

	// Print full errors after TUI exits (TUI truncates to terminal width).
	for _, svc := range dm.failed {
		if status := dm.results[svc]; status != nil && status.err != nil {
			fmt.Fprintf(os.Stderr, "\n%s: %v\n", svc, status.err)
		}
	}

	if dm.phase == phaseComplete {
		return nil
	}

	var rollbackServices []string
	switch dm.rollback {
	case rollbackAll:
		rollbackServices = services
	case rollbackFailed:
		rollbackServices = dm.failed
	case rollbackNone:
		return nil
	}

	rollbackTags := make(map[string]string, len(rollbackServices))
	for _, svc := range rollbackServices {
		if prev, ok := previousTags[svc]; ok && prev != "" {
			rollbackTags[svc] = prev
		} else {
			fmt.Printf("skipping %s: no previous deploy\n", svc)
		}
	}
	if len(rollbackTags) == 0 {
		fmt.Println("Nothing to roll back.")
		return nil
	}

	var rollbackTargets []string
	for svc := range rollbackTags {
		rollbackTargets = append(rollbackTargets, svc)
	}

	fmt.Printf("Rolling back %d service(s)...\n", len(rollbackTargets))
	result, err := deployAll(ctx, cfg, p, rollbackTargets, env, rollbackTags, tags)
	if err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	if len(result.failed) > 0 {
		return fmt.Errorf("rollback failed for: %v", result.failed)
	}
	fmt.Println("Rollback complete.")
	return nil
}

// deployAll runs parallel deploys without TUI. Returns results for the caller to handle.
// tags maps each service to the tag it should be deployed to.
func deployAll(ctx context.Context, cfg config, p providers, services []string, env string, tags map[string]string, previousTags map[string]string) (deployResult, error) {
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
			err := deployService(ctx, cfg, p, svc, env, tags[svc], oldTag)
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
		failed: failed,
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

// buildsForServices returns a builds provider for the selected services.
// When services use different provider types, it returns a merged provider
// that intersects results — only builds present in all providers are returned.
func buildsForServices(cfg config, p providers, services []string) buildsProvider {
	seen := map[string]bool{}
	var unique []buildsProvider
	for _, svc := range services {
		t := cfg.Services[svc].Type
		if seen[t] {
			continue
		}
		seen[t] = true
		if bp, ok := p.builds[t]; ok {
			unique = append(unique, bp)
		}
	}
	if len(unique) == 0 {
		return nil
	}
	if len(unique) == 1 {
		return unique[0]
	}
	return &mergedBuildsProvider{providers: unique}
}

// mergedBuildsProvider intersects builds from multiple providers.
// Only builds whose tag exists in every provider are returned.
type mergedBuildsProvider struct {
	providers []buildsProvider
}

func (m *mergedBuildsProvider) listBuilds(ctx context.Context, limit, offset int) ([]build, error) {
	const fetchLimit = 100

	type result struct {
		builds []build
		err    error
	}
	results := make([]result, len(m.providers))

	var wg sync.WaitGroup
	for i, bp := range m.providers {
		wg.Add(1)
		go func(i int, bp buildsProvider) {
			defer wg.Done()
			b, err := bp.listBuilds(ctx, fetchLimit, 0)
			results[i] = result{builds: b, err: err}
		}(i, bp)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
	}

	// Count how many providers have each tag
	counts := map[string]int{}
	byTag := map[string]build{}
	for _, r := range results {
		for _, b := range r.builds {
			counts[b.Tag]++
			byTag[b.Tag] = b
		}
	}

	// Keep only builds present in all providers
	var all []build
	for tag, count := range counts {
		if count == len(m.providers) {
			all = append(all, byTag[tag])
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Time.After(all[j].Time)
	})

	if offset >= len(all) {
		return nil, nil
	}
	all = all[offset:]
	if limit < len(all) {
		all = all[:limit]
	}

	return all, nil
}
