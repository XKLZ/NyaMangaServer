package recommendation

import (
	"encoding/base64"
	"encoding/binary"
	"math"
)

type BookInput struct {
	ID               string                    `json:"id"`
	LibraryID        string                    `json:"library_id"`
	Title            string                    `json:"title"`
	TitleOriginal    string                    `json:"title_jpn"`
	Pages            int                       `json:"pages"`
	Tags             [][2]string               `json:"tags"`
	Languages        []string                  `json:"languages"`
	CustomLabels     []string                  `json:"custom_labels,omitempty"`
	ManualProperties map[string]map[string]any `json:"manual_properties,omitempty"`
}
type WorkInput struct {
	ID                 string   `json:"id"`
	EditionIDs         []string `json:"edition_ids"`
	PreferredEditionID string   `json:"preferred_edition_id"`
}

// PackedVector transmits canonical float32 without expanding it to decimal
// float64 JSON. A 2823-book feature pool stays comfortably below the frame limit.
type PackedVector struct {
	Encoding string `json:"encoding"`
	Data     string `json:"data"`
}

func PackVector(values []float32) PackedVector {
	data := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(v))
	}
	return PackedVector{Encoding: "f32le", Data: base64.StdEncoding.EncodeToString(data)}
}
