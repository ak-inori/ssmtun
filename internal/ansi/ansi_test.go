package ansi

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]string{
		"":           "",
		"cyan":       "36",
		"Bright-Red": "91",
		"208":        "38;5;208",
		"#ff8800":    "38;2;255;136;0",
		"#F80":       "38;2;255;136;0",
	}
	for in, want := range cases {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"pink", "256", "-1", "bright-", "#12345", "#gg0000"} {
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

func TestAssignSkipsExplicitColors(t *testing.T) {
	got, err := Assign([]string{"", "cyan", "", "yellow"})
	if err != nil {
		t.Fatal(err)
	}
	// Palette starts with cyan (36) and yellow (33); both are taken explicitly.
	want := []string{"32", "36", "35", "33"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Assign = %v, want %v", got, want)
		}
	}
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Fatalf("duplicate color in %v", got)
		}
		seen[c] = true
	}
}

func TestAssignRejectsBadColor(t *testing.T) {
	if _, err := Assign([]string{"", "pink"}); err == nil {
		t.Fatal("expected error")
	}
}
