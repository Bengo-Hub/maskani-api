// Package secure holds field encryption for identity numbers and KRA PINs, phone hashing for
// matching, and short-code hashing for visitor passes (SRDD NFR-09, 17.2).
package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/Bengo-Hub/httpware/contact"
	"github.com/Bengo-Hub/httpware/pii"
)

// Box encrypts and hashes with keys derived from one secret.
type Box struct {
	aead    cipher.AEAD
	hashKey []byte
}

// NewBox derives an AES-256 key and an HMAC key from secret. secret may be base64 or any string.
func NewBox(secret string) (*Box, error) {
	if secret == "" {
		return nil, errors.New("secure: empty secret")
	}
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil || len(raw) < 16 {
		raw = []byte(secret)
	}
	encKey := sha256.Sum256(append([]byte("maskani-field-enc:"), raw...))
	hashKey := sha256.Sum256(append([]byte("maskani-hash:"), raw...))
	block, err := aes.NewCipher(encKey[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead, hashKey: hashKey[:]}, nil
}

// Encrypt returns base64(nonce|ciphertext); empty input stays empty.
func (b *Box) Encrypt(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := b.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Decrypt reverses Encrypt.
func (b *Box) Decrypt(enc string) (string, error) {
	if enc == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return "", err
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return "", errors.New("secure: ciphertext too short")
	}
	plain, err := b.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Hash returns a keyed hex HMAC of v (for phones, pass codes, device keys).
func (b *Box) Hash(v string) string {
	m := hmac.New(sha256.New, b.hashKey)
	m.Write([]byte(v))
	return hex.EncodeToString(m.Sum(nil))
}

// Mask shows only the last four characters of a sensitive value.
func Mask(v string) string {
	if len(v) <= 4 {
		return strings.Repeat("*", len(v))
	}
	return strings.Repeat("*", len(v)-4) + v[len(v)-4:]
}

// NormalizePhone validates a phone with the fleet's rules (httpware contact: each country's own
// numbering plan, local forms read as Kenyan) and returns it as digits with the country code
// (0712345678, +254 712 345 678 and 712345678 all become 254712345678). That digits form is what
// phone hashes and treasury customer keys were built on, so it must not change. "" when invalid.
func NormalizePhone(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	e164, err := contact.NormalizePhone(p, "KE")
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(e164, "+")
}

// MaskPhone shows a phone with only its prefix and last digits, for lists and logs.
func MaskPhone(p string) string { return pii.MaskPhone(p) }

// RandomDigits returns n cryptographically random digits (visitor pass codes).
func RandomDigits(n int) (string, error) {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		sb.WriteString(v.String())
	}
	return sb.String(), nil
}

// RandomToken returns a URL-safe random token of n bytes.
func RandomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AccountRef builds the display reference for a unit code and fund prefix ("" + B07, "S-" + B07).
func AccountRef(prefix, unitCode string) string {
	return fmt.Sprintf("%s%s", strings.ToUpper(strings.TrimSpace(prefix)), strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(unitCode), " ", "")))
}
