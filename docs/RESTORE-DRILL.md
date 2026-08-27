# Restore drill — TASKS 8.2

**R8.7 accepts that the server machine dying loses everything on it, on the
condition that a backup restores onto any laptop.** That acceptance is the only
reason a single-machine deployment is defensible, and it rests entirely on this
drill.

One machine holds the numbers this family settles money on every month (R2.4).
An untested backup is a rumour.

This is a **physical drill**. It is not passed by reading it.

---

## Log of drills run

Add a row every time. A drill nobody has run in a year is a rumour again.

| Date | Ran by | Machine restored onto | Backup used | Result |
|---|---|---|---|---|
| 2026-08-21 | Claude (build machine) | **Isolated directory, same machine** — see the caveat below | Freshly taken during the drill | ✅ passed |
| | | | | |

> **The 2026-08-21 run did not use a second machine.** It restored into a
> directory that had never held a database, with nothing in it but the binary
> and the backup file, and verified the figures came back over HTTP. That
> proves the tooling, the procedure and the data. It does not prove the thing
> R8.7 actually promises — that this works on *different hardware*, with a
> different OS install, a different user account, and no repository present.
>
> **That run is still owed, on the spare laptop.** Everything below is what to
> do, and it is one command.

---

## What the automated part proves

`make restore-drill` runs the whole procedure end to end. On 2026-08-21 it
seeded a shop that bought 100 boxes and sold 3, backed it up, restored into a
clean directory, and read the figures back through the API:

```
sumber:        penjualan total=333000, sisa stok=97
hasil restore: penjualan total=333000, sisa stok=97
angka cocok dengan sumber
```

It checks, in order:

1. A backup is taken from a database that has actually traded.
2. The backup is restored into a directory that has never held one.
3. The server starts against the restored file.
4. `/readyz` reports ok and the schema version is readable.
5. The browser client loads.
6. The API is alive and refuses an unauthenticated request with 401.
7. **The same credentials still work** — a restore that loses the users is a
   restore nobody can log into.
8. **Sales total and stock on hand match the source exactly.**

Go tests cover the layers underneath: `TestASnapshotIsVerifiedBeforeItIsCalledABackup`,
`TestACorruptedBackupFailsVerification`, `TestABackupOnTheSameDiskIsRefused`,
`TestARestoreRefusesABackupItCannotVouchFor`,
`TestARestoreBringsBackTheNumbersThatWereThere`.

## What no automated test can prove

- That the backup destination is a **different physical disk** — a syscall can
  compare device numbers on the shop machine, and cannot tell whether the stick
  is still plugged in next week.
- That a person who is not the developer can do this, on a bad day, from
  written instructions.
- That the spare laptop has the binary, can read the stick, and has a working
  browser.
- That anybody remembers where the backups are.

---

## The drill

Run before go-live, and again after any change to the server machine, the
backup destination, or the schema.

### What you need

1. The **spare laptop** — not the server machine, and not the developer's.
2. The `tera` binary for that laptop's OS, on a USB stick.
3. A **real backup from the shop's own destination**, not one made for the
   occasion.
4. A note of what the shop's figures currently are: today's sales total and the
   stock on hand for one product you can count by eye.

### Steps

| # | Do this | Expected |
|---|---|---|
| 1 | On the server, run `tera backups --dir <backup folder>`. | A list, newest first. If the newest is older than a couple of hours, **stop** — the backups are not running and that is the finding. |
| 2 | Copy the newest `.db` **and its `.json`** onto the stick. | Both files. The `.json` carries the checksum; without it a restore cannot verify. |
| 3 | Take the stick to the spare laptop. Copy the binary and both files into an empty folder. | Nothing else in that folder. No repository, no `.env`. |
| 4 | Run `./tera restore --from tera-<timestamp>.db`. | It prints the backup's date, its schema version, and a row count per table. |
| 5 | Read the row counts. | Non-zero, and roughly the size of the business. All zeroes means you restored an empty backup. |
| 6 | Run `./tera`. | It starts and prints an address. |
| 7 | Open that address in the laptop's browser. | The login page. |
| 8 | Log in **with the shop's own credentials**. | It works. If it does not, the restore is not usable by the people who need it. |
| 9 | Open Laporan → Penjualan for today. | The total matches what you noted in step 4 of *What you need*. |
| 10 | Open Laporan → Stok and find the product you counted. | The quantity matches. |
| 11 | Open Margin per owner for last month. | Figures appear, and drill down to individual layers. |
| 12 | **Ring a sale on the restored copy**, then void it. | Both work. A database that reads but cannot write is not a recovered shop. |
| 13 | Stop the server. Delete the folder. | Housekeeping — the restored copy must not become a second source of truth somebody keeps using. |

### If any step fails

Write down which one, and do not proceed to go-live. A failure here means the
single-machine deployment is not currently defensible, and the honest options
are: fix it, or accept the risk explicitly and in writing.

---

## Running the automated drill on the spare laptop

The same checks the build machine runs, on the machine that matters:

```bash
./scripts/restore-drill.sh /path/to/tera-<timestamp>.db
```

Given a real backup it restores it, starts the server, and checks it serves.
It cannot compare the figures against a source it never saw — that is steps 9
and 10 above, done by eye against what the shop knows.

With no argument it seeds its own shop, backs it up, restores it, and compares
the figures automatically. That version proves the tooling; the version with
the shop's own backup proves the backups.

---

## Configuration this depends on

| Setting | What it does |
|---|---|
| `TERA_BACKUP_DIR` | Where snapshots go. **Blank disables backups**, and the server warns loudly at every start when it is. |
| `TERA_BACKUP_INTERVAL_MINUTES` | Default 60. The hourly cadence is what `synchronous=NORMAL` already assumes. |
| `TERA_BACKUP_KEEP` | Default 72 — enough to reach back past a weekend. |
| `TERA_BACKUP_ALLOW_SAME_DISK` | Off. Set to `1` only knowingly: a snapshot on the same disk survives a deleted file and nothing else. It warns at every start. |

The destination must be **removable or network storage** (R14.2). A backup on
the same disk is refused, because that is the copy that would not have helped.
