// Package guiweb exposes the embedded assets for glider's web management UI.
package guiweb

import (
	"embed"
	"io/fs"
	"net/http"
)

// Assets contains the complete web interface.
//
//go:embed index.html styles.css app.js
var Assets embed.FS

// FS returns the embedded assets as an fs.FS.
func FS() fs.FS {
	return Assets
}

// Handler returns an HTTP handler that serves the embedded interface.
func Handler() http.Handler {
	return http.FileServer(http.FS(Assets))
}
