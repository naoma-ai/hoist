package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDockerLogsArgs(t *testing.T) {
	tests := []struct {
		name      string
		container string
		since     string
		n         int
		follow    bool
		want      string
	}{
		{
			name:      "follow mode",
			container: "backend",
			follow:    true,
			want:      "logs -f backend",
		},
		{
			name:      "tail N lines",
			container: "backend",
			n:         100,
			want:      "logs --tail 100 backend",
		},
		{
			name:      "since duration",
			container: "backend",
			since:     "1h",
			want:      "logs --since 1h backend",
		},
		{
			name:      "tail and since",
			container: "backend",
			n:         50,
			since:     "2h",
			want:      "logs --tail 50 --since 2h backend",
		},
		{
			name:      "follow with since",
			container: "myapp",
			since:     "30m",
			follow:    true,
			want:      "logs --since 30m -f myapp",
		},
		{
			name:      "no flags",
			container: "backend",
			want:      "logs backend",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dockerLogsArgs(tt.container, tt.since, tt.n, tt.follow)
			result := strings.Join(got, " ")
			if result != tt.want {
				t.Errorf("dockerLogsArgs() = %q, want %q", result, tt.want)
			}
		})
	}
}

func TestLinePrefixWriter(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		input  string
		want   string
	}{
		{
			name:   "single line",
			prefix: "[backend]",
			input:  "hello world\n",
			want:   "[backend] hello world\n",
		},
		{
			name:   "multiple lines",
			prefix: "[api]",
			input:  "line1\nline2\nline3\n",
			want:   "[api] line1\n[api] line2\n[api] line3\n",
		},
		{
			name:   "no trailing newline",
			prefix: "[svc]",
			input:  "partial",
			want:   "", // buffered until Flush
		},
		{
			name:   "empty input",
			prefix: "[x]",
			input:  "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := newLinePrefixWriter(&buf, tt.prefix)
			w.Write([]byte(tt.input))
			if buf.String() != tt.want {
				t.Errorf("got %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

func TestLinePrefixWriterChunked(t *testing.T) {
	var buf bytes.Buffer
	w := newLinePrefixWriter(&buf, "[svc]")

	// Write in chunks that don't align with line boundaries.
	w.Write([]byte("hel"))
	w.Write([]byte("lo\nwor"))
	w.Write([]byte("ld\n"))

	want := "[svc] hello\n[svc] world\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestLinePrefixWriterFlush(t *testing.T) {
	var buf bytes.Buffer
	w := newLinePrefixWriter(&buf, "[svc]")

	w.Write([]byte("partial line"))
	if buf.String() != "" {
		t.Errorf("expected empty before flush, got %q", buf.String())
	}

	w.Flush()
	want := "[svc] partial line\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}
