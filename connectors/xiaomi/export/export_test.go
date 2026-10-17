// SPDX-License-Identifier: AGPL-3.0-only

package export

import (
	"archive/zip"
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/soma-intelligence/open-health-sync/connectors/xiaomi"
	"github.com/soma-intelligence/open-health-sync/internal/connector"
)

var update = flag.Bool("update", false, "rewrite the golden upload of the connector's tests")

// fixture is an invented Mi Fitness copy, laid out as Xiaomi delivers it.
// fitness_data wraps every line in one more pair of quotes, as some copies
// do; sport_record is plain CSV with CRLF line ends.
const fixture = "testdata/mi_fitness"

// golden is the upload the connector's mapping tests read.
const golden = "../testdata/upload.ndjson"

func convertAll(t *testing.T, path string, opts Options) (Stats, []Part) {
	t.Helper()
	e, err := Open(path)
	require.NoError(t, err)
	defer e.Close()
	var parts []Part
	st, err := e.Convert(opts, func(p Part) error { parts = append(parts, p); return nil })
	require.NoError(t, err)
	return st, parts
}

func TestConvertGolden(t *testing.T) {
	st, parts := convertAll(t, fixture, Options{})
	var all bytes.Buffer
	for _, p := range parts {
		all.Write(p.NDJSON)
	}
	if *update {
		require.NoError(t, os.WriteFile(golden, all.Bytes(), 0o644))
	}
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, string(want), all.String(), "run go test ./connectors/xiaomi/export -update after a deliberate change")

	require.Equal(t, 15, st.Rows)
	require.Equal(t, 14, st.Lines)
	require.Equal(t, map[string]int{"unreadable value": 1}, st.Invalid)
	require.Equal(t, 2, st.Keys["fitness_data/heart_rate"])
	require.Equal(t, 1, st.Keys["sport_record/outdoor_running"])

	require.Len(t, parts, 2)
	require.Equal(t, Part{Number: 0, Type: xiaomi.TableFitnessData, Day: "2026-03-08", Lines: 12}, withoutBytes(parts[0]))
	require.Equal(t, Part{Number: 1, Type: xiaomi.TableSportRecord, Day: "2026-03-08", Lines: 2}, withoutBytes(parts[1]))

	// The account id never leaves the machine; the device name does, not
	// the alias the user gave it.
	require.NotContains(t, all.String(), "1000000001")
	require.NotContains(t, all.String(), "my band")
	require.Contains(t, all.String(), `"device":{"model":"band.example.m1","name":"Xiaomi Smart Band 8"}`)

	// Every part passes the server's check.
	for _, p := range parts {
		require.NoError(t, xiaomi.New().ParseUpload(connector.UploadMeta{}, bytes.NewReader(p.NDJSON), nil))
	}
}

func withoutBytes(p Part) Part { p.NDJSON = nil; return p }

func TestConvertZipSameAsFolder(t *testing.T) {
	dir := t.TempDir()
	zpath := filepath.Join(dir, "copy.zip")
	f, err := os.Create(zpath)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	entries, err := os.ReadDir(fixture)
	require.NoError(t, err)
	for _, de := range entries {
		b, err := os.ReadFile(filepath.Join(fixture, de.Name()))
		require.NoError(t, err)
		w, err := zw.Create("MiFitness/" + de.Name())
		require.NoError(t, err)
		_, err = w.Write(b)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())

	_, fromDir := convertAll(t, fixture, Options{})
	_, fromZip := convertAll(t, zpath, Options{})
	require.Equal(t, fromDir, fromZip)
}

func TestConvertSinceAndParts(t *testing.T) {
	st, parts := convertAll(t, fixture, Options{Since: "2026-03-09"})
	require.Zero(t, st.Lines)
	require.Equal(t, 14, st.BeforeSince)
	require.Empty(t, parts)

	// A small part cap splits a table; numbers stay in order.
	_, parts = convertAll(t, fixture, Options{MaxPartBytes: 600})
	require.Greater(t, len(parts), 2)
	for i, p := range parts {
		require.Equal(t, i, p.Number)
		require.LessOrEqual(t, len(p.NDJSON), 600+1200, "one line may exceed the cap alone")
	}
}

func TestBatchIDStable(t *testing.T) {
	e, err := Open(fixture)
	require.NoError(t, err)
	defer e.Close()
	a, b := e.BatchID(Options{}), e.BatchID(Options{})
	require.Equal(t, a, b)
	require.True(t, strings.HasPrefix(a, "mif-"))
	require.NotEqual(t, a, e.BatchID(Options{Since: "2026-01-01"}))
}

func TestOpenErrors(t *testing.T) {
	_, err := Open(t.TempDir())
	require.ErrorIs(t, err, ErrNoTables)
	notZip := filepath.Join(t.TempDir(), "x.zip")
	require.NoError(t, os.WriteFile(notZip, []byte("nope"), 0o600))
	_, err = Open(notZip)
	require.Error(t, err)
	require.Error(t, Options{Since: "yesterday"}.Validate())
}

func TestParseLine(t *testing.T) {
	for in, want := range map[string][]string{
		`a,b,"{""x"":1}",c`:         {"a", "b", `{"x":1}`, "c"},
		`"a,b,""{""""x"""":1}"",c"`: {"a", "b", `{"x":1}`, "c"},
		`"Uid,Sid,Key"`:             {"Uid", "Sid", "Key"},
		`"a,b,"{""x"":1}",c"`:       {"a", "b", `{"x":1}`, "c"},
		`single`:                    {"single"},
	} {
		got, err := parseLine(in)
		require.NoError(t, err, in)
		require.Equal(t, want, got, in)
	}
}
