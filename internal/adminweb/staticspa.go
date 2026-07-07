package adminweb

import (
	"embed"
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed assets/dist/*
var spaFS embed.FS

func realSPAAssetsBuilt() bool {
	_, err := spaFS.Open("assets/dist/index.html")
	return err == nil
}

func serveMissingAssets(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html><html><head><title>GuDa Admin</title></head><body><main><h1>Admin UI assets not built</h1><p>Run <code>./scripts/build.sh</code> to build and embed the React admin UI.</p></main></body></html>`))
}

func serveSPA(w http.ResponseWriter, r *http.Request) {
	if !realSPAAssetsBuilt() {
		serveMissingAssets(w)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/admin")
	if name == "" || name == "/" {
		name = "index.html"
	} else {
		name = strings.TrimPrefix(path.Clean("/"+name), "/")
	}
	if strings.HasPrefix(name, "..") {
		http.NotFound(w, r)
		return
	}
	fileName := "assets/dist/" + name
	if _, err := spaFS.Open(fileName); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			fileName = "assets/dist/index.html"
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, spaFS, fileName)
			return
		}
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeFileFS(w, r, spaFS, fileName)
}
