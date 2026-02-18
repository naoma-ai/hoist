package main

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type buildsLoadedMsg struct {
	builds  []build
	hasMore bool
}

type buildsErrorMsg struct{ err error }

type historyLoadedMsg struct {
	liveTags     map[string]bool
	previousTags map[string]string
}

type historyErrorMsg struct{ err error }

type buildPickerModel struct {
	bp           buildsProvider
	builds       []build
	liveTags     map[string]bool
	previousTags map[string]string
	env          string
	cursor       int
	loading      bool
	hasMore      bool
	pageSize     int
	offset       int
	done         bool
	cancelled    bool
	fetchHistory func(ctx context.Context) (map[string]bool, map[string]string, error)
	historyErr   error
}

func newBuildPickerModel(bp buildsProvider, env string, fetchHistory func(ctx context.Context) (map[string]bool, map[string]string, error)) buildPickerModel {
	return buildPickerModel{
		bp:           bp,
		env:          env,
		loading:      true,
		pageSize:     20,
		fetchHistory: fetchHistory,
	}
}

func (m buildPickerModel) Init() tea.Cmd {
	fetchBuilds := m.fetchBuilds(m.pageSize, 0)
	if m.fetchHistory == nil {
		return fetchBuilds
	}
	fn := m.fetchHistory
	return tea.Batch(
		fetchBuilds,
		func() tea.Msg {
			liveTags, previousTags, err := fn(context.Background())
			if err != nil {
				return historyErrorMsg{err: err}
			}
			return historyLoadedMsg{liveTags: liveTags, previousTags: previousTags}
		},
	)
}

func (m buildPickerModel) fetchBuilds(limit, offset int) tea.Cmd {
	bp := m.bp
	return func() tea.Msg {
		builds, err := bp.listBuilds(context.Background(), limit, offset)
		if err != nil {
			return buildsErrorMsg{err: err}
		}
		hasMore := len(builds) == limit
		return buildsLoadedMsg{builds: builds, hasMore: hasMore}
	}
}

func (m buildPickerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case buildsLoadedMsg:
		m.builds = append(m.builds, msg.builds...)
		m.offset = len(m.builds)
		m.hasMore = msg.hasMore
		m.loading = false
		return m, nil

	case buildsErrorMsg:
		m.loading = false
		return m, nil

	case historyLoadedMsg:
		m.liveTags = msg.liveTags
		m.previousTags = msg.previousTags
		return m, nil

	case historyErrorMsg:
		m.historyErr = msg.err
		return m, nil

	case tea.KeyMsg:
		if m.loading {
			return m, nil
		}
		totalRows := len(m.builds)
		if m.hasMore {
			totalRows++ // "load more" row
		}

		switch msg.String() {
		case "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < totalRows-1 {
				m.cursor++
			}
		case "enter":
			if m.cursor < len(m.builds) {
				m.done = true
				return m, tea.Quit
			}
			// "load more" row
			if m.hasMore {
				m.loading = true
				return m, m.fetchBuilds(m.pageSize, m.offset)
			}
		}
	}
	return m, nil
}

func (m buildPickerModel) View() string {
	if m.done || m.cancelled {
		return ""
	}

	var b strings.Builder

	if m.loading && len(m.builds) == 0 {
		b.WriteString("Loading builds...\n")
		return b.String()
	}

	// Show live tags header
	var liveTags []string
	for t := range m.liveTags {
		liveTags = append(liveTags, t)
	}
	if len(liveTags) > 0 {
		fmt.Fprintf(&b, "Currently live in %s: %s\n\n", m.env, strings.Join(liveTags, ", "))
	}

	b.WriteString("Select a build:\n\n")

	for i, build := range m.builds {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		live := ""
		if m.liveTags[build.Tag] {
			live = " [LIVE]"
		}
		fmt.Fprintf(&b, "%s%s%s\n", cursor, build.Tag, live)
	}

	if m.hasMore {
		cursor := "  "
		if m.cursor == len(m.builds) {
			cursor = "> "
		}
		fmt.Fprintf(&b, "%s(load more)\n", cursor)
	}

	if m.loading {
		b.WriteString("\nLoading...\n")
	}

	b.WriteString("\nenter: select  ctrl+c: cancel\n")
	return b.String()
}
