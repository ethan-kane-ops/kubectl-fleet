package versions

import "testing"

func TestMatchVersion(t *testing.T) {
	tests := []struct {
		git, want string
		match     bool
	}{
		{"v1.30.4-eks-a737599", "1.30", true},
		{"v1.30.4", "1.30.5", false},
		{"v1.30.4", "1.30.4", true},
		{"v1.31.0", "v1.30", false},
		{"v1.30.2+k3s1", "v1.30", true},
		{"garbage", "1.30", false},
		{"v1.29.3-gke.100", "1.29.3", true},
		{"v1.29.10", "1.29.3", false},
	}
	for _, tt := range tests {
		if got := MatchVersion(tt.git, tt.want); got != tt.match {
			t.Errorf("MatchVersion(%q, %q) = %v, want %v", tt.git, tt.want, got, tt.match)
		}
	}
}

func TestValidVersion(t *testing.T) {
	for v, want := range map[string]bool{"1.30": true, "v1.30": true, "1.30.4": true, "1": false, "latest": false, "1.x": false, "": false} {
		if got := ValidVersion(v); got != want {
			t.Errorf("ValidVersion(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestShortVersion(t *testing.T) {
	if got := ShortVersion("v1.29.8-eks-a737599"); got != "1.29.8" {
		t.Errorf("ShortVersion = %q", got)
	}
}
