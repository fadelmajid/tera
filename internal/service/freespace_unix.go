//go:build unix

package service

import "syscall"

// freeBytes is the space left where backups go, or zero if it cannot be read.
//
// Reported rather than enforced. A destination filling up is the ordinary way
// scheduled backups stop happening, and it stops them silently — the snapshot
// fails, the log carries one line, and nobody reads the log until the day the
// disk dies.
func freeBytes(dir string) uint64 {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(dir, &fs); err != nil {
		return 0
	}
	return fs.Bavail * uint64(fs.Bsize)
}
