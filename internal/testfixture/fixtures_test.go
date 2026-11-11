package testfixture

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnonymousOnlyAndRepeatable(t *testing.T) {
	root := t.TempDir()
	if e := Write(root); e != nil {
		t.Fatal(e)
	}
	if e := Write(root); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "foreign.cbz"), []byte("private sentinel"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := Write(root); e == nil {
		t.Fatal("foreign input accepted")
	}
	data, e := os.ReadFile(filepath.Join(root, "foreign.cbz"))
	if e != nil || string(data) != "private sentinel" {
		t.Fatal("foreign file changed")
	}
}
