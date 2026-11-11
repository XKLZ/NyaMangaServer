package recommendationpilot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCoverBoundaryAndAtomicMaterial(t *testing.T) {
	root := t.TempDir()
	name := strings.Repeat("a", 40) + ".jpg"
	for _, bad := range []string{"/original/Covers/aa/../" + name, "/original/Covers/bb/" + name, "/original/notcovers/aa/" + name} {
		if _, e := CoverPath(root, bad); e == nil {
			t.Fatal("accepted", bad)
		}
	}
	p, e := CoverPath(root, "/do-not-read-production/Covers/aa/"+name)
	if e != nil || p != filepath.Join(root, "aa", name) {
		t.Fatal(p, e)
	}
	if e = os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(filepath.Dir(p), ".snapshot-material-interrupted"), []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	added, e := publishMaterial(p, []byte("complete"))
	if !added || e != nil {
		t.Fatal(added, e)
	}
	added, e = publishMaterial(p, []byte("complete"))
	if added || e != nil {
		t.Fatal(added, e)
	}
	if _, e = publishMaterial(p, []byte("other")); e == nil {
		t.Fatal("overwrote material")
	}
	b, _ := os.ReadFile(p)
	if string(b) != "complete" {
		t.Fatal("partial material visible")
	}
	if e = os.Symlink(filepath.Dir(p), filepath.Join(root, "link")); e != nil {
		t.Fatal(e)
	}
	if _, e = CoverPath(filepath.Join(root, "link"), "/original/Covers/aa/"+name); e == nil {
		t.Fatal("accepted symlink")
	}
}
