package api

import (
	"io"
	"io/fs"
	"net/http"
	"strings"
)

func (r *Router) handleRecipientApp(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(req.URL.Path, "/p/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(w, req)
		return
	}
	serveRecipientFile(w, req, r.recipientAssets, "index.html")
}

func (r *Router) handleRecipientAsset(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(req.URL.Path, "/")
	if path == "" || strings.Contains(path, "..") || strings.Contains(path, "\\") {
		http.NotFound(w, req)
		return
	}
	serveRecipientFile(w, req, r.recipientAssets, path)
}

func serveRecipientFile(w http.ResponseWriter, req *http.Request, assets fs.FS, name string) {
	file, err := assets.Open(name)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, req)
		return
	}
	reader, ok := file.(io.ReadSeeker)
	if !ok {
		http.NotFound(w, req)
		return
	}
	http.ServeContent(w, req, info.Name(), info.ModTime(), reader)
}
