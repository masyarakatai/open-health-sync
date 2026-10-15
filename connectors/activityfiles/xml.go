// SPDX-License-Identifier: AGPL-3.0-only

package activityfiles

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxText is the longest element text kept. Every figure and name the
// mapping reads is far shorter; longer text (notes, descriptions) is cut.
const maxText = 1024

// maxDepth bounds element nesting; GPX and TCX need fewer than 10 levels.
const maxDepth = 64

// xmlHandler receives a file's elements by their local names (namespace
// prefixes such as gpxtpx: or ns3: dropped). path holds the names from the
// root to the current element, which is last.
type xmlHandler interface {
	start(path []string, attrs []xml.Attr)
	end(path []string, text string)
}

// walkXML streams an XML document to h. Leading white space and a byte
// order mark are skipped (Strava writes TCX files that start with spaces).
// Errors name the format and a line number, never the file's content.
func walkXML(format string, data []byte, h xmlHandler) error {
	data = bytes.TrimLeft(data, " \t\r\n\ufeff")
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.CharsetReader = charsetReader
	dec.Strict = true
	var (
		path []string
		text []byte
		root bool
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var se *xml.SyntaxError
			if errors.As(err, &se) {
				return fmt.Errorf("%w: %s: XML syntax error on line %d", ErrInvalidFile, format, se.Line)
			}
			return fmt.Errorf("%w: %s: unreadable XML", ErrInvalidFile, format)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if len(path) >= maxDepth {
				return fmt.Errorf("%w: %s: elements nested too deep", ErrInvalidFile, format)
			}
			path = append(path, t.Name.Local)
			root = true
			text = text[:0]
			h.start(path, t.Attr)
		case xml.CharData:
			if len(text)+len(t) <= maxText {
				text = append(text, t...)
			}
		case xml.EndElement:
			h.end(path, strings.TrimSpace(string(text)))
			text = text[:0]
			path = path[:len(path)-1]
		}
	}
	if !root {
		return fmt.Errorf("%w: %s: no XML element", ErrInvalidFile, format)
	}
	return nil
}

// charsetReader reads the single byte encodings some older files declare.
// Latin-1 maps byte for byte to the first 256 code points; Windows-1252
// differs only in characters no figure uses.
func charsetReader(charset string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "windows-1252", "cp1252", "us-ascii", "ascii":
		b, err := io.ReadAll(io.LimitReader(in, MaxFileBytes+1))
		if err != nil {
			return nil, err
		}
		out := make([]byte, 0, len(b)+len(b)/8)
		for _, c := range b {
			out = utf8.AppendRune(out, rune(c))
		}
		return bytes.NewReader(out), nil
	}
	return nil, fmt.Errorf("unsupported charset")
}

// parent is the name of the element above the current one, "" at the root.
func parent(path []string) string {
	if len(path) < 2 {
		return ""
	}
	return path[len(path)-2]
}

// within reports whether name is one of the elements above the current one.
func within(path []string, name string) bool {
	for _, p := range path[:len(path)-1] {
		if p == name {
			return true
		}
	}
	return false
}

// attr returns the value of the attribute of that local name.
func attr(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name {
			return strings.TrimSpace(a.Value)
		}
	}
	return ""
}

// num parses a decimal figure; NaN when absent or unreadable.
func num(s string) float64 {
	if s == "" {
		return nan
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nan
	}
	return v
}

// xmlTime parses an ISO 8601 time. A time without an offset is read as UTC,
// as GPX requires.
func xmlTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
