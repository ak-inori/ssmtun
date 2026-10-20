package tunnel

import (
	"bytes"
	"strings"
	"testing"
)

func TestLoggerColorsOnlyThePrefix(t *testing.T) {
	var buf bytes.Buffer
	NewLogger(&buf, "app-dev", "36").Printf("up: %d", 1)
	line := buf.String()
	if !strings.Contains(line, "\x1b[36m[app-dev]\x1b[0m up: 1") {
		t.Fatalf("unexpected line %q", line)
	}
	if strings.HasPrefix(line, "\x1b") {
		t.Fatal("timestamp should not be colored")
	}

	buf.Reset()
	NewLogger(&buf, "app-dev", "").Printf("plain")
	if strings.Contains(buf.String(), "\x1b") {
		t.Fatalf("no color requested but got %q", buf.String())
	}
}
