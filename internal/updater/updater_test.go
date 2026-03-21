package updater

import (
	"strings"
	"testing"
)

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

func TestMergeYAML_PreservesDefaultsAndOverridesOldValues(t *testing.T) {
	base := []byte(`node:
  id: 100
api:
  url: https://new.example
  token: new-token
update:
  enabled: false
  check_interval: 1h
`)
	override := []byte(`node:
  id: 999
api:
  token: old-token
`)

	out, err := mergeYAML(base, override)
	if err != nil {
		t.Fatalf("mergeYAML failed: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "id: 999") {
		t.Fatalf("expected merged node.id from old config, got: %s", s)
	}
	if !strings.Contains(s, "token: old-token") {
		t.Fatalf("expected merged api.token from old config, got: %s", s)
	}
	if !strings.Contains(s, "url: https://new.example") {
		t.Fatalf("expected new default api.url preserved, got: %s", s)
	}
	if !strings.Contains(s, "check_interval: 1h") {
		t.Fatalf("expected new default update.check_interval preserved, got: %s", s)
	}
}
