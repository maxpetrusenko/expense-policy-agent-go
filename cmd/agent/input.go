package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/pipeline"
)

var imageExts = []string{".png", ".jpg", ".jpeg", ".webp", ".gif"}

// collect turns a file or directory into documents. In a directory, name.txt
// and name.png (or .jpg and so on) are paired into one document; an image
// with no text file becomes an image-only document for vision providers.
func collect(path string) ([]pipeline.Document, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return []pipeline.Document{load(path, "")}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	byBase := map[string]*[2]string{} // [text, image]
	var order []string
	for _, n := range names {
		ext := strings.ToLower(filepath.Ext(n))
		base := strings.TrimSuffix(n, filepath.Ext(n))
		slot := byBase[base]
		if slot == nil {
			slot = &[2]string{}
			byBase[base] = slot
			order = append(order, base)
		}
		switch {
		case ext == ".txt":
			slot[0] = filepath.Join(path, n)
		case isImage(ext):
			slot[1] = filepath.Join(path, n)
		}
	}
	var docs []pipeline.Document
	for _, b := range order {
		if s := byBase[b]; s[0] != "" || s[1] != "" {
			docs = append(docs, load(s[0], s[1]))
		}
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no .txt or image files in %s", path)
	}
	return docs, nil
}

func isImage(ext string) bool {
	for _, e := range imageExts {
		if e == ext {
			return true
		}
	}
	return false
}

// load reads the text file (if any). A read error becomes an empty document
// so the pipeline dead-letters it with a classified reason instead of the CLI
// aborting the whole batch.
func load(txt, img string) pipeline.Document {
	src := txt
	if src == "" {
		src = img
	}
	if isImage(strings.ToLower(filepath.Ext(txt))) {
		txt, img = "", txt
	}
	d := pipeline.Document{Source: src, ImagePath: img}
	if txt != "" {
		if b, err := os.ReadFile(txt); err == nil {
			d.Text = string(b)
		}
	}
	return d
}
