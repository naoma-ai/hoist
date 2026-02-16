package main

import "context"

type staticBuildsProvider struct {
	builds []build
}

func (p *staticBuildsProvider) listBuilds(_ context.Context, limit, offset int) ([]build, error) {
	if offset >= len(p.builds) {
		return nil, nil
	}
	end := offset + limit
	if end > len(p.builds) {
		end = len(p.builds)
	}
	return p.builds[offset:end], nil
}
