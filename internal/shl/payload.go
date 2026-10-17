// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

// Prefix starts every share link.
const Prefix = "shlink:/"

// MaxLabel is the IG's limit on a label, in characters.
const MaxLabel = 80

// Flags of the IG, in the order a payload lists them.
const (
	// FlagLongTerm: the receiver may poll the manifest for updates. Never
	// set here: a share is a snapshot.
	FlagLongTerm = "L"
	// FlagPasscode: the manifest asks for a passcode.
	FlagPasscode = "P"
	// FlagDirect: one file, fetched with GET, no manifest. Never set here.
	FlagDirect = "U"
)

// Payload is the JSON object a share link carries.
type Payload struct {
	// URL is the manifest URL.
	URL string `json:"url"`
	// Key is the file key, 43 characters of base64url.
	Key string `json:"key"`
	// Exp is the expiry, in seconds since the epoch: a hint for the
	// receiver, the server enforces its own.
	Exp int64 `json:"exp,omitempty"`
	// Flag lists single character flags in alphabetical order.
	Flag string `json:"flag,omitempty"`
	// Label is a short description for the receiver.
	Label string `json:"label,omitempty"`
	// V is the protocol version, left out when 1.
	V int `json:"v,omitempty"`
}

// Validate checks p against the IG: an absolute http(s) manifest URL, a
// key of 32 bytes, known flags in alphabetical order, a label of at most 80
// characters and version 1.
func (p Payload) Validate() error {
	u, err := url.Parse(p.URL)
	switch {
	case p.URL == "" || err != nil:
		return errors.New("shl: the payload needs a manifest url")
	case u.Scheme != "https" && u.Scheme != "http", u.Host == "":
		return errors.New("shl: the manifest url must be an absolute http or https URL")
	case u.User != nil, strings.Contains(p.URL, "#"):
		return errors.New("shl: the manifest url must have no user info and no fragment")
	}
	if k, err := DecodeKey(p.Key); err != nil || EncodeKey(k) != p.Key {
		return errors.New("shl: the key must be 43 characters of base64url")
	}
	if p.Exp < 0 {
		return errors.New("shl: exp must be seconds since the epoch")
	}
	if err := checkFlags(p.Flag); err != nil {
		return err
	}
	if err := CheckLabel(p.Label); err != nil {
		return err
	}
	if p.V != 0 && p.V != 1 {
		return errors.New("shl: only version 1 is supported")
	}
	return nil
}

func checkFlags(f string) error {
	prev := byte(0)
	for i := range len(f) {
		c := f[i]
		if !strings.ContainsRune(FlagLongTerm+FlagPasscode+FlagDirect, rune(c)) {
			return errors.New("shl: unknown flag")
		}
		if c <= prev {
			return errors.New("shl: flags must be in alphabetical order, each once")
		}
		prev = c
	}
	if strings.Contains(f, FlagDirect) && strings.Contains(f, FlagPasscode) {
		return errors.New("shl: the U flag cannot go with P")
	}
	return nil
}

// URI is the share link: shlink:/ and the payload as minified JSON in
// base64url.
func (p Payload) URI() (string, error) {
	if err := p.Validate(); err != nil {
		return "", err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return "", err
	}
	return Prefix + b64.EncodeToString(bytes.TrimSuffix(buf.Bytes(), []byte("\n"))), nil
}

// ViewerURL is a viewer page that opens uri: the link goes in the fragment,
// which a browser never sends to the server.
func ViewerURL(viewer, uri string) string { return viewer + "#" + uri }

// Parse reads a share link, bare or at the end of a viewer URL after "#".
func Parse(s string) (Payload, error) {
	if i := strings.Index(s, "#"+Prefix); i >= 0 {
		s = s[i+1:]
	}
	body, ok := strings.CutPrefix(s, Prefix)
	if !ok {
		return Payload{}, errors.New("shl: not a share link")
	}
	raw, err := b64.DecodeString(body)
	if err != nil {
		return Payload{}, errors.New("shl: the payload is not base64url")
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payload{}, errors.New("shl: the payload is not a JSON object")
	}
	if err := p.Validate(); err != nil {
		return Payload{}, err
	}
	return p, nil
}
