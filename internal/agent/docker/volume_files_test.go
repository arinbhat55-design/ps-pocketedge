package docker

import "testing"

func TestVolumeRelativePath(t *testing.T) {
	for _, valid := range []string{"notes.txt", "folder/notes.txt", "./folder/notes.txt", "folder/../notes.txt"} {
		if _, err := volumeRelativePath(valid, false); err != nil {
			t.Errorf("rejected %q: %v", valid, err)
		}
	}
	for _, invalid := range []string{"", ".", "../secret", "folder/../../secret", "/etc/passwd", `C:\secret`, "a\\b", "bad\x00name"} {
		if _, err := volumeRelativePath(invalid, false); err == nil {
			t.Errorf("accepted %q", invalid)
		}
	}
	if got, err := volumeRelativePath("", true); err != nil || got != "." {
		t.Fatalf("root path = %q, %v", got, err)
	}
}
