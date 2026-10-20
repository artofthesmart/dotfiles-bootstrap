package copilot

import "testing"

func TestVersionParsingAndSupportedFloor(t *testing.T) {
	for _, test := range []struct {
		output    string
		version   string
		supported bool
	}{
		{"1.0.101\n", "1.0.101", true},
		{"GitHub Copilot CLI v1.0.100", "1.0.100", true},
		{"copilot version 1.0.99+build", "1.0.99", false},
	} {
		version, err := ParseVersion(test.output)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", test.output, err)
		}
		if version.Raw != test.version || version.AtLeast(minimumVersion()) != test.supported {
			t.Fatalf("ParseVersion(%q) = %#v", test.output, version)
		}
	}
	for _, invalid := range []string{"", "unknown", "1.0", "v1"} {
		if _, err := ParseVersion(invalid); err == nil {
			t.Fatalf("ParseVersion(%q) succeeded", invalid)
		}
	}
}
