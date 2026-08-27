//go:build unix

package store

import "syscall"

// sameDevice reports whether two paths sit on the same filesystem, and whether
// that could be determined at all.
//
// R14.2's teeth. Comparing the device number is the only reliable way to tell a
// USB stick from a folder on the same disk with a reassuring name — "D:\Backup"
// and "/Volumes/Backup" are both frequently the same physical drive.
func sameDevice(a, b string) (same, known bool) {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return false, false
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return false, false
	}
	return sa.Dev == sb.Dev, true
}
