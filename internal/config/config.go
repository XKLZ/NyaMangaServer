// Package config loads explicit deployment configuration. No production path defaults.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Root struct {
	Key  string `json:"root_key"`
	Name string `json:"display_name"`
	Path string `json:"path"`
}
type CoverCache struct {
	MaxBytes int64 `json:"max_bytes"`
	MaxFiles int   `json:"max_files"`
}

func (c CoverCache) Limits() (int64, int) {
	b, n := c.MaxBytes, c.MaxFiles
	if b == 0 {
		b = 2 << 30
	}
	if n == 0 {
		n = 10000
	}
	return b, n
}

type TagImageCache CoverCache

func (c TagImageCache) Limits() (int64, int) {
	b, n := c.MaxBytes, c.MaxFiles
	if b == 0 {
		b = 256 << 20
	}
	if n == 0 {
		n = 10000
	}
	return b, n
}

// WorkRecommendations is optional. Explicit paths are deployment inputs, not HTTP settings.
// Missing files disable the capability at runtime without blocking ordinary management.
type WorkRecommendations struct {
	Python    string `json:"python"`
	Worker    string `json:"worker"`
	ModelsDir string `json:"models_dir"`
}

type Config struct {
	ImageMemoryMiB          int64               `json:"image_memory_mib,omitempty"`
	SessionCookieName       string              `json:"session_cookie_name"`
	WorkRecommendations     WorkRecommendations `json:"work_recommendations"`
	ProviderKeyFile         string              `json:"provider_key_file"`
	ProviderCredentialsFile string              `json:"provider_credentials_file"`
	TagImageCache           TagImageCache       `json:"tag_image_cache"`
	CoverCache              CoverCache          `json:"cover_cache"`
	Listen                  string              `json:"listen_addr"`
	PublicURL               string              `json:"public_url"`
	DataDir                 string              `json:"data_dir"`
	SetupTokenFile          string              `json:"setup_token_file"`
	Roots                   []Root              `json:"library_roots"`
}

func Load(filename string) (Config, error) {
	var c Config
	if filename == "" {
		return c, errors.New("configuration required: --config or NYAMANGA_CONFIG")
	}
	f, e := os.Open(filename)
	if e != nil {
		return c, fmt.Errorf("open configuration: %w", e)
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if e = d.Decode(&c); e != nil {
		return c, fmt.Errorf("decode configuration: %w", e)
	}
	if e = d.Decode(new(any)); e != io.EOF {
		return c, errors.New("configuration must contain one JSON object")
	}
	if c.TagImageCache.MaxBytes < 0 || c.TagImageCache.MaxFiles < 0 {
		return c, errors.New("tag_image_cache limits must be positive or omitted")
	}
	if c.CoverCache.MaxBytes < 0 || c.CoverCache.MaxFiles < 0 {
		return c, errors.New("cover_cache limits must be positive or omitted")
	}
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	_, port, e := net.SplitHostPort(c.Listen)
	number, portError := strconv.Atoi(port)
	if e != nil || portError != nil || number < 1 || number > 65535 {
		return c, errors.New("invalid listen_addr")
	}
	u, e := url.Parse(c.PublicURL)
	if e != nil || u.Host == "" || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return c, errors.New("public_url must be an HTTP(S) origin")
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	if c.SessionCookieName != "" {
		if len(c.SessionCookieName) > 128 {
			return c, errors.New("invalid session_cookie_name")
		}
		for _, r := range c.SessionCookieName {
			if r <= 32 || r >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}\t", r) {
				return c, errors.New("invalid session_cookie_name")
			}
		}
		if (strings.HasPrefix(c.SessionCookieName, "__Host-") || strings.HasPrefix(c.SessionCookieName, "__Secure-")) && u.Scheme != "https" {
			return c, errors.New("secure cookie prefix requires HTTPS public_url")
		}
	}

	if c.ImageMemoryMiB != 0 && (c.ImageMemoryMiB < 32 || c.ImageMemoryMiB > 8192) {
		return c, errors.New("image_memory_mib must be 32–8192 or omitted")
	}
	if c.DataDir == "" {
		return c, errors.New("data_dir is required")
	}
	base, e := filepath.Abs(filepath.Dir(filename))
	if e != nil {
		return c, e
	}
	resolve := func(p string) string {
		if p == "" || filepath.IsAbs(p) {
			return p
		}
		return filepath.Join(base, p)
	}
	c.WorkRecommendations.Python = resolve(c.WorkRecommendations.Python)
	c.WorkRecommendations.Worker = resolve(c.WorkRecommendations.Worker)
	c.WorkRecommendations.ModelsDir = resolve(c.WorkRecommendations.ModelsDir)
	configured := 0
	for _, p := range []string{c.WorkRecommendations.Python, c.WorkRecommendations.Worker, c.WorkRecommendations.ModelsDir} {
		if p != "" {
			configured++
		}
	}
	if configured != 0 && configured != 3 {
		return c, errors.New("work_recommendations requires python, worker and models_dir together")
	}
	c.DataDir = resolve(c.DataDir)
	c.SetupTokenFile = resolve(c.SetupTokenFile)
	c.ProviderKeyFile = resolve(c.ProviderKeyFile)
	c.ProviderCredentialsFile = resolve(c.ProviderCredentialsFile)
	if (c.ProviderKeyFile == "") != (c.ProviderCredentialsFile == "") || (c.ProviderKeyFile != "" && c.ProviderKeyFile == c.ProviderCredentialsFile) {
		return c, errors.New("provider key and credentials require distinct explicit files")
	}
	for _, secret := range []string{c.ProviderKeyFile, c.ProviderCredentialsFile} {
		if secret != "" && (filepath.Clean(secret) == filepath.Clean(c.DataDir) || strings.HasPrefix(filepath.Clean(secret), filepath.Clean(c.DataDir)+string(filepath.Separator)) || secret == c.SetupTokenFile) {
			return c, errors.New("provider secrets must be outside application data and distinct from setup credential")
		}
	}
	seen := map[string]bool{}
	for i := range c.Roots {
		r := &c.Roots[i]
		if r.Key == "" || r.Name == "" || r.Path == "" || seen[r.Key] || strings.ContainsAny(r.Key, "/\\\x00") {
			return c, errors.New("invalid or duplicate library root")
		}
		seen[r.Key] = true
		r.Path = resolve(r.Path)
		for _, secret := range []string{c.ProviderKeyFile, c.ProviderCredentialsFile} {
			if secret != "" && (filepath.Clean(secret) == filepath.Clean(r.Path) || strings.HasPrefix(filepath.Clean(secret), filepath.Clean(r.Path)+string(filepath.Separator))) {
				return c, errors.New("provider secrets must be outside source libraries")
			}
		}
		data, source := filepath.Clean(c.DataDir), filepath.Clean(r.Path)
		if data == source || strings.HasPrefix(data, source+string(filepath.Separator)) || strings.HasPrefix(source, data+string(filepath.Separator)) {
			return c, errors.New("application data and library roots must not overlap")
		}
	}
	if c.Roots == nil {
		c.Roots = []Root{}
	}
	return c, nil
}
