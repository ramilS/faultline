// Package dashboard exposes the dashboard build embedded in the server binary.
package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed fallback.html dist/*
var embedded embed.FS

// Files returns the dashboard distribution filesystem rooted at dist.
func Files() fs.FS {
	files, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return files
}

// Index returns the dashboard application shell.
func Index() []byte {
	content, err := index(embedded)
	if err != nil {
		panic(err)
	}
	return content
}

func index(files fs.FS) ([]byte, error) {
	content, err := fs.ReadFile(files, "dist/index.html")
	if err == nil {
		return content, nil
	}
	return fs.ReadFile(files, "fallback.html")
}
