package http

import (
	"bytes"
	"fmt"
	stdhttp "net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/fadelmajid/tera/internal/service"
)

// handleExport hands the user their entire database as a zip of CSVs.
// TASKS 6.6, R5.7, R14.3.
//
// Not scoped to one company, and it cannot be. The product catalogue, the
// owners, the suppliers and the customers are shared across both by design
// (D-006), so a per-company archive would hand over two files that each contain
// the shared half and neither of which is the database. R14.3 asks for the
// data, not for a view of it — so the gate is stricter instead; see
// [requireOwnerEverywhere].
func handleExport(exp *service.Export) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		// Built in memory before a byte reaches the client: a stream that fails
		// halfway has already sent its headers and leaves the user holding a
		// truncated archive they have no way to tell apart from a whole one.
		var buf bytes.Buffer
		manifest, err := exp.Archive(r.Context(), &buf)
		if err != nil {
			writeServiceError(w, err)
			return
		}

		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="`+manifest.Filename()+`"`)
		w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
		// The manifest figures ride in headers too, so a script fetching this
		// can check the archive without unzipping it first.
		w.Header().Set("X-Tera-Schema-Version", strconv.FormatInt(manifest.SchemaVersion, 10))
		w.Header().Set("X-Tera-Total-Rows", strconv.FormatInt(manifest.TotalRows, 10))
		w.WriteHeader(stdhttp.StatusOK)
		if _, err := w.Write(buf.Bytes()); err != nil {
			// The client went away mid-download. Nothing to say to them.
			return
		}
	}
}

// requireOwnerEverywhere gates the export on owning every active company.
//
// Not owner in the selected one: the archive is the whole database, so that
// would let somebody who owns the non-PKP company download the PKP company's
// entire history — precisely the boundary R13.4 exists to draw.
//
// The refusal names the company the caller lacks, because the fix is a role
// grant and an owner can make one. A family whose owners hold both companies —
// the arrangement this was built for — never sees it.
func requireOwnerEverywhere(md *service.MasterData) func(stdhttp.Handler) stdhttp.Handler {
	return func(next stdhttp.Handler) stdhttp.Handler {
		return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
			principal, ok := PrincipalFrom(r.Context())
			if !ok {
				writeJSON(w, stdhttp.StatusUnauthorized,
					map[string]any{"error": "silakan masuk terlebih dahulu"})
				return
			}

			entities, err := md.ListEntities(r.Context())
			if err != nil {
				writeServiceError(w, err)
				return
			}

			var missing []string
			for _, e := range entities {
				if e.IsActive != 1 {
					continue
				}
				if !principal.Can(e.ID, service.Role.CanExportEverything) {
					missing = append(missing, e.Name)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				writeJSON(w, stdhttp.StatusForbidden, map[string]any{
					"error": fmt.Sprintf(
						"ekspor berisi seluruh basis data, termasuk %s. "+
							"Hanya pemilik di semua perusahaan yang dapat mengunduhnya",
						strings.Join(missing, " dan ")),
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// handleExportManifest reports what an export would contain, without building
// it.
//
// The screen shows this before the download so the person can see the archive
// covers everything — a list of tables and row counts is the only way anybody
// can tell a complete export from a plausible one.
func handleExportManifest(exp *service.Export) stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		manifest, err := exp.Manifest(r.Context())
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, stdhttp.StatusOK, manifest)
	}
}

func mountExport(r chi.Router, cfg Config) {
	if cfg.Export == nil || cfg.Master == nil {
		return
	}

	r.Group(func(r chi.Router) {
		r.Use(requireAuth)
		r.Use(requireOwnerEverywhere(cfg.Master))

		r.Get("/export", handleExport(cfg.Export))
		// Gated identically: the manifest says how many sales and purchases
		// exist, which is the shape of the business even without the rows.
		r.Get("/export/manifest", handleExportManifest(cfg.Export))
	})
}
