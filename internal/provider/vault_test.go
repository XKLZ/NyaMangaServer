package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func vaultForTest(t *testing.T) *Vault {
	t.Helper()
	dir := t.TempDir()
	return NewVault(filepath.Join(dir, "key"), filepath.Join(dir, "providers.enc"))
}
func str(v string) *string { return &v }
func TestPrivateSettingsEncryptionAndCAS(t *testing.T) {
	v := vaultForTest(t)
	initial, e := v.Settings()
	if e != nil || initial.Revision != 1 {
		t.Fatal(initial, e)
	}
	p, e := v.Update(SettingPatch{Expected: 1, Site: str("ex"), ProxyURL: str("socks5://127.0.0.1:7891")})
	if e != nil {
		t.Fatal(e)
	}
	secret := "anonymous-private-cookie-0123456789"
	p, e = v.Credentials(CredentialPatch{Expected: p.Revision, Fields: map[string]SecretChange{"ipb_pass_hash": {Op: "set", Value: &secret}}})
	if e != nil || !p.Configured["ipb_pass_hash"] {
		t.Fatal(p, e)
	}
	public, _ := json.Marshal(p)
	encrypted, e := os.ReadFile(v.filePath)
	if e != nil || bytes.Contains(public, []byte(secret)) || bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("credential exposed")
	}
	for _, path := range []string{v.keyPath, v.filePath} {
		info, e := os.Stat(path)
		if e != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private permissions", e)
		}
	}
	if _, e = v.Update(SettingPatch{Expected: 1, Site: str("eh")}); !errors.Is(e, model.ErrConflict) {
		t.Fatal(e)
	}
	reloaded := NewVault(v.keyPath, v.filePath)
	p, e = reloaded.Settings()
	if e != nil || p.Site != "ex" || !p.Configured["ipb_pass_hash"] {
		t.Fatal(p, e)
	}
	key, _ := os.ReadFile(v.keyPath)
	if e = os.Remove(v.keyPath); e != nil {
		t.Fatal(e)
	}
	if _, e = reloaded.Credentials(CredentialPatch{Expected: p.Revision, Fields: map[string]SecretChange{"ipb_pass_hash": {Op: "clear"}}}); !errors.Is(e, ErrSecrets) {
		t.Fatal(e)
	}
	unchanged, _ := os.ReadFile(v.filePath)
	if !bytes.Equal(encrypted, unchanged) {
		t.Fatal("missing key replaced ciphertext")
	}
	if _, e = os.Stat(v.keyPath); !os.IsNotExist(e) {
		t.Fatal("missing key regenerated")
	}
	if e = os.WriteFile(v.keyPath, key, 0600); e != nil {
		t.Fatal(e)
	}
	if e = os.Chmod(v.filePath, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = v.Settings(); !errors.Is(e, ErrSecrets) {
		t.Fatal(e)
	}
}
func TestSettingsRejectUnsafeValues(t *testing.T) {
	for _, proxy := range []string{"file:///etc/passwd", "http://user:secret@host:7892", "http://host/path", "http://host?x=1", "http://host:0", "socks5://host:65536"} {
		v := vaultForTest(t)
		if _, e := v.Update(SettingPatch{Expected: 1, ProxyURL: &proxy}); !errors.Is(e, model.ErrInput) {
			t.Fatal(proxy, e)
		}
	}
	v := vaultForTest(t)
	if _, e := v.Credentials(CredentialPatch{Expected: 1, Fields: map[string]SecretChange{"ipb_pass_hash": {Op: "set", Value: str("x\r\nCookie:y")}}}); !errors.Is(e, model.ErrInput) {
		t.Fatal(e)
	}
}

func TestWrongKeyRejectsExistingCiphertext(t *testing.T) {
	v := vaultForTest(t)
	if _, e := v.Update(SettingPatch{Expected: 1, Site: str("ex")}); e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(v.filePath)
	if e := os.WriteFile(v.keyPath, make([]byte, 32), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := v.Settings(); !errors.Is(e, ErrSecrets) {
		t.Fatal("wrong key accepted", e)
	}
	if _, e := v.Update(SettingPatch{Expected: 2, Site: str("eh")}); !errors.Is(e, ErrSecrets) {
		t.Fatal("wrong key overwritten", e)
	}
	after, _ := os.ReadFile(v.filePath)
	if !bytes.Equal(before, after) {
		t.Fatal("ciphertext replaced")
	}
}

func TestCookieInvalidBytesAreRejectedBeforeHTTPSerialization(t *testing.T) {
	for _, value := range []string{"quoted\"cookie", "non-ascii秘密", "a,b", "a\\b"} {
		v := vaultForTest(t)
		if _, e := v.Credentials(CredentialPatch{Expected: 1, Fields: map[string]SecretChange{"ipb_pass_hash": {Op: "set", Value: &value}}}); !errors.Is(e, model.ErrInput) {
			t.Fatal("invalid cookie accepted", e)
		}
	}
}
