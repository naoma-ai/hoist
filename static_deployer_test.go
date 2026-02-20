package main

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type stubS3Deploy struct {
	mu         sync.Mutex
	listPages  []s3.ListObjectsV2Output
	copyInputs []s3.CopyObjectInput
	putInputs  []s3.PutObjectInput
	listErr    error
	copyErr    error
	putErr     error
}

func (s *stubS3Deploy) ListObjectsV2(_ context.Context, _ *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	if len(s.listPages) == 0 {
		return &s3.ListObjectsV2Output{}, nil
	}
	s.mu.Lock()
	page := s.listPages[0]
	s.listPages = s.listPages[1:]
	s.mu.Unlock()
	return &page, nil
}

func (s *stubS3Deploy) CopyObject(_ context.Context, params *s3.CopyObjectInput, _ ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.copyInputs = append(s.copyInputs, *params)
	if s.copyErr != nil {
		return nil, s.copyErr
	}
	return &s3.CopyObjectOutput{}, nil
}

func (s *stubS3Deploy) PutObject(_ context.Context, params *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putInputs = append(s.putInputs, *params)
	if s.putErr != nil {
		return nil, s.putErr
	}
	return &s3.PutObjectOutput{}, nil
}

type stubCFInvalidate struct {
	mu    sync.Mutex
	input *cloudfront.CreateInvalidationInput
	err   error
}

func (s *stubCFInvalidate) CreateInvalidation(_ context.Context, params *cloudfront.CreateInvalidationInput, _ ...func(*cloudfront.Options)) (*cloudfront.CreateInvalidationOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.input = params
	if s.err != nil {
		return nil, s.err
	}
	return &cloudfront.CreateInvalidationOutput{}, nil
}

func s3Objects(keys ...string) []s3types.Object {
	var objs []s3types.Object
	for _, k := range keys {
		k := k
		objs = append(objs, s3types.Object{Key: &k})
	}
	return objs
}

func TestStaticDeployHappyPath(t *testing.T) {
	cfg := testConfig()
	stub := &stubS3Deploy{
		listPages: []s3.ListObjectsV2Output{
			{Contents: s3Objects(
				"builds/main-abc1234-20250101000000/index.html",
				"builds/main-abc1234-20250101000000/app.js",
			)},
		},
	}
	cf := &stubCFInvalidate{}

	d := &staticDeployer{cfg: cfg, s3: stub, cloudfront: cf}

	err := d.deploy(context.Background(), "frontend", "staging", "main-abc1234-20250101000000", "main-old1234-20241231000000", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify previous-tag and current-tag markers written.
	if len(stub.putInputs) != 2 {
		t.Fatalf("expected 2 PutObject calls, got %d", len(stub.putInputs))
	}
	if *stub.putInputs[0].Key != "previous-tag" {
		t.Errorf("put[0].Key = %q, want %q", *stub.putInputs[0].Key, "previous-tag")
	}
	if *stub.putInputs[0].Bucket != "frontend-staging" {
		t.Errorf("put[0].Bucket = %q, want %q", *stub.putInputs[0].Bucket, "frontend-staging")
	}
	if *stub.putInputs[1].Key != "current-tag" {
		t.Errorf("put[1].Key = %q, want %q", *stub.putInputs[1].Key, "current-tag")
	}

	// Verify copies.
	if len(stub.copyInputs) != 2 {
		t.Fatalf("expected 2 CopyObject calls, got %d", len(stub.copyInputs))
	}
	var dstKeys []string
	for _, c := range stub.copyInputs {
		dstKeys = append(dstKeys, *c.Key)
	}
	sort.Strings(dstKeys)
	if dstKeys[0] != "current/app.js" || dstKeys[1] != "current/index.html" {
		t.Errorf("copy destinations = %v, want [current/app.js current/index.html]", dstKeys)
	}

	// Verify CopySource format.
	for _, c := range stub.copyInputs {
		if !strings.HasPrefix(*c.CopySource, "frontend-staging/builds/main-abc1234-20250101000000/") {
			t.Errorf("CopySource = %q, want prefix %q", *c.CopySource, "frontend-staging/builds/main-abc1234-20250101000000/")
		}
	}

	// Verify CloudFront invalidation.
	if cf.input == nil {
		t.Fatal("expected CloudFront invalidation")
	}
	if *cf.input.DistributionId != "E1234567890" {
		t.Errorf("DistributionId = %q, want %q", *cf.input.DistributionId, "E1234567890")
	}
	if len(cf.input.InvalidationBatch.Paths.Items) != 1 || cf.input.InvalidationBatch.Paths.Items[0] != "/*" {
		t.Errorf("invalidation paths = %v, want [/*]", cf.input.InvalidationBatch.Paths.Items)
	}
}

func TestStaticDeployNoOldTag(t *testing.T) {
	cfg := testConfig()
	stub := &stubS3Deploy{
		listPages: []s3.ListObjectsV2Output{
			{Contents: s3Objects("builds/main-abc1234-20250101000000/index.html")},
		},
	}
	cf := &stubCFInvalidate{}

	d := &staticDeployer{cfg: cfg, s3: stub, cloudfront: cf}

	err := d.deploy(context.Background(), "frontend", "staging", "main-abc1234-20250101000000", "", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only current-tag should be written (no previous-tag).
	if len(stub.putInputs) != 1 {
		t.Fatalf("expected 1 PutObject call, got %d", len(stub.putInputs))
	}
	if *stub.putInputs[0].Key != "current-tag" {
		t.Errorf("put[0].Key = %q, want %q", *stub.putInputs[0].Key, "current-tag")
	}
}

func TestStaticDeployBuildNotFound(t *testing.T) {
	cfg := testConfig()
	stub := &stubS3Deploy{
		listPages: []s3.ListObjectsV2Output{
			{Contents: []s3types.Object{}},
		},
	}
	cf := &stubCFInvalidate{}

	d := &staticDeployer{cfg: cfg, s3: stub, cloudfront: cf}

	err := d.deploy(context.Background(), "frontend", "staging", "main-abc1234-20250101000000", "", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "build not found") {
		t.Errorf("expected 'build not found' error, got: %v", err)
	}

	// No markers should be written, no invalidation.
	if len(stub.putInputs) != 0 {
		t.Errorf("expected 0 PutObject calls, got %d", len(stub.putInputs))
	}
	if cf.input != nil {
		t.Error("expected no CloudFront invalidation")
	}
}

func TestStaticDeployListError(t *testing.T) {
	cfg := testConfig()
	stub := &stubS3Deploy{listErr: fmt.Errorf("access denied")}
	cf := &stubCFInvalidate{}

	d := &staticDeployer{cfg: cfg, s3: stub, cloudfront: cf}

	err := d.deploy(context.Background(), "frontend", "staging", "main-abc1234-20250101000000", "", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "listing build objects") {
		t.Errorf("expected 'listing build objects' error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "access denied") {
		t.Errorf("expected underlying error, got: %v", err)
	}
}

func TestStaticDeployCopyError(t *testing.T) {
	cfg := testConfig()
	stub := &stubS3Deploy{
		listPages: []s3.ListObjectsV2Output{
			{Contents: s3Objects(
				"builds/main-abc1234-20250101000000/index.html",
				"builds/main-abc1234-20250101000000/app.js",
			)},
		},
		copyErr: fmt.Errorf("copy failed"),
	}
	cf := &stubCFInvalidate{}

	d := &staticDeployer{cfg: cfg, s3: stub, cloudfront: cf}

	err := d.deploy(context.Background(), "frontend", "staging", "main-abc1234-20250101000000", "", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "copying") {
		t.Errorf("expected 'copying' error, got: %v", err)
	}

	// current-tag should NOT have been written.
	for _, p := range stub.putInputs {
		if *p.Key == "current-tag" {
			t.Error("current-tag should not be written when copy fails")
		}
	}
	// No CloudFront invalidation.
	if cf.input != nil {
		t.Error("expected no CloudFront invalidation on copy failure")
	}
}

func TestStaticDeployInvalidationError(t *testing.T) {
	cfg := testConfig()
	stub := &stubS3Deploy{
		listPages: []s3.ListObjectsV2Output{
			{Contents: s3Objects("builds/main-abc1234-20250101000000/index.html")},
		},
	}
	cf := &stubCFInvalidate{err: fmt.Errorf("throttled")}

	d := &staticDeployer{cfg: cfg, s3: stub, cloudfront: cf}

	err := d.deploy(context.Background(), "frontend", "staging", "main-abc1234-20250101000000", "", nopLogf)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "invalidating CloudFront") {
		t.Errorf("expected 'invalidating CloudFront' error, got: %v", err)
	}

	// current-tag should have been written (deploy succeeded on S3).
	found := false
	for _, p := range stub.putInputs {
		if *p.Key == "current-tag" {
			found = true
		}
	}
	if !found {
		t.Error("current-tag should be written even when invalidation fails")
	}
}

func TestStaticDeployPagination(t *testing.T) {
	cfg := testConfig()
	stub := &stubS3Deploy{
		listPages: []s3.ListObjectsV2Output{
			{
				Contents:              s3Objects("builds/main-abc1234-20250101000000/page1.html"),
				IsTruncated:           aws.Bool(true),
				NextContinuationToken: aws.String("page2"),
			},
			{
				Contents: s3Objects("builds/main-abc1234-20250101000000/page2.html"),
			},
		},
	}
	cf := &stubCFInvalidate{}

	d := &staticDeployer{cfg: cfg, s3: stub, cloudfront: cf}

	err := d.deploy(context.Background(), "frontend", "staging", "main-abc1234-20250101000000", "", nopLogf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both objects from both pages should be copied.
	if len(stub.copyInputs) != 2 {
		t.Fatalf("expected 2 CopyObject calls, got %d", len(stub.copyInputs))
	}
	var dstKeys []string
	for _, c := range stub.copyInputs {
		dstKeys = append(dstKeys, *c.Key)
	}
	sort.Strings(dstKeys)
	if dstKeys[0] != "current/page1.html" || dstKeys[1] != "current/page2.html" {
		t.Errorf("copy destinations = %v, want [current/page1.html current/page2.html]", dstKeys)
	}
}

func TestStaticDeployLogOutput(t *testing.T) {
	cfg := testConfig()
	stub := &stubS3Deploy{
		listPages: []s3.ListObjectsV2Output{
			{Contents: s3Objects(
				"builds/main-abc1234-20250101000000/index.html",
				"builds/main-abc1234-20250101000000/app.js",
			)},
		},
	}
	cf := &stubCFInvalidate{}
	d := &staticDeployer{cfg: cfg, s3: stub, cloudfront: cf}

	var buf bytes.Buffer
	var mu sync.Mutex
	logf := newServiceLogf(&buf, &mu, "frontend", 8)
	err := d.deploy(context.Background(), "frontend", "staging", "main-abc1234-20250101000000", "main-old1234-20241231000000", logf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	expected := []string{
		"writing previous-tag marker",
		"listing build objects",
		"found 2 objects",
		"copying 2 objects",
		"objects copied",
		"writing current-tag marker",
		"invalidating CloudFront",
		"CloudFront invalidation created",
	}
	for _, e := range expected {
		if !strings.Contains(output, e) {
			t.Errorf("expected %q in output, got:\n%s", e, output)
		}
	}
}
