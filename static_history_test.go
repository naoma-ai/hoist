package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type stubS3Object struct {
	body         string
	lastModified time.Time
}

type stubS3 struct {
	objects map[string]stubS3Object // keyed by "bucket/key"
	err     error                   // global error to return
}

func (s *stubS3) GetObject(_ context.Context, params *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if s.err != nil {
		return nil, s.err
	}

	key := *params.Bucket + "/" + *params.Key
	obj, ok := s.objects[key]
	if !ok {
		return nil, &types.NoSuchKey{}
	}

	return &s3.GetObjectOutput{
		Body:         io.NopCloser(strings.NewReader(obj.body)),
		LastModified: &obj.lastModified,
	}, nil
}

func TestStaticHistoryCurrent(t *testing.T) {
	cfg := testConfig()
	lastMod := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := lastMod.Add(3 * time.Hour)

	p := &staticHistoryProvider{
		cfg: cfg,
		s3: &stubS3{
			objects: map[string]stubS3Object{
				"frontend-staging/current-tag": {body: "main-abc1234-20250101000000", lastModified: lastMod},
			},
		},
		now: func() time.Time { return now },
	}

	d, err := p.current(context.Background(), "frontend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "main-abc1234-20250101000000" {
		t.Errorf("tag = %q, want %q", d.Tag, "main-abc1234-20250101000000")
	}
	if d.Uptime != 3*time.Hour {
		t.Errorf("uptime = %v, want %v", d.Uptime, 3*time.Hour)
	}
	if d.Service != "frontend" {
		t.Errorf("service = %q, want %q", d.Service, "frontend")
	}
	if d.Env != "staging" {
		t.Errorf("env = %q, want %q", d.Env, "staging")
	}
}

func TestStaticHistoryCurrentNoMarker(t *testing.T) {
	cfg := testConfig()

	p := &staticHistoryProvider{
		cfg: cfg,
		s3:  &stubS3{objects: map[string]stubS3Object{}},
	}

	d, err := p.current(context.Background(), "frontend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag, got %q", d.Tag)
	}
}

func TestStaticHistoryCurrentS3Error(t *testing.T) {
	cfg := testConfig()

	p := &staticHistoryProvider{
		cfg: cfg,
		s3:  &stubS3{err: fmt.Errorf("access denied")},
	}

	_, err := p.current(context.Background(), "frontend", "staging")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStaticHistoryPrevious(t *testing.T) {
	cfg := testConfig()
	lastMod := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	now := lastMod.Add(48 * time.Hour)

	p := &staticHistoryProvider{
		cfg: cfg,
		s3: &stubS3{
			objects: map[string]stubS3Object{
				"frontend-staging/previous-tag": {body: "main-old1234-20241231000000", lastModified: lastMod},
			},
		},
		now: func() time.Time { return now },
	}

	d, err := p.previous(context.Background(), "frontend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "main-old1234-20241231000000" {
		t.Errorf("tag = %q, want %q", d.Tag, "main-old1234-20241231000000")
	}
	if d.Uptime != 48*time.Hour {
		t.Errorf("uptime = %v, want %v", d.Uptime, 48*time.Hour)
	}
}

func TestStaticHistoryPreviousNoMarker(t *testing.T) {
	cfg := testConfig()

	p := &staticHistoryProvider{
		cfg: cfg,
		s3:  &stubS3{objects: map[string]stubS3Object{}},
	}

	d, err := p.previous(context.Background(), "frontend", "staging")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Tag != "" {
		t.Errorf("expected empty tag, got %q", d.Tag)
	}
}
