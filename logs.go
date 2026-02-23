package main

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// linePrefixWriter wraps a writer and prepends a prefix to each line.
// It buffers partial lines until a newline is seen.
// It is safe for concurrent use (e.g. when both stdout and stderr write to it).
type linePrefixWriter struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
	buf    []byte
}

func newLinePrefixWriter(w io.Writer, prefix string) *linePrefixWriter {
	return &linePrefixWriter{w: w, prefix: prefix}
}

func (w *linePrefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	total := len(p)
	w.buf = append(w.buf, p...)

	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i+1]
		if _, err := fmt.Fprintf(w.w, "%s %s", w.prefix, line); err != nil {
			return 0, err
		}
		w.buf = w.buf[i+1:]
	}

	return total, nil
}

// Flush writes any remaining buffered content (partial line without trailing newline).
func (w *linePrefixWriter) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.buf) > 0 {
		_, err := fmt.Fprintf(w.w, "%s %s\n", w.prefix, w.buf)
		w.buf = nil
		return err
	}
	return nil
}

func dockerLogsArgs(container, since string, n int, follow bool) []string {
	args := []string{"logs"}

	if n > 0 {
		args = append(args, "--tail", strconv.Itoa(n))
	}

	if since != "" {
		args = append(args, "--since", since)
	}

	if follow {
		args = append(args, "-f")
	}

	args = append(args, container)
	return args
}