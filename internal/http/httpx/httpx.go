// Package httpx holds the small request and response helpers every handler uses.
package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// JSON writes a JSON body.
func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// Error writes {"error":msg,"code":code}.
func Error(w http.ResponseWriter, status int, code, msg string) {
	JSON(w, status, map[string]string{"error": msg, "code": code})
}

// Decode reads a JSON body into v (1 MB cap, unknown fields rejected).
func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		Error(w, http.StatusUnprocessableEntity, "validation_failed", "invalid request body: "+err.Error())
		return false
	}
	return true
}

// UUIDParam parses a chi URL parameter as a UUID, writing 400 on failure.
func UUIDParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		Error(w, http.StatusBadRequest, "bad_request", "invalid "+name)
		return uuid.Nil, false
	}
	return id, true
}

// QueryUUID parses an optional query UUID.
func QueryUUID(r *http.Request, name string) *uuid.UUID {
	v := strings.TrimSpace(r.URL.Query().Get(name))
	if v == "" {
		return nil
	}
	id, err := uuid.Parse(v)
	if err != nil {
		return nil
	}
	return &id
}

// ValidationError is a 422 the service raises for bad input.
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

// Invalid returns a ValidationError.
func Invalid(msg string) error { return &ValidationError{Msg: msg} }

// ForbiddenError is a 403 raised by services (scope checks).
type ForbiddenError struct{ Msg string }

func (e *ForbiddenError) Error() string { return e.Msg }

// Forbidden returns a ForbiddenError.
func Forbidden(msg string) error { return &ForbiddenError{Msg: msg} }

// ConflictError is a 409.
type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }

// Conflict returns a ConflictError.
func Conflict(msg string) error { return &ConflictError{Msg: msg} }

// UnavailableError is a 503: a service this one depends on did not answer.
type UnavailableError struct{ Msg string }

func (e *UnavailableError) Error() string { return e.Msg }

// Unavailable returns an UnavailableError.
func Unavailable(msg string) error { return &UnavailableError{Msg: msg} }

// Fail maps a service error to a response.
func Fail(w http.ResponseWriter, err error) {
	var ve *ValidationError
	var fe *ForbiddenError
	var ce *ConflictError
	var ue *UnavailableError
	switch {
	case errors.As(err, &ve):
		Error(w, http.StatusUnprocessableEntity, "validation_failed", ve.Msg)
	case errors.As(err, &fe):
		Error(w, http.StatusForbidden, "forbidden", fe.Msg)
	case errors.As(err, &ce):
		Error(w, http.StatusConflict, "conflict", ce.Msg)
	case errors.As(err, &ue):
		Error(w, http.StatusServiceUnavailable, "unavailable", ue.Msg)
	case ent.IsNotFound(err):
		Error(w, http.StatusNotFound, "not_found", "not found")
	case ent.IsConstraintError(err):
		Error(w, http.StatusConflict, "conflict", "a record with these details already exists")
	case ent.IsValidationError(err):
		Error(w, http.StatusUnprocessableEntity, "validation_failed", err.Error())
	case errors.Is(err, tenantguard.ErrNoTenant), errors.Is(err, tenantguard.ErrTenantMismatch):
		Error(w, http.StatusForbidden, "forbidden", "tenant scope violation")
	default:
		Log.Error("request failed", zap.Error(err))
		Error(w, http.StatusInternalServerError, "server_error", "something went wrong")
	}
}

// Log receives unexpected errors; set at startup.
var Log = zap.NewNop()
