package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func updateBuilds(m buildPickerModel, msg tea.Msg) (buildPickerModel, tea.Cmd) {
	model, cmd := m.Update(msg)
	return model.(buildPickerModel), cmd
}

func sampleBuilds(n int) []build {
	var builds []build
	for i := 0; i < n; i++ {
		t := tag{
			Branch: "main",
			SHA:    fmt.Sprintf("%07d", i),
			Time:   time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(-i) * time.Hour),
		}
		builds = append(builds, buildFromTag(t))
	}
	return builds
}

func TestBuildPickerInitLoading(t *testing.T) {
	bp := &mockBuildsProvider{builds: sampleBuilds(5)}
	m := newBuildPickerModel(bp, "staging", nil)

	if !m.loading {
		t.Fatal("should start in loading state")
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should return a command")
	}

	msg := cmd()
	loaded, ok := msg.(buildsLoadedMsg)
	if !ok {
		t.Fatalf("expected buildsLoadedMsg, got %T", msg)
	}
	if len(loaded.builds) != 5 {
		t.Fatalf("expected 5 builds, got %d", len(loaded.builds))
	}
}

func TestBuildPickerPagination(t *testing.T) {
	bp := &mockBuildsProvider{builds: sampleBuilds(25)}
	m := newBuildPickerModel(bp, "staging", nil)

	// Load first page
	cmd := m.Init()
	msg := cmd()
	m, _ = updateBuilds(m, msg)

	if len(m.builds) != 20 {
		t.Fatalf("expected 20 builds, got %d", len(m.builds))
	}
	if !m.hasMore {
		t.Fatal("should have more builds")
	}

	// Navigate to "load more" row
	for i := 0; i < 20; i++ {
		m, _ = updateBuilds(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.cursor != 20 {
		t.Fatalf("cursor should be at load more (20), got %d", m.cursor)
	}

	// Press enter to load more
	m, cmd = updateBuilds(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected load more command")
	}
	msg = cmd()
	m, _ = updateBuilds(m, msg)

	if len(m.builds) != 25 {
		t.Fatalf("expected 25 builds after load more, got %d", len(m.builds))
	}
	if m.hasMore {
		t.Fatal("should not have more builds")
	}
}

func TestBuildPickerLiveMarker(t *testing.T) {
	builds := sampleBuilds(3)
	liveTag := builds[1].Tag
	bp := &mockBuildsProvider{builds: builds}
	fetchHistory := func(_ context.Context) (map[string]bool, map[string]string, error) {
		return map[string]bool{liveTag: true}, map[string]string{"frontend": liveTag}, nil
	}
	m := newBuildPickerModel(bp, "staging", fetchHistory)

	// Init returns a batch; simulate both commands resolving.
	m, _ = updateBuilds(m, buildsLoadedMsg{builds: builds, hasMore: false})
	m, _ = updateBuilds(m, historyLoadedMsg{
		liveTags:     map[string]bool{liveTag: true},
		previousTags: map[string]string{"frontend": liveTag},
	})

	view := m.View()
	if !strings.Contains(view, "[LIVE]") {
		t.Fatal("view should contain [LIVE] marker")
	}
	if !strings.Contains(view, "Currently live in staging") {
		t.Fatal("view should show currently live header")
	}
}

func TestBuildPickerSelection(t *testing.T) {
	builds := sampleBuilds(3)
	bp := &mockBuildsProvider{builds: builds}
	m := newBuildPickerModel(bp, "staging", nil)

	cmd := m.Init()
	msg := cmd()
	m, _ = updateBuilds(m, msg)

	// Select first build (cursor starts at 0)
	m, cmd = updateBuilds(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected quit command on selection")
	}
	if !m.done {
		t.Fatal("expected done to be true")
	}
	if m.builds[m.cursor].Tag != builds[0].Tag {
		t.Fatalf("expected tag %s, got %s", builds[0].Tag, m.builds[m.cursor].Tag)
	}
}

func TestBuildPickerPreSelection(t *testing.T) {
	bp := &mockBuildsProvider{builds: sampleBuilds(5)}
	m := newBuildPickerModel(bp, "staging", nil)

	if m.cursor != 0 {
		t.Fatalf("cursor should start at 0, got %d", m.cursor)
	}
}

func TestBuildPickerEmptyResults(t *testing.T) {
	bp := &mockBuildsProvider{builds: nil}
	m := newBuildPickerModel(bp, "staging", nil)

	cmd := m.Init()
	msg := cmd()
	m, _ = updateBuilds(m, msg)

	if len(m.builds) != 0 {
		t.Fatalf("expected 0 builds, got %d", len(m.builds))
	}
	if m.hasMore {
		t.Fatal("should not have more")
	}
}

func TestBuildPickerCancel(t *testing.T) {
	bp := &mockBuildsProvider{builds: sampleBuilds(3)}
	m := newBuildPickerModel(bp, "staging", nil)

	cmd := m.Init()
	msg := cmd()
	m, _ = updateBuilds(m, msg)

	m, cmd = updateBuilds(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("expected quit command on cancel")
	}
	if !m.cancelled {
		t.Fatal("expected cancelled to be true")
	}
}

func TestBuildPickerHistoryError(t *testing.T) {
	bp := &mockBuildsProvider{builds: sampleBuilds(3)}
	fetchHistory := func(_ context.Context) (map[string]bool, map[string]string, error) {
		return nil, nil, fmt.Errorf("SSH connection refused")
	}
	m := newBuildPickerModel(bp, "staging", fetchHistory)

	// Simulate history error arriving.
	m, _ = updateBuilds(m, historyErrorMsg{err: fmt.Errorf("SSH connection refused")})

	if m.historyErr == nil {
		t.Fatal("expected historyErr to be set")
	}
	if !strings.Contains(m.historyErr.Error(), "SSH connection refused") {
		t.Fatalf("expected SSH error, got: %v", m.historyErr)
	}
}

func TestBuildPickerHistoryAndBuildsLoad(t *testing.T) {
	builds := sampleBuilds(3)
	liveTag := builds[0].Tag
	bp := &mockBuildsProvider{builds: builds}
	fetchHistory := func(_ context.Context) (map[string]bool, map[string]string, error) {
		return map[string]bool{liveTag: true}, map[string]string{"backend": liveTag}, nil
	}
	m := newBuildPickerModel(bp, "staging", fetchHistory)

	// Builds arrive first — view should still show loading (waiting for history).
	m, _ = updateBuilds(m, buildsLoadedMsg{builds: builds, hasMore: false})
	if m.loading {
		t.Fatal("builds loading flag should be false after builds arrive")
	}
	if !m.historyLoading {
		t.Fatal("history should still be loading")
	}
	view := m.View()
	if !strings.Contains(view, "Loading builds...") {
		t.Fatal("should show loading screen while history is pending")
	}

	// History arrives — now show the full list with LIVE markers.
	m, _ = updateBuilds(m, historyLoadedMsg{
		liveTags:     map[string]bool{liveTag: true},
		previousTags: map[string]string{"backend": liveTag},
	})
	if m.historyLoading {
		t.Fatal("history should not be loading after arrival")
	}
	view = m.View()
	if !strings.Contains(view, "[LIVE]") {
		t.Fatal("should show LIVE marker after history loads")
	}
	if !strings.Contains(view, "Currently live in staging") {
		t.Fatal("should show currently live header")
	}
	if m.previousTags["backend"] != liveTag {
		t.Fatalf("expected previousTags[backend] = %s, got %s", liveTag, m.previousTags["backend"])
	}
}
