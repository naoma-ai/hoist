package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateSingle(m singleSelectModel, msg tea.Msg) (singleSelectModel, tea.Cmd) {
	model, cmd := m.Update(msg)
	return model.(singleSelectModel), cmd
}

func TestSingleSelectNavigation(t *testing.T) {
	m := newSingleSelectModel("Pick env", []string{"staging", "production"})

	if m.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.cursor)
	}

	m, _ = updateSingle(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("cursor should be 1, got %d", m.cursor)
	}

	m, _ = updateSingle(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Fatalf("cursor should stay 1, got %d", m.cursor)
	}
}

func TestSingleSelectConfirm(t *testing.T) {
	m := newSingleSelectModel("Pick env", []string{"staging", "production"})

	m, _ = updateSingle(m, tea.KeyMsg{Type: tea.KeyDown})
	m, cmd := updateSingle(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command on confirm")
	}
	if !m.done {
		t.Fatal("expected done to be true")
	}
	if m.items[m.cursor] != "production" {
		t.Fatalf("expected production, got %s", m.items[m.cursor])
	}
}

func TestSingleSelectCancel(t *testing.T) {
	m := newSingleSelectModel("Pick env", []string{"staging"})
	m, cmd := updateSingle(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected quit command on cancel")
	}
	if !m.cancelled {
		t.Fatal("expected cancelled to be true")
	}
}
