// SPDX-License-Identifier: AGPL-3.0-only

package shl

import (
	"bytes"
	"compress/flate"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// The JWE a share file is: compact serialization, direct encryption with a
// 256 bit key, AES-GCM, optionally raw DEFLATE inside.
const (
	// KeySize is the size of a share key, in bytes.
	KeySize = 32
	// AlgDir is the only key management algorithm: the key is used as is.
	AlgDir = "dir"
	// EncA256GCM is the only content encryption algorithm.
	EncA256GCM = "A256GCM"
	// ZipDeflate marks a plaintext compressed with raw DEFLATE (RFC 1951).
	ZipDeflate = "DEF"
	// ContentFHIR is the content type of a FHIR JSON file.
	ContentFHIR = "application/fhir+json"

	ivSize  = 12
	tagSize = 16
)

// b64 is base64url without padding, the encoding of every JWE part, of the
// key and of the payload.
var b64 = base64.RawURLEncoding.Strict()

// ErrDecrypt is the one error Decrypt returns, whatever the cause: a reader
// learns nothing about which part was wrong.
var ErrDecrypt = errors.New("shl: the file cannot be decrypted")

// Header is the JWE protected header of a share file.
type Header struct {
	Alg string `json:"alg"`
	Enc string `json:"enc"`
	Cty string `json:"cty,omitempty"`
	Zip string `json:"zip,omitempty"`
	Kid string `json:"kid,omitempty"`
}

// NewKey returns a fresh random share key.
func NewKey() ([]byte, error) {
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("shl: key: %w", err)
	}
	return k, nil
}

// EncodeKey is the key as the payload carries it: 43 characters of base64url.
func EncodeKey(key []byte) string { return b64.EncodeToString(key) }

// DecodeKey reads a key of the payload; anything but 43 characters that
// decode to 32 bytes is refused.
func DecodeKey(s string) ([]byte, error) {
	if len(s) != 43 {
		return nil, errors.New("shl: the key is not 43 characters of base64url")
	}
	k, err := b64.DecodeString(s)
	if err != nil || len(k) != KeySize {
		return nil, errors.New("shl: the key is not 43 characters of base64url")
	}
	return k, nil
}

// NewSecret returns 256 random bits as 43 characters of base64url: a
// manifest id or a file ticket.
func NewSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("shl: secret: %w", err)
	}
	return b64.EncodeToString(b), nil
}

// ValidSecret reports whether s has the shape NewSecret gives, so a lookup
// is never made for anything else.
func ValidSecret(s string) bool {
	if len(s) != 43 {
		return false
	}
	b, err := b64.DecodeString(s)
	return err == nil && len(b) == 32
}

// HashSecret is what the database keeps of a secret: its SHA-256, in hex.
func HashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// NewDeflater returns a raw DEFLATE writer into w, for EncryptDeflated.
func NewDeflater(w io.Writer) io.WriteCloser {
	d, err := flate.NewWriter(w, flate.DefaultCompression)
	if err != nil { // only for an invalid level
		panic(err)
	}
	return d
}

// Encrypt encrypts plaintext under key as a compact JWE whose content type
// is cty.
func Encrypt(key, plaintext []byte, cty string) (string, error) {
	return seal(key, Header{Alg: AlgDir, Enc: EncA256GCM, Cty: cty}, plaintext)
}

// EncryptDeflated encrypts a plaintext already compressed with raw DEFLATE
// (NewDeflater), with "zip": "DEF" in the header.
func EncryptDeflated(key, deflated []byte, cty string) (string, error) {
	return seal(key, Header{Alg: AlgDir, Enc: EncA256GCM, Cty: cty, Zip: ZipDeflate}, deflated)
}

func seal(key []byte, h Header, plaintext []byte) (string, error) {
	if len(key) != KeySize {
		return "", fmt.Errorf("shl: the key must be %d bytes", KeySize)
	}
	hj, err := json.Marshal(h)
	if err != nil {
		return "", fmt.Errorf("shl: header: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	iv := make([]byte, ivSize)
	if _, err := rand.Read(iv); err != nil {
		return "", fmt.Errorf("shl: iv: %w", err)
	}
	protected := b64.EncodeToString(hj)
	// The additional data is the ASCII of the encoded protected header
	// (RFC 7516, section 5.1, step 14).
	sealed := gcm.Seal(nil, iv, plaintext, []byte(protected))
	ct, tag := sealed[:len(sealed)-tagSize], sealed[len(sealed)-tagSize:]
	var sb strings.Builder
	sb.Grow(len(protected) + b64.EncodedLen(len(ct)) + 64)
	sb.WriteString(protected)
	sb.WriteString("..")
	sb.WriteString(b64.EncodeToString(iv))
	sb.WriteByte('.')
	sb.WriteString(b64.EncodeToString(ct))
	sb.WriteByte('.')
	sb.WriteString(b64.EncodeToString(tag))
	return sb.String(), nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("shl: cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// Decrypt opens a compact JWE made with key, as the IG describes a share
// file: alg dir, enc A256GCM, zip absent or DEF. The plaintext, inflated
// when zipped, may hold at most limit bytes. Every failure is ErrDecrypt.
func Decrypt(key []byte, jwe string, limit int64) ([]byte, Header, error) {
	plain, h, ok := decrypt(key, jwe, limit)
	if !ok {
		return nil, Header{}, ErrDecrypt
	}
	return plain, h, nil
}

func decrypt(key []byte, jwe string, limit int64) ([]byte, Header, bool) {
	var h Header
	if len(key) != KeySize {
		return nil, h, false
	}
	parts := strings.Split(jwe, ".")
	if len(parts) != 5 || parts[1] != "" {
		return nil, h, false
	}
	hj, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, h, false
	}
	var members map[string]json.RawMessage
	if json.Unmarshal(hj, &members) != nil || json.Unmarshal(hj, &h) != nil {
		return nil, h, false
	}
	// A crit member names extensions this reader would have to understand.
	if _, crit := members["crit"]; crit {
		return nil, h, false
	}
	if h.Alg != AlgDir || h.Enc != EncA256GCM || (h.Zip != "" && h.Zip != ZipDeflate) {
		return nil, h, false
	}
	iv, err1 := b64.DecodeString(parts[2])
	ct, err2 := b64.DecodeString(parts[3])
	tag, err3 := b64.DecodeString(parts[4])
	if err1 != nil || err2 != nil || err3 != nil || len(iv) != ivSize || len(tag) != tagSize {
		return nil, h, false
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, h, false
	}
	plain, err := gcm.Open(nil, iv, append(ct, tag...), []byte(parts[0]))
	if err != nil {
		return nil, h, false
	}
	if h.Zip == "" {
		return plain, h, int64(len(plain)) <= limit
	}
	r := flate.NewReader(bytes.NewReader(plain))
	defer func() { _ = r.Close() }()
	out, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(out)) > limit {
		return nil, h, false
	}
	return out, h, true
}
