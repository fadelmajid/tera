package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/domain/omzet"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
	"github.com/fadelmajid/tera/internal/store/seed"
)

var (
	// ErrOmzetConfig is returned when the threshold configuration cannot be
	// used. Deliberately not ErrValidation: the request was fine, the config is
	// not.
	ErrOmzetConfig = errors.New("service: konfigurasi batas omzet bermasalah")

	// ErrThresholdClosed is returned when an already-closed threshold is closed
	// again.
	ErrThresholdClosed = errors.New("service: batas omzet sudah ditutup")
)

// Omzet is the turnover clock against the Rp 4,8 miliar PKP threshold.
// TASKS 7.1–7.11, SPEC §5.
//
// The arithmetic is not here. This layer appends ledger rows inside the
// transaction that produced them, reads them back with the entity's calendar,
// and hands both to internal/domain/omzet — where the book-year window, the
// stickiness of a crossing and the two dates it emits are rules with tests
// (ARCHITECTURE §2).
type Omzet struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewOmzet builds the service.
func NewOmzet(db *store.DB, aud *Auditor, now func() time.Time) *Omzet {
	if now == nil {
		now = time.Now
	}
	return &Omzet{db: db, q: gen.New(db), aud: aud, now: now}
}

// --- writing the ledger (TASKS 7.4, 7.5) ------------------------------------

// appendOmzet writes one ledger row inside an open transaction.
//
// A package function rather than a method because the callers are Sales and
// Purchasing, and a sale should not have to be handed an omzet service to
// record the turnover it just created. The row goes in the sale's own
// transaction: turnover that reached the books without reaching the clock would
// leave a business measuring itself against a threshold on incomplete figures,
// and the error only shows up a year later.
func appendOmzet(
	ctx context.Context,
	tx *sql.Tx,
	clock entityClock,
	entityID string,
	kind omzet.EventType,
	effectiveDate string,
	amount money.IDR,
	sourceTxnID string,
	actor Actor,
	now int64,
) error {
	if amount.IsZero() {
		// A row that moves nothing means nothing. An untaxed, fully discounted
		// or zero-value sale is not turnover, and the table refuses it anyway.
		return nil
	}

	// The book year is denormalised at write time, in the entity's zone
	// (D-005, INV-5), and derived from the effective date rather than from
	// today: a void in January of a December sale belongs to December's year.
	year, err := clock.bookYear(effectiveDate)
	if err != nil {
		return err
	}

	if _, err := gen.New(tx).AppendOmzet(ctx, gen.AppendOmzetParams{
		ID: store.NewID(), EntityID: entityID, BookYear: int64(year),
		EffectiveDate: effectiveDate, EventType: string(kind),
		SignedAmountIdr: int64(amount), SourceTxnID: nilIfEmpty(sourceTxnID),
		CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
	}); err != nil {
		return wrapWrite(err)
	}
	return nil
}

// omzetBase reads whether this company counts turnover before or after PPN
// (SPEC §5.4). No row means the default, which is net.
func omzetBase(ctx context.Context, tx *sql.Tx, entityID string) (omzet.Base, error) {
	row, err := gen.New(tx).GetOmzetSetting(ctx, entityID)
	if errors.Is(err, sql.ErrNoRows) {
		return omzet.NetOfVAT, nil
	}
	if err != nil {
		return "", fmt.Errorf("service: omzet setting: %w", err)
	}
	switch omzet.Base(row.Base) {
	case omzet.NetOfVAT, omzet.Gross:
		return omzet.Base(row.Base), nil
	default:
		return "", fmt.Errorf("%w: dasar omzet tersimpan tidak dikenal: %q", ErrOmzetConfig, row.Base)
	}
}

// omzetAmountForSale is what a sale contributes to the clock.
//
// Net of PPN by default: the tax was collected for the state and is not this
// business's turnover. Configurable because SPEC §5.4 leaves it open and
// "bruto" arguably reads the other way — it is the second of the four questions
// in testdata/worked_examples/omzet_unverified.json.
//
// It makes no difference at the non-PKP company, which charges no PPN and is
// the only one that can still cross.
func omzetAmountForSale(base omzet.Base, dpp, total money.IDR) money.IDR {
	if base == omzet.Gross {
		return total
	}
	return dpp
}

// reverseOmzetForSale appends a negative row undoing what a sale contributed.
//
// The reversal is computed from what was actually recorded, prorated by the
// share of the sale being undone, rather than recomputed from today's config.
// A base setting changed between the sale and the return would otherwise
// reverse a different figure from the one that was counted, and the difference
// would sit in the clock forever with nothing to explain it.
//
// share is refund over the sale total: what the customer got back over what
// they paid, both PPN-inclusive, which is exactly the fraction of the sale
// being undone.
func reverseOmzetForSale(
	ctx context.Context,
	tx *sql.Tx,
	clock entityClock,
	sale gen.Sale,
	kind omzet.EventType,
	effectiveDate string,
	refund money.IDR,
	actor Actor,
	now int64,
) error {
	recorded, err := recordedOmzetForSale(ctx, tx, sale.ID)
	if err != nil {
		return err
	}
	if recorded.IsZero() {
		return nil
	}

	amount := recorded
	if kind != omzet.Void {
		if sale.TotalIdr <= 0 {
			return nil
		}
		amount = recorded.MulRatio(int64(refund), sale.TotalIdr)
	}
	if amount.IsZero() {
		return nil
	}

	return appendOmzet(ctx, tx, clock, sale.EntityID, kind, effectiveDate,
		amount.Neg(), sale.ID, actor, now)
}

// recordedOmzetForSale is the net turnover this sale has contributed so far:
// its SALE rows less anything already reversed against it.
func recordedOmzetForSale(ctx context.Context, tx *sql.Tx, saleID string) (money.IDR, error) {
	rows, err := gen.New(tx).ListOmzetForSource(ctx, &saleID)
	if err != nil {
		return 0, fmt.Errorf("service: omzet rows for sale: %w", err)
	}
	var total money.IDR
	for _, r := range rows {
		total = total.Add(money.IDR(r.SignedAmountIdr))
	}
	return total, nil
}

// --- reading the clock (TASKS 7.7–7.9) --------------------------------------

// Clock is the omzet position plus what the screen needs around it.
type Clock struct {
	Position omzet.Position
	// BookYears are the years with any turnover, newest first, so the screen
	// offers a picker built from the data rather than from a guess about when
	// trading began.
	BookYears []int
	// IsPKP says whether this company is already registered. Both are tracked,
	// but only a non-PKP one can still cross (SPEC §5.3), and a CROSSED banner
	// on a company that registered years ago is noise.
	IsPKP bool
	Base  omzet.Base
}

// Position measures one book year. An empty bookYear means the current one and
// an empty asOf means today, both in the company's own timezone (INV-5).
func (o *Omzet) Position(ctx context.Context, entityID, bookYear, asOf string) (Clock, error) {
	var out Clock

	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		clock, err := loadEntityClock(ctx, tx, entityID)
		if err != nil {
			return err
		}
		cal := clock.calendar()

		day := strings.TrimSpace(asOf)
		if day == "" {
			day = cal.Today(o.now())
		}
		if _, err := time.Parse(omzet.DateFormat, day); err != nil {
			return fmt.Errorf("%w: tanggal harus YYYY-MM-DD", ErrValidation)
		}

		year, err := resolveBookYear(cal, bookYear, day)
		if err != nil {
			return err
		}

		thresholds, err := thresholdsFor(ctx, tx, entityID)
		if err != nil {
			return err
		}

		base, err := omzetBase(ctx, tx, entityID)
		if err != nil {
			return err
		}

		// One read wide enough for both windows, and the range is declared to
		// the domain so a query that could not cover them is refused rather
		// than producing a trailing figure that is quietly short.
		window := cal.BookYearWindow(year)
		supplied := omzet.Window{
			From: earliest(window.From, minusYear(day)),
			To:   latest(window.To, day),
		}

		q := gen.New(tx)
		rows, err := q.ListOmzetEntries(ctx, gen.ListOmzetEntriesParams{
			EntityID: entityID, FromDate: supplied.From, ToDate: supplied.To,
		})
		if err != nil {
			return fmt.Errorf("service: omzet entries: %w", err)
		}

		entries := make([]omzet.Entry, 0, len(rows))
		for _, r := range rows {
			entries = append(entries, omzet.Entry{
				ID: r.ID, BookYear: int(r.BookYear), EffectiveDate: r.EffectiveDate,
				Type: omzet.EventType(r.EventType), Amount: money.IDR(r.SignedAmountIdr),
				SourceTxnID: derefString(r.SourceTxnID),
			})
		}

		position, err := omzet.Compute(omzet.Input{
			EntityID: entityID, Calendar: cal, Thresholds: thresholds,
			BookYear: year, AsOf: day, Supplied: supplied, Entries: entries,
		})
		if err != nil {
			return fmt.Errorf("%w: %w", ErrOmzetConfig, err)
		}

		years, err := q.ListOmzetBookYears(ctx, entityID)
		if err != nil {
			return fmt.Errorf("service: omzet book years: %w", err)
		}
		out = Clock{Position: position, IsPKP: clock.isPKP, Base: base}
		for _, y := range years {
			out.BookYears = append(out.BookYears, int(y))
		}
		return nil
	})
	return out, err
}

// Ledger is the rows behind one book year, newest first (SPEC §5.1's
// drill-down: every figure decomposes into the documents that produced it).
func (o *Omzet) Ledger(ctx context.Context, entityID string, bookYear int) ([]gen.ListOmzetForBookYearRow, error) {
	rows, err := o.q.ListOmzetForBookYear(ctx, gen.ListOmzetForBookYearParams{
		EntityID: entityID, BookYear: int64(bookYear),
	})
	if err != nil {
		return nil, fmt.Errorf("service: omzet ledger: %w", err)
	}
	return rows, nil
}

// --- the threshold, effective-dated (INV-4) ---------------------------------

// ListThresholds returns every threshold this company has had.
func (o *Omzet) ListThresholds(ctx context.Context, entityID string) ([]gen.OmzetThreshold, error) {
	rows, err := o.q.ListOmzetThresholds(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: omzet thresholds: %w", err)
	}
	return rows, nil
}

func thresholdsFor(ctx context.Context, tx *sql.Tx, entityID string) (omzet.ThresholdSet, error) {
	rows, err := gen.New(tx).ListOmzetThresholds(ctx, entityID)
	if err != nil {
		return omzet.ThresholdSet{}, fmt.Errorf("service: omzet thresholds: %w", err)
	}

	list := make([]omzet.Threshold, 0, len(rows))
	for _, r := range rows {
		t := omzet.Threshold{
			ID: r.ID, EntityID: r.EntityID, AmountIDR: money.IDR(r.AmountIdr),
			WatchBP: r.WatchBp, WarnBP: r.WarnBp,
			RegisterBy: omzet.RegisterByPolicy(r.RegisterByPolicy),
			VATStarts:  omzet.VATStartsPolicy(r.VatStartsPolicy),
			ValidFrom:  r.ValidFrom, LegalRef: r.LegalRef,
		}
		if r.ValidTo != nil {
			t.ValidTo = *r.ValidTo
		}
		list = append(list, t)
	}

	set, err := omzet.NewThresholdSet(list...)
	if err != nil {
		return omzet.ThresholdSet{}, fmt.Errorf("%w: %w", ErrOmzetConfig, err)
	}
	return set, nil
}

// ThresholdInput is a threshold as submitted from the admin screen.
type ThresholdInput struct {
	AmountIDR        money.IDR
	WatchBP          int64
	WarnBP           int64
	RegisterByPolicy string
	VATStartsPolicy  string
	ValidFrom        string
	ValidTo          string
	LegalRef         string
	Note             string
}

// CreateThreshold adds a threshold. Never an edit of an existing one (INV-4).
func (o *Omzet) CreateThreshold(ctx context.Context, actor Actor, in ThresholdInput) (gen.OmzetThreshold, error) {
	if actor.LegalEntityID == "" {
		return gen.OmzetThreshold{}, fmt.Errorf("%w: perusahaan belum dipilih", ErrValidation)
	}

	candidate := omzet.Threshold{
		ID: "(baru)", EntityID: actor.LegalEntityID, AmountIDR: in.AmountIDR,
		WatchBP: in.WatchBP, WarnBP: in.WarnBP,
		RegisterBy: omzet.RegisterByPolicy(strings.ToUpper(strings.TrimSpace(in.RegisterByPolicy))),
		VATStarts:  omzet.VATStartsPolicy(strings.ToUpper(strings.TrimSpace(in.VATStartsPolicy))),
		ValidFrom:  strings.TrimSpace(in.ValidFrom), ValidTo: strings.TrimSpace(in.ValidTo),
		LegalRef: strings.TrimSpace(in.LegalRef),
	}
	if err := candidate.Validate(); err != nil {
		return gen.OmzetThreshold{}, fmt.Errorf("%w: %w", ErrValidation, err)
	}

	var created gen.OmzetThreshold
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		existing, err := thresholdsFor(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		if _, err := omzet.NewThresholdSet(append(existing.Thresholds(), candidate)...); err != nil {
			return fmt.Errorf("%w: %w", ErrValidation, err)
		}

		now := o.now().Unix()
		row, err := q.CreateOmzetThreshold(ctx, gen.CreateOmzetThresholdParams{
			ID: store.NewID(), EntityID: actor.LegalEntityID,
			AmountIdr: int64(in.AmountIDR), WatchBp: in.WatchBP, WarnBp: in.WarnBP,
			RegisterByPolicy: string(candidate.RegisterBy),
			VatStartsPolicy:  string(candidate.VATStarts),
			ValidFrom:        candidate.ValidFrom, ValidTo: nilIfEmpty(candidate.ValidTo),
			LegalRef: candidate.LegalRef, Note: nilIfEmpty(strings.TrimSpace(in.Note)),
			CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "omzet_threshold", RecordID: row.ID,
			Action: ActionCreate, After: row, Reason: candidate.LegalRef,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// CloseThreshold sets a threshold's valid_to. The only edit one ever gets.
func (o *Omzet) CloseThreshold(ctx context.Context, actor Actor, id, validTo, reason string) (gen.OmzetThreshold, error) {
	validTo = strings.TrimSpace(validTo)
	if _, err := time.Parse(omzet.DateFormat, validTo); err != nil {
		return gen.OmzetThreshold{}, fmt.Errorf("%w: tanggal akhir berlaku harus YYYY-MM-DD", ErrValidation)
	}

	var closed gen.OmzetThreshold
	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		before, err := q.GetOmzetThreshold(ctx, id)
		if err != nil || before.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: batas omzet tidak ditemukan", ErrNotFound)
		}
		if before.ValidTo != nil {
			return ErrThresholdClosed
		}
		if validTo < before.ValidFrom {
			return fmt.Errorf("%w: tanggal akhir %s mendahului tanggal mulai %s",
				ErrValidation, validTo, before.ValidFrom)
		}

		now := o.now().Unix()
		row, err := q.CloseOmzetThreshold(ctx, gen.CloseOmzetThresholdParams{
			ID: id, ValidTo: &validTo, ClosedBy: nilIfEmpty(actor.UserID), ClosedAt: &now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		closed = row

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "omzet_threshold", RecordID: id,
			Action: ActionUpdate, Before: before, After: row,
			Reason: strings.TrimSpace(reason), ClientRequestID: actor.ClientRequestID,
		})
	})
	return closed, err
}

// SetBase chooses whether turnover is counted before or after PPN (SPEC §5.4).
//
// Audited, because it changes what every past figure on the clock says — and
// the user declined period locking (R7.3), so the log is the only trace that a
// year read one way in March and another in April.
func (o *Omzet) SetBase(ctx context.Context, actor Actor, base, note string) (omzet.Base, error) {
	chosen := omzet.Base(strings.ToUpper(strings.TrimSpace(base)))
	if chosen != omzet.NetOfVAT && chosen != omzet.Gross {
		return "", fmt.Errorf("%w: dasar omzet harus NET_OF_VAT atau GROSS", ErrValidation)
	}
	if actor.LegalEntityID == "" {
		return "", fmt.Errorf("%w: perusahaan belum dipilih", ErrValidation)
	}

	err := o.db.InTx(ctx, func(tx *sql.Tx) error {
		before, err := omzetBase(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}

		now := o.now().Unix()
		row, err := gen.New(tx).UpsertOmzetSetting(ctx, gen.UpsertOmzetSettingParams{
			EntityID: actor.LegalEntityID, Base: string(chosen),
			Note: nilIfEmpty(strings.TrimSpace(note)), UpdatedBy: nilIfEmpty(actor.UserID),
			UpdatedAt: now, CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}

		return o.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "omzet_setting", RecordID: actor.LegalEntityID,
			Action: ActionUpdate, Before: before, After: row,
			Reason: strings.TrimSpace(note), ClientRequestID: actor.ClientRequestID,
		})
	})
	return chosen, err
}

// seedOmzetThreshold writes the default threshold for a newly created company.
//
// Inside the transaction that creates the entity, like the tax rules: a company
// either exists with the configuration it needs or does not exist. Both
// companies get one — SPEC §5.3 tracks both, and only the non-PKP one can still
// cross.
func seedOmzetThreshold(
	ctx context.Context,
	tx *sql.Tx,
	aud *Auditor,
	actor Actor,
	entity gen.LegalEntity,
	now int64,
) error {
	t := seed.OmzetThresholdFor()

	row, err := gen.New(tx).CreateOmzetThreshold(ctx, gen.CreateOmzetThresholdParams{
		ID: store.NewID(), EntityID: entity.ID, AmountIdr: t.AmountIDR,
		WatchBp: t.WatchBP, WarnBp: t.WarnBP,
		RegisterByPolicy: t.RegisterByPolicy, VatStartsPolicy: t.VATStartsPolicy,
		ValidFrom: t.ValidFrom, LegalRef: t.LegalRef, Note: nilIfEmpty(t.Note),
		CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
	})
	if err != nil {
		return wrapWrite(err)
	}

	return aud.Record(ctx, tx, &Entry{
		ActorUserID: actor.UserID, LegalEntityID: entity.ID,
		RecordType: "omzet_threshold", RecordID: row.ID,
		Action: ActionCreate, After: row, Reason: "seed " + t.LegalRef,
		ClientRequestID: actor.ClientRequestID,
	})
}

// --- small helpers ----------------------------------------------------------

// resolveBookYear defaults an unset year to the one containing asOf.
func resolveBookYear(cal omzet.Calendar, bookYear, asOf string) (int, error) {
	bookYear = strings.TrimSpace(bookYear)
	if bookYear == "" {
		return cal.BookYear(asOf)
	}
	year, err := strconv.Atoi(bookYear)
	if err != nil {
		return 0, fmt.Errorf("%w: tahun buku harus angka", ErrValidation)
	}
	return year, nil
}

func minusYear(day string) string {
	parsed, err := time.Parse(omzet.DateFormat, day)
	if err != nil {
		return day
	}
	return parsed.AddDate(-1, 0, 0).Format(omzet.DateFormat)
}

func earliest(a, b string) string {
	if a < b {
		return a
	}
	return b
}

func latest(a, b string) string {
	if a > b {
		return a
	}
	return b
}
