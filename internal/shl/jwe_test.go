// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testKey is an invented key: bytes 0 to 31.
func testKey() []byte {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

// invented is a FHIR Bundle with invented values only.
const invented = `{"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Observation",` +
	`"status":"final","code":{"text":"heart rate"},"valueQuantity":{"value":61,"unit":"bpm"}}}]}`

func deflate(t *testing.T, p []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.BestCompression)
	require.NoError(t, err)
	_, err = w.Write(p)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestRoundTrip(t *testing.T) {
	key, err := NewKey()
	require.NoError(t, err)
	require.Len(t, key, KeySize)

	jwe, err := Encrypt(key, []byte(invented), ContentFHIR)
	require.NoError(t, err)
	parts := strings.Split(jwe, ".")
	require.Len(t, parts, 5, "compact serialization")
	assert.Empty(t, parts[1], "dir: no encrypted key")
	iv, err := b64.DecodeString(parts[2])
	require.NoError(t, err)
	assert.Len(t, iv, 12)
	tag, err := b64.DecodeString(parts[4])
	require.NoError(t, err)
	assert.Len(t, tag, 16)
	var h map[string]string
	raw, err := b64.DecodeString(parts[0])
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &h))
	assert.Equal(t, map[string]string{"alg": "dir", "enc": "A256GCM", "cty": "application/fhir+json"}, h)
	assert.NotContains(t, jwe, "heart", "the content is not readable")

	got, hdr, err := Decrypt(key, jwe, 1<<20)
	require.NoError(t, err)
	assert.Equal(t, invented, string(got))
	assert.Equal(t, Header{Alg: AlgDir, Enc: EncA256GCM, Cty: ContentFHIR}, hdr)

	// A fresh IV each time: the same content never gives the same file.
	again, err := Encrypt(key, []byte(invented), ContentFHIR)
	require.NoError(t, err)
	assert.NotEqual(t, jwe, again)
}

func TestRoundTripDeflated(t *testing.T) {
	key := testKey()
	jwe, err := EncryptDeflated(key, deflate(t, []byte(invented)), ContentFHIR)
	require.NoError(t, err)
	raw, err := b64.DecodeString(strings.Split(jwe, ".")[0])
	require.NoError(t, err)
	assert.JSONEq(t, `{"alg":"dir","enc":"A256GCM","cty":"application/fhir+json","zip":"DEF"}`, string(raw))
	got, hdr, err := Decrypt(key, jwe, 1<<20)
	require.NoError(t, err)
	assert.Equal(t, invented, string(got))
	assert.Equal(t, ZipDeflate, hdr.Zip)

	// The inflated size is bounded.
	_, _, err = Decrypt(key, jwe, int64(len(invented)-1))
	assert.ErrorIs(t, err, ErrDecrypt)
	_, _, err = Decrypt(key, jwe, int64(len(invented)))
	assert.NoError(t, err)

	// A deflate stream with a deflater the package did not write.
	var buf bytes.Buffer
	w := NewDeflater(&buf)
	_, err = w.Write([]byte(invented))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	jwe, err = EncryptDeflated(key, buf.Bytes(), ContentFHIR)
	require.NoError(t, err)
	got, _, err = Decrypt(key, jwe, 1<<20)
	require.NoError(t, err)
	assert.Equal(t, invented, string(got))
}

// handMade builds a file the way the IG describes it, with the standard
// library alone and none of the package's encryption code: the protected
// header in the IG's member order, its base64url form as the additional
// data, a 12 byte IV, AES-256-GCM, and the 16 byte tag split off.
func handMade(t *testing.T, key []byte, header string, plain []byte) string {
	t.Helper()
	protected := b64.EncodeToString([]byte(header))
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	iv := []byte("invented-iv1") // 12 bytes; a fixed IV is fine for a test vector only
	sealed := gcm.Seal(nil, iv, plain, []byte(protected))
	ct, tag := sealed[:len(sealed)-16], sealed[len(sealed)-16:]
	return strings.Join([]string{protected, "", b64.EncodeToString(iv), b64.EncodeToString(ct), b64.EncodeToString(tag)}, ".")
}

func TestDecryptTheIGStructure(t *testing.T) {
	key := testKey()
	// The links examples page's file header carries a kid; the
	// specification adds cty and, optionally, zip DEF.
	for name, c := range map[string]struct {
		header string
		plain  []byte
	}{
		"enc first, kid":   {`{"enc":"A256GCM","alg":"dir","kid":"invented-kid"}`, []byte(invented)},
		"cty":              {`{"alg":"dir","enc":"A256GCM","cty":"application/fhir+json"}`, []byte(invented)},
		"cty with version": {`{"alg":"dir","enc":"A256GCM","cty":"application/fhir+json;fhirVersion=4.0.1"}`, []byte(invented)},
		"zip DEF":          {`{"alg":"dir","enc":"A256GCM","cty":"application/fhir+json","zip":"DEF"}`, deflate(t, []byte(invented))},
	} {
		got, _, err := Decrypt(key, handMade(t, key, c.header, c.plain), 1<<20)
		require.NoError(t, err, name)
		assert.Equal(t, invented, string(got), name)
	}
}

func TestDecryptRefuses(t *testing.T) {
	key := testKey()
	good := handMade(t, key, `{"alg":"dir","enc":"A256GCM","cty":"application/fhir+json"}`, []byte(invented))
	parts := strings.Split(good, ".")
	flip := func(s string) string {
		b, err := b64.DecodeString(s)
		require.NoError(t, err)
		b[len(b)/2] ^= 1
		return b64.EncodeToString(b)
	}
	with := func(i int, v string) string {
		p := append([]string(nil), parts...)
		p[i] = v
		return strings.Join(p, ".")
	}
	other := testKey()
	other[0] = 0xff
	for name, c := range map[string]struct {
		key []byte
		jwe string
	}{
		"wrong key":          {other, good},
		"short key":          {key[:16], good},
		"header changed":     {key, with(0, b64.EncodeToString([]byte(`{"alg":"dir","enc":"A256GCM","cty":"text/plain"}`)))},
		"iv changed":         {key, with(2, flip(parts[2]))},
		"ciphertext changed": {key, with(3, flip(parts[3]))},
		"tag changed":        {key, with(4, flip(parts[4]))},
		"tag cut":            {key, with(4, parts[4][:10])},
		"iv cut":             {key, with(2, parts[2][:8])},
		"encrypted key set":  {key, with(1, "AAAA")},
		"four parts":         {key, strings.Join(parts[:4], ".")},
		"six parts":          {key, good + "."},
		"not base64":         {key, with(3, "***")},
		"alg not dir":        {key, handMade(t, key, `{"alg":"A256KW","enc":"A256GCM"}`, []byte(invented))},
		"enc not A256GCM":    {key, handMade(t, key, `{"alg":"dir","enc":"A128GCM"}`, []byte(invented))},
		"zip not DEF":        {key, handMade(t, key, `{"alg":"dir","enc":"A256GCM","zip":"GZIP"}`, []byte(invented))},
		"zip but not deflate": {key, handMade(t, key, `{"alg":"dir","enc":"A256GCM","zip":"DEF"}`,
			[]byte{0xff, 0xff, 0xff, 0xff})},
		"crit":        {key, handMade(t, key, `{"alg":"dir","enc":"A256GCM","crit":["exp"],"exp":1}`, []byte(invented))},
		"header junk": {key, handMade(t, key, `not json`, []byte(invented))},
		"empty":       {key, ""},
	} {
		_, _, err := Decrypt(c.key, c.jwe, 1<<20)
		assert.ErrorIs(t, err, ErrDecrypt, name)
		// One error for every cause: the message says nothing about which.
		if err != nil {
			assert.Equal(t, ErrDecrypt.Error(), err.Error(), name)
		}
	}
}

func TestEncryptRefusesABadKey(t *testing.T) {
	_, err := Encrypt(make([]byte, 16), []byte(invented), ContentFHIR)
	assert.Error(t, err)
	_, err = EncryptDeflated(nil, []byte(invented), ContentFHIR)
	assert.Error(t, err)
}

func TestKeyEncoding(t *testing.T) {
	key := testKey()
	s := EncodeKey(key)
	assert.Len(t, s, 43, "43 characters, 32 bytes base64url")
	assert.NotContains(t, s, "=")
	back, err := DecodeKey(s)
	require.NoError(t, err)
	assert.Equal(t, key, back)
	for _, bad := range []string{"", s[:42], s + "A", strings.Replace(s, s[:1], "+", 1), b64.EncodeToString(make([]byte, 31))} {
		_, err := DecodeKey(bad)
		assert.Error(t, err, bad)
	}
}

func TestSecrets(t *testing.T) {
	a, err := NewSecret()
	require.NoError(t, err)
	b, err := NewSecret()
	require.NoError(t, err)
	assert.Len(t, a, 43, "256 random bits, base64url")
	assert.NotEqual(t, a, b)
	assert.True(t, ValidSecret(a))
	for _, bad := range []string{"", a[:42], a + "x", "view", strings.Repeat("!", 43)} {
		assert.False(t, ValidSecret(bad), bad)
	}
	assert.Len(t, HashSecret(a), 64)
	assert.Equal(t, HashSecret(a), HashSecret(a))
	assert.NotEqual(t, HashSecret(a), HashSecret(b))
	assert.NotContains(t, HashSecret(a), a)
}
