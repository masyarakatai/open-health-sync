// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inventedURL is a manifest URL on an invented host.
const inventedURL = "https://ohs.example/shl/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func TestPayloadURI(t *testing.T) {
	key := EncodeKey(testKey())
	p := Payload{URL: inventedURL, Key: key, Exp: 1798761600, Flag: "P", Label: "Sleep & heart, last 30 days"}
	uri, err := p.URI()
	require.NoError(t, err)
	body, ok := strings.CutPrefix(uri, Prefix)
	require.True(t, ok, "starts with shlink:/")
	assert.NotContains(t, body, "=", "base64url without padding")
	assert.NotContains(t, body, "+")
	assert.NotContains(t, body, "/")
	raw, err := b64.DecodeString(body)
	require.NoError(t, err)
	// Minified JSON, the IG's members, v left out (1 is the default), & as is.
	assert.Equal(t, `{"url":"`+inventedURL+`","key":"`+key+`","exp":1798761600,"flag":"P",`+
		`"label":"Sleep & heart, last 30 days"}`, string(raw))

	back, err := Parse(uri)
	require.NoError(t, err)
	assert.Equal(t, p, back)
}

func TestPayloadOptionalMembers(t *testing.T) {
	key := EncodeKey(testKey())
	uri, err := Payload{URL: inventedURL, Key: key}.URI()
	require.NoError(t, err)
	raw, err := b64.DecodeString(strings.TrimPrefix(uri, Prefix))
	require.NoError(t, err)
	assert.Equal(t, `{"url":"`+inventedURL+`","key":"`+key+`"}`, string(raw), "no exp, flag, label or v")
}

func TestPayloadValidate(t *testing.T) {
	key := EncodeKey(testKey())
	good := Payload{URL: inventedURL, Key: key}
	require.NoError(t, good.Validate())
	for name, c := range map[string]struct {
		p  Payload
		ok bool
	}{
		"flag P":                 {Payload{URL: inventedURL, Key: key, Flag: "P"}, true},
		"flags in order":         {Payload{URL: inventedURL, Key: key, Flag: "LP"}, true},
		"flags out of order":     {Payload{URL: inventedURL, Key: key, Flag: "PL"}, false},
		"flag twice":             {Payload{URL: inventedURL, Key: key, Flag: "PP"}, false},
		"unknown flag":           {Payload{URL: inventedURL, Key: key, Flag: "X"}, false},
		"lower case flag":        {Payload{URL: inventedURL, Key: key, Flag: "p"}, false},
		"U and P":                {Payload{URL: inventedURL, Key: key, Flag: "PU"}, false},
		"label of 80":            {Payload{URL: inventedURL, Key: key, Label: strings.Repeat("a", 80)}, true},
		"label of 80 characters": {Payload{URL: inventedURL, Key: key, Label: strings.Repeat("é", 80)}, true},
		"label of 81":            {Payload{URL: inventedURL, Key: key, Label: strings.Repeat("a", 81)}, false},
		"v 1":                    {Payload{URL: inventedURL, Key: key, V: 1}, true},
		"v 2":                    {Payload{URL: inventedURL, Key: key, V: 2}, false},
		"negative exp":           {Payload{URL: inventedURL, Key: key, Exp: -1}, false},
		"no url":                 {Payload{Key: key}, false},
		"relative url":           {Payload{URL: "/shl/abc", Key: key}, false},
		"other scheme":           {Payload{URL: "ftp://ohs.example/shl/abc", Key: key}, false},
		"url with a fragment":    {Payload{URL: inventedURL + "#x", Key: key}, false},
		"url with user info":     {Payload{URL: "https://u:p@ohs.example/shl/abc", Key: key}, false},
		"plain http":             {Payload{URL: "http://localhost:8080/shl/abc", Key: key}, true},
		"no key":                 {Payload{URL: inventedURL}, false},
		"short key":              {Payload{URL: inventedURL, Key: key[:42]}, false},
		"padded key":             {Payload{URL: inventedURL, Key: key + "="}, false},
	} {
		err := c.p.Validate()
		if c.ok {
			assert.NoError(t, err, name)
		} else {
			assert.Error(t, err, name)
		}
		_, uerr := c.p.URI()
		assert.Equal(t, err == nil, uerr == nil, name+": URI validates too")
	}
}

func TestParse(t *testing.T) {
	key := EncodeKey(testKey())
	uri, err := Payload{URL: inventedURL, Key: key, Flag: "P"}.URI()
	require.NoError(t, err)

	// A viewer URL carries the payload in its fragment.
	viewer := ViewerURL("https://ohs.example/shl/view", uri)
	assert.Equal(t, "https://ohs.example/shl/view#"+uri, viewer)
	p, err := Parse(viewer)
	require.NoError(t, err)
	assert.Equal(t, inventedURL, p.URL)
	assert.Equal(t, "P", p.Flag)

	enc := func(s string) string { return Prefix + b64.EncodeToString([]byte(s)) }
	for name, bad := range map[string]string{
		"empty":         "",
		"no prefix":     strings.TrimPrefix(uri, Prefix),
		"wrong prefix":  "shc:/" + strings.TrimPrefix(uri, Prefix),
		"not base64url": Prefix + "***",
		"padded":        uri + "==",
		"not JSON":      enc("not json"),
		"JSON array":    enc(`["url"]`),
		"invalid":       enc(`{"url":"` + inventedURL + `","key":"short"}`),
		"version 2":     enc(`{"url":"` + inventedURL + `","key":"` + key + `","v":2}`),
		"viewer, no #":  "https://ohs.example/shl/view" + uri,
	} {
		_, err := Parse(bad)
		assert.Error(t, err, name)
	}
}
