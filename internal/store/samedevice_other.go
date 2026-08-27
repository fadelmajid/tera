//go:build !unix

package store

// sameDevice cannot be determined on this platform.
//
// Windows has no portable device number through the syscall package. The path
// check in checkDestination still catches a backup written into the database's
// own folder, and the restore drill (docs/RESTORE-DRILL.md) is what catches the
// rest — which is the honest answer anyway, since a drill proves the backup is
// on a different disk far better than a syscall proves it is on a different
// one.
func sameDevice(_, _ string) (same, known bool) { return false, false }
