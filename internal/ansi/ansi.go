// Package ansi maps user-facing color names to SGR escape codes.
package ansi

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Palette is cycled through when targets have no explicit color, in the
// same spirit as docker compose.
var Palette = []string{"36", "33", "32", "35", "34", "96", "93", "92", "95", "94"}

var named = map[string]string{
	"black":   "30",
	"red":     "31",
	"green":   "32",
	"yellow":  "33",
	"blue":    "34",
	"magenta": "35",
	"cyan":    "36",
	"white":   "37",
}

// Parse turns "cyan", "bright-red" or "208" (256-color index) into an SGR
// parameter string. Empty input yields empty output.
func Parse(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", nil
	}
	if n, err := strconv.Atoi(name); err == nil {
		if n < 0 || n > 255 {
			return "", fmt.Errorf("color %q must be between 0 and 255", name)
		}
		return "38;5;" + strconv.Itoa(n), nil
	}
	bright := false
	if strings.HasPrefix(name, "bright-") || strings.HasPrefix(name, "bright_") {
		bright = true
		name = name[len("bright-"):]
	}
	code, ok := named[name]
	if !ok {
		return "", fmt.Errorf("unknown color %q (use black, red, green, yellow, blue, magenta, cyan, white, bright-<name> or 0-255)", name)
	}
	if bright {
		n, _ := strconv.Atoi(code)
		code = strconv.Itoa(n + 60)
	}
	return code, nil
}

// Wrap colors s with the given SGR parameters; no-op when code is empty.
func Wrap(code, s string) string {
	if code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Enabled reports whether f is a terminal and color has not been disabled
// via NO_COLOR.
func Enabled(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
