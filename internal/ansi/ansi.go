// Package ansi maps user-facing color names to SGR escape codes.
package ansi

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Palette is cycled through, in start order, for targets without an explicit color.
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

// Parse turns "cyan", "bright-red", "208" (256-color index) or "#ff8800"
// (24-bit color) into an SGR parameter string. Empty input yields empty output.
func Parse(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return "", nil
	}
	if strings.HasPrefix(name, "#") {
		return parseHex(name)
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
		return "", fmt.Errorf("unknown color %q (use black, red, green, yellow, blue, magenta, cyan, white, bright-<name>, 0-255 or #rrggbb)", name)
	}
	if bright {
		n, _ := strconv.Atoi(code)
		code = strconv.Itoa(n + 60)
	}
	return code, nil
}

// parseHex accepts #rgb and #rrggbb and emits a 24-bit SGR sequence.
func parseHex(name string) (string, error) {
	hex := name[1:]
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return "", fmt.Errorf("color %q must be #rgb or #rrggbb", name)
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return "", fmt.Errorf("color %q must be #rgb or #rrggbb", name)
	}
	return fmt.Sprintf("38;2;%d;%d;%d", v>>16&0xff, v>>8&0xff, v&0xff), nil
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

// Assign resolves one SGR code per target: explicit colors are kept as-is,
// and the rest receive palette entries in order, skipping any entry an
// explicit color already uses so two targets in the same run never share.
func Assign(explicit []string) ([]string, error) {
	out := make([]string, len(explicit))
	used := map[string]bool{}
	for i, name := range explicit {
		code, err := Parse(name)
		if err != nil {
			return nil, err
		}
		out[i] = code
		if code != "" {
			used[code] = true
		}
	}
	next := 0
	for i := range out {
		if out[i] != "" {
			continue
		}
		for tries := 0; tries < len(Palette); tries++ {
			code := Palette[next%len(Palette)]
			next++
			if !used[code] {
				out[i] = code
				break
			}
		}
		if out[i] == "" { // every palette entry is taken; fall back to cycling
			out[i] = Palette[next%len(Palette)]
			next++
		}
		used[out[i]] = true
	}
	return out, nil
}
