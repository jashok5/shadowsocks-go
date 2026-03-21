package updater

import "testing"

func TestCompareVersion(t *testing.T) {
	v1, _ := parseVersion("v1.2.3")
	v2, _ := parseVersion("v1.2.4")
	if compareVersion(v2, v1) <= 0 {
		t.Fatalf("expected v1.2.4 > v1.2.3")
	}
	if compareVersion(v1, v2) >= 0 {
		t.Fatalf("expected v1.2.3 < v1.2.4")
	}
}

func TestParseChecksums(t *testing.T) {
	content := "aaa111  node_linux_amd64\nbbb222 *node_windows_amd64.exe\n"
	m := parseChecksums(content)
	if got := m["node_linux_amd64"]; got != "aaa111" {
		t.Fatalf("linux checksum mismatch: %q", got)
	}
	if got := m["node_windows_amd64.exe"]; got != "bbb222" {
		t.Fatalf("windows checksum mismatch: %q", got)
	}
}
