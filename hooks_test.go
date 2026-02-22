package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFirePostDeployHook(t *testing.T) {
	var received deployEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected application/json, got %s", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode error: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	event := deployEvent{
		Project: "myapp",
		Env:     "staging",
		User:    "testuser",
		Services: []serviceEvent{
			{Name: "backend", OldTag: "old-tag", NewTag: "new-tag", Status: "success"},
		},
		Result:     "success",
		DurationMs: 5000,
		Timestamp:  time.Now(),
	}

	firePostDeployHook(srv.URL, event)

	if received.Project != "myapp" {
		t.Errorf("expected project myapp, got %s", received.Project)
	}
	if received.Env != "staging" {
		t.Errorf("expected env staging, got %s", received.Env)
	}
	if len(received.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(received.Services))
	}
	if received.Services[0].Name != "backend" {
		t.Errorf("expected service backend, got %s", received.Services[0].Name)
	}
}

func TestFirePostDeployHookServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Should not panic or block
	firePostDeployHook(srv.URL, deployEvent{Project: "test"})
}

func TestFirePostDeployHookUnreachable(t *testing.T) {
	// Should not panic or block
	firePostDeployHook("http://127.0.0.1:1", deployEvent{Project: "test"})
}

func TestBuildDeployEvent(t *testing.T) {
	services := []string{"backend", "frontend"}
	tags := map[string]string{"backend": "new-tag", "frontend": "new-tag"}
	previousTags := map[string]string{"backend": "old-tag", "frontend": "old-tag"}
	result := deployResult{
		failed: []string{"frontend"},
		errors: map[string]error{"frontend": errCancelled},
	}

	event := buildDeployEvent("myapp", "prod", services, tags, previousTags, result, 3*time.Second, false)

	if event.Project != "myapp" {
		t.Errorf("expected project myapp, got %s", event.Project)
	}
	if event.Env != "prod" {
		t.Errorf("expected env prod, got %s", event.Env)
	}
	if event.Result != "failure" {
		t.Errorf("expected result failure, got %s", event.Result)
	}
	if event.DurationMs != 3000 {
		t.Errorf("expected 3000ms, got %d", event.DurationMs)
	}
	if event.IsRollback {
		t.Error("expected is_rollback=false")
	}

	if len(event.Services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(event.Services))
	}

	for _, se := range event.Services {
		switch se.Name {
		case "backend":
			if se.Status != "success" {
				t.Errorf("backend: expected status success, got %s", se.Status)
			}
			if se.Error != "" {
				t.Errorf("backend: expected no error, got %s", se.Error)
			}
		case "frontend":
			if se.Status != "failure" {
				t.Errorf("frontend: expected status failure, got %s", se.Status)
			}
			if se.Error == "" {
				t.Error("frontend: expected error message")
			}
		}
	}
}

func TestBuildDeployEventRollback(t *testing.T) {
	event := buildDeployEvent("myapp", "prod", []string{"backend"}, map[string]string{"backend": "old-tag"}, map[string]string{"backend": "new-tag"}, deployResult{}, time.Second, true)

	if !event.IsRollback {
		t.Error("expected is_rollback=true")
	}
	if event.Result != "success" {
		t.Errorf("expected result success, got %s", event.Result)
	}
}
