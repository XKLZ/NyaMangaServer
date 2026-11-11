package maintenance

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

// Validate existing payloads only. This function never imports or calls inference.
func validHealthFeature(raw, expectedRaw []byte, algorithm string) bool {
	if len(raw) > 65536 {
		return false
	}
	type header struct {
		Fingerprint string          `json:"fingerprint"`
		Model       string          `json:"model_sha256"`
		Version     string          `json:"algorithm_version"`
		Digest      string          `json:"feature_digest"`
		Input       string          `json:"input_sha256"`
		Feature     json.RawMessage `json:"feature"`
	}
	var actual, expected header
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(expectedRaw, &expected) != nil {
		return false
	}
	if actual.Fingerprint != expected.Fingerprint || actual.Model != expected.Model || actual.Version != expected.Version || actual.Digest != expected.Digest || len(actual.Input) != 64 {
		return false
	}
	if actual.Version != healthFeatureVersion(algorithm) {
		return false
	}

	// Feature payloads are scalar hex strings or numeric arrays; compacting keeps
	// Python's persisted numeric tokens, avoiding a second float normalization.
	var compact bytes.Buffer
	if json.Compact(&compact, actual.Feature) != nil {
		return false
	}
	if fmt.Sprintf("%x", sha256.Sum256(compact.Bytes())) != actual.Digest {
		return false
	}
	fingerprint, _ := json.Marshal([]string{actual.Version, actual.Model, actual.Input})
	if fmt.Sprintf("%x", sha256.Sum256(fingerprint)) != actual.Fingerprint {
		return false
	}
	if algorithm == "phash" {
		var value string
		if json.Unmarshal(actual.Feature, &value) != nil || len(value) != 16 {
			return false
		}
		_, e := hex.DecodeString(value)
		return e == nil
	}
	size := 384
	if algorithm == "sscd" {
		size = 512
	}
	var vector []float64
	if json.Unmarshal(actual.Feature, &vector) != nil || len(vector) != size {
		return false
	}
	var norm float64
	for _, v := range vector {
		f := float64(float32(v))
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return false
		}
		norm += f * f
	}
	norm = math.Sqrt(norm)
	return norm >= .999 && norm <= 1.001
}

func checkHealthSeriesBlock(raw, extra []byte) (string, string) {
	var expected struct {
		Records int    `json:"records"`
		Version string `json:"execution_version"`
	}
	if json.Unmarshal(extra, &expected) != nil {
		return "manual", "checkpoint_descriptor_invalid"
	}
	if expected.Version != "series-signals-v1" {
		return "not_checked", "checkpoint_version_unsupported"
	}
	if len(raw) > 8<<20 {
		return "not_checked", "resource_limit"
	}
	z, e := gzip.NewReader(bytes.NewReader(raw))
	if e != nil {
		return "confirmed", "checkpoint_gzip_invalid"
	}
	defer z.Close()
	plain, e := io.ReadAll(io.LimitReader(z, 16<<20+1))
	if e != nil {
		return "confirmed", "checkpoint_gzip_invalid"
	}
	if len(plain) > 16<<20 {
		return "not_checked", "resource_limit"
	}
	var v struct {
		Signals []struct {
			A      string          `json:"a"`
			B      string          `json:"b"`
			Key    string          `json:"key"`
			Signal json.RawMessage `json:"signal"`
		} `json:"signals"`
		Computed int64                      `json:"computed_pairs"`
		Reused   int64                      `json:"reused_pairs"`
		Routes   map[string]json.RawMessage `json:"routes"`
	}
	if json.Unmarshal(plain, &v) != nil || len(v.Signals) > 512 || len(v.Signals) != expected.Records || v.Computed < 0 || v.Reused < 0 {
		return "confirmed", "checkpoint_payload_invalid"
	}
	seen := map[string]bool{}
	for _, signal := range v.Signals {
		if signal.A == "" || signal.B == "" || signal.A == signal.B || signal.Key == "" || seen[signal.Key] || !json.Valid(signal.Signal) {
			return "confirmed", "checkpoint_signal_invalid"
		}
		seen[signal.Key] = true
	}
	return "normal", "checkpoint_verified"
}

func healthFeatureVersion(algorithm string) string {
	switch algorithm {
	case "phash":
		return "phash-dct32-low8-dc-excluded-v1"
	case "sscd":
		return "sscd-skew320-bilinear-imagenet-v1"
	case "minilm":
		return "minilm-preferred-jpn-raw-title-128-mean-l2-v1"
	}
	return "unsupported"
}
