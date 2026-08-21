package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fadelmajid/tera/internal/store/gen"
)

// businessDateFormat is the stored form: 'YYYY-MM-DD', checked by every
// migration that carries the column.
const businessDateFormat = "2006-01-02"

// businessDate is the day an instant counts for, in the entity's timezone.
//
// INV-5 and D-005. Never UTC, and never the server's zone: a sale rung at 23:30
// WIB on 31 December belongs to the closing book year, and a machine set to
// UTC would file it in January. The conversion cannot happen in SQL either --
// SQLite's localtime modifier uses the server's zone -- so it is done here, at
// write time, and stored.
func businessDate(t time.Time, loc *time.Location) string {
	return t.In(loc).Format(businessDateFormat)
}

// parseBusinessDate reads a 'YYYY-MM-DD' the user supplied, rejecting anything
// else rather than storing a string the CHECK constraint will bounce.
func parseBusinessDate(s string, loc *time.Location) (time.Time, error) {
	d, err := time.ParseInLocation(businessDateFormat, s, loc)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: tanggal harus berformat YYYY-MM-DD, diterima %q", ErrValidation, s)
	}
	return d, nil
}

// entityClock is a company's timezone and PKP status -- the two facts almost
// every operation in this package has to know before it can write anything.
//
// is_pkp is not a display flag. It decides whether input PPN is creditable at
// all (SPEC §2.3), and therefore what a stock layer costs (SPEC §3.2). Loading
// it inside the transaction rather than trusting a caller's copy keeps that
// decision anchored to what the database actually says at the moment of write.
type entityClock struct {
	loc   *time.Location
	isPKP bool
}

func loadEntityClock(ctx context.Context, tx *sql.Tx, entityID string) (entityClock, error) {
	if entityID == "" {
		return entityClock{}, fmt.Errorf("%w: perusahaan belum dipilih", ErrValidation)
	}

	e, err := gen.New(tx).GetLegalEntity(ctx, entityID)
	if err != nil {
		return entityClock{}, fmt.Errorf("%w: perusahaan tidak ditemukan", ErrNotFound)
	}

	loc, err := time.LoadLocation(e.Timezone)
	if err != nil {
		// Refuse rather than falling back to UTC. A wrong zone silently files
		// transactions on the wrong day, and the omzet clock is measured on
		// exactly those days (INV-5).
		return entityClock{}, fmt.Errorf("%w: zona waktu perusahaan %q tidak dikenal", ErrValidation, e.Timezone)
	}
	return entityClock{loc: loc, isPKP: e.IsPkp == 1}, nil
}

// resolveDate turns an optional user-supplied 'YYYY-MM-DD' into the day a
// document counts for and the instant it is treated as having happened.
//
// The instant is midnight of that day in the entity's zone, not the moment of
// data entry. FIFO consumes oldest-acquired first (SPEC §3.3), so a delivery
// note entered three days late has to sort where the goods actually arrived,
// not where the typing happened. Ordering within a day falls to the id
// tiebreak, which is insertion order because ids are UUIDv7 (D-003).
func (c entityClock) resolveDate(supplied string, fallback time.Time) (day string, instant time.Time, err error) {
	if supplied == "" {
		day = businessDate(fallback, c.loc)
	} else {
		day = supplied
	}

	parsed, err := parseBusinessDate(day, c.loc)
	if err != nil {
		return "", time.Time{}, err
	}
	return day, parsed, nil
}
