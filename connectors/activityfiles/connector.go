// SPDX-License-Identifier: AGPL-3.0-only

// Package activityfiles is the device connector for activity files: FIT, the
// binary format sports watches and bike computers record, and the XML
// formats GPX and TCX. Garmin, COROS, Wahoo, Suunto, Polar, Zwift and Strava
// all let a person download their own files, one by one or as an account
// export, which brings their workouts in without a vendor API.
//
// `ohs import files` (package activityfiles/export finds the files) uploads
// each file as one part to POST /v1/ingest/activity-files: one NDJSON line
// {"type":"fit","data":"<base64>"}. ParseUpload refuses a part whose file is
// not an activity; the stored part keeps the original file byte for byte.
// The ETL later hands the line to Map, which parses the file again and emits
// workouts, route points, and heart rate and power samples. The line format
// and the mapping are documented in docs/connectors/activity-files.md.
package activityfiles

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/soma-intelligence/open-health-sync/internal/connector"
	"github.com/soma-intelligence/open-health-sync/internal/model"
)

// Slug is the provider name of this connector. It is also the source slug of
// a file whose vendor is unknown.
const Slug = "activity_files"

// MaxFileBytes is the largest file a line may carry, once base64 decoded.
// Its base64 line stays under the ETL reader's line cap of 32 MiB.
const MaxFileBytes = 20 << 20

// maxLineBytes bounds the one line of a part: the base64 of MaxFileBytes and
// the JSON around it.
const maxLineBytes = (MaxFileBytes+2)/3*4 + 1024

// File formats, the line's "type" and the part's X-OHS-Type.
const (
	FormatFIT = "fit"
	FormatGPX = "gpx"
	FormatTCX = "tcx"
)

// futureSlack is how far ahead of the server clock an activity may start, the
// ETL validator's own allowance for a future timestamp.
const futureSlack = 48 * time.Hour

func init() {
	connector.Register(New())
}

// Connector implements connector.DeviceConnector for activity files.
type Connector struct {
	now func() time.Time
}

var _ connector.DeviceConnector = (*Connector)(nil)

// New returns a ready activity file connector.
func New() *Connector { return &Connector{now: time.Now} }

// Slug returns "activity_files".
func (c *Connector) Slug() string { return Slug }

// Enabled is always true: a device connector needs no operator keys.
func (c *Connector) Enabled(connector.Config) bool { return true }

// Errors of ParseUpload and Map. Their messages name the problem, never a
// value of the file.
var (
	// ErrInvalidLine reports a part that is not one JSON line with a known
	// type and base64 data.
	ErrInvalidLine = errors.New("activity_files: invalid upload line")
	// ErrNoLines reports a part with no line at all.
	ErrNoLines = errors.New("activity_files: upload holds no line")
	// ErrTooLarge reports a file over MaxFileBytes.
	ErrTooLarge = fmt.Errorf("activity_files: the file is larger than %d bytes", MaxFileBytes)
	// ErrInvalidFile reports a file that does not parse in its format.
	ErrInvalidFile = errors.New("activity_files: unreadable file")
	// ErrNotActivity reports a file that parses but holds no activity: a FIT
	// course, settings or monitoring file, a GPX route without times.
	ErrNotActivity = errors.New("activity_files: the file holds no activity")
)

// Line returns the NDJSON line, newline included, that carries one file in a
// part.
func Line(format string, data []byte) []byte {
	var b bytes.Buffer
	b.Grow(base64.StdEncoding.EncodedLen(len(data)) + 32)
	b.WriteString(`{"type":"`)
	b.WriteString(format)
	b.WriteString(`","data":"`)
	enc := base64.NewEncoder(base64.StdEncoding, &b)
	_, _ = enc.Write(data)
	_ = enc.Close()
	b.WriteString("\"}\n")
	return b.Bytes()
}

// ValidFormat reports whether format is one of FormatFIT, FormatGPX and
// FormatTCX.
func ValidFormat(format string) bool {
	return format == FormatFIT || format == FormatGPX || format == FormatTCX
}

// Info summarizes a parsed file.
type Info struct {
	Format      string
	Start       time.Time // the earliest workout start, UTC
	Workouts    int
	RoutePoints int // route points stored (none for a virtual activity)
	HeartRate   int // heart rate samples stored
	Power       int // power samples stored
}

// Inspect parses a file and summarizes it. It returns ErrTooLarge,
// ErrInvalidFile or ErrNotActivity (wrapped) for a file the server would
// refuse.
func Inspect(format string, data []byte) (Info, error) {
	a, err := parse(format, data)
	if err != nil {
		return Info{}, err
	}
	return a.info(), nil
}

// ParseUpload checks a decompressed part: exactly one line, a JSON object
// whose type is fit, gpx or tcx and equal to X-OHS-Type, whose data is the
// base64 of a file of at most MaxFileBytes that parses as an activity
// starting no more than 48 hours after now. The upload handler stores the
// original bytes verbatim, so emit is not used and may be nil.
func (c *Connector) ParseUpload(meta connector.UploadMeta, body io.Reader, _ connector.RawSink) error {
	l, err := readOneLine(body)
	if err != nil {
		return err
	}
	if l.Type != meta.Type {
		return fmt.Errorf("%w: the line's type %q is not X-OHS-Type", ErrInvalidLine, l.Type)
	}
	data, err := l.decode()
	if err != nil {
		return err
	}
	a, err := parse(l.Type, data)
	if err != nil {
		return err
	}
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	if a.start().After(now().Add(futureSlack)) {
		return fmt.Errorf("%w: the activity starts more than 48 hours from now", ErrNotActivity)
	}
	return nil
}

// Map parses the file of one stored line and returns its workouts, route
// points and samples.
func (c *Connector) Map(raw connector.RawRecord) (model.MapResult, error) {
	format, _ := raw.Fields["type"].(string)
	data, _ := raw.Fields["data"].(string)
	l := line{Type: format, Data: data}
	if err := l.check(); err != nil {
		return model.MapResult{}, err
	}
	b, err := l.decode()
	if err != nil {
		return model.MapResult{}, err
	}
	a, err := parse(format, b)
	if err != nil {
		return model.MapResult{}, err
	}
	return a.mapResult(raw.UserID), nil
}

// line is the one line of a part.
type line struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func (l line) check() error {
	if !ValidFormat(l.Type) {
		return fmt.Errorf("%w: type must be fit, gpx or tcx", ErrInvalidLine)
	}
	if l.Data == "" {
		return fmt.Errorf("%w: data is empty", ErrInvalidLine)
	}
	return nil
}

func (l line) decode() ([]byte, error) {
	if base64.StdEncoding.DecodedLen(len(l.Data)) > MaxFileBytes+2 {
		return nil, ErrTooLarge
	}
	b, err := base64.StdEncoding.DecodeString(l.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: data is not standard base64", ErrInvalidLine)
	}
	if len(b) > MaxFileBytes {
		return nil, ErrTooLarge
	}
	return b, nil
}

// readOneLine reads the part's single non-blank line, of at most
// maxLineBytes, and checks its fields.
func readOneLine(body io.Reader) (line, error) {
	r := bufio.NewReaderSize(body, 64*1024)
	var (
		l     line
		lines int
		buf   []byte
	)
	for {
		chunk, err := r.ReadSlice('\n')
		if len(buf)+len(chunk) > maxLineBytes+1 { // +1: the newline itself
			return line{}, fmt.Errorf("%w: the line is longer than %d bytes", ErrInvalidLine, maxLineBytes)
		}
		buf = append(buf, chunk...)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return line{}, fmt.Errorf("activity_files: read upload: %w", err)
		}
		if trimmed := bytes.TrimSpace(buf); len(trimmed) > 0 {
			lines++
			if lines > 1 {
				return line{}, fmt.Errorf("%w: a part holds one file, on one line", ErrInvalidLine)
			}
			if trimmed[0] != '{' || json.Unmarshal(trimmed, &l) != nil {
				return line{}, fmt.Errorf("%w: line 1 is not a JSON object", ErrInvalidLine)
			}
			if err := l.check(); err != nil {
				return line{}, err
			}
		}
		buf = buf[:0]
		if errors.Is(err, io.EOF) {
			break
		}
	}
	if lines == 0 {
		return line{}, ErrNoLines
	}
	return l, nil
}
