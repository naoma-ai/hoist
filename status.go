package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type statusRow struct {
	Service  string
	Env      string
	Tag      string
	Type     string
	Uptime   time.Duration
	Health   string // server only
	Schedule string // cronjob only
	LastRun  string // cronjob only: "2h ago (exit 0)"
}

func getStatus(ctx context.Context, cfg config, p providers, envFilter string) ([]statusRow, error) {
	type query struct {
		name string
		env  string
		svc  serviceConfig
	}

	var queries []query
	for _, name := range sortedServiceNames(cfg) {
		svc := cfg.Services[name]
		envs := make([]string, 0, len(svc.Env))
		for e := range svc.Env {
			envs = append(envs, e)
		}
		sort.Strings(envs)

		for _, env := range envs {
			if envFilter != "" && env != envFilter {
				continue
			}
			if _, ok := p.history[svc.Type]; !ok {
				continue
			}
			queries = append(queries, query{name: name, env: env, svc: svc})
		}
	}

	type result struct {
		index int
		row   statusRow
		err   error
	}

	results := make([]result, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(i int, q query) {
			defer wg.Done()
			hp := p.history[q.svc.Type]
			cur, err := hp.current(ctx, q.name, q.env)
			if err != nil {
				results[i] = result{err: fmt.Errorf("getting status for %s/%s: %w", q.name, q.env, err)}
				return
			}

			row := statusRow{
				Service: q.name,
				Env:     q.env,
				Tag:     cur.Tag,
				Type:    q.svc.Type,
				Uptime:  cur.Uptime,
			}

			switch q.svc.Type {
			case "server":
				row.Health = "healthy"
			case "cronjob":
				row.Schedule = q.svc.Schedule
				if cur.Uptime > 0 {
					row.LastRun = fmt.Sprintf("%s ago (exit %d)", formatUptime(cur.Uptime), cur.ExitCode)
				} else if cur.Tag != "" {
					row.LastRun = "never"
				}
			}

			results[i] = result{row: row}
		}(i, q)
	}
	wg.Wait()

	rows := make([]statusRow, 0, len(queries))
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		rows = append(rows, r.row)
	}
	return rows, nil
}

func formatUptime(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func formatStatusTable(rows []statusRow) string {
	if len(rows) == 0 {
		return "No services found.\n"
	}

	// Group by type.
	groups := map[string][]statusRow{}
	for _, r := range rows {
		groups[r.Type] = append(groups[r.Type], r)
	}

	var b strings.Builder

	// Render sections in order: servers, static, cronjobs.
	sectionOrder := []struct {
		key   string
		label string
	}{
		{"server", "SERVERS"},
		{"static", "STATIC"},
		{"cronjob", "CRONJOBS"},
	}

	first := true
	for _, sec := range sectionOrder {
		sectionRows, ok := groups[sec.key]
		if !ok || len(sectionRows) == 0 {
			continue
		}
		if !first {
			b.WriteString("\n")
		}
		first = false

		fmt.Fprintf(&b, "%s\n", sec.label)

		switch sec.key {
		case "server":
			formatServerSection(&b, sectionRows)
		case "static":
			formatStaticSection(&b, sectionRows)
		case "cronjob":
			formatCronjobSection(&b, sectionRows)
		}
	}

	return b.String()
}

func formatServerSection(b *strings.Builder, rows []statusRow) {
	svcW, envW, tagW, upW, healthW := len("SERVICE"), len("ENV"), len("TAG"), len("UPTIME"), len("HEALTH")
	for _, r := range rows {
		svcW = max(svcW, len(r.Service))
		envW = max(envW, len(r.Env))
		tagW = max(tagW, len(r.Tag))
		upW = max(upW, len(formatUptime(r.Uptime)))
		healthW = max(healthW, len(r.Health))
	}

	fmt.Fprintf(b, "%-*s  %-*s  %-*s  %-*s  %-*s\n", svcW, "SERVICE", envW, "ENV", tagW, "TAG", upW, "UPTIME", healthW, "HEALTH")
	for _, r := range rows {
		fmt.Fprintf(b, "%-*s  %-*s  %-*s  %-*s  %-*s\n", svcW, r.Service, envW, r.Env, tagW, r.Tag, upW, formatUptime(r.Uptime), healthW, r.Health)
	}
}

func formatStaticSection(b *strings.Builder, rows []statusRow) {
	svcW, envW, tagW, upW := len("SERVICE"), len("ENV"), len("TAG"), len("UPTIME")
	for _, r := range rows {
		svcW = max(svcW, len(r.Service))
		envW = max(envW, len(r.Env))
		tagW = max(tagW, len(r.Tag))
		upW = max(upW, len(formatUptime(r.Uptime)))
	}

	fmt.Fprintf(b, "%-*s  %-*s  %-*s  %-*s\n", svcW, "SERVICE", envW, "ENV", tagW, "TAG", upW, "UPTIME")
	for _, r := range rows {
		fmt.Fprintf(b, "%-*s  %-*s  %-*s  %-*s\n", svcW, r.Service, envW, r.Env, tagW, r.Tag, upW, formatUptime(r.Uptime))
	}
}

func formatCronjobSection(b *strings.Builder, rows []statusRow) {
	svcW, envW, tagW, schedW, lastW := len("SERVICE"), len("ENV"), len("TAG"), len("SCHEDULE"), len("LAST RUN")
	for _, r := range rows {
		svcW = max(svcW, len(r.Service))
		envW = max(envW, len(r.Env))
		tagW = max(tagW, len(r.Tag))
		schedW = max(schedW, len(r.Schedule))
		lastW = max(lastW, len(r.LastRun))
	}

	fmt.Fprintf(b, "%-*s  %-*s  %-*s  %-*s  %-*s\n", svcW, "SERVICE", envW, "ENV", tagW, "TAG", schedW, "SCHEDULE", lastW, "LAST RUN")
	for _, r := range rows {
		fmt.Fprintf(b, "%-*s  %-*s  %-*s  %-*s  %-*s\n", svcW, r.Service, envW, r.Env, tagW, r.Tag, schedW, r.Schedule, lastW, r.LastRun)
	}
}
