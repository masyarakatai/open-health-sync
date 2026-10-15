// SPDX-License-Identifier: AGPL-3.0-only

// Package export finds activity files for `ohs import files`: in a single
// file, a folder, or a zip, zips nested in zips included (a Garmin account
// export holds its FIT files in zips inside the export zip). It yields the
// .fit, .gpx and .tcx files, and their .gz versions decompressed (a Strava
// archive compresses some), one at a time, so the memory used does not grow
// with the size of the export. It reads nothing else: the JSON and CSV files
// of vendor exports are not activities.
package export

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/soma-intelligence/open-health-sync/connectors/activityfiles"
)

// MaxNestedZipBytes bounds a zip inside a zip, which is copied to a temporary
// file to be read (a zip needs random access).
const MaxNestedZipBytes int64 = 16 << 30

// maxDepth bounds how deep zips nest.
const maxDepth = 4

// File is one activity file found.
type File struct {
	Format string // activityfiles.FormatFIT, FormatGPX or FormatTCX
	Data   []byte // the file, decompressed when it was a .gz
	SHA256 [32]byte
}

// Stats counts what a walk found. It holds counts only, never a file name.
type Stats struct {
	Files      map[string]int // activity files yielded, by format
	TooLarge   int            // activity files over activityfiles.MaxFileBytes
	Unreadable int            // .gz files and nested zips that do not decompress
	Archives   int            // zips opened, nested ones included
	Other      int            // other files, not read
}

func (s *Stats) found(format string) {
	if s.Files == nil {
		s.Files = map[string]int{}
	}
	s.Files[format]++
}

// Walk calls fn with every activity file under root: a file, a folder (walked
// in lexical order) or a zip. An error from fn stops the walk and is
// returned. A corrupt nested zip or .gz is counted and skipped; a root zip
// that cannot be opened is an error.
func Walk(ctx context.Context, root string, fn func(File) error) (Stats, error) {
	w := &walker{ctx: ctx, fn: fn}
	info, err := os.Stat(root)
	if err != nil {
		return w.st, fmt.Errorf("open %s: %w", filepath.Base(root), noPath(err))
	}
	if !info.IsDir() {
		err = w.diskFile(root, true)
		return w.st, err
	}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("read the folder: %w", noPath(err))
		}
		if d.IsDir() {
			if p != root && skipName(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || skipName(d.Name()) {
			return nil
		}
		return w.diskFile(p, false)
	})
	return w.st, err
}

type walker struct {
	ctx context.Context
	fn  func(File) error
	st  Stats
}

// noPath drops the path from a file system error, so a message names no
// file.
func noPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return fmt.Errorf("%s: %w", pe.Op, pe.Err)
	}
	return err
}

// skipName reports macOS metadata a zip tool adds: the __MACOSX folder and
// the "._" companion files, which hold no activity.
func skipName(name string) bool {
	return name == "__MACOSX" || strings.HasPrefix(name, "._") || name == ".DS_Store"
}

// kind classifies a file by name: an activity format, "zip", or "".
func kind(name string) (format string, gz bool) {
	n := strings.ToLower(name)
	if strings.HasSuffix(n, ".gz") {
		n, gz = strings.TrimSuffix(n, ".gz"), true
	}
	switch ext := path.Ext(n); ext {
	case ".fit", ".gpx", ".tcx":
		return ext[1:], gz
	case ".zip":
		if !gz {
			return "zip", false
		}
	}
	return "", false
}

// diskFile handles one file on disk. A root zip that does not open is an
// error; one found in a folder is counted as unreadable.
func (w *walker) diskFile(p string, isRoot bool) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	format, gz := kind(filepath.Base(p))
	switch format {
	case "":
		w.st.Other++
		return nil
	case "zip":
		zr, err := zip.OpenReader(p)
		if err != nil {
			if isRoot {
				return fmt.Errorf("open %s: not a readable zip", filepath.Base(p))
			}
			w.st.Unreadable++
			return nil
		}
		defer zr.Close()
		return w.zip(&zr.Reader, 1)
	}
	f, err := os.Open(p)
	if err != nil {
		// A file found in a folder is not named: a Garmin export names its
		// files after the account's email address.
		if isRoot {
			return fmt.Errorf("open %s: %w", filepath.Base(p), noPath(err))
		}
		return fmt.Errorf("open an activity file: %w", noPath(err))
	}
	defer f.Close()
	return w.activity(f, format, gz)
}

// zip walks the entries of a zip, in the order they are stored.
func (w *walker) zip(zr *zip.Reader, depth int) error {
	w.st.Archives++
	for _, e := range zr.File {
		if err := w.ctx.Err(); err != nil {
			return err
		}
		if e.FileInfo().IsDir() || skipEntry(e.Name) {
			continue
		}
		format, gz := kind(path.Base(e.Name))
		switch format {
		case "":
			w.st.Other++
		case "zip":
			if depth >= maxDepth {
				w.st.Other++
				continue
			}
			if err := w.nested(e, depth); err != nil {
				return err
			}
		default:
			rc, err := e.Open()
			if err != nil {
				w.st.Unreadable++
				continue
			}
			err = w.activity(rc, format, gz)
			rc.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// skipEntry reports a zip entry under __MACOSX or a "._" companion file.
func skipEntry(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if skipName(part) {
			return true
		}
	}
	return false
}

// nested copies a zip entry that is itself a zip to a temporary file and
// walks it. The temporary file is removed before returning.
func (w *walker) nested(e *zip.File, depth int) error {
	rc, err := e.Open()
	if err != nil {
		w.st.Unreadable++
		return nil
	}
	defer rc.Close()
	tmp, err := os.CreateTemp("", "ohs-import-*.zip")
	if err != nil {
		return fmt.Errorf("temporary file for a nested zip: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	n, err := io.Copy(tmp, io.LimitReader(rc, MaxNestedZipBytes+1))
	if err != nil {
		var pe *fs.PathError
		if errors.As(err, &pe) {
			return fmt.Errorf("temporary file for a nested zip: %w", pe.Err)
		}
		w.st.Unreadable++
		return nil
	}
	if n > MaxNestedZipBytes {
		w.st.TooLarge++
		return nil
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		w.st.Unreadable++
		return nil
	}
	return w.zip(zr, depth+1)
}

// activity reads one activity file, decompressing a .gz, and hands it to fn.
func (w *walker) activity(r io.Reader, format string, gz bool) error {
	if gz {
		zr, err := gzip.NewReader(r)
		if err != nil {
			w.st.Unreadable++
			return nil
		}
		defer zr.Close()
		r = zr
	}
	data, err := io.ReadAll(io.LimitReader(r, activityfiles.MaxFileBytes+1))
	if err != nil {
		w.st.Unreadable++
		return nil
	}
	if len(data) > activityfiles.MaxFileBytes {
		w.st.TooLarge++
		return nil
	}
	w.st.found(format)
	return w.fn(File{Format: format, Data: data, SHA256: sha256.Sum256(data)})
}

// Formats lists the formats of a Stats in order, for printing.
func (s Stats) Formats() []string {
	out := make([]string, 0, len(s.Files))
	for k := range s.Files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
