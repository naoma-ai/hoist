package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type tag struct {
	Branch  string
	SHA     string
	Time    time.Time
	Attempt int
}

var sanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9.\-]`)

func sanitizeBranch(s string) string {
	s = sanitizeRe.ReplaceAllString(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func generateTag(branch, sha string, ts time.Time, attempt int) string {
	b := sanitizeBranch(branch)
	if len(sha) > 7 {
		sha = sha[:7]
	}
	stamp := ts.UTC().Format("20060102150405")
	t := fmt.Sprintf("%s-%s-%s", b, sha, stamp)
	if attempt >= 2 {
		t = fmt.Sprintf("%s-%d", t, attempt)
	}
	return t
}

var (
	shaRe       = regexp.MustCompile(`^[0-9a-f]{7}$`)
	timestampRe = regexp.MustCompile(`^\d{14}$`)
	digitRe     = regexp.MustCompile(`^\d+$`)
)

func parseTag(s string) (tag, error) {
	if s == "" {
		return tag{}, fmt.Errorf("empty tag string")
	}

	parts := strings.Split(s, "-")
	if len(parts) < 3 {
		return tag{}, fmt.Errorf("tag too short: %q", s)
	}

	attempt := 0

	// Check if last segment is a numeric attempt (not a 14-digit timestamp)
	last := parts[len(parts)-1]
	if digitRe.MatchString(last) && len(last) != 14 {
		var err error
		attempt, err = strconv.Atoi(last)
		if err != nil {
			return tag{}, fmt.Errorf("invalid attempt: %q", last)
		}
		parts = parts[:len(parts)-1]
	}

	if len(parts) < 3 {
		return tag{}, fmt.Errorf("tag too short after removing attempt: %q", s)
	}

	// Last segment must be 14-digit timestamp
	tsStr := parts[len(parts)-1]
	if !timestampRe.MatchString(tsStr) {
		return tag{}, fmt.Errorf("invalid timestamp: %q", tsStr)
	}
	ts, err := time.Parse("20060102150405", tsStr)
	if err != nil {
		return tag{}, fmt.Errorf("invalid timestamp: %q: %w", tsStr, err)
	}

	// Second-to-last must be 7 hex chars
	shaStr := parts[len(parts)-2]
	if !shaRe.MatchString(shaStr) {
		return tag{}, fmt.Errorf("invalid SHA: %q", shaStr)
	}

	// Everything before is the branch
	branchParts := parts[:len(parts)-2]
	if len(branchParts) == 0 {
		return tag{}, fmt.Errorf("empty branch in tag: %q", s)
	}
	branch := strings.Join(branchParts, "-")

	return tag{
		Branch:  branch,
		SHA:     shaStr,
		Time:    ts.UTC(),
		Attempt: attempt,
	}, nil
}
