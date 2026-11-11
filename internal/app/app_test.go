package app

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetupCredentialFileBoundary(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "setup-token")
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	for _, test := range []struct {
		name, value string
		mode        os.FileMode
		valid       bool
	}{
		{"owner read/write", token, 0600, true},
		{"owner read-only", token, 0400, true},
		{"world readable", token, 0644, false},
		{"short entropy", base64.RawURLEncoding.EncodeToString(make([]byte, 16)), 0600, false},
		{"malformed encoding", "not valid base64", 0600, false},
		{"oversize", strings.Repeat("x", 1025), 0600, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			os.Remove(filename)
			if e := os.WriteFile(filename, []byte(test.value), test.mode); e != nil {
				t.Fatal(e)
			}
			_, e := readSetupToken(filename)
			if (e == nil) != test.valid {
				t.Fatal("credential boundary", e)
			}
		})
	}
	if _, e := readSetupToken(filepath.Dir(filename)); e == nil {
		t.Fatal("directory accepted")
	}
}
