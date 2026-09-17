package client

import "testing"

func TestParseDetachKey(t *testing.T) {
	cases := []struct {
		in   string
		want byte
	}{
		{"^]", 0x1D}, // the default
		{"^^", 0x1E},
		{"^_", 0x1F},
		{"^a", 0x01}, // lowercase letters fold to uppercase
		{"^A", 0x01},
		{"^Z", 0x1A},
		{"^?", 0x7f},
	}
	for _, c := range cases {
		got, err := ParseDetachKey(c.in)
		if err != nil {
			t.Errorf("ParseDetachKey(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseDetachKey(%q) = %#x, want %#x", c.in, got, c.want)
		}
	}
}

func TestParseDetachKeyInvalid(t *testing.T) {
	for _, in := range []string{"", "]", "^", "^^^", "abc", "^0", "$]"} {
		if _, err := ParseDetachKey(in); err == nil {
			t.Errorf("ParseDetachKey(%q): want error, got nil", in)
		}
	}
}

func TestFormatKey(t *testing.T) {
	cases := []struct {
		in   byte
		want string
	}{
		{0x1D, "Ctrl+]"},
		{0x1E, "Ctrl+^"},
		{0x01, "Ctrl+A"},
		{0x7f, "Ctrl+?"},
	}
	for _, c := range cases {
		if got := formatKey(c.in); got != c.want {
			t.Errorf("formatKey(%#x) = %q, want %q", c.in, got, c.want)
		}
	}
}
