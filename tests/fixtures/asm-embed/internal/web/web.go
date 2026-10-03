// Package web serves embedded files.
package web

import "embed"

// A directory pattern leaves out names starting with "_" or ".".
//
//go:embed static
var files embed.FS

//go:embed static/index.html
var index string

// Index returns the embedded index page.
func Index() string { return index }

// Names lists the embedded files, sorted.
func Names() []string {
	entries, err := files.ReadDir("static")
	if err != nil {
		panic(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
