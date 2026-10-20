package tunnel

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ak-inori/ssmtun/internal/ansi"
)

// Logger writes compact, human-oriented lines tagged with the target name.
type Logger struct {
	mu     sync.Mutex
	w      io.Writer
	prefix string
}

// NewLogger tags every line with target, colored with the SGR code when
// one is given.
func NewLogger(w io.Writer, target, color string) *Logger {
	return &Logger{w: w, prefix: ansi.Wrap(color, "["+target+"]")}
}

// Printf writes one timestamped line.
func (l *Logger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "%s %s %s\n", time.Now().Format("15:04:05"), l.prefix, fmt.Sprintf(format, args...))
}
