// Package imports loads a property's units and owners from a CSV file (SRDD 16.1 step 4 and
// 16.2 step 1). Every import is validated first (dry run, nothing written), then committed in the
// background in batches, re-checking each row so a re-run never duplicates anything.
package imports

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/bengobox/maskani-api/internal/ent"
	"github.com/bengobox/maskani-api/internal/ent/importjob"
	"github.com/bengobox/maskani-api/internal/http/httpx"
	"github.com/bengobox/maskani-api/internal/modules/register"
	"github.com/bengobox/maskani-api/internal/platform/tenantguard"
	"github.com/bengobox/maskani-api/internal/shared/secure"
)

const (
	// MaxRows bounds one file; larger registers are split into several imports.
	MaxRows   = 5000
	batchSize = 500
	// bom is the UTF-8 byte order mark Excel writes at the start of a CSV.
	bom = "\xef\xbb\xbf"
)

// Columns of the units and owners template, in template order. Only unit_code is required.
var Columns = []string{"unit_code", "block", "unit_type", "bedrooms", "bathrooms", "size_sqm", "floor",
	"sale_status", "occupancy_status", "owner_name", "owner_phone", "owner_email", "owner_since"}

var (
	saleStatuses = map[string]bool{"not_for_sale": true, "available": true, "reserved": true, "under_agreement": true,
		"fully_paid": true, "handed_over": true, "titled": true, "in_default": true}
	occupancyStatuses = map[string]bool{"vacant": true, "owner_occupied": true, "tenanted": true, "under_renovation": true, "coming_vacant": true}
)

// Row is one parsed line of the file.
type Row struct {
	Line       int      `json:"line"`
	UnitCode   string   `json:"unit_code"`
	Block      string   `json:"block,omitempty"`
	UnitType   string   `json:"unit_type,omitempty"`
	Bedrooms   *int     `json:"bedrooms,omitempty"`
	Bathrooms  *int     `json:"bathrooms,omitempty"`
	SizeSqm    *float64 `json:"size_sqm,omitempty"`
	Floor      string   `json:"floor,omitempty"`
	SaleStatus string   `json:"sale_status,omitempty"`
	Occupancy  string   `json:"occupancy_status,omitempty"`
	OwnerName  string   `json:"owner_name,omitempty"`
	OwnerPhone string   `json:"owner_phone,omitempty"`
	OwnerEmail string   `json:"owner_email,omitempty"`
	OwnerSince string   `json:"owner_since,omitempty"`
}

// Plan is what a valid row will do when committed.
type Plan struct {
	Line   int    `json:"line"`
	Unit   string `json:"unit"`   // create or update
	Block  string `json:"block"`  // existing, create or none
	Owner  string `json:"owner"`  // existing, create or none
	Link   string `json:"link"`   // create, existing or none
	Detail string `json:"detail"` // unit code and owner, for the report
}

// RowError is one problem with one line.
type RowError struct {
	Line    int    `json:"line"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// Service runs imports.
type Service struct {
	client   *ent.Client
	register *register.Service
	log      *zap.Logger
}

// NewService builds the import service.
func NewService(client *ent.Client, reg *register.Service, log *zap.Logger) *Service {
	return &Service{client: client, register: reg, log: log}
}

// Parse reads the CSV (header row required, columns in any order, unknown columns ignored).
func Parse(r io.Reader) ([]Row, []RowError, error) {
	cr := csv.NewReader(r)
	cr.TrimLeadingSpace = true
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, nil, httpx.Invalid("the file is empty or not a CSV")
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, bom)))] = i
	}
	if _, ok := idx["unit_code"]; !ok {
		return nil, nil, httpx.Invalid("the header must include unit_code (download the template)")
	}
	get := func(rec []string, col string) string {
		if i, ok := idx[col]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	var rows []Row
	var errs []RowError
	line := 1
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			line++
			errs = append(errs, RowError{Line: line, Message: "could not read this line"})
			continue
		}
		// The reader skips empty lines, so take the file's own line number.
		line, _ = cr.FieldPos(0)
		if len(rows) >= MaxRows {
			return nil, nil, httpx.Invalid(fmt.Sprintf("at most %d rows per file; split the register into several files", MaxRows))
		}
		blank := true
		for _, v := range rec {
			if strings.TrimSpace(v) != "" {
				blank = false
				break
			}
		}
		if blank {
			continue
		}
		row := Row{Line: line, UnitCode: strings.ToUpper(get(rec, "unit_code")), Block: strings.ToUpper(get(rec, "block")),
			UnitType: get(rec, "unit_type"), Floor: get(rec, "floor"), SaleStatus: strings.ToLower(get(rec, "sale_status")),
			Occupancy: strings.ToLower(get(rec, "occupancy_status")), OwnerName: get(rec, "owner_name"),
			OwnerPhone: get(rec, "owner_phone"), OwnerEmail: get(rec, "owner_email"), OwnerSince: get(rec, "owner_since")}
		if v := get(rec, "bedrooms"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				errs = append(errs, RowError{Line: line, Field: "bedrooms", Message: "must be a whole number"})
			} else {
				row.Bedrooms = &n
			}
		}
		if v := get(rec, "bathrooms"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				errs = append(errs, RowError{Line: line, Field: "bathrooms", Message: "must be a whole number"})
			} else {
				row.Bathrooms = &n
			}
		}
		if v := get(rec, "size_sqm"); v != "" {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f <= 0 {
				errs = append(errs, RowError{Line: line, Field: "size_sqm", Message: "must be a number above zero"})
			} else {
				row.SizeSqm = &f
			}
		}
		rows = append(rows, row)
	}
	return rows, errs, nil
}

// validate checks each row against the rules and the property's existing records, and returns the
// plan for the rows that will be committed.
func (s *Service) validate(ctx context.Context, propertyID uuid.UUID, rows []Row, parseErrs []RowError) ([]Plan, []RowError) {
	errs := append([]RowError{}, parseErrs...)
	bad := map[int]bool{}
	for _, e := range parseErrs {
		bad[e.Line] = true
	}
	seen := map[string]int{}
	var plans []Plan
	for _, r := range rows {
		fail := func(field, msg string) {
			errs = append(errs, RowError{Line: r.Line, Field: field, Message: msg})
			bad[r.Line] = true
		}
		if r.UnitCode == "" {
			fail("unit_code", "unit_code is required")
		} else if first, dup := seen[r.UnitCode]; dup {
			fail("unit_code", fmt.Sprintf("unit %s also appears on line %d", r.UnitCode, first))
		} else {
			seen[r.UnitCode] = r.Line
		}
		if r.SaleStatus != "" && !saleStatuses[r.SaleStatus] {
			fail("sale_status", "unknown sale status "+r.SaleStatus)
		}
		if r.Occupancy != "" && !occupancyStatuses[r.Occupancy] {
			fail("occupancy_status", "unknown occupancy status "+r.Occupancy)
		}
		if r.OwnerPhone != "" && secure.NormalizePhone(r.OwnerPhone) == "" {
			fail("owner_phone", "phone number is not valid")
		}
		if r.OwnerName != "" && r.OwnerPhone == "" {
			fail("owner_phone", "an owner needs a phone number (it is how they sign in)")
		}
		if r.OwnerSince != "" {
			if _, err := time.Parse("2006-01-02", r.OwnerSince); err != nil {
				fail("owner_since", "use the date format YYYY-MM-DD")
			}
		}
		if bad[r.Line] {
			continue
		}
		p := Plan{Line: r.Line, Unit: "create", Block: "none", Owner: "none", Link: "none", Detail: r.UnitCode}
		if r.Block != "" {
			p.Block = "create"
			if b, err := s.register.BlockByCode(ctx, propertyID, r.Block); err == nil && b != nil {
				p.Block = "existing"
			}
		}
		u, err := s.register.UnitByCode(ctx, propertyID, r.UnitCode)
		if err != nil {
			fail("unit_code", "could not check this unit")
			continue
		}
		if u != nil {
			p.Unit = "update"
		}
		if r.OwnerPhone != "" {
			p.Owner, p.Link = "create", "create"
			if who, err := s.register.PartyByPhone(ctx, r.OwnerPhone); err == nil && who != nil {
				p.Owner = "existing"
				if u != nil {
					if linked, err := s.register.HasActiveLink(ctx, u.ID, who.ID, "owner"); err == nil && linked {
						p.Link = "existing"
					}
				}
			}
			p.Detail = r.UnitCode + ", " + r.OwnerName
		}
		plans = append(plans, p)
	}
	return plans, errs
}

// Summary counts a plan for the report.
func summary(plans []Plan) map[string]int {
	out := map[string]int{}
	for _, p := range plans {
		out["units_"+p.Unit]++
		if p.Block == "create" {
			out["blocks_create"]++
		}
		if p.Owner != "none" {
			out["owners_"+p.Owner]++
		}
		if p.Link == "create" {
			out["links_create"]++
		}
	}
	return out
}

// DryRun parses and validates a file and records the job; nothing else is written.
func (s *Service) DryRun(ctx context.Context, actor, propertyID uuid.UUID, fileName string, r io.Reader) (*ent.ImportJob, error) {
	if _, err := s.client.Property.Get(ctx, propertyID); err != nil {
		return nil, httpx.Invalid("property not found")
	}
	rows, parseErrs, err := Parse(r)
	if err != nil {
		return nil, err
	}
	plans, errs := s.validate(ctx, propertyID, rows, parseErrs)
	status := importjob.StatusValidated
	sum := map[string]any{"counts": summary(plans), "plans": plans}
	if len(plans) == 0 {
		status = importjob.StatusFailed
	} else {
		// The parsed rows (owner phones and emails included) are kept only until the job is committed.
		sum["rows"] = rows
	}
	job, err := s.client.ImportJob.Create().SetKind(importjob.KindOwnerships).SetPropertyID(propertyID).SetDryRun(true).
		SetStatus(status).SetFileName(fileName).SetRowsTotal(len(rows)).SetRowsValid(len(plans)).
		SetRowsFailed(len(rows) - len(plans)).SetErrors(toMaps(errs)).SetSummary(sum).
		SetCreatedBy(actor).Save(ctx)
	if err != nil {
		return nil, err
	}
	return redact(job), nil
}

// redact drops the raw rows from a job before it leaves the service.
func redact(job *ent.ImportJob) *ent.ImportJob {
	if job == nil || job.Summary == nil {
		return job
	}
	out := make(map[string]any, len(job.Summary))
	for k, v := range job.Summary {
		if k != "rows" {
			out[k] = v
		}
	}
	job.Summary = out
	return job
}

// Commit applies a validated job in the background and returns at once; the job row shows progress.
func (s *Service) Commit(ctx context.Context, actor, jobID uuid.UUID) (*ent.ImportJob, error) {
	job, err := s.client.ImportJob.Get(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != importjob.StatusValidated || job.PropertyID == nil {
		return nil, httpx.Conflict("this import is not ready to commit (status " + string(job.Status) + ")")
	}
	rows, err := rowsOf(job.Summary)
	if err != nil {
		return nil, err
	}
	// Claim the job so a double click cannot commit it twice.
	n, err := s.client.ImportJob.Update().Where(importjob.ID(job.ID), importjob.StatusEQ(importjob.StatusValidated)).
		SetStatus(importjob.StatusCommitting).SetDryRun(false).Save(ctx)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, httpx.Conflict("this import is already being committed")
	}
	tenantID, _ := tenantguard.TenantID(ctx)
	propertyID := *job.PropertyID
	go s.apply(tenantID, actor, job.ID, propertyID, rows, job.Summary)
	return s.Get(ctx, job.ID)
}

// staleAfter is how long a validated import keeps its raw rows (owner phones and emails) waiting
// for a commit; commitStuckAfter is how long a commit may go without progress before another pod
// takes it over.
const (
	staleAfter       = 7 * 24 * time.Hour
	commitStuckAfter = 15 * time.Minute
)

// Housekeep expires validated imports never committed (their raw rows are dropped) and resumes
// commits a stopped pod left half done (system job, all tenants). Applying a row is an upsert on
// natural keys, so a resumed commit does not duplicate what the first run wrote.
func (s *Service) Housekeep(ctx context.Context) (expired, resumed int, err error) {
	stale, err := s.client.ImportJob.Query().Where(importjob.StatusEQ(importjob.StatusValidated),
		importjob.CreatedAtLT(time.Now().Add(-staleAfter))).Limit(200).All(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, j := range stale {
		if err := s.client.ImportJob.UpdateOneID(j.ID).SetStatus(importjob.StatusFailed).SetSummary(redact(j).Summary).
			SetErrors([]map[string]any{{"line": 0, "message": "this check expired after 7 days without a commit; upload the file again"}}).
			Exec(tenantguard.With(ctx, j.TenantID)); err == nil {
			expired++
		}
	}
	cutoff := time.Now().Add(-commitStuckAfter)
	stuck, err := s.client.ImportJob.Query().Where(importjob.StatusEQ(importjob.StatusCommitting),
		importjob.UpdatedAtLT(cutoff)).Limit(20).All(ctx)
	if err != nil {
		return expired, 0, err
	}
	for _, j := range stuck {
		// Claim by touching the row; a second pod sees the fresh time and leaves it.
		n, err := s.client.ImportJob.Update().Where(importjob.ID(j.ID), importjob.UpdatedAtLT(cutoff)).
			SetUpdatedAt(time.Now()).Save(tenantguard.With(ctx, j.TenantID))
		if err != nil || n == 0 || j.PropertyID == nil {
			continue
		}
		rows, err := rowsOf(j.Summary)
		if err != nil {
			continue
		}
		actor := uuid.Nil
		if j.CreatedBy != nil {
			actor = *j.CreatedBy
		}
		go s.apply(j.TenantID, actor, j.ID, *j.PropertyID, rows, j.Summary)
		resumed++
	}
	return expired, resumed, nil
}

func (s *Service) apply(tenantID, actor, jobID, propertyID uuid.UUID, rows []Row, sum map[string]any) {
	ctx, cancel := context.WithTimeout(tenantguard.With(context.Background(), tenantID), 30*time.Minute)
	defer cancel()
	plans, _ := s.validate(ctx, propertyID, rows, nil)
	valid := map[int]bool{}
	for _, p := range plans {
		valid[p.Line] = true
	}
	var errs []RowError
	done := 0
	for i, r := range rows {
		if !valid[r.Line] {
			continue
		}
		if err := s.applyRow(ctx, actor, propertyID, r); err != nil {
			errs = append(errs, RowError{Line: r.Line, Message: err.Error()})
		} else {
			done++
		}
		if (i+1)%batchSize == 0 {
			_ = s.client.ImportJob.UpdateOneID(jobID).SetRowsCommitted(done).Exec(ctx)
		}
	}
	status := importjob.StatusCommitted
	if done == 0 && len(errs) > 0 {
		status = importjob.StatusFailed
	}
	finished := redact(&ent.ImportJob{Summary: sum}).Summary
	u := s.client.ImportJob.UpdateOneID(jobID).SetStatus(status).SetRowsCommitted(done).SetSummary(finished)
	if len(errs) > 0 {
		u.SetErrors(toMaps(errs))
	}
	if err := u.Exec(ctx); err != nil {
		s.log.Error("import: finish job", zap.Error(err), zap.String("job", jobID.String()))
	}
}

// applyRow upserts the block and unit, then the owner and the owner link, all by natural key.
func (s *Service) applyRow(ctx context.Context, actor, propertyID uuid.UUID, r Row) error {
	in := register.UnitInput{PropertyID: &propertyID, Code: &r.UnitCode}
	if r.Block != "" {
		b, _, err := s.register.EnsureBlock(ctx, propertyID, r.Block)
		if err != nil {
			return fmt.Errorf("block %s: %w", r.Block, err)
		}
		in.BlockID = &b.ID
	}
	if r.UnitType != "" {
		in.UnitType = &r.UnitType
	}
	in.Bedrooms, in.Bathrooms, in.SizeSqm = r.Bedrooms, r.Bathrooms, r.SizeSqm
	if r.Floor != "" {
		in.Floor = &r.Floor
	}
	if r.SaleStatus != "" {
		in.SaleStatus = &r.SaleStatus
	}
	u, err := s.register.UnitByCode(ctx, propertyID, r.UnitCode)
	if err != nil {
		return err
	}
	if u == nil {
		if u, err = s.register.CreateUnit(ctx, actor, in); err != nil {
			return fmt.Errorf("unit %s: %w", r.UnitCode, err)
		}
	} else {
		in.PropertyID, in.Code = nil, nil
		if u, err = s.register.UpdateUnit(ctx, u.ID, in); err != nil {
			return fmt.Errorf("unit %s: %w", r.UnitCode, err)
		}
	}
	if r.OwnerPhone != "" {
		who, err := s.register.PartyByPhone(ctx, r.OwnerPhone)
		if err != nil {
			return err
		}
		if who == nil {
			pin := register.PartyInput{Kind: strPtr("person"), Phone: &r.OwnerPhone, PreferredChannel: strPtr("whatsapp")}
			if name := strings.TrimSpace(r.OwnerName); name != "" {
				parts := strings.Fields(name)
				pin.DisplayName, pin.FirstName = &name, &parts[0]
				if len(parts) > 1 {
					last := strings.Join(parts[1:], " ")
					pin.LastName = &last
				}
			}
			if r.OwnerEmail != "" {
				pin.Email = &r.OwnerEmail
			}
			if who, err = s.register.CreateParty(ctx, actor, pin); err != nil {
				return fmt.Errorf("owner %s: %w", r.OwnerName, err)
			}
		}
		linked, err := s.register.HasActiveLink(ctx, u.ID, who.ID, "owner")
		if err != nil {
			return err
		}
		if !linked {
			link := register.LinkInput{PartyID: who.ID, Role: "owner", IsPrimary: true, Source: "import"}
			if t, err := time.Parse("2006-01-02", r.OwnerSince); err == nil {
				link.StartDate = &t
			}
			if _, err := s.register.LinkParty(ctx, u.ID, actor, link); err != nil {
				return fmt.Errorf("owner link %s: %w", r.UnitCode, err)
			}
		}
	}
	// Occupancy last: linking an owner marks a vacant unit owner occupied, the file has the final word.
	if r.Occupancy != "" {
		if _, err := s.register.UpdateUnit(ctx, u.ID, register.UnitInput{OccupancyStatus: &r.Occupancy}); err != nil {
			return fmt.Errorf("unit %s occupancy: %w", r.UnitCode, err)
		}
	}
	return nil
}

// Get returns one job without its raw rows.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*ent.ImportJob, error) {
	job, err := s.client.ImportJob.Get(ctx, id)
	return redact(job), err
}

// Recent returns the latest jobs, newest first (bounded).
func (s *Service) Recent(ctx context.Context, limit int) ([]*ent.ImportJob, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	return s.client.ImportJob.Query().Order(ent.Desc(importjob.FieldCreatedAt)).Limit(limit).
		Select(importjob.FieldID, importjob.FieldKind, importjob.FieldPropertyID, importjob.FieldStatus, importjob.FieldFileName,
			importjob.FieldRowsTotal, importjob.FieldRowsValid, importjob.FieldRowsFailed, importjob.FieldRowsCommitted,
			importjob.FieldCreatedAt, importjob.FieldDryRun).All(ctx)
}

// Template is the header line (plus one example) for the download.
func Template() string {
	return strings.Join(Columns, ",") + "\nB07,B,3br_apartment,3,2,110,2,handed_over,owner_occupied,Jane Wanjiru,0712345678,jane@example.com,2025-06-01\n"
}

func rowsOf(summary map[string]any) ([]Row, error) {
	raw, ok := summary["rows"]
	if !ok {
		return nil, httpx.Invalid("this import has no rows")
	}
	return decodeRows(raw)
}

func strPtr(v string) *string { return &v }
