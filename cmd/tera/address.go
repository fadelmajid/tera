package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	terahttp "github.com/fadelmajid/tera/internal/transport/http"
)

// addressState is the last set of LAN addresses this machine served on.
type addressState struct {
	Addresses []string `json:"addresses"`
	SeenAt    string   `json:"seen_at"`
}

// warnIfAddressChanged notices when the router has handed this machine a
// different address since the last run. R8.6, TASKS 8.3.
//
// This is the failure the requirement is about: DHCP renews the lease until one
// day it does not, every bookmarked address in the shop breaks at once, and the
// only symptom is a browser that cannot connect — which looks exactly like the
// server being down. The server itself is the only thing in the building that
// can see both the old address and the new one, so it is the only thing that
// can say what happened.
//
// A warning, never a refusal. A shop whose till will not start because its IP
// moved is a worse outage than the one this is warning about.
func warnIfAddressChanged(dbPath string, logger *slog.Logger) {
	path := filepath.Join(filepath.Dir(dbPath), ".tera-alamat.json")
	current := terahttp.LANAddresses()
	if len(current) == 0 {
		return
	}

	previous, err := readAddressState(path)
	switch {
	case err != nil:
		// First run, or the file was lost with the machine. Nothing to compare.
	case !sameAddresses(previous.Addresses, current):
		logger.Warn("alamat IP mesin ini berubah sejak terakhir dijalankan",
			"sebelumnya", strings.Join(previous.Addresses, " "),
			"sekarang", strings.Join(current, " "),
			"terakhir_dilihat", previous.SeenAt,
			"akibat", "bookmark di perangkat lain kemungkinan besar sudah tidak berlaku",
			"saran", "pakai alamat .local, atau minta router menetapkan IP tetap untuk mesin ini")
	}

	body, err := json.MarshalIndent(addressState{
		Addresses: current, SeenAt: time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return
	}
	// Best effort. A read-only folder must not stop the shop trading.
	_ = os.WriteFile(path, append(body, '\n'), 0o600)
}

func readAddressState(path string) (addressState, error) {
	body, err := os.ReadFile(path) //nolint:gosec // a path this process wrote beside its own database
	if err != nil {
		return addressState{}, err
	}
	var out addressState
	if err := json.Unmarshal(body, &out); err != nil {
		return addressState{}, err
	}
	return out, nil
}

func sameAddresses(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
