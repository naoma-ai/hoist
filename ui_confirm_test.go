package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func updateConfirm(m confirmModel, msg tea.Msg) (confirmModel, tea.Cmd) {
	model, cmd := m.Update(msg)
	return model.(confirmModel), cmd
}

func TestConfirmAccept(t *testing.T) {
	m := newConfirmModel("staging", []serviceChange{
		{service: "frontend", oldTag: "old-tag-1234567-20250101000000", newTag: "new-tag-abc1234-20250102000000"},
	})

	m, cmd := updateConfirm(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if cmd == nil {
		t.Fatal("expected quit command on y")
	}
	if m.result != confirmAccepted {
		t.Fatal("expected accepted")
	}
}

func TestConfirmRejectN(t *testing.T) {
	m := newConfirmModel("staging", nil)

	m, cmd := updateConfirm(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd == nil {
		t.Fatal("expected quit command on n")
	}
	if m.result != confirmRejected {
		t.Fatal("expected rejected")
	}
}

func TestConfirmDefaultYesOnEnter(t *testing.T) {
	m := newConfirmModel("staging", nil)

	m, cmd := updateConfirm(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command on enter")
	}
	if m.result != confirmAccepted {
		t.Fatal("expected accepted (default Yes)")
	}
}

func TestConfirmViewFirstDeploy(t *testing.T) {
	m := newConfirmModel("staging", []serviceChange{
		{service: "backend", oldTag: "", newTag: "main-abc1234-20250101000000"},
	})

	view := m.View()
	if !strings.Contains(view, "(first deploy)") {
		t.Fatal("should show (first deploy) for empty old tag")
	}
	if !strings.Contains(view, "Deploy to staging") {
		t.Fatal("should show env in header")
	}
}

func TestConfirmViewNoChange(t *testing.T) {
	tag := "main-abc1234-20250101000000"
	m := newConfirmModel("production", []serviceChange{
		{service: "frontend", oldTag: tag, newTag: tag},
	})

	view := m.View()
	if !strings.Contains(view, "(no change)") {
		t.Fatal("should show (no change) when old == new")
	}
}

func TestConfirmViewNormalChange(t *testing.T) {
	m := newConfirmModel("staging", []serviceChange{
		{service: "frontend", oldTag: "old-1234567-20250101000000", newTag: "new-abc1234-20250102000000"},
		{service: "backend", oldTag: "", newTag: "new-abc1234-20250102000000"},
	})

	view := m.View()
	if !strings.Contains(view, "frontend") {
		t.Fatal("should show frontend")
	}
	if !strings.Contains(view, "backend") {
		t.Fatal("should show backend")
	}
	if !strings.Contains(view, "Proceed? [Y/n]") {
		t.Fatal("should show prompt")
	}
}
