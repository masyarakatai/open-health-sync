// SPDX-License-Identifier: AGPL-3.0-only

package etl

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gzipRepeated builds a gzip body of n copies of one line. A few kilobytes of
// gzip expand to megabytes here, which is the shape of the attack: the upload
// handler hashes the compressed bytes and deliberately never decompresses, so
// nothing before the ETL sees the expansion.
func gzipRepeated(line string, n int) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	for range n {
		mustGzip(gz.Write([]byte(line + "\n")))
	}
	mustGzip(0, gz.Close())
	return buf.Bytes()
}

func fakeGetterFor(key string, data []byte) *fakeGetter {
	return &fakeGetter{
		data: map[string][]byte{key: data},
	}
}

// A gzip whose decompressed size runs away must stop at the ceiling instead of
// expanding into memory until the single ohs binary is
// OOM-killed.
func TestReaderStopsAtDecompressedByteCap(t *testing.T) {
	// ~40 bytes per line x 200k lines = ~8 MB decompressed, from a few KB of gzip.
	line := `{"a":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
	data := gzipRepeated(line, 200_000)
	require.Less(t, len(data), 200*1024, "the compressed body must stay tiny, that is the bomb")

	fake := fakeGetterFor("test/bomb.ndjson.gz", data)
	// 1 MB decompressed ceiling, generous line cap, generous record cap.
	reader := NewReaderWithLimits(fake, DefaultMaxLineBytes, 1024*1024, DefaultMaxRecords)

	result, err := reader.ReadNDJSON(context.Background(), "test/bomb.ndjson.gz")

	require.NoError(t, err, "hitting the cap is a truncation, not a hard read error")
	assert.True(t, result.ScanTruncated, "the cap must be reported, never a silent EOF")
	assert.Equal(t, TruncationLimit, result.Truncation,
		"a cap is deterministic: re-reading hits it again, so it must not look retryable")
	assert.Less(t, result.RecordCount, 200_000,
		"the read must stop well before the whole bomb is materialized")
	assert.Greater(t, result.RecordCount, 0, "the parsed head is still returned")

	// Control: the cap is what stops it. Without one, the whole expansion lands
	// in memory, which is exactly the behaviour this closes.
	uncapped := NewReaderWithLimits(fakeGetterFor("test/bomb.ndjson.gz", data),
		DefaultMaxLineBytes, 1<<40, DefaultMaxRecords)
	all, err := uncapped.ReadNDJSON(context.Background(), "test/bomb.ndjson.gz")
	require.NoError(t, err)
	assert.False(t, all.ScanTruncated)
	assert.Equal(t, 200_000, all.RecordCount)
}

// The default constructor must carry the ceiling. A cap that only exists when a
// test asks for it protects nothing in production.
func TestNewReaderCarriesTheDefaultCaps(t *testing.T) {
	r := NewReader(&fakeGetter{})
	assert.Equal(t, DefaultMaxDecompressedBytes, r.maxDecompressedBytes)
	assert.Equal(t, DefaultMaxRecords, r.maxRecords)
	assert.Equal(t, DefaultMaxLineBytes, r.maxLineBytes)
}

// A stream of millions of tiny records stays under the byte cap, so the record
// cap is the one that bounds it.
func TestReaderStopsAtRecordCap(t *testing.T) {
	data := gzipRepeated(`{"a":1}`, 5_000)
	fake := fakeGetterFor("test/many.ndjson.gz", data)
	reader := NewReaderWithLimits(fake, DefaultMaxLineBytes, DefaultMaxDecompressedBytes, 100)

	result, err := reader.ReadNDJSON(context.Background(), "test/many.ndjson.gz")

	require.NoError(t, err)
	assert.True(t, result.ScanTruncated)
	assert.Equal(t, TruncationLimit, result.Truncation)
	assert.Equal(t, 100, result.RecordCount)
	assert.Len(t, result.Records, 100)
}

// A body that ends mid-stream is a different animal from a cap: the object in
// the bucket is usually intact, so it must read as retryable.
func TestReaderClassifiesTruncatedStreamAsRetryable(t *testing.T) {
	full := gzipLines([]string{`{"a":1}`, `{"b":2}`, `{"c":3}`})
	truncated := full[:len(full)-5]

	fake := fakeGetterFor("test/trunc.ndjson.gz", truncated)
	reader := NewReader(fake)

	result, err := reader.ReadNDJSON(context.Background(), "test/trunc.ndjson.gz")

	require.NoError(t, err)
	assert.True(t, result.ScanTruncated)
	assert.Equal(t, TruncationStream, result.Truncation,
		"a mid-stream abort must be retryable, not filed as a deterministic partial")
}

// An over-cap line is deterministic: every re-read produces the same abort.
func TestReaderClassifiesOverCapLineAsDeterministic(t *testing.T) {
	big := strings.Repeat("x", 2*1024*1024)
	data := gzipLines([]string{`{"a":1}`, fmt.Sprintf(`{"big":"%s"}`, big)})

	fake := fakeGetterFor("test/oversized.ndjson.gz", data)
	reader := NewReaderWithMaxLine(fake, 1024*1024)

	result, err := reader.ReadNDJSON(context.Background(), "test/oversized.ndjson.gz")

	require.NoError(t, err)
	assert.True(t, result.ScanTruncated)
	assert.Equal(t, TruncationLimit, result.Truncation)
	assert.Equal(t, 1, result.RecordCount)
}

// A file that is malformed all the way down must not turn every bad line into a
// live ParseError pinning its own raw text. The count stays exact.
func TestReaderCapsRetainedParseErrors(t *testing.T) {
	data := gzipRepeated(`{not json`, maxRetainedParseErrors+500)
	fake := fakeGetterFor("test/garbage.ndjson.gz", data)
	reader := NewReader(fake)

	result, err := reader.ReadNDJSON(context.Background(), "test/garbage.ndjson.gz")

	require.NoError(t, err)
	assert.Len(t, result.ParseErrors, maxRetainedParseErrors, "retention is capped")
	assert.Equal(t, 500, result.ParseErrorsDropped, "the overflow is counted, not lost")
	assert.Empty(t, result.Records)
}

// The raw text budget is what actually bounds memory: 10k retained entries each
// holding a megabyte would still be 10 GB.
func TestReaderCapsRetainedParseErrorBytes(t *testing.T) {
	// 2 MB of junk per line, 8 lines: 16 MB of raw text for an 8 MB budget.
	junk := strings.Repeat("j", 2*1024*1024)
	lines := make([]string, 8)
	for i := range lines {
		lines[i] = "{not json " + junk
	}
	data := gzipLines(lines)
	fake := fakeGetterFor("test/bigjunk.ndjson.gz", data)
	reader := NewReader(fake)

	result, err := reader.ReadNDJSON(context.Background(), "test/bigjunk.ndjson.gz")

	require.NoError(t, err)
	require.Len(t, result.ParseErrors, 8, "every bad line is still reported")
	kept := 0
	for _, pe := range result.ParseErrors {
		kept += len(pe.RawLine)
	}
	assert.LessOrEqual(t, kept, maxRetainedParseErrorBytes,
		"retained raw lines must stay inside the byte budget")
	assert.Greater(t, kept, 0, "the first lines are still recoverable verbatim")
}

// The streaming path must not retain the raw maps: that buffering is what makes
// 16 in-flight files an OOM instead of a slow ETL.
func TestStreamNDJSONRetainsNoRecords(t *testing.T) {
	data := gzipLines([]string{`{"a":1}`, `{"b":2}`, `{"c":3}`})
	fake := fakeGetterFor("test/stream.ndjson.gz", data)
	reader := NewReader(fake)

	var seen []int
	result, err := reader.StreamNDJSON(context.Background(), "test/stream.ndjson.gz",
		func(idx int, record map[string]any) error {
			seen = append(seen, idx)
			return nil
		})

	require.NoError(t, err)
	assert.Nil(t, result.Records, "the streaming path must not buffer the file")
	assert.Equal(t, 3, result.RecordCount)
	assert.Equal(t, []int{0, 1, 2}, seen, "the callback index is the record position")
	assert.Equal(t, int64(len(data)), result.FileSize)
}

// A callback error aborts the read and reaches the caller unchanged.
func TestStreamNDJSONPropagatesCallbackError(t *testing.T) {
	data := gzipLines([]string{`{"a":1}`, `{"b":2}`})
	fake := fakeGetterFor("test/cberr.ndjson.gz", data)
	reader := NewReader(fake)

	boom := fmt.Errorf("callback exploded")
	_, err := reader.StreamNDJSON(context.Background(), "test/cberr.ndjson.gz",
		func(int, map[string]any) error { return boom })

	require.ErrorIs(t, err, boom)
}
