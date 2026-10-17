package docs

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/document"
	"github.com/bengobox/maskani-api/internal/ent/documentsignature"
	"github.com/bengobox/maskani-api/internal/events"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/otp"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// DocumentExecuted tells key-date reminders and the record's people that an agreement is signed.
const DocumentExecuted = "document.executed"

// SetSigning gives the service the box that keeps one-time codes for accepting agreements.
func (s *Service) SetSigning(box *secure.Box) { s.box = box }

// Acceptance is one person accepting an agreement by reading back a code sent to the phone or email
// on their record, in the portal or at the office.
type Acceptance struct {
	PartyID   uuid.UUID
	Code      string
	Via       string // portal, office
	IP        string
	UserAgent string
}

// signable loads an issued agreement that still waits for signatures.
func (s *Service) signable(ctx context.Context, id uuid.UUID) (*ent.Document, error) {
	d, err := s.client.Document.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	k, ok := DocKindOf(d.Kind)
	if !ok || !k.NeedsSignature {
		return nil, httpx.Invalid("this document is a notice, not an agreement; it is not signed")
	}
	if d.Status != document.StatusIssued && d.Status != document.StatusPartlySigned {
		return nil, httpx.Conflict("this document is not waiting for signatures")
	}
	return d, nil
}

// signedParties returns who has accepted a document so far.
func (s *Service) signedParties(ctx context.Context, docID uuid.UUID) (map[string]bool, error) {
	rows, err := s.client.DocumentSignature.Query().Where(documentsignature.DocumentID(docID), documentsignature.PartyIDNotNil()).
		Select(documentsignature.FieldPartyID).All(ctx)
	out := map[string]bool{}
	for _, r := range rows {
		out[r.PartyID.String()] = true
	}
	return out, err
}

// codeMeta is the code state kept for one person on the document's metadata.
func codeMeta(d *ent.Document, partyID uuid.UUID) map[string]any {
	all, _ := d.Metadata["acceptance_codes"].(map[string]any)
	m, _ := all[partyID.String()].(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return maps.Clone(m)
}

func withCodeMeta(d *ent.Document, partyID uuid.UUID, m map[string]any) map[string]any {
	meta := maps.Clone(d.Metadata)
	if meta == nil {
		meta = map[string]any{}
	}
	all, _ := meta["acceptance_codes"].(map[string]any)
	all = maps.Clone(all)
	if all == nil {
		all = map[string]any{}
	}
	all[partyID.String()] = m
	meta["acceptance_codes"] = all
	return meta
}

// SendAcceptanceCode sends a person named on the agreement a code to accept it, to the phone and
// email on their record.
func (s *Service) SendAcceptanceCode(ctx context.Context, docID, partyID uuid.UUID) error {
	if s.box == nil {
		return httpx.Unavailable("document acceptance is not set up")
	}
	d, err := s.signable(ctx, docID)
	if err != nil {
		return err
	}
	if !slices.Contains(d.PartyIds, partyID.String()) {
		return httpx.Forbidden("this person is not named on the document")
	}
	if done, err := s.signedParties(ctx, d.ID); err != nil {
		return err
	} else if done[partyID.String()] {
		return httpx.Conflict("this person has already accepted the document")
	}
	p, err := s.client.Party.Get(ctx, partyID)
	if err != nil {
		return err
	}
	m := codeMeta(d, partyID)
	code, err := otp.Issue(m, s.box, d.ID.String()+":"+partyID.String())
	if err != nil {
		return err
	}
	if err := s.client.Document.UpdateOneID(d.ID).SetMetadata(withCodeMeta(d, partyID, m)).Exec(ctx); err != nil {
		return err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	return otp.Publish(ctx, s.client.OutboxEvent, tenantID, d.ID.String()+":"+partyID.String(), m, "document",
		partyName(p), p.Phone, p.Email, code)
}

// Accept records a person's acceptance with the document's hash, the time and where it came from.
// Once everyone named has accepted, the document is executed.
func (s *Service) Accept(ctx context.Context, docID uuid.UUID, a Acceptance) (*ent.Document, error) {
	if s.box == nil {
		return nil, httpx.Unavailable("document acceptance is not set up")
	}
	d, err := s.signable(ctx, docID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(d.PartyIds, a.PartyID.String()) {
		return nil, httpx.Forbidden("this person is not named on the document")
	}
	m := codeMeta(d, a.PartyID)
	if err := otp.Check(m, s.box, d.ID.String()+":"+a.PartyID.String(), a.Code); err != nil {
		if uerr := s.client.Document.UpdateOneID(d.ID).SetMetadata(withCodeMeta(d, a.PartyID, m)).Exec(ctx); uerr != nil {
			return nil, uerr
		}
		return nil, err
	}
	done, err := s.signedParties(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	if done[a.PartyID.String()] {
		return nil, httpx.Conflict("this person has already accepted the document")
	}
	done[a.PartyID.String()] = true
	now := time.Now()
	ua := a.UserAgent
	if len(ua) > 300 {
		ua = ua[:300]
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.DocumentSignature.Create().SetDocumentID(d.ID).SetPartyID(a.PartyID).SetMethod(documentsignature.MethodOtpAcceptance).
		SetSignedAt(now).SetDocumentHash(d.Sha256).SetIP(a.IP).SetDevice(map[string]any{"via": a.Via, "user_agent": ua}).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	all := true
	for _, p := range d.PartyIds {
		all = all && done[p]
	}
	if d, err = s.advance(ctx, tx, d, withCodeMeta(d, a.PartyID, m), all, now); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return HideCodes(d), tx.Commit()
}

// RecordWetSigned records a signed paper copy the office photographed (uploaded as media of kind
// documents): the agreement is executed.
func (s *Service) RecordWetSigned(ctx context.Context, docID, by uuid.UUID, keys []string) (*ent.Document, error) {
	d, err := s.signable(ctx, docID)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 || len(keys) > 20 {
		return nil, httpx.Invalid("upload between 1 and 20 photos of the signed pages")
	}
	tenantID, err := tenantguard.MustTenant(ctx)
	if err != nil {
		return nil, err
	}
	prefix := "maskani/" + tenantID.String() + "/documents/"
	for _, k := range keys {
		if !strings.HasPrefix(k, prefix) || strings.Contains(k, "..") {
			return nil, httpx.Invalid("upload the signed pages through the documents upload first")
		}
	}
	now := time.Now()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.DocumentSignature.Create().SetDocumentID(d.ID).SetMethod(documentsignature.MethodWetUpload).SetSignedAt(now).
		SetDocumentHash(d.Sha256).SetScanFileKey(strings.Join(keys, ",")).
		SetDevice(map[string]any{"via": "office", "recorded_by": by.String()}).Exec(ctx); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if d, err = s.advance(ctx, tx, d, maps.Clone(d.Metadata), true, now); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return HideCodes(d), tx.Commit()
}

// advance moves the document to partly signed or executed and announces execution.
func (s *Service) advance(ctx context.Context, tx *ent.Tx, d *ent.Document, meta map[string]any, all bool, now time.Time) (*ent.Document, error) {
	if meta == nil {
		meta = map[string]any{}
	}
	if all {
		delete(meta, "acceptance_codes")
	}
	u := tx.Document.UpdateOneID(d.ID).SetMetadata(meta)
	if !all {
		return u.SetStatus(document.StatusPartlySigned).Save(ctx)
	}
	out, err := u.SetStatus(document.StatusExecuted).SetExecutedAt(now).Save(ctx)
	if err != nil {
		return nil, err
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	return out, events.Publish(ctx, tx.OutboxEvent, tenantID, d.ID.String(), DocumentExecuted, map[string]any{
		"document_id": d.ID, "number": d.Number, "kind": d.Kind, "title": d.Title, "entity_type": d.EntityType,
		"entity_id": d.EntityID, "executed_at": now.Format(time.RFC3339)})
}

// Signatures lists a document's acceptances and signed copies, oldest first (scan keys included
// for staff, who open them through signed media links).
func (s *Service) Signatures(ctx context.Context, docID uuid.UUID) ([]*ent.DocumentSignature, error) {
	return s.client.DocumentSignature.Query().Where(documentsignature.DocumentID(docID)).
		Order(ent.Asc(documentsignature.FieldSignedAt)).All(ctx)
}

// DocumentView is a document as lists return it: code hashes hidden, whether it is an agreement,
// and (portal) whether the caller has already accepted it.
type DocumentView struct {
	*ent.Document
	NeedsSignature bool `json:"needs_signature"`
	AcceptedByMe   bool `json:"accepted_by_me,omitempty"`
}

// Views builds list views; with partyIDs it marks the documents those parties accepted, from one
// query over the listed documents.
func (s *Service) Views(ctx context.Context, rows []*ent.Document, partyIDs []uuid.UUID) ([]DocumentView, error) {
	out := make([]DocumentView, 0, len(rows))
	accepted := map[uuid.UUID]bool{}
	if len(partyIDs) > 0 && len(rows) > 0 {
		ids := make([]uuid.UUID, len(rows))
		for i, d := range rows {
			ids[i] = d.ID
		}
		sigs, err := s.client.DocumentSignature.Query().Where(documentsignature.DocumentIDIn(ids...),
			documentsignature.PartyIDIn(partyIDs...)).Select(documentsignature.FieldDocumentID).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, sg := range sigs {
			accepted[sg.DocumentID] = true
		}
	}
	for _, d := range rows {
		k, _ := DocKindOf(d.Kind)
		out = append(out, DocumentView{Document: HideCodes(d), NeedsSignature: k.NeedsSignature, AcceptedByMe: accepted[d.ID]})
	}
	return out, nil
}

// HideCodes drops one-time code hashes from a document before it leaves the API.
func HideCodes(d *ent.Document) *ent.Document {
	if d == nil || d.Metadata == nil {
		return d
	}
	if _, ok := d.Metadata["acceptance_codes"]; ok {
		meta := maps.Clone(d.Metadata)
		delete(meta, "acceptance_codes")
		d.Metadata = meta
	}
	return d
}
