package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeWorker(t *testing.T) *Worker {
	t.Helper()
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Skip("Python not installed")
	}
	d := t.TempDir()
	script := filepath.Join(d, "worker.py")
	body := `import json,sys,time
for line in sys.stdin:
 r=json.loads(line)
 if r['operation']=='hang': time.sleep(60)
 if r['operation']=='bad': print('broken',flush=True);continue
 print(json.dumps({'protocol':'work-recommendation-worker-v1','result':{'ok':True},'resources':{'cache_hits':1}}),flush=True)
`
	if e = os.WriteFile(script, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	w := NewWorker(WorkerConfig{Python: python, Script: script, Models: d, Cache: filepath.Join(d, "cache")})
	t.Cleanup(func() { w.Close() })
	return w
}
func TestOptionalWorker(t *testing.T) {
	w := NewWorker(WorkerConfig{})
	var out map[string]any
	if _, e := w.Call(context.Background(), "capabilities", nil, &out); !errors.Is(e, ErrUnavailable) {
		t.Fatal(e)
	}
}
func TestWorkerRoundtripAndProtocolRecovery(t *testing.T) {
	w := fakeWorker(t)
	var out struct {
		OK bool `json:"ok"`
	}
	resources, e := w.Call(context.Background(), "capabilities", map[string]any{}, &out)
	if e != nil || !out.OK || resources.CacheHits != 1 {
		t.Fatal(resources, out, e)
	}
	if _, e = w.Call(context.Background(), "bad", nil, &out); !errors.Is(e, ErrWorker) {
		t.Fatal(e)
	}
	if _, e = w.Call(context.Background(), "capabilities", nil, &out); e != nil {
		t.Fatal(e)
	}
}
func TestCancelReapsOldWorkerBeforeRestart(t *testing.T) {
	w := fakeWorker(t)
	var out map[string]any
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, e := w.Call(ctx, "hang", nil, &out); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal(e)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("cancellation did not reap promptly")
	}
	if _, e := w.Call(context.Background(), "capabilities", nil, &out); e != nil {
		t.Fatal(e)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, e := w.Call(canceled, "hang", nil, &out); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestMessageLimit(t *testing.T) {
	w := fakeWorker(t)
	var out map[string]any
	if _, e := w.Call(context.Background(), "capabilities", map[string]string{"large": strings.Repeat("a", MaxMessage)}, &out); !errors.Is(e, ErrWorker) {
		t.Fatal(e)
	}
	if w.process != nil {
		t.Fatal("oversized request started process")
	}
}

func TestPackedAnonymousPoolFitsProtocol(t *testing.T) {
	books := make([]BookInput, 2823)
	works := make([]WorkInput, 2823)
	sscd := make(map[string]PackedVector)
	minilm := make(map[string]PackedVector)
	for i := range books {
		id := fmt.Sprintf("anonymous-%d", i)
		books[i] = BookInput{ID: id, LibraryID: "anonymous", Title: "匿名标题 Example", Pages: 32, Tags: [][2]string{{"artist", "anonymous"}}}
		works[i] = WorkInput{ID: id, EditionIDs: []string{id}, PreferredEditionID: id}
		a := make([]float32, 512)
		a[0] = 1
		b := make([]float32, 384)
		b[0] = 1
		sscd[id] = PackVector(a)
		minilm[id] = PackVector(b)
	}
	wire, e := json.Marshal(map[string]any{"books": books, "works": works, "features": map[string]any{"sscd": sscd, "minilm": minilm}})
	if e != nil || len(wire) >= MaxMessage {
		t.Fatal(len(wire), e)
	}
	t.Logf("2823 anonymous records: %d bytes, frame budget %d", len(wire), MaxMessage)
}

func TestWorkerResourceDeltas(t *testing.T) {
	w := fakeWorker(t)
	var out map[string]any
	first, e := w.Call(context.Background(), "capabilities", nil, &out)
	if e != nil || first.CacheHits != 1 {
		t.Fatal(first, e)
	}
	next, e := w.Call(context.Background(), "capabilities", nil, &out)
	if e != nil || next.CacheHits != 0 {
		t.Fatal("cumulative counters counted twice", next, e)
	}
	if _, e = w.Call(context.Background(), "bad", nil, &out); e == nil {
		t.Fatal("bad protocol accepted")
	}
	restarted, e := w.Call(context.Background(), "capabilities", nil, &out)
	if e != nil || restarted.CacheHits != 1 {
		t.Fatal("worker restart did not reset baseline", restarted, e)
	}
}

func TestWorkerPoolLossHasSafeRecoverableCategory(t *testing.T) {
	w := fakeWorker(t)
	script := `import json,sys
for line in sys.stdin:
 r=json.loads(line)
 print(json.dumps({'protocol':'work-recommendation-worker-v1','error':r['operation']}),flush=True)
`
	if e := os.WriteFile(w.config.Script, []byte(script), 0600); e != nil {
		t.Fatal(e)
	}
	for _, code := range []string{"discovery_pool_missing", "discovery_pool_changed", "private filename and credentials"} {
		var out any
		_, e := w.Call(context.Background(), code, nil, &out)
		if !errors.Is(e, ErrWorker) || errors.Is(e, ErrPoolLost) != (code != "private filename and credentials") {
			t.Fatal(code, e)
		}
		if strings.Contains(e.Error(), "credentials") {
			t.Fatal("worker leaked raw errors")
		}
	}
}
