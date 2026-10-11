// Package otp issues and checks the six digit codes a person reads back to prove they hold the
// phone or email on record (a privacy request made at the office, a buyer accepting a new payment
// plan). The code lives only as a keyed hash in the record's own metadata, with its expiry, wrong
// tries and how many were sent; the plain code goes out once in a party.otp_requested event, which
// the outbox prunes after 15 minutes. Codes are sent by notifications-api with the shared auth OTP
// templates, whose wording says five minutes.
package otp

import (
	"context"
	"crypto/subtle"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// Requested is the event carrying a code to a person: {purpose, name, phone, email, otp}.
const Requested = "party.otp_requested"

// Limits: a code lasts 5 minutes, takes 5 wrong tries, and a record gets at most 5 codes.
const (
	TTL         = 5 * time.Minute
	MaxAttempts = 5
	MaxSends    = 5
)

const (
	keyHash     = "otp_hash"
	keyExpires  = "otp_expires_at"
	keyAttempts = "otp_attempts"
	keySent     = "codes_sent"
)

// Issue writes a fresh code's hash into meta (a copy the caller saves) and returns the plain code.
// subject ties the hash to one record, so a code for one request never verifies another.
func Issue(meta map[string]any, box *secure.Box, subject string) (string, error) {
	if Count(meta[keySent]) >= MaxSends {
		return "", httpx.Conflict("five codes have been sent; confirm this person another way")
	}
	code, err := secure.RandomDigits(6)
	if err != nil {
		return "", err
	}
	meta[keyHash] = box.Hash(subject + ":" + code)
	meta[keyExpires] = time.Now().Add(TTL).Format(time.RFC3339Nano)
	meta[keyAttempts] = 0
	meta[keySent] = Count(meta[keySent]) + 1
	return code, nil
}

// Publish sends the plain code to the phone and email on record through the outbox. aggregate
// plus the send count keeps each code its own event.
func Publish(ctx context.Context, pub events.Publisher, tenantID uuid.UUID, aggregate string, meta map[string]any, purpose, name, phone, email, code string) error {
	if phone == "" && email == "" {
		return httpx.Invalid("there is no phone or email on record to send a code to")
	}
	return events.Publish(ctx, pub, tenantID, aggregate+":code:"+strconv.Itoa(Count(meta[keySent])), Requested,
		map[string]any{"purpose": purpose, "name": name, "phone": phone, "email": email, "otp": code})
}

// Check verifies a code against meta. A wrong code counts a try in meta (the caller saves meta
// either way) and is refused; the right one clears the code so it cannot be used twice.
func Check(meta map[string]any, box *secure.Box, subject, code string) error {
	hash, _ := meta[keyHash].(string)
	if hash == "" {
		return httpx.Invalid("send a code first")
	}
	if Count(meta[keyAttempts]) >= MaxAttempts {
		return httpx.Conflict("too many wrong codes; send a new one")
	}
	exp, err := time.Parse(time.RFC3339Nano, str(meta[keyExpires]))
	if err != nil || time.Now().After(exp) {
		return httpx.Invalid("that code has expired; send a new one")
	}
	want := box.Hash(subject + ":" + strings.TrimSpace(code))
	if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) != 1 {
		meta[keyAttempts] = Count(meta[keyAttempts]) + 1
		return httpx.Invalid("that code is not right")
	}
	Clear(meta)
	return nil
}

// Clear removes a live code from meta (on success, or when the record closes).
func Clear(meta map[string]any) {
	delete(meta, keyHash)
	delete(meta, keyExpires)
}

// Hide returns meta without the code hash, for anything leaving the API.
func Hide(meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		if k != keyHash {
			out[k] = v
		}
	}
	return out
}

// Count reads a counter that may have come back from JSON as a float.
func Count(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	}
	return 0
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case time.Time:
		return t.Format(time.RFC3339Nano)
	}
	return ""
}
