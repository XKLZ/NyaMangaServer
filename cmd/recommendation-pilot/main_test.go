package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExclusivePrivateAtomicWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "anonymous.json")
	if e := exclusive(p, []byte("complete")); e != nil {
		t.Fatal(e)
	}
	if e := exclusive(p, []byte("other")); e == nil {
		t.Fatal("overwrote existing file")
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != "complete" {
		t.Fatal(e)
	}
	st, e := os.Stat(p)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, e)
	}
}
