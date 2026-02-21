package main

import (
	"context"
	"fmt"
	"io"
)

type staticLogsProvider struct{}

func (p *staticLogsProvider) tail(_ context.Context, service, _ string, _ int, _ string, _ io.Writer) error {
	return fmt.Errorf("logs are not available for static service %q (no running containers)", service)
}
