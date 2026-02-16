package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type confirmResult int

const (
	confirmPending  confirmResult = iota
	confirmAccepted
	confirmRejected
)

type serviceChange struct {
	service string
	oldTag  string
	newTag  string
}

type confirmModel struct {
	env     string
	changes []serviceChange
	result  confirmResult
}

func newConfirmModel(env string, changes []serviceChange) confirmModel {
	return confirmModel{env: env, changes: changes}
}

func (m confirmModel) Init() tea.Cmd { return nil }

func (m confirmModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y", "enter":
			m.result = confirmAccepted
			return m, tea.Quit
		case "n", "N", "ctrl+c":
			m.result = confirmRejected
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m confirmModel) View() string {
	if m.result != confirmPending {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Deploy to %s:\n\n", m.env)

	for _, c := range m.changes {
		old := c.oldTag
		switch {
		case old == "":
			old = "(first deploy)"
		case old == c.newTag:
			old = "(no change)"
		}
		fmt.Fprintf(&b, "  %-16s %s -> %s\n", c.service, old, c.newTag)
	}

	b.WriteString("\nProceed? [Y/n] ")
	return b.String()
}
