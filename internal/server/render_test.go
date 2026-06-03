package server

import (
	"html/template"
	"io"
	"testing"

	"r2/internal/r2"
)

func tpl(t *testing.T, page string) *template.Template {
	t.Helper()
	return template.Must(template.New("base.html").Funcs(funcMap).ParseFS(tplFS, "templates/base.html", "templates/"+page))
}

func exec(t *testing.T, page string, data any) {
	t.Helper()
	if err := tpl(t, page).ExecuteTemplate(io.Discard, "base.html", data); err != nil {
		t.Fatalf("execute %s: %v", page, err)
	}
}

func TestBucketsTemplate(t *testing.T) {
	exec(t, "buckets.html", bucketsPage{
		base:    base{Buckets: []string{"images", "media"}, Active: "", Title: "buckets"},
		Buckets: []bucketRow{{Name: "images", Created: "Jan 1, 2026"}},
	})
	// empty state
	exec(t, "buckets.html", bucketsPage{base: base{Title: "buckets"}})
}

func TestFilesTemplate(t *testing.T) {
	exec(t, "files.html", filesPage{
		base:    base{Buckets: []string{"images"}, Active: "images", Title: "images"},
		Bucket:  "images",
		Prefix:  "photos/",
		Crumbs:  buildCrumbs("images", "photos/"),
		Folders: []folder{{Name: "2024", URL: "/b/images?p=photos/2024/"}},
		Files: []fileRow{
			{Name: "a.png", Ext: "png", Key: "photos/a.png", URL: "/x", Size: "1.2 MB", Modified: "Jan 1", Cat: classify("a.png")},
			{Name: "notes.txt", Ext: "txt", Key: "photos/notes.txt", URL: "/y", Size: "2 KB", Modified: "Jan 2", Cat: classify("notes.txt")},
		},
		FileCount: 2,
		TotalSize: "1.2 MB",
	})
	// empty bucket
	exec(t, "files.html", filesPage{base: base{Title: "x"}, Bucket: "x", Crumbs: buildCrumbs("x", "")})
}

func TestFileTemplate(t *testing.T) {
	for _, preview := range []string{"image", "pdf", "text", "none"} {
		exec(t, "file.html", filePage{
			base:        base{Buckets: []string{"images"}, Active: "images", Title: "a.png"},
			Bucket:      "images",
			Key:         "photos/a.png",
			Name:        "a.png",
			Crumbs:      append(buildCrumbs("images", "photos/"), crumb{Label: "a.png"}),
			Size:        "1.2 MB",
			Modified:    "Jan 1, 2026",
			ContentType: "image/png",
			Cat:         classify("a.png"),
			Preview:     preview,
			TextBody:    "hello",
			ViewURL:     "/view",
			DownloadURL: "/dl",
			Link:        &r2.Link{URL: "https://media.x.com/a.png", Type: "public"},
		})
	}
	// presigned + nil link branches
	exec(t, "file.html", filePage{
		base: base{Title: "a"}, Bucket: "b", Key: "k", Name: "a.bin", Cat: classify("a.bin"),
		Preview: "none", Link: &r2.Link{URL: "https://x/y", Type: "presigned", Expires: "expires in 7 days"},
	})
	exec(t, "file.html", filePage{base: base{Title: "a"}, Bucket: "b", Key: "k", Name: "a.bin", Cat: catOther, Preview: "none", Link: nil})
}
