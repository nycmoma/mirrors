package debmeta

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{"1.0", "1.0", 0},
		{"2:1.0", "1:9.9", 1},
		{"1.0-2", "1.0-10", -1},
		{"1.0~rc1", "1.0", -1},
		{"1.0~~", "1.0~", -1},
		{"1.0a", "1.0~beta", 1},
		{"1.001", "1.1", 0},
	}

	for _, tt := range tests {
		got := CompareVersions(tt.left, tt.right)
		if got != tt.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, want %d", tt.left, tt.right, got, tt.want)
		}
		if reverse := CompareVersions(tt.right, tt.left); reverse != -tt.want {
			t.Fatalf("reverse CompareVersions(%q, %q) = %d, want %d", tt.right, tt.left, reverse, -tt.want)
		}
	}
}
