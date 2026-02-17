package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type stubS3List struct {
	pages []s3.ListObjectsV2Output
	err   error
}

func (s *stubS3List) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.pages) == 0 {
		return &s3.ListObjectsV2Output{}, nil
	}
	page := s.pages[0]
	s.pages = s.pages[1:]
	return &page, nil
}

func prefixes(tags ...string) []types.CommonPrefix {
	var cps []types.CommonPrefix
	for _, t := range tags {
		p := "builds/" + t + "/"
		cps = append(cps, types.CommonPrefix{Prefix: &p})
	}
	return cps
}

func TestStaticBuildsFiltering(t *testing.T) {
	stub := &stubS3List{
		pages: []s3.ListObjectsV2Output{
			{
				CommonPrefixes: prefixes(
					"main-abc1234-20250101100000",
					"cache",
					"latest",
					"not-a-valid-tag",
					"feat-xyz-def5678-20250101090000",
				),
			},
		},
	}

	p := &staticBuildsProvider{s3: stub, bucket: "test-bucket"}
	builds, err := p.listBuilds(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(builds) != 2 {
		t.Fatalf("expected 2 builds, got %d", len(builds))
	}
}

func TestStaticBuildsSorting(t *testing.T) {
	stub := &stubS3List{
		pages: []s3.ListObjectsV2Output{
			{
				CommonPrefixes: prefixes(
					"main-abc1234-20250101080000",
					"main-def5678-20250101100000",
					"main-aae9012-20250101090000",
				),
			},
		},
	}

	p := &staticBuildsProvider{s3: stub, bucket: "test-bucket"}
	builds, err := p.listBuilds(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(builds) != 3 {
		t.Fatalf("expected 3 builds, got %d", len(builds))
	}
	if builds[0].SHA != "def5678" {
		t.Errorf("builds[0].SHA = %q, want %q", builds[0].SHA, "def5678")
	}
	if builds[1].SHA != "aae9012" {
		t.Errorf("builds[1].SHA = %q, want %q", builds[1].SHA, "aae9012")
	}
	if builds[2].SHA != "abc1234" {
		t.Errorf("builds[2].SHA = %q, want %q", builds[2].SHA, "abc1234")
	}
}

func TestStaticBuildsOffsetLimit(t *testing.T) {
	stub := &stubS3List{
		pages: []s3.ListObjectsV2Output{
			{
				CommonPrefixes: prefixes(
					"main-aaa1111-20250101050000",
					"main-bbb2222-20250101040000",
					"main-ccc3333-20250101030000",
					"main-ddd4444-20250101020000",
					"main-eee5555-20250101010000",
				),
			},
		},
	}

	p := &staticBuildsProvider{s3: stub, bucket: "test-bucket"}
	builds, err := p.listBuilds(context.Background(), 2, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(builds) != 2 {
		t.Fatalf("expected 2 builds, got %d", len(builds))
	}
	// After sorting descending: aaa(05), bbb(04), ccc(03), ddd(02), eee(01)
	// offset=1 skips aaa, limit=2 gives bbb and ccc
	if builds[0].SHA != "bbb2222" {
		t.Errorf("builds[0].SHA = %q, want %q", builds[0].SHA, "bbb2222")
	}
	if builds[1].SHA != "ccc3333" {
		t.Errorf("builds[1].SHA = %q, want %q", builds[1].SHA, "ccc3333")
	}
}

func TestStaticBuildsOffsetPastEnd(t *testing.T) {
	stub := &stubS3List{
		pages: []s3.ListObjectsV2Output{
			{
				CommonPrefixes: prefixes("main-abc1234-20250101100000"),
			},
		},
	}

	p := &staticBuildsProvider{s3: stub, bucket: "test-bucket"}
	builds, err := p.listBuilds(context.Background(), 10, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if builds != nil {
		t.Errorf("expected nil builds for offset past end, got %d", len(builds))
	}
}

func TestStaticBuildsPagination(t *testing.T) {
	stub := &stubS3List{
		pages: []s3.ListObjectsV2Output{
			{
				CommonPrefixes: prefixes("main-abc1234-20250101100000"),
				IsTruncated:    aws.Bool(true),
				NextContinuationToken: aws.String("page2"),
			},
			{
				CommonPrefixes: prefixes("main-def5678-20250101090000"),
			},
		},
	}

	p := &staticBuildsProvider{s3: stub, bucket: "test-bucket"}
	builds, err := p.listBuilds(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(builds) != 2 {
		t.Fatalf("expected 2 builds from 2 pages, got %d", len(builds))
	}
}

func TestStaticBuildsS3Error(t *testing.T) {
	stub := &stubS3List{err: fmt.Errorf("access denied")}
	p := &staticBuildsProvider{s3: stub, bucket: "test-bucket"}

	_, err := p.listBuilds(context.Background(), 10, 0)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStaticBuildsEmpty(t *testing.T) {
	stub := &stubS3List{
		pages: []s3.ListObjectsV2Output{
			{CommonPrefixes: []types.CommonPrefix{}},
		},
	}

	p := &staticBuildsProvider{s3: stub, bucket: "test-bucket"}
	builds, err := p.listBuilds(context.Background(), 10, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if builds != nil {
		t.Errorf("expected nil builds, got %d", len(builds))
	}
}
