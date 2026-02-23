package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateMulti(m multiSelectModel, msg tea.Msg) (multiSelectModel, tea.Cmd) {
	model, cmd := m.Update(msg)
	return model.(multiSelectModel), cmd
}

func TestMultiSelectToggle(t *testing.T) {
	m := newMultiSelectModel("Pick services", []string{"frontend", "backend", "worker"})

	for i := range m.items {
		if m.selected[i] {
			t.Fatalf("item %d should start unchecked", i)
		}
	}

	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if !m.selected[0] {
		t.Fatal("item 0 should be selected after toggle")
	}

	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if m.selected[0] {
		t.Fatal("item 0 should be deselected after second toggle")
	}
}

func TestMultiSelectCursorBounds(t *testing.T) {
	m := newMultiSelectModel("Pick", []string{"a", "b", "c"})

	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Fatalf("cursor should be 0, got %d", m.cursor)
	}

	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Fatalf("cursor should be 2, got %d", m.cursor)
	}

	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Fatalf("cursor should stay 2, got %d", m.cursor)
	}
}

func TestMultiSelectConfirm(t *testing.T) {
	m := newMultiSelectModel("Pick", []string{"a", "b", "c"})

	// Select "b" and "c" (skip "a")
	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateMulti(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})

	m, cmd := updateMulti(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command on confirm")
	}
	if !m.done {
		t.Fatal("expected done to be true")
	}
	chosen := m.chosen()
	if len(chosen) != 2 || chosen[0] != "b" || chosen[1] != "c" {
		t.Fatalf("expected [b c], got %v", chosen)
	}
}

func TestMultiSelectEmptyGuard(t *testing.T) {
	m := newMultiSelectModel("Pick", []string{"a"})

	// All items start unchecked, so enter should be blocked immediately.
	m, cmd := updateMulti(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("confirm with no selection should return nil cmd")
	}
	if m.done {
		t.Fatal("done should be false")
	}
}

func TestMultiSelectCancel(t *testing.T) {
	m := newMultiSelectModel("Pick", []string{"a", "b"})
	m, cmd := updateMulti(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected quit command on cancel")
	}
	if !m.cancelled {
		t.Fatal("expected cancelled to be true")
	}
}
