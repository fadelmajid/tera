package fifo_test

import (
	"errors"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/fifo"
	"github.com/fadelmajid/tera/internal/domain/money"
)

// The cast. Ids are readable rather than UUIDs — the domain does not parse
// them, and a failing test that says "budi" beats one that says
// "o1f4c2a0-...".
const (
	entityPKP    = "entity-pkp"
	entityNonPKP = "entity-non-pkp"

	gloves  = "gloves"
	syringe = "syringe"

	ownerBudi fifo.OwnerID = "budi"
	ownerSari fifo.OwnerID = "sari"
)

// wib is the entity's zone. Business-day boundaries resolve here, never in UTC
// (INV-5) — irrelevant to ordering within this package, but the fixtures should
// not quietly teach the wrong habit.
var wib = time.FixedZone("WIB", 7*60*60)

func day(d int) time.Time { return time.Date(2026, time.August, d, 9, 0, 0, 0, wib) }

// lay builds a layer in the PKP entity for gloves. Explicit about the two
// numbers that matter: what came in, and how much has already been drawn.
func lay(id string, acquired int, owner fifo.OwnerID, qtyIn, consumed int64, cost money.IDR) fifo.Layer {
	return fifo.Layer{
		ID:          id,
		EntityID:    entityPKP,
		ProductID:   gloves,
		OwnerID:     owner,
		AcquiredAt:  day(acquired),
		QtyIn:       qtyIn,
		QtyConsumed: consumed,
		CostTotal:   cost,
	}
}

func request(owner fifo.OwnerID, qty int64) fifo.Request {
	return fifo.Request{
		EntityID:   entityPKP,
		ProductID:  gloves,
		OwnerID:    owner,
		Qty:        qty,
		MovementID: "sale-001",
		OccurredAt: day(31),
	}
}

func mustConsume(t *testing.T, req fifo.Request, layers []fifo.Layer) fifo.Result {
	t.Helper()

	got, err := fifo.Consume(req, layers)
	if err != nil {
		t.Fatalf("Consume: unexpected error: %v", err)
	}
	return got
}

// drawn flattens a result to (layer, qty, cost) triples for comparison.
type draw struct {
	layer string
	qty   int64
	cost  money.IDR
}

func drawn(r fifo.Result) []draw {
	out := make([]draw, 0, len(r.Consumptions))
	for _, c := range r.Consumptions {
		out = append(out, draw{c.LayerID, c.QtyOut, c.Cost})
	}
	return out
}

// --- ordering ---------------------------------------------------------------

func TestConsumeDrawsOldestLayersFirst(t *testing.T) {
	t.Parallel()

	// Three deliveries at rising cost. A draw of 8 must exhaust August's two
	// cheapest batches and take the balance from the third.
	layers := []fifo.Layer{
		lay("sept", 20, ownerBudi, 5, 0, 60_000), // newest, listed first on purpose
		lay("aug-1", 1, ownerBudi, 5, 0, 50_000),
		lay("aug-2", 10, ownerBudi, 5, 0, 55_000),
	}

	got := mustConsume(t, request(ownerBudi, 8), layers)

	want := []draw{
		{"aug-1", 5, 50_000},
		{"aug-2", 3, 33_000}, // 3 of 5 at Rp 55.000 total
	}
	if !slices.Equal(drawn(got), want) {
		t.Errorf("draws = %+v, want %+v", drawn(got), want)
	}
	if got.COGS != 83_000 {
		t.Errorf("COGS = %s, want %s", got.COGS, money.IDR(83_000))
	}
}

func TestConsumeBreaksAcquiredAtTiesByLayerID(t *testing.T) {
	t.Parallel()

	// One purchase, several lines, all stamped the same second. Ids are UUIDv7
	// and so time-ordered (D-003), which makes id order insertion order — what
	// FIFO means here. Without a tiebreak the family could be told the cheaper
	// batch went first when it did not.
	layers := []fifo.Layer{
		lay("c", 1, ownerBudi, 1, 0, 3_000),
		lay("a", 1, ownerBudi, 1, 0, 1_000),
		lay("b", 1, ownerBudi, 1, 0, 2_000),
	}

	got := mustConsume(t, request(ownerBudi, 2), layers)

	want := []draw{{"a", 1, 1_000}, {"b", 1, 2_000}}
	if !slices.Equal(drawn(got), want) {
		t.Errorf("draws = %+v, want %+v", drawn(got), want)
	}
}

func TestConsumeIsDeterministicWhateverOrderLayersArrive(t *testing.T) {
	t.Parallel()

	base := []fifo.Layer{
		lay("l1", 1, ownerBudi, 3, 0, 30_000),
		lay("l2", 2, ownerBudi, 4, 1, 44_000),
		lay("l3", 2, ownerBudi, 5, 0, 55_000),
		lay("l4", 9, ownerBudi, 2, 0, 22_000),
	}
	want := drawn(mustConsume(t, request(ownerBudi, 9), slices.Clone(base)))

	rng := rand.New(rand.NewSource(20260821))
	for i := range 50 {
		shuffled := slices.Clone(base)
		rng.Shuffle(len(shuffled), func(a, b int) { shuffled[a], shuffled[b] = shuffled[b], shuffled[a] })

		if got := drawn(mustConsume(t, request(ownerBudi, 9), shuffled)); !slices.Equal(got, want) {
			t.Fatalf("shuffle %d changed the result:\n got %+v\nwant %+v", i, got, want)
		}
	}
}

func TestConsumeSkipsExhaustedLayers(t *testing.T) {
	t.Parallel()

	// An exhausted layer stays in the trail forever (INV-7) — it just has
	// nothing left to give.
	layers := []fifo.Layer{
		lay("spent", 1, ownerBudi, 5, 5, 50_000),
		lay("live", 2, ownerBudi, 5, 0, 60_000),
	}

	got := mustConsume(t, request(ownerBudi, 2), layers)

	if want := []draw{{"live", 2, 24_000}}; !slices.Equal(drawn(got), want) {
		t.Errorf("draws = %+v, want %+v", drawn(got), want)
	}
}

func TestConsumeRespectsQuantityAlreadyDrawn(t *testing.T) {
	t.Parallel()

	// Remaining is qty_in − Σ qty_out, derived every time (SPEC §3.1). The
	// layer row itself is never decremented.
	layers := []fifo.Layer{
		lay("part", 1, ownerBudi, 10, 7, 100_000),
		lay("next", 2, ownerBudi, 10, 0, 200_000),
	}

	got := mustConsume(t, request(ownerBudi, 5), layers)

	// The 3 left on "part" cost the tail of its Rp 100.000: 100.000 − 70.000.
	want := []draw{{"part", 3, 30_000}, {"next", 2, 40_000}}
	if !slices.Equal(drawn(got), want) {
		t.Errorf("draws = %+v, want %+v", drawn(got), want)
	}
}

// --- owner scoping (INV-8) --------------------------------------------------

// TestConsumeNeverDrawsAnotherOwnersLayers is TASKS 1.3 ⭐ and acceptance
// criterion 6.
//
// Budi has 3 boxes. Sari has 100 of the same product on the same shelf. A sale
// of 10 of Budi's must fail. Taking the other 7 from Sari would move real money
// between two family members who settle monthly on these figures (R2.4) — it is
// not a convenience, it is a silent transfer.
func TestConsumeNeverDrawsAnotherOwnersLayers(t *testing.T) {
	t.Parallel()

	layers := []fifo.Layer{
		lay("budi-1", 1, ownerBudi, 3, 0, 30_000),
		lay("sari-1", 2, ownerSari, 100, 0, 1_000_000),
		lay("company-1", 3, fifo.Company, 50, 0, 500_000),
	}

	got, err := fifo.Consume(request(ownerBudi, 10), layers)

	if !errors.Is(err, fifo.ErrInsufficientStock) {
		t.Fatalf("got error %v, want ErrInsufficientStock — a short owner must never be covered from elsewhere", err)
	}
	if len(got.Consumptions) != 0 || !got.COGS.IsZero() {
		t.Errorf("a failed draw returned %+v; it must be all-or-nothing", got)
	}

	var short *fifo.InsufficientStockError
	if !errors.As(err, &short) {
		t.Fatalf("error %v does not carry the detail the UI needs", err)
	}
	if short.OwnerID != ownerBudi {
		t.Errorf("OwnerID = %s, want %s", short.OwnerID, ownerBudi)
	}
	if short.Requested != 10 || short.Available != 3 || short.Short() != 7 {
		t.Errorf("requested %d, available %d, short %d; want 10, 3, 7",
			short.Requested, short.Available, short.Short())
	}
	// 150 on the shelf, none of it Budi's. Reporting the figure is what turns
	// "out of stock" in front of a full shelf into an answer someone can act on.
	if short.OtherOwnersAvailable != 150 {
		t.Errorf("OtherOwnersAvailable = %d, want 150", short.OtherOwnersAvailable)
	}
}

func TestConsumeTreatsTheCompanyBucketAsItsOwnOwner(t *testing.T) {
	t.Parallel()

	// owner_id NULL is a real attribution reported alongside the named owners
	// (SPEC §4.3, R2.2) — not a shared pool everyone may dip into.
	layers := []fifo.Layer{
		lay("company-1", 1, fifo.Company, 10, 0, 100_000),
		lay("budi-1", 2, ownerBudi, 10, 0, 200_000),
	}

	company := mustConsume(t, request(fifo.Company, 4), layers)
	if want := []draw{{"company-1", 4, 40_000}}; !slices.Equal(drawn(company), want) {
		t.Errorf("company draw = %+v, want %+v", drawn(company), want)
	}

	budi := mustConsume(t, request(ownerBudi, 4), layers)
	if want := []draw{{"budi-1", 4, 80_000}}; !slices.Equal(drawn(budi), want) {
		t.Errorf("owner draw = %+v, want %+v — the company bucket is not a fallback", drawn(budi), want)
	}
}

func TestConsumeIgnoresOtherEntitiesAndProducts(t *testing.T) {
	t.Parallel()

	// Scoping is enforced here rather than trusted to the caller's WHERE
	// clause. Stock in the other company is not this company's stock, however
	// the same the two look on a shelf.
	other := lay("wrong-entity", 1, ownerBudi, 100, 0, 1_000_000)
	other.EntityID = entityNonPKP

	wrongProduct := lay("wrong-product", 1, ownerBudi, 100, 0, 1_000_000)
	wrongProduct.ProductID = syringe

	layers := []fifo.Layer{other, wrongProduct, lay("right", 5, ownerBudi, 4, 0, 40_000)}

	got := mustConsume(t, request(ownerBudi, 4), layers)

	if want := []draw{{"right", 4, 40_000}}; !slices.Equal(drawn(got), want) {
		t.Errorf("draws = %+v, want %+v", drawn(got), want)
	}

	// And the same layers cannot rescue a short request either.
	if _, err := fifo.Consume(request(ownerBudi, 5), layers); !errors.Is(err, fifo.ErrInsufficientStock) {
		t.Errorf("got error %v, want ErrInsufficientStock", err)
	}
}

func TestConsumeWithNoLayersAtAllIsInsufficientStock(t *testing.T) {
	t.Parallel()

	_, err := fifo.Consume(request(ownerBudi, 1), nil)
	if !errors.Is(err, fifo.ErrInsufficientStock) {
		t.Fatalf("got error %v, want ErrInsufficientStock", err)
	}

	var short *fifo.InsufficientStockError
	if !errors.As(err, &short) {
		t.Fatalf("error %v does not carry the detail the UI needs", err)
	}
	if short.Available != 0 || short.OtherOwnersAvailable != 0 {
		t.Errorf("available %d, other owners %d; want 0, 0", short.Available, short.OtherOwnersAvailable)
	}
}

func TestConsumeExactlyEmptiesTheOwnersStock(t *testing.T) {
	t.Parallel()

	// The boundary either side of the refusal.
	layers := []fifo.Layer{
		lay("l1", 1, ownerBudi, 3, 0, 30_000),
		lay("l2", 2, ownerBudi, 4, 0, 40_000),
	}

	got := mustConsume(t, request(ownerBudi, 7), layers)
	if got.COGS != 70_000 {
		t.Errorf("COGS = %s, want %s", got.COGS, money.IDR(70_000))
	}

	if _, err := fifo.Consume(request(ownerBudi, 8), layers); !errors.Is(err, fifo.ErrInsufficientStock) {
		t.Errorf("one unit past the last: got error %v, want ErrInsufficientStock", err)
	}
}

// --- precision (TASKS 1.4 ⭐) ------------------------------------------------

// TestSevenUnitLayerAtOneHundredThousandConsumesToExactlyOneHundredThousand is
// the worked example from SPEC §1 and TASKS 1.4.
//
// Rp 100.000 over 7 units is Rp 14.285,714…/unit. Store that rounded and
// multiply it back out and the layer gives up Rp 99.995 or Rp 100.002 — a
// rupiah or two adrift on every batch, compounding across a month, in the one
// number the family splits money on.
func TestSevenUnitLayerAtOneHundredThousandConsumesToExactlyOneHundredThousand(t *testing.T) {
	t.Parallel()

	const (
		qtyIn = int64(7)
		cost  = money.IDR(100_000)
	)

	// Every way of drawing the layer down, one draw at a time.
	for _, pattern := range [][]int64{
		{7},
		{1, 1, 1, 1, 1, 1, 1},
		{3, 4},
		{4, 3},
		{1, 5, 1},
		{2, 2, 2, 1},
		{6, 1},
	} {
		t.Run(patternName(pattern), func(t *testing.T) {
			t.Parallel()

			var (
				consumed int64
				total    money.IDR
			)
			for i, take := range pattern {
				got := mustConsume(t, request(ownerBudi, take),
					[]fifo.Layer{lay("seven", 1, ownerBudi, qtyIn, consumed, cost)})

				if len(got.Consumptions) != 1 {
					t.Fatalf("draw %d hit %d layers, want 1", i, len(got.Consumptions))
				}
				consumed += take
				total = total.Add(got.COGS)
			}

			if consumed != qtyIn {
				t.Fatalf("drew %d units, want %d", consumed, qtyIn)
			}
			if total != cost {
				t.Errorf("the layer gave up %s in total, want exactly %s (%s adrift)",
					total, cost, total.Sub(cost))
			}
		})
	}
}

// TestALayerAlwaysGivesUpExactlyWhatItCost generalises the case above: no
// combination of layer size, cost, and draw sizes may lose or invent a rupiah.
func TestALayerAlwaysGivesUpExactlyWhatItCost(t *testing.T) {
	t.Parallel()

	// Sizes and costs chosen to divide badly: primes, and totals that are not
	// multiples of the quantity.
	quantities := []int64{1, 2, 3, 7, 11, 13, 17, 100, 999}
	costs := []money.IDR{0, 1, 7, 100, 99_999, 100_000, 1_000_001, 7_777_777, 123_456_789}

	rng := rand.New(rand.NewSource(1_700_000_007))

	for _, qtyIn := range quantities {
		for _, cost := range costs {
			// Draw the layer down in random slices, several different ways.
			for attempt := range 25 {
				var (
					consumed int64
					total    money.IDR
				)
				for consumed < qtyIn {
					take := int64(rng.Intn(int(qtyIn-consumed))) + 1

					got := mustConsume(t, request(ownerBudi, take),
						[]fifo.Layer{lay("l", 1, ownerBudi, qtyIn, consumed, cost)})

					// No individual draw may be negative or exceed the layer.
					if got.COGS.IsNegative() || got.COGS > cost {
						t.Fatalf("qtyIn=%d cost=%s: a draw of %d cost %s, which is out of range",
							qtyIn, cost, take, got.COGS)
					}

					consumed += take
					total = total.Add(got.COGS)
				}

				if total != cost {
					t.Fatalf("qtyIn=%d cost=%s attempt=%d: drew a total of %s, want exactly %s",
						qtyIn, cost, attempt, total, cost)
				}
			}
		}
	}
}

func TestConsumeAcrossSeparateCallsLosesNoRupiah(t *testing.T) {
	t.Parallel()

	// A batch half-sold in March and finished in September is ordinary, and the
	// two calls share no state through which a remainder could be carried. Each
	// draw is anchored to a prefix of the layer instead, so the books still
	// close to the rupiah.
	const (
		qtyIn = int64(3)
		cost  = money.IDR(10_000) // Rp 3.333,33/unit
	)

	march := mustConsume(t, request(ownerBudi, 1), []fifo.Layer{lay("l", 1, ownerBudi, qtyIn, 0, cost)})
	june := mustConsume(t, request(ownerBudi, 1), []fifo.Layer{lay("l", 1, ownerBudi, qtyIn, 1, cost)})
	sept := mustConsume(t, request(ownerBudi, 1), []fifo.Layer{lay("l", 1, ownerBudi, qtyIn, 2, cost)})

	if total := money.Sum(march.COGS, june.COGS, sept.COGS); total != cost {
		t.Errorf("three draws months apart totalled %s, want exactly %s", total, cost)
	}
}

func TestCOGSEqualsTheSumOfItsConsumptions(t *testing.T) {
	t.Parallel()

	// The drill-down in SPEC §4.2 walks an owner's total down to individual
	// layer costs. If the headline does not equal the rows beneath it, the one
	// hard requirement of the margin report is already broken.
	layers := []fifo.Layer{
		lay("l1", 1, ownerBudi, 7, 0, 100_000),
		lay("l2", 2, ownerBudi, 3, 1, 10_000),
		lay("l3", 3, ownerBudi, 11, 0, 999_999),
	}

	got := mustConsume(t, request(ownerBudi, 15), layers)

	var sum money.IDR
	for _, c := range got.Consumptions {
		sum = sum.Add(c.Cost)
	}
	if sum != got.COGS {
		t.Errorf("COGS = %s but the consumptions sum to %s", got.COGS, sum)
	}
}

// --- derived quantities -----------------------------------------------------

func TestLayerRemainingAndCostAreDerivedNotStored(t *testing.T) {
	t.Parallel()

	l := lay("l", 1, ownerBudi, 7, 3, 100_000)

	if got := l.Remaining(); got != 4 {
		t.Errorf("Remaining() = %d, want 4", got)
	}
	if got := l.CostConsumed(); got != 42_857 {
		t.Errorf("CostConsumed() = %s, want %s", got, money.IDR(42_857))
	}
	if got := l.CostRemaining(); got != 57_143 {
		t.Errorf("CostRemaining() = %s, want %s", got, money.IDR(57_143))
	}
	if got := l.CostConsumed().Add(l.CostRemaining()); got != l.CostTotal {
		t.Errorf("consumed + remaining = %s, want %s", got, l.CostTotal)
	}
	if got := l.CostAt(0); !got.IsZero() {
		t.Errorf("CostAt(0) = %s, want zero", got)
	}
	if got := l.CostAt(l.QtyIn); got != l.CostTotal {
		t.Errorf("CostAt(qtyIn) = %s, want the full %s", got, l.CostTotal)
	}
}

// --- purity -----------------------------------------------------------------

func TestConsumeDoesNotTouchTheLayersItIsGiven(t *testing.T) {
	t.Parallel()

	// Layers are append-only (INV-7). Consume sorts a copy and never writes a
	// quantity back — the caller's slice comes out exactly as it went in, in
	// the same order.
	layers := []fifo.Layer{
		lay("z", 9, ownerBudi, 5, 0, 50_000),
		lay("a", 1, ownerBudi, 5, 2, 50_000),
		lay("m", 5, ownerSari, 5, 0, 50_000),
	}
	before := slices.Clone(layers)

	mustConsume(t, request(ownerBudi, 6), layers)

	for i := range layers {
		if layers[i] != before[i] {
			t.Errorf("layer at index %d was modified:\n got %+v\nwant %+v", i, layers[i], before[i])
		}
	}
}

// --- validation -------------------------------------------------------------

func TestConsumeRejectsMalformedRequests(t *testing.T) {
	t.Parallel()

	good := request(ownerBudi, 1)
	layers := []fifo.Layer{lay("l", 1, ownerBudi, 10, 0, 100_000)}

	tests := []struct {
		name   string
		mutate func(*fifo.Request)
	}{
		{"no entity", func(r *fifo.Request) { r.EntityID = "" }},
		{"blank entity", func(r *fifo.Request) { r.EntityID = "   " }},
		{"no product", func(r *fifo.Request) { r.ProductID = "" }},
		{"no movement to attribute the draw to", func(r *fifo.Request) { r.MovementID = "" }},
		{"zero quantity", func(r *fifo.Request) { r.Qty = 0 }},
		{"negative quantity is not how a return works", func(r *fifo.Request) { r.Qty = -3 }},
		{"no timestamp", func(r *fifo.Request) { r.OccurredAt = time.Time{} }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := good
			tc.mutate(&req)

			if _, err := fifo.Consume(req, layers); !errors.Is(err, fifo.ErrInvalidRequest) {
				t.Errorf("got error %v, want ErrInvalidRequest", err)
			}
		})
	}
}

func TestConsumeRejectsImpossibleLayers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*fifo.Layer)
	}{
		{"no id", func(l *fifo.Layer) { l.ID = "" }},
		{"no entity", func(l *fifo.Layer) { l.EntityID = "" }},
		{"no product", func(l *fifo.Layer) { l.ProductID = "" }},
		{"no acquisition time to order by", func(l *fifo.Layer) { l.AcquiredAt = time.Time{} }},
		{"brought in nothing", func(l *fifo.Layer) { l.QtyIn = 0 }},
		{"brought in a negative", func(l *fifo.Layer) { l.QtyIn = -5 }},
		{"negative total drawn", func(l *fifo.Layer) { l.QtyConsumed = -1 }},
		{"drawn beyond what it held", func(l *fifo.Layer) { l.QtyIn, l.QtyConsumed = 5, 6 }},
		{"negative cost", func(l *fifo.Layer) { l.CostTotal = -1 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			bad := lay("bad", 1, ownerBudi, 10, 0, 100_000)
			tc.mutate(&bad)

			// Refused even though a healthy layer alongside it could have
			// covered the draw: a malformed layer means the caller's query is
			// wrong, and a draw that happens to miss it is worse than a stop.
			layers := []fifo.Layer{lay("good", 2, ownerBudi, 100, 0, 1_000_000), bad}

			if _, err := fifo.Consume(request(ownerBudi, 1), layers); !errors.Is(err, fifo.ErrInvalidLayer) {
				t.Errorf("got error %v, want ErrInvalidLayer", err)
			}
		})
	}
}

func TestConsumeRejectsTheSameLayerTwice(t *testing.T) {
	t.Parallel()

	// A duplicated row would count one batch of stock twice and let a sale
	// succeed against goods that are not there.
	layers := []fifo.Layer{
		lay("l1", 1, ownerBudi, 5, 0, 50_000),
		lay("l1", 1, ownerBudi, 5, 0, 50_000),
	}

	if _, err := fifo.Consume(request(ownerBudi, 6), layers); !errors.Is(err, fifo.ErrInvalidLayer) {
		t.Errorf("got error %v, want ErrInvalidLayer", err)
	}
}

func TestConsumptionsCarryTheMovementAndTime(t *testing.T) {
	t.Parallel()

	// Every draw has to be traceable back to what caused it, or the drill-down
	// has nothing to walk (SPEC §4.2).
	req := request(ownerBudi, 6)
	got := mustConsume(t, req, []fifo.Layer{
		lay("l1", 1, ownerBudi, 4, 0, 40_000),
		lay("l2", 2, ownerBudi, 4, 0, 40_000),
	})

	if len(got.Consumptions) != 2 {
		t.Fatalf("got %d consumptions, want 2", len(got.Consumptions))
	}
	for _, c := range got.Consumptions {
		if c.MovementID != req.MovementID {
			t.Errorf("consumption on layer %s carries movement %q, want %q", c.LayerID, c.MovementID, req.MovementID)
		}
		if !c.OccurredAt.Equal(req.OccurredAt) {
			t.Errorf("consumption on layer %s occurred at %s, want %s", c.LayerID, c.OccurredAt, req.OccurredAt)
		}
	}
}

func TestOwnerIDCompanyBucket(t *testing.T) {
	t.Parallel()

	if !fifo.Company.IsCompany() {
		t.Error("Company.IsCompany() = false")
	}
	if ownerBudi.IsCompany() {
		t.Error("a named owner reported itself as the company bucket")
	}
	if got := fifo.Company.String(); got != "company" {
		t.Errorf("Company.String() = %q, want %q", got, "company")
	}
	if got := ownerBudi.String(); got != "budi" {
		t.Errorf("ownerBudi.String() = %q, want %q", got, "budi")
	}
}

func patternName(pattern []int64) string {
	name := ""
	for i, p := range pattern {
		if i > 0 {
			name += "+"
		}
		name += string(rune('0' + p))
	}
	return name
}
