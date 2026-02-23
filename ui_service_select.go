package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// multiSelectModel lets the user toggle multiple items on/off.
// All items start unchecked. Space toggles, enter confirms (>= 1 required).
type multiSelectModel struct {
	title     string
	items     []string
	selected  map[int]bool
	cursor    int
	done      bool
	cancelled bool
}

func newMultiSelectModel(title string, items []string) multiSelectModel {
	return multiSelectModel{
		title:    title,
		items:    items,
		selected: make(map[int]bool, len(items)),
	}
}

func (m multiSelectModel) Init() tea.Cmd { return nil }

func (m multiSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		case " ":
			m.selected[m.cursor] = !m.selected[m.cursor]
		case "enter":
			hasSelection := false
			for _, v := range m.selected {
				if v {
					hasSelection = true
					break
				}
			}
			if !hasSelection {
				return m, nil
			}
			m.done = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m multiSelectModel) View() string {
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
		check := "[ ]"
		if m.selected[i] {
			check = "[x]"
		}
		fmt.Fprintf(&b, "%s%s %s\n", cursor, check, item)
	}
	b.WriteString("\nspace: toggle  enter: confirm  ctrl+c: cancel\n")
	return b.String()
}

func (m multiSelectModel) chosen() []string {
	var result []string
	for i, item := range m.items {
		if m.selected[i] {
			result = append(result, item)
		}
	}
	return result
}
