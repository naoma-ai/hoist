package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	Service  string
	Env      string
	Tag      string
	Uptime   time.Duration
	ExitCode int // cronjob: last run exit code
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
	deploy(ctx context.Context, service, env, tag, oldTag string, logf func(string, ...any)) error
}

type historyProvider interface {
	current(ctx context.Context, service, env string) (deploy, error)
	previous(ctx context.Context, service, env string) (deploy, error)
}

type logsProvider interface {
	tail(ctx context.Context, service, env string, n int, since string, w io.Writer) error
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
	errors map[string]error
}

type rollbackChoice int

const (
	rollbackAll    rollbackChoice = iota
	rollbackNone
	rollbackFailed
)

func newServiceLogf(w io.Writer, mu *sync.Mutex, service string, padLen int) func(string, ...any) {
	prefix := fmt.Sprintf("[%-*s]", padLen, service)
	return func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "%s %s\n", prefix, msg)
	}
}

func maxServiceNameLen(services []string) int {
	n := 0
	for _, s := range services {
		if len(s) > n {
			n = len(s)
		}
	}
	return n
}

func promptRollback(r io.Reader) rollbackChoice {
	fmt.Print("Rollback? [Y/n/s] (Y=all, n=leave, s=failed only) ")
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return rollbackNone
	}
	line := strings.TrimSpace(scanner.Text())
	switch {
	case line == "" || line == "Y" || line == "y":
		return rollbackAll
	case line == "n" || line == "N":
		return rollbackNone
	case line == "s" || line == "S":
		return rollbackFailed
	default:
		return rollbackNone
	}
}

func runDeploy(ctx context.Context, cfg config, p providers, opts deployOpts) error {
	env := opts.Env
	if env == "" {
		envs := allEnvironments(cfg)
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

	services := opts.Services
	if len(services) == 0 {
		names := servicesWithEnv(cfg, env)
		if len(names) == 0 {
			return fmt.Errorf("no services have environment %q", env)
		}
		if len(names) == 1 {
			services = names
		} else {
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
	}

	for _, svc := range services {
		if _, ok := cfg.Services[svc]; !ok {
			return fmt.Errorf("unknown service: %q", svc)
		}
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

	return deployAllWithLog(ctx, cfg, p, services, env, tags, previousTags, os.Stdout, os.Stdin)
}

// deployAllWithLog runs parallel deploys with plain log output.
func deployAllWithLog(ctx context.Context, cfg config, p providers, services []string, env string, tags map[string]string, previousTags map[string]string, w io.Writer, promptIn io.Reader) error {
	padLen := maxServiceNameLen(services)
	var mu sync.Mutex

	start := time.Now()
	result, err := deployAll(ctx, cfg, p, services, env, tags, previousTags, w, &mu, padLen)
	if err != nil {
		return err
	}
	duration := time.Since(start)

	if len(result.failed) == 0 {
		fmt.Fprintln(w, "Deploy complete!")
		if cfg.Hooks.PostDeploy != "" {
			event := buildDeployEvent(cfg.Project, env, services, tags, previousTags, result, duration, false)
			go firePostDeployHook(cfg.Hooks.PostDeploy, event)
		}
		return nil
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Deploy failed!")
	for _, svc := range result.failed {
		fmt.Fprintf(w, "  %s: %v\n", svc, result.errors[svc])
	}
	fmt.Fprintln(w)

	if cfg.Hooks.PostDeploy != "" {
		event := buildDeployEvent(cfg.Project, env, services, tags, previousTags, result, duration, false)
		go firePostDeployHook(cfg.Hooks.PostDeploy, event)
	}

	choice := promptRollback(promptIn)

	var rollbackServices []string
	switch choice {
	case rollbackAll:
		rollbackServices = services
	case rollbackFailed:
		rollbackServices = result.failed
	case rollbackNone:
		return nil
	}

	rollbackTags := make(map[string]string, len(rollbackServices))
	for _, svc := range rollbackServices {
		if prev, ok := previousTags[svc]; ok && prev != "" {
			rollbackTags[svc] = prev
		} else {
			fmt.Fprintf(w, "skipping %s: no previous deploy\n", svc)
		}
	}
	if len(rollbackTags) == 0 {
		fmt.Fprintln(w, "Nothing to roll back.")
		return nil
	}

	var rollbackTargets []string
	for svc := range rollbackTags {
		rollbackTargets = append(rollbackTargets, svc)
	}

	fmt.Fprintf(w, "Rolling back %d service(s)...\n", len(rollbackTargets))
	rbStart := time.Now()
	rbResult, err := deployAll(ctx, cfg, p, rollbackTargets, env, rollbackTags, tags, w, &mu, padLen)
	if err != nil {
		return fmt.Errorf("rollback: %w", err)
	}
	if len(rbResult.failed) > 0 {
		return fmt.Errorf("rollback failed for: %v", rbResult.failed)
	}
	fmt.Fprintln(w, "Rollback complete.")

	if cfg.Hooks.PostDeploy != "" {
		rbDuration := time.Since(rbStart)
		event := buildDeployEvent(cfg.Project, env, rollbackTargets, rollbackTags, tags, rbResult, rbDuration, true)
		go firePostDeployHook(cfg.Hooks.PostDeploy, event)
	}

	return nil
}

// deployAll runs parallel deploys with log output. Returns results for the caller to handle.
func deployAll(ctx context.Context, cfg config, p providers, services []string, env string, tags map[string]string, previousTags map[string]string, w io.Writer, mu *sync.Mutex, padLen int) (deployResult, error) {
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
			logf := newServiceLogf(w, mu, svc, padLen)
			oldTag := previousTags[svc]
			logf("deploying %s -> %s (env=%s)", oldTag, tags[svc], env)
			err := deployService(ctx, cfg, p, svc, env, tags[svc], oldTag, logf)
			if err != nil {
				logf("FAILED: %v", err)
			} else {
				logf("done")
			}
			results <- result{service: svc, err: err}
		}(svc)
	}

	wg.Wait()
	close(results)

	var failed []string
	errs := make(map[string]error)
	for r := range results {
		if r.err != nil {
			failed = append(failed, r.service)
			errs[r.service] = r.err
		}
	}

	return deployResult{failed: failed, errors: errs}, nil
}

func deployService(ctx context.Context, cfg config, p providers, service, env, tag, oldTag string, logf func(string, ...any)) error {
	svc := cfg.Services[service]

	d, ok := p.deployers[svc.Type]
	if !ok {
		return fmt.Errorf("no deployer for service type %q", svc.Type)
	}

	return d.deploy(ctx, service, env, tag, oldTag, logf)
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

func allEnvironments(cfg config) []string {
	seen := make(map[string]bool)
	for _, svc := range cfg.Services {
		for env := range svc.Env {
			seen[env] = true
		}
	}
	var result []string
	for env := range seen {
		result = append(result, env)
	}
	sort.Strings(result)
	return result
}

func servicesWithEnv(cfg config, env string) []string {
	var result []string
	for name, svc := range cfg.Services {
		if _, ok := svc.Env[env]; ok {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
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
// When services have different builds providers, it returns a merged provider
// that intersects results — only builds present in all providers are returned.
func buildsForServices(cfg config, p providers, services []string) buildsProvider {
	seen := map[buildsProvider]bool{}
	var unique []buildsProvider
	for _, svc := range services {
		bp, ok := p.builds[svc]
		if !ok {
			continue
		}
		if seen[bp] {
			continue
		}
		seen[bp] = true
		unique = append(unique, bp)
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
