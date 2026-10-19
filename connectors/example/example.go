// SPDX-License-Identifier: AGPL-3.0-only

//go:build docs

// Package example is the connector written in docs/writing-a-connector.md: a
// Bluetooth scale that an SDK uploads as NDJSON, one weigh-in per line. It
// compiles only with `-tags docs` and is not imported by connectors/all, so it
// never ships in the binary. Copy it to start a real device connector.
package example

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/soma-intelligence/open-health-sync/internal/connector"
	"github.com/soma-intelligence/open-health-sync/internal/model"
)

// Slug is the provider name: it shows in GET /v1/providers, in raw keys and in
// the provider field of every record.
const Slug = "example_scale"

func init() {
	connector.Register(New())
}

// Connector implements connector.DeviceConnector.
type Connector struct{}

var _ connector.DeviceConnector = (*Connector)(nil)

// New returns the connector.
func New() *Connector { return &Connector{} }

// Slug returns the provider name.
func (c *Connector) Slug() string { return Slug }

// Enabled is always true: a device connector needs no operator keys.
func (c *Connector) Enabled(connector.Config) bool { return true }

// weighIn is one uploaded line.
type weighIn struct {
	MeasuredAt time.Time `json:"measuredAt"`
	Kilograms  float64   `json:"kg"`
	Device     string    `json:"device"`
	Timezone   string    `json:"timezone"`
}

// ParseUpload checks an upload body before it is stored. The upload handler
// keeps the original bytes, so nothing is emitted: only reject what the ETL
// could never read.
func (c *Connector) ParseUpload(_ connector.UploadMeta, body io.Reader, _ connector.RawSink) error {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	lines := 0
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var w weighIn
		if err := json.Unmarshal(sc.Bytes(), &w); err != nil {
			return fmt.Errorf("example_scale: line %d: %w", lines+1, err)
		}
		lines++
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("example_scale: read upload: %w", err)
	}
	if lines == 0 {
		return errors.New("example_scale: upload holds no line")
	}
	return nil
}

// Map is pure: one stored line in, canonical records out. It must be
// deterministic, because the ETL may map the same raw object again.
func (c *Connector) Map(raw connector.RawRecord) (model.MapResult, error) {
	line, err := json.Marshal(raw.Fields)
	if err != nil {
		return model.MapResult{}, err
	}
	var w weighIn
	if err := json.Unmarshal(line, &w); err != nil {
		return model.MapResult{}, fmt.Errorf("example_scale: %w", err)
	}
	if w.MeasuredAt.IsZero() {
		return model.MapResult{}, errors.New("example_scale: missing measuredAt")
	}
	at := w.MeasuredAt.UTC()
	meta := map[string]any{}
	if w.Timezone != "" {
		meta["timezone"] = w.Timezone // the ETL derives the user's local day from it
	}
	return model.MapResult{QuantitySamples: []model.QuantitySample{{
		UserID:       raw.UserID,
		Provider:     Slug,
		SourceName:   "Example Scale",
		DeviceID:     w.Device,
		QuantityType: model.CanonicalQuantityType("body_mass"),
		Value:        w.Kilograms,
		Unit:         "kg",
		StartDate:    at,
		EndDate:      at,
		Metadata:     meta,
		// Identity of the reading: the same weigh-in uploaded twice hashes the
		// same and is stored once.
		ContentHash: model.ComputeContentHash(map[string]string{
			"user_id":       raw.UserID,
			"provider":      Slug,
			"quantity_type": "body_mass",
			"start_date":    at.Format(time.RFC3339Nano),
			"value":         model.CanonicalFloat(w.Kilograms),
		}),
	}}}, nil
}

// DataTypes implements connector.DataTypes: what this connector delivers.
func (c *Connector) DataTypes() []string {
	return []string{"sample", "body_mass"}
}
