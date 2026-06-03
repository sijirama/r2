package server

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"r2/internal/r2"
)

//go:embed templates/*.html
var tplFS embed.FS

// Server holds dependencies for the HTTP handlers.
type Server struct {
	client *r2.Client
	mux    *http.ServeMux
	tpls   map[string]*template.Template
}

// New wires up routes and parses templates.
func New(client *r2.Client) *Server {
	s := &Server{client: client, mux: http.NewServeMux(), tpls: map[string]*template.Template{}}
	for _, page := range []string{"buckets.html", "files.html", "file.html"} {
		s.tpls[page] = template.Must(
			template.New("base.html").Funcs(funcMap).ParseFS(tplFS, "templates/base.html", "templates/"+page))
	}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /", s.handleBuckets)
	s.mux.HandleFunc("POST /buckets/new", s.handleNewBucket)
	s.mux.HandleFunc("POST /buckets/delete", s.handleDeleteBucket)

	s.mux.HandleFunc("GET /b/{bucket}", s.handleFiles)
	s.mux.HandleFunc("GET /b/{bucket}/file", s.handleFile)
	s.mux.HandleFunc("POST /b/{bucket}/upload", s.handleUpload)
	s.mux.HandleFunc("POST /b/{bucket}/newfile", s.handleNewFile)
	s.mux.HandleFunc("POST /b/{bucket}/newfolder", s.handleNewFolder)
	s.mux.HandleFunc("POST /b/{bucket}/delete", s.handleDeleteObject)
	s.mux.HandleFunc("GET /b/{bucket}/download", s.handleDownload)
	s.mux.HandleFunc("GET /b/{bucket}/view", s.handleView)
}

// ---- shared page scaffolding ----

type base struct {
	Buckets []string // sidebar
	Active  string   // active bucket name
	Title   string
}

func (s *Server) baseData(ctx context.Context, active, title string) base {
	names := []string{}
	if bs, err := s.client.ListBuckets(ctx); err == nil {
		for _, b := range bs {
			names = append(names, b.Name)
		}
		sort.Strings(names)
	}
	return base{Buckets: names, Active: active, Title: title}
}

func (s *Server) render(w http.ResponseWriter, page string, data any) {
	t, ok := s.tpls[page]
	if !ok {
		http.Error(w, "unknown page "+page, 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, "base.html", data); err != nil {
		log.Printf("render %s: %v", page, err)
	}
}

func (s *Server) errPage(w http.ResponseWriter, code int, err error) {
	w.WriteHeader(code)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<div style="font-family:Inter,sans-serif;max-width:600px;margin:80px auto;padding:24px;border:1px solid #fecaca;background:#fef2f2;border-radius:16px;color:#991b1b">
	<h2 style="margin:0 0 8px">Something went wrong</h2><p style="margin:0 0 16px;color:#7f1d1d">%s</p>
	<a href="/" style="color:#4f46e5;text-decoration:none;font-weight:600">← Back to buckets</a></div>`, template.HTMLEscapeString(err.Error()))
}

// ---- buckets overview ----

type bucketRow struct {
	Name    string
	Created string
}

type bucketsPage struct {
	base
	Buckets []bucketRow
}

func (s *Server) handleBuckets(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	ctx := r.Context()
	bs, err := s.client.ListBuckets(ctx)
	if err != nil {
		s.errPage(w, http.StatusBadGateway, err)
		return
	}
	rows := make([]bucketRow, 0, len(bs))
	for _, b := range bs {
		rows = append(rows, bucketRow{Name: b.Name, Created: prettyDate(b.Created)})
	}
	s.render(w, "buckets.html", bucketsPage{
		base:    s.baseData(ctx, "", "buckets"),
		Buckets: rows,
	})
}

func (s *Server) handleNewBucket(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.errPage(w, http.StatusBadRequest, fmt.Errorf("bucket name required"))
		return
	}
	if err := s.client.CreateBucket(r.Context(), name); err != nil {
		s.errPage(w, http.StatusBadGateway, err)
		return
	}
	http.Redirect(w, r, "/b/"+url.PathEscape(name), http.StatusSeeOther)
}

func (s *Server) handleDeleteBucket(w http.ResponseWriter, r *http.Request) {
	name := r.FormValue("bucket")
	if err := s.client.DeleteBucket(r.Context(), name); err != nil {
		s.errPage(w, http.StatusBadGateway, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---- files (bucket) ----

type crumb struct {
	Label string
	URL   string
}

type folder struct {
	Name string
	URL  string
}

type fileRow struct {
	Name     string
	Ext      string
	Key      string
	URL      string
	Size     string
	Modified string
	Cat      category
}

type filesPage struct {
	base
	Bucket    string
	Crumbs    []crumb
	Folders   []folder
	Files     []fileRow
	FileCount int
	TotalSize string
	Prefix    string
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bucket := r.PathValue("bucket")
	prefix := r.URL.Query().Get("p")

	listing, err := s.client.ListObjects(ctx, bucket, prefix)
	if err != nil {
		s.errPage(w, http.StatusBadGateway, err)
		return
	}

	folders := make([]folder, 0, len(listing.Prefixes))
	for _, p := range listing.Prefixes {
		folders = append(folders, folder{
			Name: strings.TrimSuffix(strings.TrimPrefix(p, prefix), "/"),
			URL:  fmt.Sprintf("/b/%s?p=%s", url.PathEscape(bucket), url.QueryEscape(p)),
		})
	}

	files := make([]fileRow, 0, len(listing.Objects))
	var total int64
	for _, o := range listing.Objects {
		name := strings.TrimPrefix(o.Key, prefix)
		files = append(files, fileRow{
			Name:     name,
			Ext:      ext(name),
			Key:      o.Key,
			URL:      fmt.Sprintf("/b/%s/file?k=%s", url.PathEscape(bucket), url.QueryEscape(o.Key)),
			Size:     humanSize(o.Size),
			Modified: prettyDate(o.Modified),
			Cat:      classify(name),
		})
		total += o.Size
	}

	s.render(w, "files.html", filesPage{
		base:      s.baseData(ctx, bucket, bucket),
		Bucket:    bucket,
		Crumbs:    buildCrumbs(bucket, prefix),
		Folders:   folders,
		Files:     files,
		FileCount: len(files),
		TotalSize: humanSize(total),
		Prefix:    prefix,
	})
}

// ---- file detail ----

type filePage struct {
	base
	Bucket      string
	Key         string
	Name        string
	Ext         string
	Crumbs      []crumb
	Size        string
	Modified    string
	ContentType string
	Cat         category
	Preview     string // "image" | "pdf" | "text" | "none"
	TextBody    string
	ViewURL     string
	DownloadURL string
	Link        *r2.Link
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	bucket := r.PathValue("bucket")
	key := r.URL.Query().Get("k")
	if key == "" {
		s.errPage(w, http.StatusBadRequest, fmt.Errorf("missing file key"))
		return
	}

	obj, ct, err := s.client.Head(ctx, bucket, key)
	if err != nil {
		s.errPage(w, http.StatusBadGateway, err)
		return
	}
	cat := classify(key)
	name := key
	if i := strings.LastIndex(key, "/"); i >= 0 {
		name = key[i+1:]
	}

	viewURL := fmt.Sprintf("/b/%s/view?k=%s", url.PathEscape(bucket), url.QueryEscape(key))
	dlURL := fmt.Sprintf("/b/%s/download?k=%s", url.PathEscape(bucket), url.QueryEscape(key))

	preview, textBody := "none", ""
	switch {
	case cat.Group == "image":
		preview = "image"
	case strings.HasSuffix(strings.ToLower(name), ".pdf"):
		preview = "pdf"
	case cat.Group == "text" && obj.Size > 0 && obj.Size < 512*1024:
		if body, _, derr := s.client.Download(ctx, bucket, key); derr == nil {
			defer body.Close()
			if b, rerr := io.ReadAll(io.LimitReader(body, 512*1024)); rerr == nil {
				preview, textBody = "text", string(b)
			}
		}
	}

	// Prefix crumbs based on the key's folder path.
	prefix := ""
	if i := strings.LastIndex(key, "/"); i >= 0 {
		prefix = key[:i+1]
	}
	crumbs := buildCrumbs(bucket, prefix)
	crumbs = append(crumbs, crumb{Label: name})

	link, lerr := s.client.ObjectLink(ctx, bucket, key)
	if lerr != nil {
		log.Printf("file page: link for %q/%q failed: %v", bucket, key, lerr)
	}

	s.render(w, "file.html", filePage{
		base:        s.baseData(ctx, bucket, name),
		Bucket:      bucket,
		Key:         key,
		Name:        name,
		Ext:         ext(name),
		Crumbs:      crumbs,
		Size:        humanSize(obj.Size),
		Modified:    prettyDate(obj.Modified),
		ContentType: ctOr(ct, "application/octet-stream"),
		Cat:         cat,
		Preview:     preview,
		TextBody:    textBody,
		ViewURL:     viewURL,
		DownloadURL: dlURL,
		Link:        link,
	})
}

// ---- object actions ----

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		s.errPage(w, http.StatusBadRequest, err)
		return
	}
	prefix := r.FormValue("prefix")
	for _, fh := range r.MultipartForm.File["file"] {
		f, err := fh.Open()
		if err != nil {
			s.errPage(w, http.StatusBadRequest, err)
			return
		}
		key := strings.TrimPrefix(path.Join(prefix, fh.Filename), "/")
		err = s.client.Upload(r.Context(), bucket, key, f, fh.Header.Get("Content-Type"))
		_ = f.Close()
		if err != nil {
			s.errPage(w, http.StatusBadGateway, err)
			return
		}
	}
	s.redirectFiles(w, r, bucket, prefix)
}

func (s *Server) handleNewFile(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	prefix := r.FormValue("prefix")
	key := strings.TrimPrefix(strings.TrimSpace(prefix+r.FormValue("name")), "/")
	if r.FormValue("name") == "" {
		s.errPage(w, http.StatusBadRequest, fmt.Errorf("file name required"))
		return
	}
	if err := s.client.Upload(r.Context(), bucket, key, strings.NewReader(r.FormValue("content")), "text/plain; charset=utf-8"); err != nil {
		s.errPage(w, http.StatusBadGateway, err)
		return
	}
	s.redirectFiles(w, r, bucket, prefix)
}

func (s *Server) handleNewFolder(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	prefix := r.FormValue("prefix")
	name := strings.Trim(strings.TrimSpace(r.FormValue("name")), "/")
	if name == "" {
		s.errPage(w, http.StatusBadRequest, fmt.Errorf("folder name required"))
		return
	}
	// A folder in R2/S3 is a zero-byte object whose key ends in "/".
	key := strings.TrimPrefix(prefix+name+"/", "/")
	if err := s.client.Upload(r.Context(), bucket, key, strings.NewReader(""), "application/x-directory"); err != nil {
		s.errPage(w, http.StatusBadGateway, err)
		return
	}
	// Drop into the freshly created folder.
	s.redirectFiles(w, r, bucket, key)
}

func (s *Server) handleDeleteObject(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	key := r.FormValue("key")
	prefix := r.FormValue("prefix")
	if err := s.client.DeleteObject(r.Context(), bucket, key); err != nil {
		s.errPage(w, http.StatusBadGateway, err)
		return
	}
	s.redirectFiles(w, r, bucket, prefix)
}

func (s *Server) redirectFiles(w http.ResponseWriter, r *http.Request, bucket, prefix string) {
	u := "/b/" + url.PathEscape(bucket)
	if prefix != "" {
		u += "?p=" + url.QueryEscape(prefix)
	}
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, true)
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	s.stream(w, r, false)
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request, attach bool) {
	bucket := r.PathValue("bucket")
	key := r.URL.Query().Get("k")
	if key == "" {
		http.Error(w, "missing key", http.StatusBadRequest)
		return
	}
	body, ct, err := s.client.Download(r.Context(), bucket, key)
	if err != nil {
		if r2.IsNotFound(err) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", ctOr(ct, "application/octet-stream"))
	if attach {
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", path.Base(key)))
	} else {
		w.Header().Set("Content-Disposition", "inline")
	}
	_, _ = io.Copy(w, body)
}

// ---- helpers ----

func ctOr(ct, fallback string) string {
	if ct == "" {
		return fallback
	}
	return ct
}

func buildCrumbs(bucket, prefix string) []crumb {
	crumbs := []crumb{{Label: bucket, URL: "/b/" + url.PathEscape(bucket)}}
	acc := ""
	for _, seg := range strings.Split(prefix, "/") {
		if seg == "" {
			continue
		}
		acc += seg + "/"
		crumbs = append(crumbs, crumb{
			Label: seg,
			URL:   fmt.Sprintf("/b/%s?p=%s", url.PathEscape(bucket), url.QueryEscape(acc)),
		})
	}
	return crumbs
}

func humanSize(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	f := float64(n)
	i := -1
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

func prettyDate(rfc string) string {
	if rfc == "" {
		return "—"
	}
	t, err := time.Parse(time.RFC3339, rfc)
	if err != nil {
		return rfc
	}
	return t.Local().Format("Jan 2, 2006 · 3:04 PM")
}

var funcMap = template.FuncMap{
	"sub": func(a, b int) int { return a - b },
}
