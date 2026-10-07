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

// NormalizePhone converts Kenyan and international formats to digits with country code
// (0712345678, +254 712 345 678, 712345678 all become 254712345678). Returns "" when invalid.
func NormalizePhone(p string) string {
	var d strings.Builder
	for _, r := range p {
		if r >= '0' && r <= '9' {
			d.WriteRune(r)
		}
	}
	s := d.String()
	switch {
	case strings.HasPrefix(s, "254") && len(s) == 12:
		return s
	case strings.HasPrefix(s, "0") && len(s) == 10:
		return "254" + s[1:]
	case (strings.HasPrefix(s, "7") || strings.HasPrefix(s, "1")) && len(s) == 9:
		return "254" + s
	case len(s) >= 10 && len(s) <= 15 && !strings.HasPrefix(s, "0"):
		return s
	}
	return ""
}

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

// AccountMatchKey normalises a paybill account reference for matching: upper case, separators
// removed, leading zeros stripped from each digit run. "b 07", "B-07" and "B7" all give "B7";
// "S-B07" gives "SB7". treasury-api applies the identical rule to BillRefNumber.
func AccountMatchKey(ref string) string {
	var out strings.Builder
	var digits strings.Builder
	flush := func() {
		if digits.Len() == 0 {
			return
		}
		ds := strings.TrimLeft(digits.String(), "0")
		if ds == "" {
			ds = "0"
		}
		out.WriteString(ds)
		digits.Reset()
	}
	for _, r := range strings.ToUpper(ref) {
		switch {
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			flush()
			out.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	return out.String()
}

// AccountRef builds the display reference for a unit code and fund prefix ("" + B07, "S-" + B07).
func AccountRef(prefix, unitCode string) string {
	return fmt.Sprintf("%s%s", strings.ToUpper(strings.TrimSpace(prefix)), strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(unitCode), " ", "")))
}
