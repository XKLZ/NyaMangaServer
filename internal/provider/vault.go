// Package provider implements bounded external metadata access and private settings.
package provider

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"nyamangaserver.local/server/internal/appdata"
	"nyamangaserver.local/server/internal/model"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var ErrSecrets = errors.New("provider_secrets_unavailable")

type Settings struct {
	Revision   int64           `json:"revision"`
	Site       string          `json:"site"`
	ProxyURL   string          `json:"proxy_url"`
	Configured map[string]bool `json:"configured"`
}
type SettingPatch struct {
	Expected int64   `json:"expected_revision"`
	Site     *string `json:"site,omitempty"`
	ProxyURL *string `json:"proxy_url,omitempty"`
}
type SecretChange struct {
	Op    string  `json:"op"`
	Value *string `json:"value,omitempty"`
}
type CredentialPatch struct {
	Expected int64                   `json:"expected_revision"`
	Fields   map[string]SecretChange `json:"fields"`
}
type privateSettings struct {
	Revision int64             `json:"revision"`
	Site     string            `json:"site"`
	ProxyURL string            `json:"proxy_url"`
	Secrets  map[string]string `json:"secrets"`
}
type envelope struct {
	Version int    `json:"version"`
	Nonce   []byte `json:"nonce"`
	Data    []byte `json:"data"`
}
type Vault struct {
	mu                sync.Mutex
	keyPath, filePath string
}

func NewVault(keyPath, filePath string) *Vault { return &Vault{keyPath: keyPath, filePath: filePath} }

// ValidateBackupPair checks the frozen ciphertext/key together without returning
// plaintext or creating default credentials. Both absent is an unconfigured vault.
func ValidateBackupPair(keyPath, filePath string) (bool, error) {
	key, ke := os.Lstat(keyPath)
	file, fe := os.Lstat(filePath)
	if os.IsNotExist(ke) && os.IsNotExist(fe) {
		return false, nil
	}
	if ke != nil || fe != nil || !key.Mode().IsRegular() || !file.Mode().IsRegular() {
		return false, ErrSecrets
	}
	_, e := NewVault(keyPath, filePath).snapshot()
	return e == nil, e
}

var secretKeys = map[string]bool{"ipb_member_id": true, "ipb_pass_hash": true, "igneous": true, "proxy_username": true, "proxy_password": true}

func defaultPrivate() privateSettings {
	return privateSettings{Revision: 1, Site: "eh", Secrets: map[string]string{}}
}
func privateRead(path string, max int64) ([]byte, error) {
	if e := appdata.NoSymlinks(path); e != nil {
		return nil, ErrSecrets
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrSecrets
	}
	b, e := io.ReadAll(io.LimitReader(f, max+1))
	if e != nil || int64(len(b)) > max {
		return nil, ErrSecrets
	}
	return b, nil
}
func (v *Vault) read() (privateSettings, error) {
	state := defaultPrivate()
	if v.filePath == "" {
		return state, nil
	}
	b, e := privateRead(v.filePath, 64<<10)
	if os.IsNotExist(e) {
		return state, nil
	}
	if e != nil {
		return state, ErrSecrets
	}
	key, e := privateRead(v.keyPath, 32)
	if e != nil || len(key) != 32 {
		return state, ErrSecrets
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return state, ErrSecrets
	}
	gcm, e := cipher.NewGCM(block)
	if e != nil {
		return state, ErrSecrets
	}
	var env envelope
	if json.Unmarshal(b, &env) != nil || env.Version != 1 || len(env.Nonce) != gcm.NonceSize() {
		return state, ErrSecrets
	}
	plain, e := gcm.Open(nil, env.Nonce, env.Data, []byte("nyamangaserver-provider-v1"))
	if e != nil || json.Unmarshal(plain, &state) != nil || state.Revision < 1 || state.Secrets == nil {
		return defaultPrivate(), ErrSecrets
	}
	if validateSettings(state) != nil {
		return defaultPrivate(), ErrSecrets
	}
	return state, nil
}
func (v *Vault) snapshot() (privateSettings, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.read()
}
func publicSettings(p privateSettings) Settings {
	flags := map[string]bool{}
	for key := range secretKeys {
		flags[key] = p.Secrets[key] != ""
	}
	return Settings{Revision: p.Revision, Site: p.Site, ProxyURL: p.ProxyURL, Configured: flags}
}
func (v *Vault) Settings() (Settings, error) { p, e := v.snapshot(); return publicSettings(p), e }
func validateSettings(p privateSettings) error {
	if p.Site != "eh" && p.Site != "ex" {
		return model.ErrInput
	}
	if p.ProxyURL != "" {
		u, e := url.Parse(p.ProxyURL)
		if e != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || len(p.ProxyURL) > 2048 || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
			return model.ErrInput
		}
		if u.Port() != "" {
			port, err := strconv.Atoi(u.Port())
			if err != nil || port < 1 || port > 65535 {
				return model.ErrInput
			}
		}
	}
	for key, value := range p.Secrets {
		if !secretKeys[key] || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
			return model.ErrInput
		}
		if strings.HasPrefix(key, "ipb_") || key == "igneous" {
			for _, b := range []byte(value) {
				if b < 0x21 || b > 0x7e || b == '"' || b == ',' || b == ';' || b == '\\' {
					return model.ErrInput
				}
			}
		}
	}
	return nil
}
func atomicPrivate(path string, data []byte) error {
	if e := appdata.NoSymlinks(path); e != nil {
		return ErrSecrets
	}
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return ErrSecrets
	}
	if e := appdata.NoSymlinks(dir); e != nil {
		return ErrSecrets
	}
	f, e := os.CreateTemp(dir, "pending-provider-")
	if e != nil {
		return ErrSecrets
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(data); e != nil {
		f.Close()
		return ErrSecrets
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return ErrSecrets
	}
	if e = f.Close(); e != nil {
		return ErrSecrets
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return ErrSecrets
	}
	d, e := os.Open(dir)
	if e != nil {
		return ErrSecrets
	}
	defer d.Close()
	if e = d.Sync(); e != nil {
		return ErrSecrets
	}
	return nil
}
func (v *Vault) save(p privateSettings) error {
	if v.keyPath == "" || v.filePath == "" || v.keyPath == v.filePath {
		return ErrSecrets
	}
	key, e := privateRead(v.keyPath, 32)
	if os.IsNotExist(e) {
		if _, e = os.Lstat(v.filePath); e == nil || !os.IsNotExist(e) {
			return ErrSecrets
		}
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return ErrSecrets
		}
		if e = appdata.NoSymlinks(v.keyPath); e != nil {
			return ErrSecrets
		}
		if e = os.MkdirAll(filepath.Dir(v.keyPath), 0700); e != nil {
			return ErrSecrets
		}
		f, e := os.OpenFile(v.keyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return ErrSecrets
		}
		_, e = f.Write(key)
		syncErr := f.Sync()
		closeErr := f.Close()
		if e != nil || syncErr != nil || closeErr != nil {
			return ErrSecrets
		}
	} else if e != nil || len(key) != 32 {
		return ErrSecrets
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return ErrSecrets
	}
	gcm, e := cipher.NewGCM(block)
	if e != nil {
		return ErrSecrets
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return ErrSecrets
	}
	plain, e := json.Marshal(p)
	if e != nil {
		return ErrSecrets
	}
	out, e := json.Marshal(envelope{1, nonce, gcm.Seal(nil, nonce, plain, []byte("nyamangaserver-provider-v1"))})
	if e != nil {
		return ErrSecrets
	}
	return atomicPrivate(v.filePath, out)
}
func (v *Vault) Update(in SettingPatch) (Settings, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	p, e := v.read()
	if e != nil {
		return Settings{}, e
	}
	if in.Expected != p.Revision {
		return Settings{}, model.ErrConflict
	}
	if in.Site != nil {
		p.Site = *in.Site
	}
	if in.ProxyURL != nil {
		p.ProxyURL = *in.ProxyURL
	}
	if e = validateSettings(p); e != nil {
		return Settings{}, e
	}
	p.Revision++
	if e = v.save(p); e != nil {
		return Settings{}, e
	}
	return publicSettings(p), nil
}
func (v *Vault) Credentials(in CredentialPatch) (Settings, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	p, e := v.read()
	if e != nil {
		return Settings{}, e
	}
	if in.Expected != p.Revision {
		return Settings{}, model.ErrConflict
	}
	if len(in.Fields) == 0 || len(in.Fields) > len(secretKeys) {
		return Settings{}, model.ErrInput
	}
	for k, c := range in.Fields {
		if !secretKeys[k] {
			return Settings{}, model.ErrInput
		}
		switch c.Op {
		case "keep":
			if c.Value != nil {
				return Settings{}, model.ErrInput
			}
		case "clear":
			if c.Value != nil {
				return Settings{}, model.ErrInput
			}
			delete(p.Secrets, k)
		case "set":
			if c.Value == nil || *c.Value == "" {
				return Settings{}, model.ErrInput
			}
			p.Secrets[k] = *c.Value
		default:
			return Settings{}, model.ErrInput
		}
	}
	if e = validateSettings(p); e != nil {
		return Settings{}, e
	}
	p.Revision++
	if e = v.save(p); e != nil {
		return Settings{}, e
	}
	return publicSettings(p), nil
}
