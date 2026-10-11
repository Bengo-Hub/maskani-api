// Package privacy handles data subject requests from residents and owners under the Kenya Data
// Protection Act 2019: a copy of their data, a correction, or erasure. A request made in the portal
// is verified by the signed-in session; one recorded by the estate office is verified with a code
// sent to the phone or email on record, or in person. Erasure anonymises the person and keeps the
// financial and statutory records (bills, payments, contracts), which point at the party by id only.
package privacy

import (
	"context"
	"crypto/subtle"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/party"
	"github.com/bengobox/maskani-api/internal/ent/privacyrequest"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/page"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// Events: the office hears of a new request; the person gets the one-time code.
const (
	Requested    = "privacy.requested"
	OTPRequested = "privacy.otp_requested"
)

// Deadlines from receipt, per the Data Protection (General) Regulations 2021: access within 7
// days, correction and erasure within 14.
var dueDays = map[privacyrequest.Kind]int{
	privacyrequest.KindAccess:     7,
	privacyrequest.KindCorrection: 14,
	privacyrequest.KindDeletion:   14,
}

// The one-time code: 6 digits, valid 5 minutes (the shared OTP templates say so), 5 tries per code
// and 5 codes per request.
const (
	codeTTL      = 5 * time.Minute
	maxAttempts  = 5
	maxCodesSent = 5
)

// Service is the privacy service.
type Service struct {
	client   *ent.Client
	register *register.Service
	box      *secure.Box
	log      *zap.Logger
}

// NewService builds the service.
func NewService(client *ent.Client, reg *register.Service, box *secure.Box, log *zap.Logger) *Service {
	return &Service{client: client, register: reg, box: box, log: log.Named("privacy")}
}

// Input is a new request. Source is "portal" (the person, already signed in) or "office".
type Input struct {
	PartyID uuid.UUID `json:"party_id"`
	Kind    string    `json:"kind"`
	Notes   string    `json:"notes"`
}

// Create records a request with its deadline. A portal request is verified at once; an office one
// waits for the code or an in-person check. One open request per kind per person.
func (s *Service) Create(ctx context.Context, in Input, source string, by uuid.UUID) (*ent.PrivacyRequest, error) {
	kind := privacyrequest.Kind(in.Kind)
	if privacyrequest.KindValidator(kind) != nil {
		return nil, httpx.Invalid("kind must be access, correction or deletion")
	}
	in.Notes = strings.TrimSpace(in.Notes)
	if len(in.Notes) > 2000 {
		return nil, httpx.Invalid("keep the notes under 2000 characters")
	}
	if kind == privacyrequest.KindCorrection && in.Notes == "" {
		return nil, httpx.Invalid("say what should be corrected")
	}
	p, err := s.client.Party.Get(ctx, in.PartyID)
	if err != nil {
		return nil, err
	}
	if p.Status == party.StatusAnonymised {
		return nil, httpx.Conflict("this person's details have already been erased")
	}
	open, err := s.client.PrivacyRequest.Query().Where(privacyrequest.PartyID(p.ID), privacyrequest.KindEQ(kind),
		privacyrequest.StatusIn(privacyrequest.StatusReceived, privacyrequest.StatusVerified, privacyrequest.StatusInProgress)).Exist(ctx)
	if err != nil {
		return nil, err
	}
	if open {
		return nil, httpx.Conflict("a request of this kind is already open for this person")
	}
	now := time.Now()
	status := privacyrequest.StatusReceived
	meta := map[string]any{"source": source, "recorded_by": by.String()}
	if source == "portal" {
		status = privacyrequest.StatusVerified
		meta["verified_by"] = "portal"
		meta["verified_at"] = now
	}
	req, err := s.client.PrivacyRequest.Create().SetPartyID(p.ID).SetRequesterName(p.DisplayName).
		SetRequesterPhone(p.Phone).SetKind(kind).SetStatus(status).SetReceivedAt(now).
		SetDueAt(now.AddDate(0, 0, dueDays[kind])).SetNotes(in.Notes).SetMetadata(meta).Save(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	_ = events.Publish(ctx, s.client.OutboxEvent, tenantID, req.ID.String(), Requested, map[string]any{
		"request_id": req.ID, "kind": string(kind), "name": p.DisplayName, "source": source,
		"due_at": req.DueAt.Format("2006-01-02")})
	return req, nil
}

// Filter narrows the office list.
type Filter struct {
	Status  string
	Overdue bool
	PartyID *uuid.UUID
}

// List is a keyset page, newest first.
func (s *Service) List(ctx context.Context, f Filter, p page.Params) (page.Result[*ent.PrivacyRequest], error) {
	q := s.client.PrivacyRequest.Query()
	if f.Status != "" {
		q = q.Where(privacyrequest.StatusEQ(privacyrequest.Status(f.Status)))
	}
	if f.Overdue {
		q = q.Where(privacyrequest.StatusIn(privacyrequest.StatusReceived, privacyrequest.StatusVerified, privacyrequest.StatusInProgress),
			privacyrequest.DueAtLT(time.Now()))
	}
	if f.PartyID != nil {
		q = q.Where(privacyrequest.PartyID(*f.PartyID))
	}
	rows, err := q.Where(p.Predicate()).Modify(page.Order()).Limit(p.Limit + 1).All(ctx)
	if err != nil {
		return page.Result[*ent.PrivacyRequest]{}, err
	}
	for _, r := range rows {
		hideSecrets(r)
	}
	return page.Build(rows, p.Limit, func(r *ent.PrivacyRequest) (uuid.UUID, time.Time) { return r.ID, r.CreatedAt }), nil
}

// ForParties returns a person's own requests (the portal), newest first.
func (s *Service) ForParties(ctx context.Context, partyIDs []uuid.UUID) ([]*ent.PrivacyRequest, error) {
	if len(partyIDs) == 0 {
		return []*ent.PrivacyRequest{}, nil
	}
	rows, err := s.client.PrivacyRequest.Query().Where(privacyrequest.PartyIDIn(partyIDs...)).
		Order(ent.Desc(privacyrequest.FieldCreatedAt)).Limit(50).All(ctx)
	for _, r := range rows {
		hideSecrets(r)
	}
	return rows, err
}

// Get returns one request.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*ent.PrivacyRequest, error) {
	r, err := s.client.PrivacyRequest.Get(ctx, id)
	if err == nil {
		hideSecrets(r)
	}
	return r, err
}

// hideSecrets drops the code hash from a request before it leaves the service.
func hideSecrets(r *ent.PrivacyRequest) {
	if r.Metadata == nil {
		return
	}
	m := make(map[string]any, len(r.Metadata))
	for k, v := range r.Metadata {
		if k != "otp_hash" {
			m[k] = v
		}
	}
	r.Metadata = m
}

// SendCode sends a fresh code to the phone and email on record (never to a number the caller
// gives, so only the real person can answer it).
func (s *Service) SendCode(ctx context.Context, id uuid.UUID) error {
	req, err := s.client.PrivacyRequest.Get(ctx, id)
	if err != nil {
		return err
	}
	if req.Status != privacyrequest.StatusReceived || req.PartyID == nil {
		return httpx.Conflict("only a request waiting for verification takes a code")
	}
	meta := copyMeta(req.Metadata)
	if num(meta["codes_sent"]) >= maxCodesSent {
		return httpx.Conflict("five codes have been sent; verify this person in person instead")
	}
	p, err := s.client.Party.Get(ctx, *req.PartyID)
	if err != nil {
		return err
	}
	if p.Phone == "" && p.Email == "" {
		return httpx.Invalid("there is no phone or email on record; verify this person in person")
	}
	code, err := secure.RandomDigits(6)
	if err != nil {
		return err
	}
	meta["otp_hash"] = s.box.Hash(req.ID.String() + ":" + code)
	meta["otp_expires_at"] = time.Now().Add(codeTTL)
	meta["otp_attempts"] = 0
	meta["codes_sent"] = num(meta["codes_sent"]) + 1
	if err := s.client.PrivacyRequest.UpdateOneID(req.ID).SetMetadata(meta).Exec(ctx); err != nil {
		return err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	return events.Publish(ctx, s.client.OutboxEvent, tenantID, req.ID.String()+":code:"+strconv.Itoa(num(meta["codes_sent"])), OTPRequested,
		map[string]any{"request_id": req.ID, "name": p.DisplayName, "phone": p.Phone, "email": p.Email, "otp": code})
}

// Verify checks the code, or records an in-person check by the office (an ID document seen).
func (s *Service) Verify(ctx context.Context, id, by uuid.UUID, code string, inPerson bool, note string) (*ent.PrivacyRequest, error) {
	req, err := s.client.PrivacyRequest.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Status != privacyrequest.StatusReceived {
		return nil, httpx.Conflict("this request is not waiting for verification")
	}
	meta := copyMeta(req.Metadata)
	now := time.Now()
	if inPerson {
		if strings.TrimSpace(note) == "" {
			return nil, httpx.Invalid("say how the person was identified, such as the ID document seen")
		}
		meta["verified_by"] = "in_person"
		meta["verified_note"] = strings.TrimSpace(note)
	} else {
		hash, _ := meta["otp_hash"].(string)
		if hash == "" {
			return nil, httpx.Invalid("send a code first")
		}
		if num(meta["otp_attempts"]) >= maxAttempts {
			return nil, httpx.Conflict("too many wrong codes; send a new one")
		}
		if exp, err := time.Parse(time.RFC3339Nano, str(meta["otp_expires_at"])); err != nil || now.After(exp) {
			return nil, httpx.Invalid("that code has expired; send a new one")
		}
		want := s.box.Hash(req.ID.String() + ":" + strings.TrimSpace(code))
		if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) != 1 {
			meta["otp_attempts"] = num(meta["otp_attempts"]) + 1
			if err := s.client.PrivacyRequest.UpdateOneID(req.ID).SetMetadata(meta).Exec(ctx); err != nil {
				return nil, err
			}
			return nil, httpx.Invalid("that code is not right")
		}
		meta["verified_by"] = "code"
	}
	delete(meta, "otp_hash")
	delete(meta, "otp_expires_at")
	meta["verified_at"] = now
	meta["verified_by_user"] = by.String()
	out, err := s.client.PrivacyRequest.UpdateOneID(req.ID).SetStatus(privacyrequest.StatusVerified).SetMetadata(meta).Save(ctx)
	if err == nil {
		hideSecrets(out)
	}
	return out, err
}

// Complete closes an access or correction request with what was done. Erasure completes through
// Anonymise.
func (s *Service) Complete(ctx context.Context, id, by uuid.UUID, outcome string) (*ent.PrivacyRequest, error) {
	req, err := s.verified(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Kind == privacyrequest.KindDeletion {
		return nil, httpx.Invalid("erasure completes by erasing the person's details")
	}
	outcome = strings.TrimSpace(outcome)
	if req.Kind == privacyrequest.KindCorrection && outcome == "" {
		return nil, httpx.Invalid("say what was corrected")
	}
	return s.close(ctx, req, by, privacyrequest.StatusCompleted, "outcome", outcome)
}

// Reject closes a request with the reason, such as identity not confirmed or a record the law
// requires the estate to keep.
func (s *Service) Reject(ctx context.Context, id, by uuid.UUID, reason string) (*ent.PrivacyRequest, error) {
	req, err := s.client.PrivacyRequest.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !open(req) {
		return nil, httpx.Conflict("this request is already closed")
	}
	if reason = strings.TrimSpace(reason); reason == "" {
		return nil, httpx.Invalid("give the reason; the person is told it")
	}
	return s.close(ctx, req, by, privacyrequest.StatusRejected, "reason", reason)
}

func (s *Service) close(ctx context.Context, req *ent.PrivacyRequest, by uuid.UUID, status privacyrequest.Status, key, text string) (*ent.PrivacyRequest, error) {
	meta := copyMeta(req.Metadata)
	if text != "" {
		meta[key] = text
	}
	meta["closed_by"] = by.String()
	delete(meta, "otp_hash")
	out, err := s.client.PrivacyRequest.UpdateOneID(req.ID).SetStatus(status).SetCompletedAt(time.Now()).SetMetadata(meta).Save(ctx)
	if err == nil {
		hideSecrets(out)
	}
	return out, err
}

// verified loads a request that has passed verification and is still open.
func (s *Service) verified(ctx context.Context, id uuid.UUID) (*ent.PrivacyRequest, error) {
	req, err := s.client.PrivacyRequest.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Status != privacyrequest.StatusVerified && req.Status != privacyrequest.StatusInProgress {
		return nil, httpx.Conflict("verify the person first, or the request is already closed")
	}
	if req.PartyID == nil {
		return nil, httpx.Invalid("this request has no person")
	}
	return req, nil
}

func open(r *ent.PrivacyRequest) bool {
	return r.Status == privacyrequest.StatusReceived || r.Status == privacyrequest.StatusVerified || r.Status == privacyrequest.StatusInProgress
}

func copyMeta(m map[string]any) map[string]any {
	out := make(map[string]any, len(m)+4)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// num reads a counter that may have come back from JSON as a float.
func num(v any) int {
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
