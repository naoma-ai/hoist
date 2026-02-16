package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// singleSelectModel lets the user pick exactly one item.
type singleSelectModel struct {
	title     string
	items     []string
	cursor    int
	done      bool
	cancelled bool
}

func newSingleSelectModel(title string, items []string) singleSelectModel {
	return singleSelectModel{
		title: title,
		items: items,
	}
}

func (m singleSelectModel) Init() tea.Cmd { return nil }

func (m singleSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m singleSelectModel) View() string {
	if m.done || m.cancelled {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", m.title)
	for i, item := range m.items {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		fmt.Fprintf(&b, "%s%s\n", cursor, item)
	}
	b.WriteString("\nenter: select  ctrl+c: cancel\n")
	return b.String()
}
