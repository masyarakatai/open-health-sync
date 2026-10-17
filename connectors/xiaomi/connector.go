// SPDX-License-Identifier: AGPL-3.0-only

// Package xiaomi is the Xiaomi device connector. Xiaomi publishes no health
// API, but every Mi Fitness account can download a copy of its data from the
// Xiaomi account privacy center: a set of CSV files, one row per reading,
// with the reading itself as a JSON object. `ohs import mi-fitness` (package
// xiaomi/export) turns those rows into NDJSON lines and uploads them in gzip
// parts to POST /v1/ingest/xiaomi. The parts are stored verbatim; the ETL
// later hands each line to Map.
//
// Data a Xiaomi app writes to Health Connect or Apple Health arrives through
// those connectors instead, attributed to the source slug "xiaomi". The line
// format is documented in docs/connectors/xiaomi.md.
package xiaomi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/soma-intelligence/open-health-sync/internal/connector"
	"github.com/soma-intelligence/open-health-sync/internal/model"
)

// Slug is the provider name of this connector, and the source slug of every
// record it maps.
const Slug = "xiaomi"

// MaxLineBytes is the longest line an upload may hold. It matches the ETL
// reader's line cap, so a part accepted here is never one the ETL cannot read.
const MaxLineBytes = 32 * 1024 * 1024

// Tables are the export tables a line can come from.
const (
	TableFitnessData = "fitness_data"
	TableSportRecord = "sport_record"
)

func init() {
	connector.Register(New())
}

// Connector implements connector.DeviceConnector for Xiaomi exports.
type Connector struct{}

var _ connector.DeviceConnector = (*Connector)(nil)

// New returns a ready Xiaomi connector.
func New() *Connector { return &Connector{} }

// Slug returns "xiaomi".
func (c *Connector) Slug() string { return Slug }

// Enabled is always true: a device connector needs no operator keys.
func (c *Connector) Enabled(connector.Config) bool { return true }

// Map turns one uploaded line into canonical records. A line of a table or
// key this connector does not store gives an empty result and no error.
func (c *Connector) Map(raw connector.RawRecord) (model.MapResult, error) {
	res, err := mapLine(raw.Fields, raw.UserID)
	if err != nil || res == nil {
		return model.MapResult{}, err
	}
	return *res, nil
}

// ErrInvalidLine reports an uploaded line that is not a JSON object with a
// table and a key.
var ErrInvalidLine = errors.New("xiaomi: invalid upload line")

// ErrNoLines reports an upload with no line at all.
var ErrNoLines = errors.New("xiaomi: upload holds no line")

// ParseUpload validates a decompressed upload body: every non-blank line must
// be a JSON object no longer than MaxLineBytes with a string table and key,
// and there must be at least one. The upload handler stores the original
// bytes verbatim, so emit is not used and may be nil.
func (c *Connector) ParseUpload(_ connector.UploadMeta, body io.Reader, _ connector.RawSink) error {
	r := bufio.NewReaderSize(body, 64*1024)
	var (
		line  []byte
		lines int
		num   int
	)
	for {
		chunk, err := r.ReadSlice('\n')
		if len(line)+len(chunk) > MaxLineBytes+1 { // +1: the newline itself
			return fmt.Errorf("%w: line %d is longer than %d bytes", ErrInvalidLine, num+1, MaxLineBytes)
		}
		line = append(line, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("xiaomi: read upload: %w", err)
		}
		if len(line) > 0 {
			num++
			if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
				var head struct {
					Table *string `json:"table"`
					Key   *string `json:"key"`
				}
				if trimmed[0] != '{' || json.Unmarshal(trimmed, &head) != nil {
					return fmt.Errorf("%w: line %d is not a JSON object", ErrInvalidLine, num)
				}
				if head.Table == nil || *head.Table == "" || head.Key == nil || *head.Key == "" {
					return fmt.Errorf("%w: line %d needs a table and a key", ErrInvalidLine, num)
				}
				lines++
			}
		}
		line = line[:0]
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if lines == 0 {
		return ErrNoLines
	}
	return nil
}
