package components

import (
	"html/template"
	"time"
)

// User describes the signed-in user for the navigation shell.
type User struct {
	ID          int64
	Name        string
	Email       string
	Role        string
	IsAdmin     bool
	UnreadCount int
}

// Flash is a one-shot message rendered at the top of the page body.
type Flash struct {
	Kind string // "error", "info", "success"
	Text string
}

// PageData carries the data needed to render the full page shell.
type PageData struct {
	Lang        string
	Dir         string
	Title       string
	Description string
	Canonical   string
	MetaRefresh int // seconds; 0 disables the refresh meta tag
	CSRF        string
	Body        template.HTML
	User        *User
	Flash       *Flash
	Year        int
}

// normalize fills in safe defaults so templates never render empty attributes.
func (p *PageData) normalize() {
	if p.Lang == "" {
		p.Lang = "en"
	}
	if p.Dir == "" {
		p.Dir = "ltr"
	}
	if p.Year == 0 {
		p.Year = time.Now().Year()
	}
}

// Layout renders the complete HTML document shell with the page body.
func Layout(p PageData) (template.HTML, error) {
	p.normalize()
	return execute("layout", p)
}

// NotFound renders the 404 body.
func NotFound() (template.HTML, error) {
	return execute("notfound", nil)
}

// EmptyState renders a reusable empty-state section.
func EmptyState(title, body string) (template.HTML, error) {
	return execute("emptystate", map[string]string{"Title": title, "Body": body})
}
