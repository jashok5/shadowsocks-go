package runtime

import "testing"

func TestBlockedPort(t *testing.T) {
	tests := []struct {
		name     string
		port     int
		forbid   string
		expected bool
	}{
		{name: "single hit", port: 80, forbid: "80,443", expected: true},
		{name: "single miss", port: 8080, forbid: "80,443", expected: false},
		{name: "range hit", port: 1500, forbid: "1000-2000", expected: true},
		{name: "range miss", port: 2500, forbid: "1000-2000", expected: false},
		{name: "mix hit", port: 22, forbid: "53, 20-25", expected: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := blockedPort(tt.port, tt.forbid); got != tt.expected {
				t.Fatalf("blockedPort(%d,%q)=%v want=%v", tt.port, tt.forbid, got, tt.expected)
			}
		})
	}
}

func TestBlockedIP(t *testing.T) {
	if !blockedIP("1.2.3.4", "1.2.3.4,5.6.7.8") {
		t.Fatalf("expected ip blocked")
	}
	if blockedIP("1.2.3.4", "5.6.7.8") {
		t.Fatalf("expected ip not blocked")
	}
}

func TestMatchPattern(t *testing.T) {
	if !matchPattern("hello world", "hello\\s+world") {
		t.Fatalf("expected regex match")
	}
	if !matchPattern("abcdef", "cde") {
		t.Fatalf("expected contains fallback/match")
	}
	if !matchPattern("abcxyz[", "xyz[") {
		t.Fatalf("expected fallback contains match")
	}
}
