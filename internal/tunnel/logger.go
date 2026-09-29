package tunnel

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// Logger writes compact, human-oriented lines tagged with the target name.
type Logger struct {
	mu     sync.Mutex
	w      io.Writer
	target string
}

// NewLogger tags every line with target.
func NewLogger(w io.Writer, target string) *Logger {
	return &Logger{w: w, target: target}
}

// Printf writes one timestamped line.
func (l *Logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s [%s] %s\n", time.Now().Format("15:04:05"), l.target, fmt.Sprintf(format, args...))
}
