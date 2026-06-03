package server

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"

	"r2/internal/r2"
)

//go:embed web/*
var webFS embed.FS

// Server holds dependencies for the HTTP handlers.
type Server struct {
	client *r2.Client
	mux    *http.ServeMux
}

// New wires up routes.
func New(client *r2.Client) *Server {
	s := &Server{client: client, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.handleIndex)

	s.mux.HandleFunc("GET /api/buckets", s.handleListBuckets)
	s.mux.HandleFunc("POST /api/buckets", s.handleCreateBucket)
	s.mux.HandleFunc("DELETE /api/buckets/{bucket}", s.handleDeleteBucket)

	s.mux.HandleFunc("GET /api/buckets/{bucket}/objects", s.handleListObjects)
	s.mux.HandleFunc("POST /api/buckets/{bucket}/upload", s.handleUpload)
	s.mux.HandleFunc("POST /api/buckets/{bucket}/newfile", s.handleNewFile)
	s.mux.HandleFunc("DELETE /api/buckets/{bucket}/objects", s.handleDeleteObject)
	s.mux.HandleFunc("GET /api/buckets/{bucket}/download", s.handleDownload)
	s.mux.HandleFunc("GET /api/buckets/{bucket}/link", s.handleLink)
}

// ---- HTML ----

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		s.fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// ---- Buckets ----

func (s *Server) handleListBuckets(w http.ResponseWriter, r *http.Request) {
	buckets, err := s.client.ListBuckets(r.Context())
	if err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	s.ok(w, map[string]any{"buckets": buckets})
}

func (s *Server) handleCreateBucket(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		s.fail(w, http.StatusBadRequest, errors.New("bucket name is required"))
		return
	}
	if err := s.client.CreateBucket(r.Context(), body.Name); err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	s.ok(w, map[string]any{"created": body.Name})
}

func (s *Server) handleDeleteBucket(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("bucket")
	if err := s.client.DeleteBucket(r.Context(), name); err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	s.ok(w, map[string]any{"deleted": name})
}

// ---- Objects ----

func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	prefix := r.URL.Query().Get("prefix")
	listing, err := s.client.ListObjects(r.Context(), bucket, prefix)
	if err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	s.ok(w, listing)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB in memory, rest to disk
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	prefix := r.FormValue("prefix")
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		s.fail(w, http.StatusBadRequest, errors.New("no files in 'file' field"))
		return
	}
	uploaded := make([]string, 0, len(files))
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			s.fail(w, http.StatusBadRequest, err)
			return
		}
		key := path.Join(prefix, fh.Filename)
		key = strings.TrimPrefix(key, "/")
		ct := fh.Header.Get("Content-Type")
		err = s.client.Upload(r.Context(), bucket, key, f, ct)
		_ = f.Close()
		if err != nil {
			s.fail(w, http.StatusBadGateway, err)
			return
		}
		uploaded = append(uploaded, key)
	}
	s.ok(w, map[string]any{"uploaded": uploaded})
}

func (s *Server) handleNewFile(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	var body struct {
		Key     string `json:"key"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.fail(w, http.StatusBadRequest, err)
		return
	}
	key := strings.TrimPrefix(strings.TrimSpace(body.Key), "/")
	if key == "" {
		s.fail(w, http.StatusBadRequest, errors.New("key is required"))
		return
	}
	if err := s.client.Upload(r.Context(), bucket, key, strings.NewReader(body.Content), "text/plain; charset=utf-8"); err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	s.ok(w, map[string]any{"created": key})
}

func (s *Server) handleDeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.URL.Query().Get("key")
	if key == "" {
		s.fail(w, http.StatusBadRequest, errors.New("key is required"))
		return
	}
	if err := s.client.DeleteObject(r.Context(), bucket, key); err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	s.ok(w, map[string]any{"deleted": key})
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.URL.Query().Get("key")
	if key == "" {
		s.fail(w, http.StatusBadRequest, errors.New("key is required"))
		return
	}
	body, ct, err := s.client.Download(r.Context(), bucket, key)
	if err != nil {
		if r2.IsNotFound(err) {
			s.fail(w, http.StatusNotFound, err)
			return
		}
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	defer body.Close()

	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(key)))
	_, _ = io.Copy(w, body)
}

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.URL.Query().Get("key")
	if key == "" {
		s.fail(w, http.StatusBadRequest, errors.New("key is required"))
		return
	}
	link, err := s.client.ObjectLink(r.Context(), bucket, key)
	if err != nil {
		s.fail(w, http.StatusBadGateway, err)
		return
	}
	s.ok(w, link)
}

// ---- helpers ----

func (s *Server) ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
