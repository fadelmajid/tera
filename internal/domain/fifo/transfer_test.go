package fifo_test

import (
	"errors"
	"testing"

	"github.com/fadelmajid/tera/internal/domain/fifo"
	"github.com/fadelmajid/tera/internal/domain/money"
)

// The two directions fail differently and neither is free (REQUIREMENTS §6.2).
// This is the table that says so, and the reason "at cost, no markup" (R4.3) is
// a misleading way to think about an inter-company transfer.
func TestDirectionConsequences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		dir            fifo.Direction
		taxable        bool
		canFaktur      bool
		destroysCredit bool
	}{
		{
			// The direction that looks free at transfer time and costs more
			// later: the receiving PKP entity will owe full output PPN with
			// nothing to set against it.
			name:    "non-PKP to PKP forfeits the credit",
			dir:     fifo.Direction{FromIsPKP: false, ToIsPKP: true},
			taxable: false, canFaktur: false, destroysCredit: true,
		},
		{
			// PPN applies now, and the non-PKP receiver can never credit it, so
			// it lands in their cost. Expensive, but nothing is destroyed that
			// could have been kept.
			name:    "PKP to non-PKP is a taxable delivery",
			dir:     fifo.Direction{FromIsPKP: true, ToIsPKP: false},
			taxable: true, canFaktur: true, destroysCredit: false,
		},
		{
			name:    "PKP to PKP passes the credit through",
			dir:     fifo.Direction{FromIsPKP: true, ToIsPKP: true},
			taxable: true, canFaktur: true, destroysCredit: false,
		},
		{
			name:    "non-PKP to non-PKP has no PPN anywhere in it",
			dir:     fifo.Direction{FromIsPKP: false, ToIsPKP: false},
			taxable: false, canFaktur: false, destroysCredit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.dir.TaxableDelivery(); got != tt.taxable {
				t.Errorf("TaxableDelivery() = %v, want %v", got, tt.taxable)
			}
			if got := tt.dir.CanIssueFaktur(); got != tt.canFaktur {
				t.Errorf("CanIssueFaktur() = %v, want %v", got, tt.canFaktur)
			}
			if got := tt.dir.DestroysInputCredit(); got != tt.destroysCredit {
				t.Errorf("DestroysInputCredit() = %v, want %v", got, tt.destroysCredit)
			}
		})
	}
}

// SPEC §3.4's two rows, to the rupiah. Same goods, same cost, and the
// receiving layer costs a different amount depending only on which way it
// crossed.
func TestDestinationBasisByDirection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		transfer   fifo.Transfer
		wantCost   money.IDR
		wantCredit money.IDR
	}{
		{
			// PKP → non-PKP: PPN applies on the delivery and the receiver can
			// never credit it, so it is cost. Rp 100.000 of goods arrives on
			// the books at Rp 111.000.
			name: "PKP to non-PKP: the PPN becomes cost",
			transfer: fifo.Transfer{
				Direction:    fifo.Direction{FromIsPKP: true, ToIsPKP: false},
				Cost:         100_000,
				PPN:          11_000,
				FakturIssued: true,
			},
			wantCost: 111_000, wantCredit: 0,
		},
		{
			// non-PKP → PKP: no PPN can be charged and no faktur can exist, so
			// the layer costs what it cost — and carries zero credit into a
			// company that will owe output PPN on every sale of it.
			name: "non-PKP to PKP: cost carries across, credit does not",
			transfer: fifo.Transfer{
				Direction: fifo.Direction{FromIsPKP: false, ToIsPKP: true},
				Cost:      100_000,
			},
			wantCost: 100_000, wantCredit: 0,
		},
		{
			name: "PKP to PKP with a faktur: the credit survives",
			transfer: fifo.Transfer{
				Direction:    fifo.Direction{FromIsPKP: true, ToIsPKP: true},
				Cost:         100_000,
				PPN:          11_000,
				FakturIssued: true,
			},
			wantCost: 100_000, wantCredit: 11_000,
		},
		{
			// A PKP that charged PPN but issued no faktur leaves the receiver
			// exactly where a supplier who forgot the paperwork would: paying
			// the tax and unable to credit it (INV-9).
			name: "PKP to PKP without a faktur: the PPN becomes cost",
			transfer: fifo.Transfer{
				Direction: fifo.Direction{FromIsPKP: true, ToIsPKP: true},
				Cost:      100_000,
				PPN:       11_000,
			},
			wantCost: 111_000, wantCredit: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.transfer.DestinationBasis()
			if err != nil {
				t.Fatalf("DestinationBasis: %v", err)
			}
			if got.CostTotal != tt.wantCost {
				t.Errorf("layer cost = %s, want %s", got.CostTotal, tt.wantCost)
			}
			if got.CreditablePPN != tt.wantCredit {
				t.Errorf("creditable PPN = %s, want %s", got.CreditablePPN, tt.wantCredit)
			}
		})
	}
}

// SPEC §2.3, enforced rather than conventional: a non-PKP cannot charge PPN and
// cannot issue a faktur. Letting either through would manufacture a creditable
// layer out of a delivery that legally cannot support one.
func TestANonPKPSenderCannotChargePPNOrIssueAFaktur(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		transfer fifo.Transfer
	}{
		{
			name: "PPN on a non-PKP delivery",
			transfer: fifo.Transfer{
				Direction: fifo.Direction{FromIsPKP: false, ToIsPKP: true},
				Cost:      100_000, PPN: 11_000,
			},
		},
		{
			name: "a faktur from a company that cannot issue one",
			transfer: fifo.Transfer{
				Direction: fifo.Direction{FromIsPKP: false, ToIsPKP: true},
				Cost:      100_000, FakturIssued: true,
			},
		},
		{
			name: "a negative transfer cost",
			transfer: fifo.Transfer{
				Direction: fifo.Direction{FromIsPKP: true, ToIsPKP: false},
				Cost:      -1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, err := tt.transfer.DestinationBasis(); !errors.Is(err, fifo.ErrInvalidAcquisition) {
				t.Fatalf("got %v, want ErrInvalidAcquisition", err)
			}
		})
	}
}
