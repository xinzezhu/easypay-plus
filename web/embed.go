package web

import "embed"

// Assets contains the administration SPA.
//
//go:embed index.html styles.css app.js
var Assets embed.FS
