package main

import "context"

type serverBuildsProvider struct {
	builds []build
}

func (p *serverBuildsProvider) listBuilds(_ context.Context, limit, offset int) ([]build, error) {
	if offset >= len(p.builds) {
		return nil, nil
	}
	end := offset + limit
	if end > len(p.builds) {
		end = len(p.builds)
	}
	return p.builds[offset:end], nil
}
