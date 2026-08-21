package store_test

import (
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fadelmajid/tera/internal/domain/fifo"
	"github.com/fadelmajid/tera/internal/domain/money"
	"github.com/fadelmajid/tera/internal/store"
	"github.com/fadelmajid/tera/internal/store/gen"
)

// TestConcurrentDrawsCannotOverConsumeALayer is the guarantee TASKS 2.2 rests
// on, proved rather than reasoned about.
//
// A sale reads the FIFO layers, decides in the domain, then writes the
// consumptions (ARCHITECTURE §4). That read-then-write shape is the classic
// place to lose stock: two cashiers ring the last box at the same moment, both
// read "1 remaining", and both sell it. The layer goes negative, and every
// margin figure drawn from it afterwards is wrong.
//
// Two settings in store.Open close it -- a single pooled connection, and
// _txlock=immediate so the write lock is taken at BEGIN rather than on the
// first write. This test fires far more concurrent draws than there is stock
// and asserts that exactly the available quantity is sold, no more, and that
// the layer never goes negative.
func TestConcurrentDrawsCannotOverConsumeALayer(t *testing.T) {
	t.Parallel()

	db, q, ctx := migrated(t)
	f := newStockFixtures(ctx, t, q)

	const (
		stock   = 10
		buyers  = 40
		perDraw = 1
	)
	layerID := f.layer(ctx, t, q, &f.budi, 1_800_000_000, stock, 100_000, 1)

	// One cashier ringing one unit: exactly the sale flow of TASKS 2.2.
	sell := func(movementID string) error {
		return db.InTx(ctx, func(tx *sql.Tx) error {
			qtx := gen.New(tx)

			balances, err := qtx.ListLayersForConsumption(ctx, gen.ListLayersForConsumptionParams{
				EntityID: f.entity, ProductID: f.product,
			})
			if err != nil {
				return err
			}

			layers := make([]fifo.Layer, 0, len(balances))
			for _, b := range balances {
				layers = append(layers, fifo.Layer{
					ID: b.ID, EntityID: b.EntityID, ProductID: b.ProductID,
					OwnerID:    fifo.OwnerID(derefOr(b.OwnerID, "")),
					AcquiredAt: time.Unix(b.AcquiredAt, 0).UTC(),
					QtyIn:      b.QtyIn, QtyConsumed: b.QtyConsumed,
					CostTotal: moneyOf(b.CostTotalIdr),
				})
			}

			drawn, err := fifo.Consume(fifo.Request{
				EntityID: f.entity, ProductID: f.product, OwnerID: fifo.OwnerID(f.budi),
				Qty: perDraw, MovementID: movementID, OccurredAt: time.Unix(1_800_500_000, 0),
			}, layers)
			if err != nil {
				return err
			}

			for _, c := range drawn.Consumptions {
				if _, err := qtx.RecordConsumption(ctx, gen.RecordConsumptionParams{
					ID: store.NewID(), LayerID: c.LayerID, MovementID: movementID,
					MovementType: "SALE", QtyOut: c.QtyOut, CostIdr: int64(c.Cost),
					OccurredAt: 1_800_500_000, BusinessDate: "2026-08-20", CreatedAt: 1_800_500_000,
				}); err != nil {
					return err
				}
			}
			return nil
		})
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		sold   int
		short  int
		others []error
	)
	start := make(chan struct{})

	for range buyers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // all forty go at once

			err := sell(store.NewID())
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				sold++
			case errors.Is(err, fifo.ErrInsufficientStock):
				short++
			default:
				others = append(others, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	for _, err := range others {
		t.Errorf("a draw failed for a reason other than running out of stock: %v", err)
	}
	if sold != stock {
		t.Errorf("%d units sold from a layer holding %d", sold, stock)
	}
	if short != buyers-stock {
		t.Errorf("%d draws were refused, want %d", short, buyers-stock)
	}

	// The books agree: the layer is empty, not negative.
	balance, err := q.GetLayerBalance(ctx, layerID)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if balance.QtyRemaining != 0 {
		t.Errorf("layer holds %d after being sold out; a concurrent draw over-consumed it", balance.QtyRemaining)
	}

	// And every rupiah of the layer was accounted for exactly once -- the same
	// property the domain guarantees for sequential draws, holding under
	// contention.
	consumptions, err := q.ListConsumptionsForLayer(ctx, layerID)
	if err != nil {
		t.Fatalf("consumptions: %v", err)
	}
	var totalQty, totalCost int64
	for _, c := range consumptions {
		totalQty += c.QtyOut
		totalCost += c.CostIdr
	}
	if totalQty != stock {
		t.Errorf("consumption rows total %d units, want %d", totalQty, stock)
	}
	if totalCost != 100_000 {
		t.Errorf("consumption rows total %d rupiah, want exactly the layer cost 100000", totalCost)
	}
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

func moneyOf(v int64) money.IDR { return money.IDR(v) }
