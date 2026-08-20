package http

import (
	"io/fs"
	"log/slog"
	stdhttp "net/http"
	"path"
	"strings"

	"github.com/fadelmajid/tera/web"
)

// spaHandler serves the embedded browser client.
//
// Vite fingerprints everything under assets/, so those are immutable and cached
// for a year. index.html is not fingerprinted and must never be cached, or a
// staff member keeps loading last week's build against this week's API — on a
// LAN, with no CDN to purge and nobody to tell them to hard-refresh.
func spaHandler(log *slog.Logger) stdhttp.HandlerFunc {
	assets, ok := web.Assets()
	if !ok {
		log.Warn("antarmuka belum dibangun — jalankan `make web`; server hanya melayani API")
		return placeholderHandler()
	}

	files := stdhttp.FileServerFS(assets)

	return func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "." {
			serveIndex(w, r, assets)
			return
		}

		info, err := fs.Stat(assets, name)
		if err != nil || info.IsDir() {
			// Not a file: it is a client-side route. The SPA resolves it, so
			// hand over index.html rather than 404 — otherwise a staff member
			// refreshing the page on /produk gets an error instead of the page
			// they were already looking at.
			serveIndex(w, r, assets)
			return
		}

		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	}
}

func serveIndex(w stdhttp.ResponseWriter, r *stdhttp.Request, assets fs.FS) {
	body, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		writeJSON(w, stdhttp.StatusInternalServerError, map[string]any{"error": "antarmuka tidak tersedia"})
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == stdhttp.MethodHead {
		w.WriteHeader(stdhttp.StatusOK)
		return
	}
	w.WriteHeader(stdhttp.StatusOK)
	_, _ = w.Write(body)
}

// placeholderHandler answers when the binary was built without the front end.
func placeholderHandler() stdhttp.HandlerFunc {
	return func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(stdhttp.StatusOK)
		_, _ = w.Write([]byte(
			"Tera — server berjalan, API aktif.\n" +
				"Antarmuka belum disertakan dalam binary ini. Jalankan `make web` lalu build ulang.\n"))
	}
}
