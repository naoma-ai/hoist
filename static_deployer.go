package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3DeployAPI interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	CopyObject(ctx context.Context, params *s3.CopyObjectInput, optFns ...func(*s3.Options)) (*s3.CopyObjectOutput, error)
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

type cfInvalidateAPI interface {
	CreateInvalidation(ctx context.Context, params *cloudfront.CreateInvalidationInput, optFns ...func(*cloudfront.Options)) (*cloudfront.CreateInvalidationOutput, error)
}

type staticDeployer struct {
	cfg        config
	s3         s3DeployAPI
	cloudfront cfInvalidateAPI
}

func (d *staticDeployer) deploy(ctx context.Context, service, env, tag, oldTag string, logf func(string, ...any)) error {
	ec := d.cfg.Services[service].Env[env]
	bucket := ec.Bucket
	distID := ec.CloudFront

	// Write previous-tag marker.
	if oldTag != "" {
		logf("writing previous-tag marker (%s) to s3://%s/previous-tag", oldTag, bucket)
		if err := d.putMarker(ctx, bucket, "previous-tag", oldTag); err != nil {
			return fmt.Errorf("writing previous-tag marker: %w", err)
		}
	}

	// List build objects.
	logf("listing build objects in s3://%s/builds/%s/", bucket, tag)
	keys, err := d.listBuildObjects(ctx, bucket, tag)
	if err != nil {
		return fmt.Errorf("listing build objects in s3://%s/builds/%s/: %w", bucket, tag, err)
	}
	if len(keys) == 0 {
		return fmt.Errorf("build not found: s3://%s/builds/%s/", bucket, tag)
	}
	logf("found %d objects", len(keys))

	// Copy build objects to current/.
	buildPrefix := "builds/" + tag + "/"
	logf("copying %d objects from builds/%s/ to current/", len(keys), tag)
	if err := d.copyObjects(ctx, bucket, buildPrefix, "current/", keys); err != nil {
		return err
	}
	logf("objects copied")

	// Write current-tag marker.
	logf("writing current-tag marker (%s) to s3://%s/current-tag", tag, bucket)
	if err := d.putMarker(ctx, bucket, "current-tag", tag); err != nil {
		return fmt.Errorf("writing current-tag marker: %w", err)
	}

	// Invalidate CloudFront.
	logf("invalidating CloudFront distribution %s", distID)
	callerRef := fmt.Sprintf("hoist-%s-%d", tag, time.Now().UnixNano())
	path := "/*"
	quantity := int32(1)
	_, err = d.cloudfront.CreateInvalidation(ctx, &cloudfront.CreateInvalidationInput{
		DistributionId: &distID,
		InvalidationBatch: &cftypes.InvalidationBatch{
			CallerReference: &callerRef,
			Paths: &cftypes.Paths{
				Quantity: &quantity,
				Items:    []string{path},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("invalidating CloudFront %s: %w", distID, err)
	}
	logf("CloudFront invalidation created")

	return nil
}

func (d *staticDeployer) putMarker(ctx context.Context, bucket, key, value string) error {
	body := strings.NewReader(value)
	_, err := d.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   body,
	})
	return err
}

func (d *staticDeployer) listBuildObjects(ctx context.Context, bucket, tag string) ([]string, error) {
	var keys []string
	prefix := "builds/" + tag + "/"
	input := &s3.ListObjectsV2Input{
		Bucket: &bucket,
		Prefix: &prefix,
	}

	for {
		out, err := d.s3.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Contents {
			if obj.Key != nil {
				keys = append(keys, *obj.Key)
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		input.ContinuationToken = out.NextContinuationToken
	}

	return keys, nil
}

func (d *staticDeployer) copyObjects(ctx context.Context, bucket, srcPrefix, dstPrefix string, keys []string) error {
	const maxWorkers = 20

	sem := make(chan struct{}, maxWorkers)
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup

	for _, key := range keys {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			relKey := strings.TrimPrefix(key, srcPrefix)
			dst := dstPrefix + relKey
			src := bucket + "/" + key

			_, err := d.s3.CopyObject(ctx, &s3.CopyObjectInput{
				Bucket:     &bucket,
				Key:        aws.String(dst),
				CopySource: aws.String(src),
			})
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = fmt.Errorf("copying s3://%s/%s to s3://%s/%s: %w", bucket, key, bucket, dst, err)
				}
				mu.Unlock()
			}
		}(key)
	}

	wg.Wait()
	return firstErr
}
