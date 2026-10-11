package sales

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/document"
	"github.com/bengobox/maskani-api/internal/ent/salecontract"
	"github.com/bengobox/maskani-api/internal/ent/titlestage"
	"github.com/bengobox/maskani-api/internal/ent/unit"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
)

// titleReleased is the last title stage: the title in the owner's hands. Marking it done moves the
// contract and the unit to titled.
const titleReleased = "title_released"

// CatalogueFunc reads a tenant's merged catalogue (settings.Catalogue), wired in app.go.
type CatalogueFunc func(ctx context.Context, tenantID uuid.UUID, kind string) ([]*ent.CatalogEntry, error)

// SetCatalogue wires the catalogue the title stages come from.
func (s *Service) SetCatalogue(f CatalogueFunc) { s.catalogue = f }

// TitleStageView is one stage on the way to title: the estate's catalogue entry with its progress.
type TitleStageView struct {
	Code       string     `json:"code"`
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	StageDate  *time.Time `json:"stage_date,omitempty"`
	Reference  string     `json:"reference,omitempty"`
	DocumentID *uuid.UUID `json:"document_id,omitempty"`
	Notes      string     `json:"notes,omitempty"`
}

// TitleStages lists the estate's title stages in order with each one's progress on a contract.
func (s *Service) TitleStages(ctx context.Context, contractID uuid.UUID) ([]TitleStageView, error) {
	if s.catalogue == nil {
		return nil, httpx.Unavailable("title stages are not set up")
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	cat, err := s.catalogue(ctx, tenantID, "title_stage")
	if err != nil {
		return nil, err
	}
	rows, err := s.client.TitleStage.Query().Where(titlestage.ContractID(contractID)).All(ctx)
	if err != nil {
		return nil, err
	}
	byCode := map[string]*ent.TitleStage{}
	for _, r := range rows {
		byCode[r.Stage] = r
	}
	out := make([]TitleStageView, 0, len(cat))
	for _, c := range cat {
		v := TitleStageView{Code: c.Code, Name: c.Name, Status: string(titlestage.StatusPending)}
		if r := byCode[c.Code]; r != nil {
			v.Status, v.StageDate, v.Reference, v.DocumentID, v.Notes = string(r.Status), r.StageDate, r.Reference, r.DocumentID, r.Notes
		}
		out = append(out, v)
	}
	return out, nil
}

// TitleStageInput updates one stage.
type TitleStageInput struct {
	Status     string     `json:"status"` // pending, in_progress, done, blocked
	StageDate  *time.Time `json:"stage_date"`
	Reference  string     `json:"reference"`
	DocumentID *uuid.UUID `json:"document_id"`
	Notes      string     `json:"notes"`
}

// SetTitleStage records progress on one stage of a handed-over or fully paid sale. A done stage
// needs its date; the document, when given, must be one issued or uploaded for this estate. The
// last stage done moves the contract and unit to titled.
func (s *Service) SetTitleStage(ctx context.Context, contractID uuid.UUID, code string, in TitleStageInput) ([]TitleStageView, error) {
	sc, err := s.client.SaleContract.Get(ctx, contractID)
	if err != nil {
		return nil, err
	}
	if sc.Status != salecontract.StatusFullyPaid && sc.Status != salecontract.StatusHandedOver && sc.Status != salecontract.StatusTitled {
		return nil, httpx.Conflict("title work starts once the contract is fully paid")
	}
	stages, err := s.TitleStages(ctx, contractID)
	if err != nil {
		return nil, err
	}
	known := false
	for _, st := range stages {
		known = known || st.Code == code
	}
	if !known {
		return nil, httpx.Invalid("that is not one of the estate's title stages")
	}
	status := titlestage.Status(in.Status)
	if titlestage.StatusValidator(status) != nil {
		return nil, httpx.Invalid("status must be pending, in_progress, done or blocked")
	}
	if status == titlestage.StatusDone && in.StageDate == nil {
		return nil, httpx.Invalid("give the date the stage was done")
	}
	in.Reference, in.Notes = strings.TrimSpace(in.Reference), strings.TrimSpace(in.Notes)
	if len(in.Reference) > 120 || len(in.Notes) > 2000 {
		return nil, httpx.Invalid("keep the reference under 120 and the notes under 2000 characters")
	}
	if in.DocumentID != nil {
		if ok, err := s.client.Document.Query().Where(document.ID(*in.DocumentID)).Exist(ctx); err != nil {
			return nil, err
		} else if !ok {
			return nil, httpx.Invalid("that document is not on record")
		}
	}
	existing, err := s.client.TitleStage.Query().Where(titlestage.ContractID(sc.ID), titlestage.Stage(code)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		c := s.client.TitleStage.Create().SetContractID(sc.ID).SetUnitID(sc.UnitID).SetStage(code).SetStatus(status).
			SetReference(in.Reference).SetNotes(in.Notes).SetNillableStageDate(in.StageDate).SetNillableDocumentID(in.DocumentID)
		err = c.Exec(ctx)
	case err == nil:
		u := existing.Update().SetStatus(status).SetReference(in.Reference).SetNotes(in.Notes)
		if in.StageDate != nil {
			u.SetStageDate(*in.StageDate)
		} else {
			u.ClearStageDate()
		}
		if in.DocumentID != nil {
			u.SetDocumentID(*in.DocumentID)
		} else {
			u.ClearDocumentID()
		}
		err = u.Exec(ctx)
	}
	if err != nil {
		return nil, err
	}
	if code == titleReleased && status == titlestage.StatusDone && sc.Status != salecontract.StatusTitled {
		if err := s.client.SaleContract.UpdateOneID(sc.ID).SetStatus(salecontract.StatusTitled).Exec(ctx); err != nil {
			return nil, err
		}
		if err := s.client.Unit.UpdateOneID(sc.UnitID).SetSaleStatus(unit.SaleStatusTitled).Exec(ctx); err != nil {
			return nil, err
		}
	}
	return s.TitleStages(ctx, contractID)
}
