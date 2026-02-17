package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3ListObjectsAPI interface {
	ListObjectsV2(ctx context.Context, params *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
}

type staticBuildsProvider struct {
	s3     s3ListObjectsAPI
	bucket string
}

func (p *staticBuildsProvider) listBuilds(ctx context.Context, limit, offset int) ([]build, error) {
	var all []build

	prefix := "builds/"
	delimiter := "/"
	input := &s3.ListObjectsV2Input{
		Bucket:    &p.bucket,
		Prefix:    &prefix,
		Delimiter: &delimiter,
	}

	for {
		out, err := p.s3.ListObjectsV2(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("listing S3 prefixes: %w", err)
		}

		for _, cp := range out.CommonPrefixes {
			if cp.Prefix == nil {
				continue
			}
			tagStr := strings.TrimPrefix(*cp.Prefix, "builds/")
			tagStr = strings.TrimSuffix(tagStr, "/")

			t, err := parseTag(tagStr)
			if err != nil {
				continue
			}
			all = append(all, buildFromTag(t))
		}

		if out.IsTruncated == nil || !*out.IsTruncated {
			break
		}
		input.ContinuationToken = out.NextContinuationToken
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].Time.After(all[j].Time)
	})

	if offset >= len(all) {
		return nil, nil
	}
	all = all[offset:]

	if limit < len(all) {
		all = all[:limit]
	}

	return all, nil
}
