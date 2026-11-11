package main

import (
	"context"
	"encoding/json"
	"errors"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/auth"
	"nyamangaserver.local/server/internal/config"
	"nyamangaserver.local/server/internal/provider"
	"nyamangaserver.local/server/internal/store"
	"nyamangaserver.local/server/internal/testfixture"
	"nyamangaserver.local/server/migrations"
	"os"
	"path/filepath"
	"strings"
)

// Creates an isolated, fully anonymous instance; no fake backup/restore API.
func PrepareRecoveryFixture(root string) (string, error) {
	absolute, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	cwd, e := os.Getwd()
	if e != nil {
		return "", e
	}
	allowed := false
	for _, base := range []string{filepath.Join(cwd, ".runtime/tests"), os.TempDir(), "/private/tmp"} {
		base = filepath.Clean(base)
		if strings.HasPrefix(absolute, base+string(filepath.Separator)) {
			allowed = true
		}
	}
	if !allowed {
		return "", errors.New("recovery fixture must be under repository .runtime/tests or system temp")
	}
	if e = appdata.NoSymlinks(absolute); e != nil {
		return "", e
	}
	if e = os.Mkdir(absolute, 0700); e != nil {
		return "", errors.New("recovery fixture requires a new empty output directory")
	}
	c := config.Config{Listen: "127.0.0.1:18081", PublicURL: "http://127.0.0.1:18081", DataDir: filepath.Join(absolute, "data"), SetupTokenFile: filepath.Join(absolute, "secrets/setup-token"), ProviderKeyFile: filepath.Join(absolute, "secrets/provider-key"), ProviderCredentialsFile: filepath.Join(absolute, "secrets/providers.enc"), Roots: []config.Root{{Key: "fixture", Name: "匿名整实例样本", Path: filepath.Join(absolute, "library")}}}
	if e = os.MkdirAll(filepath.Dir(c.SetupTokenFile), 0700); e != nil {
		return "", e
	}
	token, e := auth.RandomToken(32)
	if e != nil {
		return "", e
	}
	if e = os.WriteFile(c.SetupTokenFile, []byte(token), 0600); e != nil {
		return "", e
	}
	lock, e := appdata.Acquire(c.DataDir)
	if e != nil {
		return "", e
	}
	defer lock.Close()
	db, e := store.Open(context.Background(), c.DataDir, migrations.Files)
	if e != nil {
		return "", e
	}
	defer db.Close()
	if _, _, e = auth.New(db, token).Setup(context.Background(), token, "anonymous recovery password"); e != nil {
		return "", e
	}
	if e = testfixture.SeedInstanceRecovery(context.Background(), db.Write, c.DataDir, c.Roots[0].Path, c.Roots[0].Key); e != nil {
		return "", e
	}
	secret := "anonymous-cookie"
	if _, e = provider.NewVault(c.ProviderKeyFile, c.ProviderCredentialsFile).Credentials(provider.CredentialPatch{Expected: 1, Fields: map[string]provider.SecretChange{"ipb_pass_hash": {Op: "set", Value: &secret}}}); e != nil {
		return "", e
	}
	raw, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return "", e
	}
	filename := filepath.Join(absolute, "config.json")
	if e = os.WriteFile(filename, append(raw, '\n'), 0600); e != nil {
		return "", e
	}
	return filename, nil
}
