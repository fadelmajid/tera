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
	"github.com/fadelmajid/tera/internal/domain/tax"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
	"github.com/fadelmajid/tera/internal/store/seed"
)

var (
	// ErrTaxConfig is returned when the stored tax rules cannot be used.
	//
	// Deliberately not ErrValidation: the request was fine, the configuration is
	// not. It carries the domain's own wording, which names the rule and the
	// date to go and look at.
	ErrTaxConfig = errors.New("service: konfigurasi pajak bermasalah")

	// ErrRuleInForce is returned when a rule that has already started is deleted.
	ErrRuleInForce = errors.New("service: aturan pajak sudah berlaku dan tidak bisa dihapus")

	// ErrRuleClosed is returned when an already-closed rule is closed again.
	ErrRuleClosed = errors.New("service: aturan pajak sudah ditutup")
)

// Tax owns the effective-dated tax configuration and the PPN position.
// TASKS 5.1, 5.2, 5.9, 5.10, 5.11.
//
// The arithmetic is not here. This layer reads config rows, hands them to
// internal/domain/tax as values, and persists what comes back (ARCHITECTURE §2).
// Nothing in this file knows what a rate is.
type Tax struct {
	db  *store.DB
	q   *gen.Queries
	aud *Auditor
	now func() time.Time
}

// NewTax builds the service.
func NewTax(db *store.DB, aud *Auditor, now func() time.Time) *Tax {
	if now == nil {
		now = time.Now
	}
	return &Tax{db: db, q: gen.New(db), aud: aud, now: now}
}

// --- reading the rules ------------------------------------------------------

// RulesFor loads one company's PPN rules and builds the domain rule set.
//
// Construction validates and rejects overlaps, so a rate that cannot be true is
// caught here rather than at the till. An empty set is not an error: it is what
// a non-PKP entity has, and refusing it is domain/tax's job on a PKP sale.
func (t *Tax) RulesFor(ctx context.Context, entityID string) (tax.RuleSet, error) {
	return rulesFor(ctx, t.q, entityID)
}

// rulesFor is the same read against either the pool or an open transaction.
func rulesFor(ctx context.Context, q *gen.Queries, entityID string) (tax.RuleSet, error) {
	rows, err := q.ListTaxRulesForEntity(ctx, gen.ListTaxRulesForEntityParams{
		EntityID: entityID, TaxType: string(tax.PPN),
	})
	if err != nil {
		return tax.RuleSet{}, fmt.Errorf("service: tax rules: %w", err)
	}

	rules := make([]tax.Rule, 0, len(rows))
	for _, r := range rows {
		rules = append(rules, toDomainRule(r))
	}

	set, err := tax.NewRuleSet(rules...)
	if err != nil {
		return tax.RuleSet{}, fmt.Errorf("%w: %w", ErrTaxConfig, err)
	}
	return set, nil
}

func toDomainRule(r gen.TaxRule) tax.Rule {
	out := tax.Rule{
		ID: r.ID, EntityID: r.EntityID, Type: tax.Type(r.TaxType),
		RateBP: r.RateBp, DPPNum: r.DppFactorNum, DPPDen: r.DppFactorDen,
		Inclusive: r.IsInclusive == 1, Level: tax.Level(r.CalculationLevel),
		Rounding: tax.RoundingMode(r.RoundingMode), RoundingUnit: r.RoundingUnit,
		ValidFrom: r.ValidFrom, LegalRef: r.LegalRef,
	}
	if r.ValidTo != nil {
		out.ValidTo = *r.ValidTo
	}
	return out
}

// ListRules returns every rule a company has ever had, newest first. The admin
// screen shows closed rules alongside live ones: a rate that has been superseded
// is still the rate a past sale was priced under (TASKS 5.10).
func (t *Tax) ListRules(ctx context.Context, entityID string) ([]gen.TaxRule, error) {
	rows, err := t.q.ListTaxRules(ctx, entityID)
	if err != nil {
		return nil, fmt.Errorf("service: list tax rules: %w", err)
	}
	return rows, nil
}

// --- changing the rules -----------------------------------------------------

// TaxRuleInput is a rule as submitted from the admin screen.
type TaxRuleInput struct {
	Type             string
	RateBP           int64
	DPPNum           int64
	DPPDen           int64
	Inclusive        bool
	CalculationLevel string
	RoundingMode     string
	RoundingUnit     int64
	ValidFrom        string
	ValidTo          string
	LegalRef         string
	Note             string
}

func (in *TaxRuleInput) normalise() {
	in.Type = strings.ToUpper(strings.TrimSpace(in.Type))
	in.CalculationLevel = strings.ToUpper(strings.TrimSpace(in.CalculationLevel))
	in.RoundingMode = strings.ToUpper(strings.TrimSpace(in.RoundingMode))
	in.ValidFrom = strings.TrimSpace(in.ValidFrom)
	in.ValidTo = strings.TrimSpace(in.ValidTo)
	in.LegalRef = strings.TrimSpace(in.LegalRef)
	in.Note = strings.TrimSpace(in.Note)

	if in.Type == "" {
		in.Type = string(tax.PPN)
	}
	if in.CalculationLevel == "" {
		in.CalculationLevel = string(tax.LevelInvoice)
	}
	if in.RoundingMode == "" {
		in.RoundingMode = string(tax.HalfUp)
	}
	if in.RoundingUnit == 0 {
		in.RoundingUnit = 1
	}
}

// CreateRule adds a rule. Never an edit of an existing one (INV-4).
//
// The domain validates before anything is written, so the error a person sees
// names what is wrong with the rate rather than reporting a constraint. The
// storage-layer triggers in migration 012 are the backstop, not the first line.
//
// A rule may be staged ahead of its start date, and for the non-PKP entity that
// is the correct thing to do: after crossing the threshold, registration is due
// by the end of the book year and the obligation starts in the first tax period
// of the following one (PMK 164/2023 Pasal 17(3), Pasal 18). What is refused is
// a rule that would be in force today at an entity still marked non-PKP —
// domain/tax refuses to price a sale in that state, so creating one would break
// the till at the next sale rather than at the moment somebody could fix it.
func (t *Tax) CreateRule(ctx context.Context, actor Actor, in TaxRuleInput) (gen.TaxRule, error) {
	in.normalise()

	if actor.LegalEntityID == "" {
		return gen.TaxRule{}, fmt.Errorf("%w: perusahaan belum dipilih", ErrValidation)
	}

	candidate := tax.Rule{
		ID: "(baru)", EntityID: actor.LegalEntityID, Type: tax.Type(in.Type),
		RateBP: in.RateBP, DPPNum: in.DPPNum, DPPDen: in.DPPDen,
		Inclusive: in.Inclusive, Level: tax.Level(in.CalculationLevel),
		Rounding: tax.RoundingMode(in.RoundingMode), RoundingUnit: in.RoundingUnit,
		ValidFrom: in.ValidFrom, ValidTo: in.ValidTo, LegalRef: in.LegalRef,
	}
	if err := candidate.Validate(); err != nil {
		return gen.TaxRule{}, fmt.Errorf("%w: %w", ErrValidation, err)
	}

	var created gen.TaxRule
	err := t.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		today, _, err := clock.resolveDate("", t.now())
		if err != nil {
			return err
		}

		if !clock.isPKP && candidate.Type == tax.PPN && candidate.AppliesOn(today) {
			return fmt.Errorf(
				"%w: perusahaan ini belum PKP, jadi aturan PPN tidak boleh berlaku mulai %s (%s sudah lewat). "+
					"Daftarkan perusahaan sebagai PKP dulu, atau isi tanggal mulai setelah hari ini",
				ErrValidation, in.ValidFrom, in.ValidFrom)
		}

		// The existing rules plus this one, validated together. The overlap
		// check lives in the domain and the trigger in migration 012 repeats
		// it; getting the domain's wording out first is what makes the message
		// say which rule to close.
		existing, err := rulesFor(ctx, q, actor.LegalEntityID)
		if err != nil {
			return err
		}
		if _, err := tax.NewRuleSet(append(existing.Rules(), candidate)...); err != nil {
			return fmt.Errorf("%w: %w", ErrValidation, err)
		}

		now := t.now().Unix()
		row, err := q.CreateTaxRule(ctx, gen.CreateTaxRuleParams{
			ID: store.NewID(), EntityID: actor.LegalEntityID, TaxType: in.Type,
			RateBp: in.RateBP, DppFactorNum: in.DPPNum, DppFactorDen: in.DPPDen,
			IsInclusive: boolToInt(in.Inclusive), CalculationLevel: in.CalculationLevel,
			RoundingMode: in.RoundingMode, RoundingUnit: in.RoundingUnit,
			ValidFrom: in.ValidFrom, ValidTo: nilIfEmpty(in.ValidTo),
			LegalRef: in.LegalRef, Note: nilIfEmpty(in.Note),
			CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		created = row

		return t.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "tax_rule", RecordID: row.ID,
			Action: ActionCreate, After: row,
			Reason:          in.LegalRef,
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return created, err
}

// CloseRule sets a rule's valid_to. The only edit a rule ever gets (INV-4).
//
// Sales already priced under it keep their own snapshot and do not move
// (INV-3, TASKS 5.11); closing decides which rule the next sale reads.
func (t *Tax) CloseRule(ctx context.Context, actor Actor, id, validTo, reason string) (gen.TaxRule, error) {
	validTo = strings.TrimSpace(validTo)
	if _, err := time.Parse(tax.DateFormat, validTo); err != nil {
		return gen.TaxRule{}, fmt.Errorf("%w: tanggal akhir berlaku harus YYYY-MM-DD", ErrValidation)
	}

	var closed gen.TaxRule
	err := t.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		before, err := q.GetTaxRule(ctx, id)
		if err != nil || before.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: aturan pajak tidak ditemukan", ErrNotFound)
		}
		if before.ValidTo != nil {
			return ErrRuleClosed
		}
		if validTo < before.ValidFrom {
			return fmt.Errorf("%w: tanggal akhir %s mendahului tanggal mulai %s",
				ErrValidation, validTo, before.ValidFrom)
		}

		now := t.now().Unix()
		row, err := q.CloseTaxRule(ctx, gen.CloseTaxRuleParams{
			ID: id, ValidTo: &validTo, ClosedBy: nilIfEmpty(actor.UserID), ClosedAt: &now,
		})
		if err != nil {
			return wrapWrite(err)
		}
		closed = row

		return t.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "tax_rule", RecordID: id,
			Action: ActionUpdate, Before: before, After: row,
			Reason:          strings.TrimSpace(reason),
			ClientRequestID: actor.ClientRequestID,
		})
	})
	return closed, err
}

// DeleteRule removes a rule that has not started yet.
//
// A rule already in force is closed, never deleted: sales were priced under it,
// and the report explaining them reads its citation. A rule staged for a future
// date has priced nothing and a typo in it is worth removing rather than
// documenting forever.
//
// "Today" is the entity's own date (INV-5), which is why this is decided here
// and not by a trigger.
func (t *Tax) DeleteRule(ctx context.Context, actor Actor, id, reason string) error {
	return t.db.InTx(ctx, func(tx *sql.Tx) error {
		q := gen.New(tx)

		before, err := q.GetTaxRule(ctx, id)
		if err != nil || before.EntityID != actor.LegalEntityID {
			return fmt.Errorf("%w: aturan pajak tidak ditemukan", ErrNotFound)
		}

		clock, err := loadEntityClock(ctx, tx, actor.LegalEntityID)
		if err != nil {
			return err
		}
		today, _, err := clock.resolveDate("", t.now())
		if err != nil {
			return err
		}
		if before.ValidFrom <= today {
			return fmt.Errorf("%w: berlaku sejak %s, tutup saja dengan tanggal akhir",
				ErrRuleInForce, before.ValidFrom)
		}

		if err := q.DeleteTaxRule(ctx, gen.DeleteTaxRuleParams{ID: id, Today: today}); err != nil {
			return wrapWrite(err)
		}

		return t.aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: actor.LegalEntityID,
			RecordType: "tax_rule", RecordID: id,
			Action: ActionDelete, Before: before,
			Reason:          strings.TrimSpace(reason),
			ClientRequestID: actor.ClientRequestID,
		})
	})
}

// seedTaxRules writes the default rules for a newly created company
// (TASKS 5.2).
//
// Called from inside the transaction that creates the entity, so a company
// either exists with its tax configuration or does not exist. A package
// function rather than a method because MasterData owns entity creation and
// should not have to be handed a tax service to make a company.
//
// A non-PKP entity is seeded with nothing; see [seed.TaxRulesFor] for why,
// including why no PPnBM row is written.
func seedTaxRules(
	ctx context.Context,
	tx *sql.Tx,
	aud *Auditor,
	actor Actor,
	entity gen.LegalEntity,
	now int64,
) error {
	q := gen.New(tx)

	for _, r := range seed.TaxRulesFor(entity.IsPkp == 1) {
		row, err := q.CreateTaxRule(ctx, gen.CreateTaxRuleParams{
			ID: store.NewID(), EntityID: entity.ID, TaxType: r.Type,
			RateBp: r.RateBP, DppFactorNum: r.DPPNum, DppFactorDen: r.DPPDen,
			IsInclusive: boolToInt(r.Inclusive), CalculationLevel: r.Level,
			RoundingMode: r.Rounding, RoundingUnit: r.RoundingUnit,
			ValidFrom: r.ValidFrom, LegalRef: r.LegalRef, Note: nilIfEmpty(r.Note),
			CreatedBy: nilIfEmpty(actor.UserID), CreatedAt: now,
		})
		if err != nil {
			return wrapWrite(err)
		}

		// Audited like any other rule (INV-10). A seeded rate is still a rate
		// somebody's sales will be priced under, and "where did this come from"
		// deserves an answer that is not "it was always there".
		if err := aud.Record(ctx, tx, &Entry{
			ActorUserID: actor.UserID, LegalEntityID: entity.ID,
			RecordType: "tax_rule", RecordID: row.ID,
			Action: ActionCreate, After: row,
			Reason:          "seed " + r.LegalRef,
			ClientRequestID: actor.ClientRequestID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// --- the PPN position (TASKS 5.9, SPEC §2.4) --------------------------------

// PPNPosition is one company's PPN position for one masa pajak, with the
// documents behind every figure.
type PPNPosition struct {
	Masa     string
	From     string
	To       string
	Position tax.Position

	// Sales and Purchases are the drill-down. A tax figure the owner cannot
	// decompose is a figure they have to take on trust, and this one is going
	// to a konsultan pajak.
	Sales     []gen.ListSalesForPPNRow
	Purchases []gen.ListPurchasesForPPNRow
}

// Position reports output PPN less creditable input PPN for a masa pajak.
//
//	output PPN  = Σ tax on sales
//	input PPN   = Σ tax on purchases WHERE faktur_received = true
//	payable     = output − input
//
// The filter on the input side is the whole reason purchase tracking exists.
// The absence of a filter on the output side is TASKS 5.4: a PKP owes output
// PPN whether or not the buyer took a faktur, so the walk-in half is reported
// on its own line rather than summed away.
//
// An empty masa means the current month in the company's own timezone (INV-5).
func (t *Tax) Position(ctx context.Context, entityID, masa string) (PPNPosition, error) {
	period, err := t.resolveMasa(ctx, entityID, masa)
	if err != nil {
		return PPNPosition{}, err
	}
	from, to := period.Range()

	output, err := t.q.SumOutputPPN(ctx, gen.SumOutputPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return PPNPosition{}, fmt.Errorf("service: sum output ppn: %w", err)
	}
	outputReversed, err := t.q.SumReversedOutputPPN(ctx, gen.SumReversedOutputPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return PPNPosition{}, fmt.Errorf("service: sum reversed output ppn: %w", err)
	}
	inputCreditable, err := t.q.SumCreditableInputPPN(ctx, gen.SumCreditableInputPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return PPNPosition{}, fmt.Errorf("service: sum creditable input ppn: %w", err)
	}
	inputReversed, err := t.q.SumReversedInputPPN(ctx, gen.SumReversedInputPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return PPNPosition{}, fmt.Errorf("service: sum reversed input ppn: %w", err)
	}
	inputNonCreditable, err := t.q.SumNonCreditableInputPPN(ctx, gen.SumNonCreditableInputPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return PPNPosition{}, fmt.Errorf("service: sum non-creditable input ppn: %w", err)
	}

	sales, err := t.q.ListSalesForPPN(ctx, gen.ListSalesForPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return PPNPosition{}, fmt.Errorf("service: sales for ppn: %w", err)
	}
	purchases, err := t.q.ListPurchasesForPPN(ctx, gen.ListPurchasesForPPNParams{
		EntityID: entityID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return PPNPosition{}, fmt.Errorf("service: purchases for ppn: %w", err)
	}

	return PPNPosition{
		Masa: period.String(), From: from, To: to,
		Position: tax.Position{
			Masa:                period,
			OutputWithFaktur:    money.IDR(output.WithFakturIdr),
			OutputWithoutFaktur: money.IDR(output.WithoutFakturIdr),
			OutputReversed:      money.IDR(outputReversed),
			InputCreditable:     money.IDR(inputCreditable),
			InputReversed:       money.IDR(inputReversed),
			InputNonCreditable:  money.IDR(inputNonCreditable),
		},
		Sales: sales, Purchases: purchases,
	}, nil
}

// Today is the company's own date, for a screen deciding whether a rule is in
// force, staged, or finished.
//
// Read here rather than in the browser: the entity's timezone is a property of
// the company (INV-5), and a laptop with a wrong clock or a different zone
// would otherwise decide which tax rule the screen calls current.
func (t *Tax) Today(ctx context.Context, entityID string) (string, error) {
	var day string
	err := t.db.InTx(ctx, func(tx *sql.Tx) error {
		clock, err := loadEntityClock(ctx, tx, entityID)
		if err != nil {
			return err
		}
		day, _, err = clock.resolveDate("", t.now())
		return err
	})
	return day, err
}

// resolveMasa parses the requested month, defaulting to the company's current
// one in its own timezone (INV-5).
func (t *Tax) resolveMasa(ctx context.Context, entityID, masa string) (tax.Masa, error) {
	masa = strings.TrimSpace(masa)
	if masa != "" {
		parsed, err := tax.ParseMasa(masa)
		if err != nil {
			return tax.Masa{}, fmt.Errorf("%w: masa pajak harus YYYY-MM", ErrValidation)
		}
		return parsed, nil
	}

	var clock entityClock
	err := t.db.InTx(ctx, func(tx *sql.Tx) error {
		var err error
		clock, err = loadEntityClock(ctx, tx, entityID)
		return err
	})
	if err != nil {
		return tax.Masa{}, err
	}

	today := t.now().In(clock.loc)
	return tax.Masa{Year: today.Year(), Month: today.Month()}, nil
}

// --- pricing a sale (TASKS 5.3, 5.4, 5.5) -----------------------------------

// priceTax runs a priced cart through domain/tax and translates a refusal into
// something a cashier can act on.
//
// It lives here rather than in sales.go because it is the whole of the tax
// engine's contact with a sale, and because the wording of these refusals is
// the entire user-visible surface of TASKS 5.4 and 5.5. Each one names the
// company, the date and what to do next: a till that stops with "kesalahan
// internal" teaches a cashier to stop trusting the system, and this till stops
// for two reasons that are both somebody forgetting to configure something.
func priceTax(
	ctx context.Context,
	q *gen.Queries,
	entityID string,
	isPKP bool,
	businessDate string,
	fakturIssued bool,
	priced pricedCart,
) (tax.Result, error) {
	rules, err := rulesFor(ctx, q, entityID)
	if err != nil {
		return tax.Result{}, err
	}

	cart := tax.Cart{BusinessDate: businessDate, FakturIssued: fakturIssued}
	for i, l := range priced.lines {
		cart.Lines = append(cart.Lines, tax.Line{
			// The sale lines do not exist yet, so the reference is the position
			// in the cart. It is echoed back untouched and reaches no
			// arithmetic; the results come back in the order they went in.
			Ref:    strconv.Itoa(i + 1),
			Amount: l.net,
		})
	}

	result, err := tax.Calculate(tax.Seller{EntityID: entityID, IsPKP: isPKP}, cart, rules)
	if err != nil {
		return tax.Result{}, translateTaxRefusal(err, businessDate)
	}
	if len(result.Lines) != len(priced.lines) {
		// Cannot happen: the domain returns one result per cart line. Asserted
		// because the loop that writes the sale lines indexes into this by
		// position, and a mismatch there would file one line's tax against
		// another line's owner.
		return tax.Result{}, fmt.Errorf("service: tax returned %d lines for a cart of %d",
			len(result.Lines), len(priced.lines))
	}
	return result, nil
}

// translateTaxRefusal turns a domain refusal into Bahasa Indonesia that says
// what to go and fix.
func translateTaxRefusal(err error, businessDate string) error {
	var missing *tax.NoEffectiveRuleError
	if errors.As(err, &missing) {
		// TASKS 5.4. The till stops rather than ringing the sale at no PPN: the
		// liability accrues on the delivery either way, and an hour of downtime
		// is cheaper than finding it at the masa pajak filing.
		return fmt.Errorf(
			"%w: belum ada aturan PPN yang berlaku pada %s. "+
				"Perusahaan ini terdaftar sebagai PKP, jadi PPN tetap terutang walau tidak dipungut. "+
				"Tambahkan aturan pajak di Pengaturan Pajak sebelum melanjutkan",
			ErrTaxConfig, businessDate)
	}

	var contradiction *tax.NonPKPChargeError
	if errors.As(err, &contradiction) {
		return fmt.Errorf(
			"%w: perusahaan ini ditandai bukan PKP, tetapi aturan PPN %s sudah berlaku sejak %s. "+
				"Salah satu harus diperbaiki: tandai perusahaan sebagai PKP, atau hapus aturan tersebut",
			ErrTaxConfig, contradiction.LegalRef, contradiction.ValidFrom)
	}

	if errors.Is(err, tax.ErrNonPKPFaktur) {
		return fmt.Errorf(
			"%w: perusahaan ini bukan PKP dan tidak boleh menerbitkan faktur pajak",
			ErrValidation)
	}

	return fmt.Errorf("%w: %w", ErrTaxConfig, err)
}
