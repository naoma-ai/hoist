package main

import (
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	ts := time.Date(2026, 2, 13, 11, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		branch  string
		sha     string
		time    time.Time
		attempt int
	}{
		{
			name:    "simple branch",
			branch:  "main",
			sha:     "abc1234",
			time:    ts,
			attempt: 0,
		},
		{
			name:    "branch with slashes",
			branch:  "feature/add-login",
			sha:     "dee5678",
			time:    ts,
			attempt: 0,
		},
		{
			name:    "attempt 1 no suffix",
			branch:  "main",
			sha:     "abc1234",
			time:    ts,
			attempt: 1,
		},
		{
			name:    "attempt 2 with suffix",
			branch:  "main",
			sha:     "abc1234",
			time:    ts,
			attempt: 2,
		},
		{
			name:    "attempt 3 with suffix",
			branch:  "deploy",
			sha:     "ff00112",
			time:    ts,
			attempt: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generated := generateTag(tt.branch, tt.sha, tt.time, tt.attempt)
			parsed, err := parseTag(generated)
			if err != nil {
				t.Fatalf("parseTag(%q) error: %v", generated, err)
			}
			wantBranch := sanitizeBranch(tt.branch)
			if parsed.Branch != wantBranch {
				t.Errorf("branch = %q, want %q", parsed.Branch, wantBranch)
			}
			wantSHA := tt.sha
			if len(wantSHA) > 7 {
				wantSHA = wantSHA[:7]
			}
			if parsed.SHA != wantSHA {
				t.Errorf("sha = %q, want %q", parsed.SHA, wantSHA)
			}
			if !parsed.Time.Equal(tt.time) {
				t.Errorf("time = %v, want %v", parsed.Time, tt.time)
			}
			// attempt 0 and 1 both produce no suffix, so parsed attempt is 0
			wantAttempt := tt.attempt
			if wantAttempt < 2 {
				wantAttempt = 0
			}
			if parsed.Attempt != wantAttempt {
				t.Errorf("attempt = %d, want %d", parsed.Attempt, wantAttempt)
			}
		})
	}
}

func TestSanitizeBranch(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"slashes to hyphens", "feature/login", "feature-login"},
		{"underscores to hyphens", "my_branch", "my-branch"},
		{"dots preserved", "v1.2.3", "v1.2.3"},
		{"hyphens preserved", "fix-bug", "fix-bug"},
		{"spaces to hyphens", "my branch", "my-branch"},
		{"truncation at 40", "a123456789012345678901234567890123456789extra", "a123456789012345678901234567890123456789"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeBranch(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeBranch(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseTagHyphenatedBranch(t *testing.T) {
	input := "fix-auth-redirect-ee410d3-20260213110000"
	parsed, err := parseTag(input)
	if err != nil {
		t.Fatalf("parseTag(%q) error: %v", input, err)
	}
	if parsed.Branch != "fix-auth-redirect" {
		t.Errorf("branch = %q, want %q", parsed.Branch, "fix-auth-redirect")
	}
	if parsed.SHA != "ee410d3" {
		t.Errorf("sha = %q, want %q", parsed.SHA, "ee410d3")
	}
}

func TestParseTagWithAttempt(t *testing.T) {
	input := "add-client-tools-a3f9c21-20260213143022-2"
	parsed, err := parseTag(input)
	if err != nil {
		t.Fatalf("parseTag(%q) error: %v", input, err)
	}
	if parsed.Branch != "add-client-tools" {
		t.Errorf("branch = %q, want %q", parsed.Branch, "add-client-tools")
	}
	if parsed.SHA != "a3f9c21" {
		t.Errorf("sha = %q, want %q", parsed.SHA, "a3f9c21")
	}
	if parsed.Attempt != 2 {
		t.Errorf("attempt = %d, want %d", parsed.Attempt, 2)
	}
}

func TestParseTagErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"empty string", ""},
		{"too short", "a-b"},
		{"bad SHA not hex", "main-ghijklm-20260213110000"},
		{"bad SHA wrong length", "main-abc12-20260213110000"},
		{"bad timestamp", "main-abc1234-notadate"},
		{"empty branch", "abc1234-20260213110000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseTag(tt.input)
			if err == nil {
				t.Errorf("parseTag(%q) expected error, got nil", tt.input)
			}
		})
	}
}
