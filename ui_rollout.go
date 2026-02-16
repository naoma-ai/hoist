package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type deployPhase int

const (
	phaseDeploying deployPhase = iota
	phaseComplete
	phaseRollbackPrompt
)

type serviceStatus struct {
	service string
	err     error
}

type serviceStatusMsg serviceStatus

type rollbackChoice int

const (
	rollbackAll    rollbackChoice = iota
	rollbackNone
	rollbackFailed
)

type deployModel struct {
	services       []string
	results        map[string]*serviceStatus
	pending        int
	phase          deployPhase
	spinner        spinner.Model
	failed         []string
	rollback       rollbackChoice
	rollbackChosen bool
}

func newDeployModel(services []string) deployModel {
	s := spinner.New()
	s.Spinner = spinner.Dot
	results := make(map[string]*serviceStatus, len(services))
	return deployModel{
		services: services,
		results:  results,
		pending:  len(services),
		phase:    phaseDeploying,
		spinner:  s,
	}
}

func (m deployModel) Init() tea.Cmd {
	return m.spinner.Tick
}

func (m deployModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case serviceStatusMsg:
		status := serviceStatus(msg)
		m.results[status.service] = &status
		m.pending--
		if status.err != nil {
			m.failed = append(m.failed, status.service)
		}
		if m.pending <= 0 {
			if len(m.failed) > 0 {
				m.phase = phaseRollbackPrompt
			} else {
				m.phase = phaseComplete
				return m, tea.Quit
			}
		}
		return m, nil

	case tea.KeyMsg:
		if m.phase == phaseRollbackPrompt {
			switch msg.String() {
			case "Y", "y", "enter":
				m.rollback = rollbackAll
				m.rollbackChosen = true
				return m, tea.Quit
			case "n", "N":
				m.rollback = rollbackNone
				m.rollbackChosen = true
				return m, tea.Quit
			case "s", "S":
				m.rollback = rollbackFailed
				m.rollbackChosen = true
				return m, tea.Quit
			case "ctrl+c":
				m.rollback = rollbackNone
				m.rollbackChosen = true
				return m, tea.Quit
			}
		}
		if m.phase == phaseDeploying {
			if msg.String() == "ctrl+c" {
				m.rollback = rollbackNone
				m.rollbackChosen = true
				return m, tea.Quit
			}
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m deployModel) View() string {
	var b strings.Builder

	switch m.phase {
	case phaseDeploying:
		b.WriteString(fmt.Sprintf("%s Deploying...\n\n", m.spinner.View()))
		for _, svc := range m.services {
			status, ok := m.results[svc]
			if !ok {
				fmt.Fprintf(&b, "  %s  deploying...\n", svc)
			} else if status.err != nil {
				fmt.Fprintf(&b, "  %s  FAILED: %v\n", svc, status.err)
			} else {
				fmt.Fprintf(&b, "  %s  done\n", svc)
			}
		}

	case phaseComplete:
		b.WriteString("Deploy complete!\n\n")
		for _, svc := range m.services {
			fmt.Fprintf(&b, "  %s  done\n", svc)
		}

	case phaseRollbackPrompt:
		b.WriteString("Deploy failed!\n\n")
		for _, svc := range m.services {
			status := m.results[svc]
			if status.err != nil {
				fmt.Fprintf(&b, "  %s  FAILED: %v\n", svc, status.err)
			} else {
				fmt.Fprintf(&b, "  %s  done\n", svc)
			}
		}
		b.WriteString("\nRollback? [Y/n/s] (Y=all, n=leave, s=failed only) ")
	}

	return b.String()
}
