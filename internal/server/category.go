package server

import (
	"path"
	"strings"
)

// category describes a file class with a single subtle accent — a small dot,
// the way the website's project cards mark languages. No tiles, no chips.
type category struct {
	Name  string // lowercase label, e.g. "image"
	Group string // image | text | pdf | video | audio | archive | other
	Dot   string // tailwind bg-* for the dot
}

var (
	catImage   = category{"image", "image", "bg-violet-400"}
	catDoc     = category{"doc", "text", "bg-sky-400"}
	catPDF     = category{"pdf", "pdf", "bg-rose-400"}
	catVideo   = category{"video", "video", "bg-fuchsia-400"}
	catAudio   = category{"audio", "audio", "bg-amber-400"}
	catArchive = category{"archive", "archive", "bg-orange-400"}
	catOther   = category{"file", "other", "bg-zinc-300"}
)

var extCat = map[string]category{
	".png": catImage, ".jpg": catImage, ".jpeg": catImage, ".gif": catImage, ".webp": catImage,
	".svg": catImage, ".bmp": catImage, ".ico": catImage, ".avif": catImage, ".heic": catImage,
	".pdf": catPDF,
	".mp4": catVideo, ".mov": catVideo, ".avi": catVideo, ".mkv": catVideo, ".webm": catVideo,
	".mp3": catAudio, ".wav": catAudio, ".flac": catAudio, ".aac": catAudio, ".ogg": catAudio, ".m4a": catAudio,
	".zip": catArchive, ".tar": catArchive, ".gz": catArchive, ".rar": catArchive, ".7z": catArchive,
	".txt": catDoc, ".md": catDoc, ".markdown": catDoc, ".json": catDoc, ".jsonl": catDoc, ".csv": catDoc,
	".tsv": catDoc, ".xml": catDoc, ".yaml": catDoc, ".yml": catDoc, ".toml": catDoc, ".ini": catDoc,
	".log": catDoc, ".html": catDoc, ".htm": catDoc, ".css": catDoc, ".js": catDoc, ".ts": catDoc,
	".tsx": catDoc, ".jsx": catDoc, ".go": catDoc, ".py": catDoc, ".rb": catDoc, ".rs": catDoc,
	".java": catDoc, ".c": catDoc, ".cpp": catDoc, ".h": catDoc, ".sh": catDoc, ".sql": catDoc,
	".doc": catDoc, ".docx": catDoc, ".xls": catDoc, ".xlsx": catDoc, ".ppt": catDoc, ".pptx": catDoc,
}

// textGroupExts are the extensions we can safely render inline as text.
var textGroupExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".json": true, ".jsonl": true, ".csv": true,
	".tsv": true, ".xml": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true,
	".log": true, ".html": true, ".htm": true, ".css": true, ".js": true, ".ts": true,
	".tsx": true, ".jsx": true, ".go": true, ".py": true, ".rb": true, ".rs": true,
	".java": true, ".c": true, ".cpp": true, ".h": true, ".sh": true, ".sql": true,
}

func classify(name string) category {
	ext := strings.ToLower(path.Ext(name))
	c, ok := extCat[ext]
	if !ok {
		return catOther
	}
	// Office docs share catDoc but must not be treated as inline text.
	if c.Group == "text" && !textGroupExts[ext] {
		c.Group = "other"
	}
	return c
}

// ext returns the lowercase extension without the dot, for a tiny mono tag.
func ext(name string) string {
	e := strings.ToLower(path.Ext(name))
	return strings.TrimPrefix(e, ".")
}
