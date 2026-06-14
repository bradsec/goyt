package ui

import (
	"embed"
	"log"
	"net/http"

	"goyt/internal/config"
)

//go:embed assets
var Assets embed.FS

//go:embed index.html
var indexHTML []byte

//go:embed login.html
var loginHTML []byte

//go:embed assets/img/favicon.ico
var faviconICO []byte

type TemplateHandler struct {
	config *config.Config
}

func NewTemplateHandler(cfg *config.Config) *TemplateHandler {
	return &TemplateHandler{config: cfg}
}

func (th *TemplateHandler) ServeIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(indexHTML); err != nil {
		log.Printf("Failed to write HTML response: %v", err)
	}
}

// ServeLogin serves the self-contained login page.
func (th *TemplateHandler) ServeLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if _, err := w.Write(loginHTML); err != nil {
		log.Printf("Failed to write login HTML response: %v", err)
	}
}

// ServeFavicon serves the root favicon fallback for browsers and crawlers.
func (th *TemplateHandler) ServeFavicon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/x-icon")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	if _, err := w.Write(faviconICO); err != nil {
		log.Printf("Failed to write favicon response: %v", err)
	}
}
