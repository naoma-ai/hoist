package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

type deployEvent struct {
	Project    string         `json:"project"`
	Env        string         `json:"env"`
	User       string         `json:"user"`
	Services   []serviceEvent `json:"services"`
	Result     string         `json:"result"`
	IsRollback bool           `json:"is_rollback"`
	DurationMs int64          `json:"duration_ms"`
	Timestamp  time.Time      `json:"timestamp"`
}

type serviceEvent struct {
	Name   string `json:"name"`
	OldTag string `json:"old_tag"`
	NewTag string `json:"new_tag"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func buildDeployEvent(project, env string, services []string, tags, previousTags map[string]string, result deployResult, duration time.Duration, isRollback bool) deployEvent {
	var events []serviceEvent
	for _, svc := range services {
		se := serviceEvent{
			Name:   svc,
			OldTag: previousTags[svc],
			NewTag: tags[svc],
			Status: "success",
		}
		if err, ok := result.errors[svc]; ok {
			se.Status = "failure"
			se.Error = err.Error()
		}
		events = append(events, se)
	}

	overallResult := "success"
	if len(result.failed) > 0 {
		overallResult = "failure"
	}

	return deployEvent{
		Project:    project,
		Env:        env,
		User:       os.Getenv("USER"),
		Services:   events,
		Result:     overallResult,
		IsRollback: isRollback,
		DurationMs: duration.Milliseconds(),
		Timestamp:  time.Now(),
	}
}

func firePostDeployHook(url string, event deployEvent) {
	body, err := json.Marshal(event)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hook: marshal error: %v\n", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "hook: request error: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hook: %v\n", err)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 400 {
		fmt.Fprintf(os.Stderr, "hook: unexpected status %d\n", resp.StatusCode)
	}
}
