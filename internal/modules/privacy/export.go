package privacy

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/billquery"
	"github.com/bengobox/maskani-api/internal/ent/privacyrequest"
	"github.com/bengobox/maskani-api/internal/ent/unitaccount"
	"github.com/bengobox/maskani-api/internal/ent/unitparty"
	"github.com/bengobox/maskani-api/internal/ent/vehicle"
	"github.com/bengobox/maskani-api/internal/ent/visitorpass"
	"github.com/bengobox/maskani-api/internal/ent/workorder"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/docs"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

// exportRows bounds each section; a person's own records stay well inside it.
const exportRows = 500

// section is one table of the export: a CSV file and a key in data.json.
type section struct {
	Name   string
	Header []string
	Rows   [][]string
}

// Export builds the person's data as a zip (data.json plus one CSV per section) from the live
// records. Staff may export once the request is verified; the person once it is completed.
func (s *Service) Export(ctx context.Context, id uuid.UUID, forPerson bool) (*docs.File, error) {
	req, err := s.client.PrivacyRequest.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Kind != privacyrequest.KindAccess || req.PartyID == nil {
		return nil, httpx.Invalid("only an access request has an export")
	}
	if forPerson && req.Status != privacyrequest.StatusCompleted {
		return nil, httpx.Conflict("your copy is ready once the estate office completes the request")
	}
	if req.Status == privacyrequest.StatusReceived || req.Status == privacyrequest.StatusRejected {
		return nil, httpx.Conflict("verify the person before exporting their data")
	}
	sections, profile, err := s.collect(ctx, *req.PartyID)
	if err != nil {
		return nil, err
	}
	body, err := bundle(profile, sections)
	if err != nil {
		return nil, err
	}
	return &docs.File{Body: body, Mime: "application/zip", Name: "my-data-" + time.Now().Format("2006-01-02") + ".zip"}, nil
}

func (s *Service) collect(ctx context.Context, partyID uuid.UUID) ([]section, map[string]any, error) {
	p, err := s.client.Party.Get(ctx, partyID)
	if err != nil {
		return nil, nil, err
	}
	v := s.register.View(p)
	profile := map[string]any{
		"name": p.DisplayName, "first_name": p.FirstName, "last_name": p.LastName, "company_name": p.CompanyName,
		"registration_number": p.RegistrationNumber, "phone": p.Phone, "alt_phone": p.AltPhone, "email": p.Email,
		"id_type": p.IDType, "national_id": v.NationalIDMasked, "kra_pin": v.KRAPinMasked, "nationality": p.Nationality,
		"postal_address": p.PostalAddress, "preferred_channel": p.PreferredChannel, "language": p.Language,
		"consents": p.Consents, "terms_accepted_version": p.TermsAcceptedVersion, "terms_accepted_at": p.TermsAcceptedAt,
		"custom_fields": p.CustomFields, "status": p.Status, "added_on": p.CreatedAt,
	}
	var out []section

	links, err := s.client.UnitParty.Query().Where(unitparty.PartyID(partyID)).WithUnit().
		Order(ent.Desc(unitparty.FieldStartDate)).Limit(exportRows).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	sec := section{Name: "units", Header: []string{"unit", "role", "from", "to", "bills_sent_to_you"}}
	for _, l := range links {
		code := ""
		if l.Edges.Unit != nil {
			code = l.Edges.Unit.Code
		}
		sec.Rows = append(sec.Rows, []string{code, string(l.Role), day(&l.StartDate), day(l.EndDate), fmt.Sprint(l.BillTo)})
	}
	out = append(out, sec)

	accs, err := s.client.UnitAccount.Query().Where(unitaccount.PrimaryPartyID(partyID)).Limit(exportRows).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	sec = section{Name: "accounts", Header: []string{"account", "name_on_account", "phone_on_account", "balance", "last_payment"}}
	for _, a := range accs {
		sec.Rows = append(sec.Rows, []string{a.AccountRef, a.CustomerName, a.CustomerPhone, a.Balance.StringFixed(2), day(a.LastPaymentAt)})
	}
	out = append(out, sec)

	cars, err := s.client.Vehicle.Query().Where(vehicle.PartyID(partyID)).Limit(exportRows).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	sec = section{Name: "vehicles", Header: []string{"plate", "make", "model", "colour", "status"}}
	for _, c := range cars {
		sec.Rows = append(sec.Rows, []string{c.Plate, c.Make, c.Model, c.Colour, string(c.Status)})
	}
	out = append(out, sec)

	// Visitors are other people: their phones stay masked in your copy.
	passes, err := s.client.VisitorPass.Query().Where(visitorpass.HostPartyID(partyID)).
		Order(ent.Desc(visitorpass.FieldValidFrom)).Limit(exportRows).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	sec = section{Name: "visitor_passes", Header: []string{"visitor", "visitor_phone", "type", "vehicle", "valid_from", "valid_to", "status"}}
	for _, v := range passes {
		sec.Rows = append(sec.Rows, []string{v.VisitorName, secure.MaskPhone(v.VisitorPhone), string(v.PassType), v.VehiclePlate,
			stamp(v.ValidFrom), stamp(v.ValidTo), string(v.Status)})
	}
	out = append(out, sec)

	wos, err := s.client.WorkOrder.Query().Where(workorder.RequestedByPartyID(partyID)).
		Order(ent.Desc(workorder.FieldCreatedAt)).Limit(exportRows).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	sec = section{Name: "requests", Header: []string{"number", "title", "category", "status", "raised"}}
	for _, w := range wos {
		sec.Rows = append(sec.Rows, []string{w.Number, w.Title, w.Category, string(w.Status), stamp(w.CreatedAt)})
	}
	out = append(out, sec)

	qs, err := s.client.BillQuery.Query().Where(billquery.PartyID(partyID)).
		Order(ent.Desc(billquery.FieldCreatedAt)).Limit(exportRows).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	sec = section{Name: "bill_queries", Header: []string{"subject", "status", "raised"}}
	for _, q := range qs {
		sec.Rows = append(sec.Rows, []string{q.Subject, string(q.Status), stamp(q.CreatedAt)})
	}
	out = append(out, sec)

	prs, err := s.client.PrivacyRequest.Query().Where(privacyrequest.PartyID(partyID)).
		Order(ent.Desc(privacyrequest.FieldCreatedAt)).Limit(exportRows).All(ctx)
	if err != nil {
		return nil, nil, err
	}
	sec = section{Name: "privacy_requests", Header: []string{"kind", "status", "received", "due", "closed"}}
	for _, r := range prs {
		sec.Rows = append(sec.Rows, []string{string(r.Kind), string(r.Status), stamp(r.ReceivedAt), day(&r.DueAt), day(r.CompletedAt)})
	}
	out = append(out, sec)
	return out, profile, nil
}

const readme = `Your data held by the estate

data.json has everything in one file. Each CSV holds one part:
units, accounts, vehicles, visitor_passes, requests, bill_queries and privacy_requests.

Your identity numbers show only their last four characters, and your visitors' phones are masked,
because the file may travel by email. Bills, receipts and statements are in the portal under each
account, where you can download them at any time.
`

// bundle writes data.json, one CSV per section and a short readme into a zip.
func bundle(profile map[string]any, sections []section) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	all := map[string]any{"profile": profile, "exported_at": time.Now()}
	for _, sec := range sections {
		rows := make([]map[string]string, 0, len(sec.Rows))
		for _, r := range sec.Rows {
			m := make(map[string]string, len(sec.Header))
			for i, h := range sec.Header {
				m[h] = r[i]
			}
			rows = append(rows, m)
		}
		all[sec.Name] = rows
		f, err := zw.Create(sec.Name + ".csv")
		if err != nil {
			return nil, err
		}
		cw := csv.NewWriter(f)
		_ = cw.Write(sec.Header)
		_ = cw.WriteAll(sec.Rows)
		if err := cw.Error(); err != nil {
			return nil, err
		}
	}
	f, err := zw.Create("data.json")
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(all); err != nil {
		return nil, err
	}
	f, err = zw.Create("README.txt")
	if err != nil {
		return nil, err
	}
	if _, err := f.Write([]byte(readme)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func day(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
