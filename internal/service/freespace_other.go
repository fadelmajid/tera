//go:build !unix

package service

// freeBytes cannot be read portably on this platform.
func freeBytes(_ string) uint64 { return 0 }
