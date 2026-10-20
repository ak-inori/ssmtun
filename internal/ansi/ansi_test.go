package ansi

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"cyan":       "36",
		"Bright-Red": "91",
		"208":        "38;5;208",
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"pink", "256", "-1", "bright-"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestWrap(t *testing.T) {
	if Wrap("", "x") != "x" {
		t.Fatal("empty code must not wrap")
	}
	if Wrap("36", "x") != "\x1b[36mx\x1b[0m" {
		t.Fatal("unexpected escape sequence")
	}
}
