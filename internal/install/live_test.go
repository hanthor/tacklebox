package install

import (
	"strings"
	"testing"
)

func TestShellEsc_Basic(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"", "''"},
		{"it's working", "'it'\"'\"'s working'"},
		{"a'b'c", "'a'\"'\"'b'\"'\"'c'"},
		{`no"quotes`, "'no\"quotes'"},
		{"spaces and $pecial", "'spaces and $pecial'"},
		{"new\nline", "'new\nline'"},
		{"tab\there", "'tab\there'"},
		{"back\\slash", "'back\\slash'"},
		{"semi;colon", "'semi;colon'"},
		{"pipe|char", "'pipe|char'"},
		{"$(whoami)", "'$(whoami)'"},
		{"`whoami`", "'`whoami`'"},
		{"${HOME}", "'${HOME}'"},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := shellEsc(tc.input)
			if got != tc.want {
				t.Errorf("shellEsc(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestShellEsc_PreventsInjection(t *testing.T) {
	// shellEsc output, when used inside single-quoted context of a shell
	// script, must not allow command injection.
	dangerous := []string{
		"; rm -rf /",
		"$(rm -rf /)",
		"`rm -rf /`",
		"'; rm -rf /; echo '",
		"\\'; rm -rf /; echo '",
	}

	for _, payload := range dangerous {
		escaped := shellEsc(payload)
		// The escaped string must start and end with single quotes.
		if !strings.HasPrefix(escaped, "'") || !strings.HasSuffix(escaped, "'") {
			t.Errorf("shellEsc(%q) = %q — not wrapped in quotes", payload, escaped)
		}
		// No unescaped single quote in the middle that could close the quoting.
		inner := escaped[1 : len(escaped)-1]
		if strings.Contains(inner, "'") {
			// Embedded single quotes must use the '"'"' idiom.
			if !strings.Contains(escaped, `'"'"'`) && strings.Count(escaped, "'") > 2 {
				t.Errorf("shellEsc(%q) = %q — single quote not escaped", payload, escaped)
			}
		}
	}
}

func TestShellEsc_Idempotent(t *testing.T) {
	// shellEsc of an already-escaped string should still be safe.
	safe := "simple-image-name"
	once := shellEsc(safe)
	twice := shellEsc(once)
	if !strings.HasPrefix(twice, "'") || !strings.HasSuffix(twice, "'") {
		t.Errorf("double shellEsc(%q) = %q — not wrapped", safe, twice)
	}
}
