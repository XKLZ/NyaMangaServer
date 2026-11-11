package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitConfiguration(t *testing.T) {
	if _, e := Load(""); e == nil {
		t.Fatal("missing configuration accepted")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	write := func(s string) {
		t.Helper()
		if e := os.WriteFile(p, []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(`{"public_url":"http://localhost:8080","data_dir":"data","setup_token_file":"secrets/setup","library_roots":[]}`)
	c, e := Load(p)
	if e != nil {
		t.Fatal(e)
	}
	if c.DataDir != filepath.Join(dir, "data") || c.SetupTokenFile != filepath.Join(dir, "secrets/setup") {
		t.Fatal("relative paths not based on config directory")
	}
	for _, s := range []string{`{"public_url":"http://host","data_dir":"data","library_roots":[{"root_key":"overlap","display_name":"Bad","path":"data/library"}]}`, `{"public_url":"http://host","data_dir":"data","listen_addr":"localhost:abc"}`, `{"public_url":"http://host","data_dir":"data","listen_addr":"localhost:65536"}`, `{}`, `{"public_url":"https://host/path","data_dir":"data"}`, `{"public_url":"http://host","data_dir":"data","typo":true}`, `{"public_url":"http://host","data_dir":"data"}{}`, `{"public_url":"http://host","data_dir":"data","library_roots":[{"root_key":"a","display_name":"A","path":"a"},{"root_key":"a","display_name":"B","path":"b"}]}`} {
		write(s)
		if _, e = Load(p); e == nil {
			t.Fatal("invalid configuration accepted", s)
		}
	}
}

func TestCoverCacheConfiguration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	for _, test := range []struct {
		cache string
		bytes int64
		files int
		valid bool
	}{
		{"{}", 2 << 30, 10000, true}, {`{"max_bytes":128,"max_files":2}`, 128, 2, true}, {`{"max_bytes":-1}`, 0, 0, false}, {`{"max_files":-1}`, 0, 0, false},
	} {
		text := `{"public_url":"http://localhost","data_dir":"data","cover_cache":` + test.cache + `}`
		if e := os.WriteFile(p, []byte(text), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := Load(p)
		if (e == nil) != test.valid {
			t.Fatal("cover cache validation", e)
		}
		if e == nil {
			b, n := c.CoverCache.Limits()
			if b != test.bytes || n != test.files {
				t.Fatal("cache limits", b, n)
			}
		}
	}
}

func TestProviderPathsAreExplicitAndSeparate(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "config.json")
	for _, test := range []struct {
		key, credentials string
		valid            bool
	}{
		{"secrets/key", "secrets/providers", true}, {"secrets/key", "", false}, {"secrets/key", "secrets/key", false}, {"data/key", "secrets/providers", false}, {"library/key", "secrets/providers", false}, {"secrets/setup", "secrets/providers", false},
	} {
		data := `{"public_url":"http://localhost","data_dir":"data","setup_token_file":"secrets/setup","provider_key_file":"` + test.key + `","provider_credentials_file":"` + test.credentials + `","library_roots":[{"root_key":"anon","display_name":"Anonymous","path":"library"}]}`
		if e := os.WriteFile(filename, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := Load(filename)
		if (e == nil) != test.valid {
			t.Fatal(test, e)
		}
		if e == nil && c.ProviderKeyFile != filepath.Join(dir, test.key) {
			t.Fatal(c)
		}
	}
}

func TestOptionalRecommendationConfiguration(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "config.json")
	for _, test := range []struct {
		extra string
		valid bool
	}{
		{``, true}, {`,"work_recommendations":{"python":"python","worker":"worker.py","models_dir":"models"}`, true}, {`,"work_recommendations":{"python":"python"}`, false},
	} {
		if e := os.WriteFile(p, []byte(`{"public_url":"http://localhost","data_dir":"data"`+test.extra+`}`), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := Load(p)
		if (e == nil) != test.valid {
			t.Fatal(test, e)
		}
		if e == nil && c.WorkRecommendations.Python != "" && c.WorkRecommendations.Python != filepath.Join(d, "python") {
			t.Fatal(c.WorkRecommendations)
		}
	}
}

func TestSessionCookieNameValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	for _, test := range []struct {
		name  string
		valid bool
	}{{"", true}, {"nyamanga_recommendation_pilot", true}, {"bad name", false}, {"bad=name", false}, {"bad;name", false}, {`bad\name`, false}, {"中文", false}, {"__Host-pilot", false}, {"__Secure-pilot", false}} {
		raw, _ := json.Marshal(map[string]any{"public_url": "http://127.0.0.1:5174", "data_dir": "data", "session_cookie_name": test.name})
		if e := os.WriteFile(p, raw, 0600); e != nil {
			t.Fatal(e)
		}
		_, e := Load(p)
		if (e == nil) != test.valid {
			t.Fatal(test, e)
		}
	}
}

func TestTagImageCacheConfiguration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	for _, tc := range []struct {
		extra string
		valid bool
		bytes int64
		files int
	}{{"", true, 256 << 20, 10000}, {`,"tag_image_cache":{"max_bytes":4096,"max_files":2}`, true, 4096, 2}, {`,"tag_image_cache":{"max_bytes":-1}`, false, 0, 0}, {`,"tag_image_cache":{"max_files":-1}`, false, 0, 0}} {
		raw := `{"public_url":"http://localhost","data_dir":"data"` + tc.extra + `}`
		if e := os.WriteFile(p, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		c, e := Load(p)
		if !tc.valid {
			if e == nil {
				t.Fatal("negative budget accepted")
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		b, n := c.TagImageCache.Limits()
		if b != tc.bytes || n != tc.files {
			t.Fatal(b, n)
		}
	}
}
