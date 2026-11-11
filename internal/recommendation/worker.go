// Package recommendation owns optional, local-only Work recommendation inference.
// The worker is serialized and isolated from HTTP, SQL and experimental modules.
package recommendation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"nyamangaserver.local/server/internal/appdata"
	"os"
	"os/exec"
	"path/filepath"
)

const Protocol = "work-recommendation-worker-v1"
const MaxMessage = 32 << 20

var ErrUnavailable = errors.New("work_recommendation_unavailable")
var ErrWorker = errors.New("work_recommendation_worker_failed")
var ErrPoolLost = errors.New("discovery_pool_lost")
var ErrSeriesCheckpoint = errors.New("series_checkpoint_invalid")
var ErrWorkerBudget = errors.New("worker_resource_budget_exceeded")

type WorkerConfig struct{ Python, Script, Models, Cache string }
type Worker struct {
	config   WorkerConfig
	gate     chan struct{}
	process  *process
	previous Resources
}
type process struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
	done   chan error
}
type Resources struct {
	CacheHits        int     `json:"cache_hits"`
	ComputedInputs   int     `json:"computed_inputs"`
	ModelLoads       int     `json:"model_loads"`
	InferenceSeconds float64 `json:"inference_seconds"`
	OperationSeconds float64 `json:"operation_seconds"`
	PeakRSS          int64   `json:"peak_rss_bytes"`
}
type reply struct {
	Protocol  string          `json:"protocol"`
	Result    json.RawMessage `json:"result"`
	Error     string          `json:"error"`
	Resources Resources       `json:"resources"`
}

func NewWorker(config WorkerConfig) *Worker {
	return &Worker{config: config, gate: make(chan struct{}, 1)}
}
func (w *Worker) Enabled() bool {
	return w != nil && w.config.Python != "" && w.config.Script != "" && w.config.Models != "" && w.config.Cache != ""
}

func (w *Worker) start() error {
	if !w.Enabled() {
		return ErrUnavailable
	}
	cmd := exec.Command(w.config.Python, "-B", w.config.Script, "--models", w.config.Models, "--cache", w.config.Cache)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "HF_HUB_OFFLINE=1", "TRANSFORMERS_OFFLINE=1", "HF_HUB_DISABLE_IMPLICIT_TOKEN=1", "HF_HUB_DISABLE_TELEMETRY=1", "DO_NOT_TRACK=1", "TOKENIZERS_PARALLELISM=false", "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1", "MKL_NUM_THREADS=1", "VECLIB_MAXIMUM_THREADS=1", "NUMEXPR_NUM_THREADS=1")
	input, e := cmd.StdinPipe()
	if e != nil {
		return ErrUnavailable
	}
	output, e := cmd.StdoutPipe()
	if e != nil {
		input.Close()
		return ErrUnavailable
	}
	// Runtime diagnostics can contain file names. Never forward private stderr to logs.
	cmd.Stderr = io.Discard
	if e = cmd.Start(); e != nil {
		input.Close()
		output.Close()
		return ErrUnavailable
	}
	p := &process{cmd: cmd, input: input, output: bufio.NewReaderSize(output, 64<<10), done: make(chan error, 1)}
	w.process = p
	go func() { p.done <- cmd.Wait() }()
	return nil
}
func (w *Worker) stop() {
	p := w.process
	if p == nil {
		return
	}
	w.process = nil
	w.previous = Resources{}
	p.input.Close()
	_ = p.cmd.Process.Kill()
	<-p.done
}
func (w *Worker) Close() error {
	if w == nil {
		return nil
	}
	w.gate <- struct{}{}
	defer func() { <-w.gate }()
	w.stop()
	return nil
}

// Call cancels queued work without starting a process. Cancellation during IO
// kills and reaps the worker before releasing ownership to the next caller.
func (w *Worker) Call(ctx context.Context, operation string, input any, output any) (Resources, error) {
	var resources Resources
	if !w.Enabled() {
		return resources, ErrUnavailable
	}
	select {
	case w.gate <- struct{}{}:
	case <-ctx.Done():
		return resources, ctx.Err()
	}
	defer func() { <-w.gate }()
	if e := ctx.Err(); e != nil {
		return resources, e
	}
	raw, e := json.Marshal(input)
	if e != nil {
		return resources, e
	}
	var message map[string]json.RawMessage
	if e = json.Unmarshal(raw, &message); e != nil {
		return resources, e
	}
	if message == nil {
		message = map[string]json.RawMessage{}
	}
	message["protocol"], _ = json.Marshal(Protocol)
	message["operation"], _ = json.Marshal(operation)
	raw, e = json.Marshal(message)
	if e != nil || len(raw)+1 > MaxMessage {
		return resources, ErrWorker
	}
	if w.process == nil {
		if e = w.start(); e != nil {
			return resources, e
		}
	}
	p := w.process
	type response struct {
		data []byte
		err  error
	}
	completed := make(chan response, 1)
	go func() {
		if _, err := p.input.Write(append(raw, '\n')); err != nil {
			completed <- response{err: err}
			return
		}
		var data []byte
		for {
			part, err := p.output.ReadSlice('\n')
			if len(data)+len(part) > MaxMessage {
				completed <- response{err: ErrWorker}
				return
			}
			data = append(data, part...)
			if err == bufio.ErrBufferFull {
				continue
			}
			completed <- response{data: data, err: err}
			return
		}
	}()
	var result response
	select {
	case <-ctx.Done():
		w.stop()
		<-completed
		return resources, ctx.Err()
	case result = <-completed:
	}
	if result.err != nil {
		w.stop()
		return resources, ErrWorker
	}
	var r reply
	decoder := json.NewDecoder(bytes.NewReader(result.data))
	if decoder.Decode(&r) != nil || r.Protocol != Protocol {
		w.stop()
		return resources, ErrWorker
	}
	if e = decoder.Decode(new(any)); e != io.EOF {
		w.stop()
		return resources, ErrWorker
	}
	delta := r.Resources
	delta.CacheHits -= w.previous.CacheHits
	delta.ComputedInputs -= w.previous.ComputedInputs
	delta.ModelLoads -= w.previous.ModelLoads
	delta.InferenceSeconds -= w.previous.InferenceSeconds
	w.previous = r.Resources
	if r.Error != "" || len(r.Result) == 0 {
		w.stop()
		switch r.Error {
		case "discovery_pool_missing", "discovery_pool_changed", "series_pool_missing", "series_pool_changed":
			return delta, errors.Join(ErrWorker, ErrPoolLost)
		case "invalid_series_checkpoint":
			return delta, errors.Join(ErrWorker, ErrSeriesCheckpoint)
		case "worker_resource_budget_exceeded":
			return delta, errors.Join(ErrWorker, ErrWorkerBudget)
		}
		return delta, ErrWorker
	}
	if e = json.Unmarshal(r.Result, output); e != nil {
		w.stop()
		return delta, ErrWorker
	}
	return delta, nil
}

// ConfigurationState performs only bounded filesystem metadata checks, never
// launches Python, reads model bytes or treats mere deployment paths as ready.
func (w *Worker) ConfigurationState() string {
	if !w.Enabled() {
		return "not_configured"
	}
	for _, p := range []string{w.config.Python, w.config.Script} {
		i, e := os.Stat(p)
		if e != nil || !i.Mode().IsRegular() {
			return "unavailable"
		}
	}
	i, e := os.Stat(w.config.Models)
	if e != nil || !i.IsDir() {
		if os.IsNotExist(e) {
			return "not_configured"
		}
		return "unavailable"
	}
	for _, key := range []string{"sscd_disc_mixup-skew320-opset17.onnx", "minilm/model.onnx", "minilm/tokenizer.json", "minilm/sentence_bert_config.json", "minilm/1_Pooling/config.json"} {
		path := filepath.Join(w.config.Models, filepath.FromSlash(key))
		if appdata.NoSymlinks(path) != nil {
			return "unavailable"
		}
		i, e := os.Stat(path)
		if os.IsNotExist(e) {
			return "not_configured"
		}
		if e != nil || !i.Mode().IsRegular() || i.Size() == 0 {
			return "unavailable"
		}
	}
	return "configured_unverified"
}
