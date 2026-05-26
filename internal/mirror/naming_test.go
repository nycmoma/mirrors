package mirror

import "testing"

func TestSuiteName(t *testing.T) {
	tests := []struct {
		dist    string
		release string
		want    string
	}{
		{dist: "focal", release: "default", want: "focal"},
		{dist: "focal", release: "", want: "focal"},
		{dist: "focal", release: "updates", want: "focal-updates"},
	}

	for _, tt := range tests {
		if got := SuiteName(tt.dist, tt.release); got != tt.want {
			t.Fatalf("SuiteName(%q, %q) = %q, want %q", tt.dist, tt.release, got, tt.want)
		}
	}
}

func TestComponentMirrorName(t *testing.T) {
	got := ComponentMirrorName("ubuntu", "focal", "updates", "main")
	want := "ubuntu-focal-updates-main"
	if got != want {
		t.Fatalf("ComponentMirrorName returned %q, want %q", got, want)
	}
}

func TestSnapshotName(t *testing.T) {
	got := SnapshotName("ubuntu-focal-main", "2026-05-26")
	want := "ubuntu-focal-main_2026-05-26"
	if got != want {
		t.Fatalf("SnapshotName returned %q, want %q", got, want)
	}
}
