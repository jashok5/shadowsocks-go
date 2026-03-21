package autoblock

import "testing"

func TestNewBackend(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "", want: "noop"},
		{name: "noop", want: "noop"},
		{name: "ipset", want: "ipset"},
		{name: "nft", want: "nft"},
		{name: "unknown", want: "noop"},
	}

	for _, tt := range tests {
		be := newBackend(tt.name, nil)
		if got := be.Name(); got != tt.want {
			t.Fatalf("newBackend(%q)=%q, want %q", tt.name, got, tt.want)
		}
	}
}
