package main

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateDeploy(m deployModel, msg tea.Msg) (deployModel, tea.Cmd) {
	model, cmd := m.Update(msg)
	return model.(deployModel), cmd
}

func TestDeployAllSuccess(t *testing.T) {
	m := newDeployModel([]string{"frontend", "backend"})

	m, _ = updateDeploy(m, serviceStatusMsg{service: "frontend"})
	if m.phase != phaseDeploying {
		t.Fatal("should still be deploying with 1 pending")
	}

	m, cmd := updateDeploy(m, serviceStatusMsg{service: "backend"})
	if m.phase != phaseComplete {
		t.Fatal("should be complete")
	}
	if cmd == nil {
		t.Fatal("expected quit command")
	}
}

func TestDeployPartialFailure(t *testing.T) {
	m := newDeployModel([]string{"frontend", "backend"})

	m, _ = updateDeploy(m, serviceStatusMsg{service: "frontend"})
	m, _ = updateDeploy(m, serviceStatusMsg{service: "backend", err: fmt.Errorf("connection refused")})

	if m.phase != phaseRollbackPrompt {
		t.Fatal("should be in rollback prompt")
	}
	if len(m.failed) != 1 || m.failed[0] != "backend" {
		t.Fatalf("expected [backend] failed, got %v", m.failed)
	}
}

func TestDeployRollbackPromptKeys(t *testing.T) {
	tests := []struct {
		key    tea.KeyMsg
		choice rollbackChoice
	}{
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("Y")}, rollbackAll},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")}, rollbackAll},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}, rollbackNone},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("N")}, rollbackNone},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}, rollbackFailed},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")}, rollbackFailed},
	}

	for _, tc := range tests {
		m := newDeployModel([]string{"a"})
		m, _ = updateDeploy(m, serviceStatusMsg{service: "a", err: fmt.Errorf("fail")})

		if m.phase != phaseRollbackPrompt {
			t.Fatalf("should be in rollback prompt for key %v", tc.key)
		}

		m, cmd := updateDeploy(m, tc.key)
		if cmd == nil {
			t.Fatalf("expected quit command for key %v", tc.key)
		}
		if !m.rollbackChosen {
			t.Fatalf("expected rollbackChosen for key %v", tc.key)
		}
		if m.rollback != tc.choice {
			t.Fatalf("key %v: expected choice %d, got %d", tc.key, tc.choice, m.rollback)
		}
	}
}

func TestDeployRollbackDefaultYesOnEnter(t *testing.T) {
	m := newDeployModel([]string{"a"})
	m, _ = updateDeploy(m, serviceStatusMsg{service: "a", err: fmt.Errorf("fail")})

	m, cmd := updateDeploy(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command on enter")
	}
	if m.rollback != rollbackAll {
		t.Fatalf("expected rollbackAll on enter, got %d", m.rollback)
	}
}

func TestDeployViewDeploying(t *testing.T) {
	m := newDeployModel([]string{"frontend", "backend"})
	m, _ = updateDeploy(m, serviceStatusMsg{service: "frontend"})

	view := m.View()
	if !strings.Contains(view, "Deploying...") {
		t.Fatal("should show deploying")
	}
	if !strings.Contains(view, "frontend  done") {
		t.Fatal("should show frontend done")
	}
	if !strings.Contains(view, "backend  deploying...") {
		t.Fatal("should show backend deploying")
	}
}

func TestDeployViewComplete(t *testing.T) {
	m := newDeployModel([]string{"frontend"})
	m, _ = updateDeploy(m, serviceStatusMsg{service: "frontend"})

	view := m.View()
	if !strings.Contains(view, "Deploy complete!") {
		t.Fatal("should show complete")
	}
}

func TestDeployViewRollbackPrompt(t *testing.T) {
	m := newDeployModel([]string{"frontend", "backend"})
	m, _ = updateDeploy(m, serviceStatusMsg{service: "frontend"})
	m, _ = updateDeploy(m, serviceStatusMsg{service: "backend", err: fmt.Errorf("timeout")})

	view := m.View()
	if !strings.Contains(view, "Deploy failed!") {
		t.Fatal("should show failed")
	}
	if !strings.Contains(view, "FAILED: timeout") {
		t.Fatal("should show error")
	}
	if !strings.Contains(view, "Rollback? [Y/n/s]") {
		t.Fatal("should show rollback prompt")
	}
}
