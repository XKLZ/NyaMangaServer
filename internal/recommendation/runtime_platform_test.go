package recommendation

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeMissingModelsRemainUnconfiguredWithoutExecutingWorker(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "worker.py")
	python := filepath.Join(root, "python")
	for _, path := range []string{script, python} {
		if e := os.WriteFile(path, []byte("must never execute"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	models := filepath.Join(root, "models")
	if e := os.Mkdir(models, 0700); e != nil {
		t.Fatal(e)
	}
	w := NewWorker(WorkerConfig{Python: python, Script: script, Models: models, Cache: filepath.Join(root, "cache")})
	if state := w.ConfigurationState(); state != "not_configured" {
		t.Fatalf("empty model mount: %s", state)
	}
	for _, key := range []string{"sscd_disc_mixup-skew320-opset17.onnx", "minilm/model.onnx", "minilm/tokenizer.json", "minilm/sentence_bert_config.json", "minilm/1_Pooling/config.json"} {
		path := filepath.Join(models, filepath.FromSlash(key))
		if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte("anonymous metadata-only placeholder"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	if state := w.ConfigurationState(); state != "configured_unverified" {
		t.Fatalf("metadata must not assert models verified: %s", state)
	}
	if w.process != nil {
		t.Fatal("read-only status launched worker")
	}
}
