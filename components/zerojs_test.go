package components

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoScriptTagsOrInlineHandlersInTemplates is a repeatable, durable
// substitute for the manual "run every journey with JavaScript disabled"
// pass PLAN.md section 18/21 and TODO.md Phase 8 call for: it walks every
// .tmpl source file (the only place markup is authored — components/*.go
// only composes and escapes data into these fragments) and fails the build
// if any forbidden pattern appears. This runs on every `go test ./...`,
// unlike a one-off manual browser check.
func TestNoScriptTagsOrInlineHandlersInTemplates(t *testing.T) {
	root := "templates"
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read templates dir: %v", err)
	}

	// Matches on{event}="..." / on{event}='...' attributes (onclick,
	// onload, onsubmit, ...), case-insensitively, without needing an
	// exhaustive event-name list.
	inlineHandler := regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tmpl") {
			continue
		}
		path := filepath.Join(root, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(data)
		lower := strings.ToLower(body)

		if strings.Contains(lower, "<script") {
			t.Errorf("%s: contains a <script> tag; the app ships zero JavaScript (PLAN.md hard constraint)", path)
		}
		if strings.Contains(lower, "javascript:") {
			t.Errorf("%s: contains a javascript: URI", path)
		}
		if loc := inlineHandler.FindString(body); loc != "" {
			t.Errorf("%s: contains an inline event handler attribute (%q)", path, strings.TrimSpace(loc))
		}
		if strings.Contains(lower, "<iframe") {
			t.Errorf("%s: contains an <iframe>; no third-party widgets are permitted", path)
		}
	}
}

// TestNoScriptTagsInHandlerSource covers the admin console's hand-built HTML
// fragments (handlers/admin.go, handlers/payments.go, handlers/payouts.go
// via handlers/payments.go): those pages are assembled as raw Go string
// literals rather than .tmpl files, so TestNoScriptTagsOrInlineHandlersInTemplates
// above cannot see them. This walks ../handlers/*.go (excluding tests) for
// the same forbidden patterns.
func TestNoScriptTagsInHandlerSource(t *testing.T) {
	dir := filepath.Join("..", "handlers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read handlers dir: %v", err)
	}
	inlineHandler := regexp.MustCompile(`(?i)\son[a-z]+\s*=\s*["']`)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		body := string(data)
		lower := strings.ToLower(body)
		if strings.Contains(lower, "<script") {
			t.Errorf("%s: contains a <script> tag in a Go string literal", path)
		}
		if loc := inlineHandler.FindString(body); loc != "" {
			t.Errorf("%s: contains an inline event handler attribute (%q)", path, strings.TrimSpace(loc))
		}
	}
}

// TestLayoutRendersLangDirAndNoScript renders the actual layout component
// (not just its source) and checks the composed output — belt-and-braces
// against a future layout change that assembles markup outside layout.tmpl.
func TestLayoutRendersLangDirAndNoScript(t *testing.T) {
	html, err := Layout(PageData{Title: "Test page", Body: "<p>hello</p>"})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	out := string(html)

	if !strings.Contains(out, `<html lang="en" dir="ltr">`) {
		t.Errorf("rendered layout missing <html lang dir>, got prefix: %.200s", out)
	}
	if strings.Contains(strings.ToLower(out), "<script") {
		t.Errorf("rendered layout contains a <script> tag")
	}
}

// TestMetaRefreshOnlyWhenRequested confirms the layout's only auto-refresh
// mechanism is the server-controlled <meta http-equiv="refresh"> tag
// (PLAN.md section 6: "For status pages that should auto-refresh ...
// use <meta http-equiv=\"refresh\">, not JavaScript"), and that it is
// absent unless a page explicitly opts in via PageData.MetaRefresh.
func TestMetaRefreshOnlyWhenRequested(t *testing.T) {
	without, err := Layout(PageData{Title: "No refresh", Body: "<p>ok</p>"})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if strings.Contains(string(without), "http-equiv=\"refresh\"") {
		t.Errorf("layout without MetaRefresh set should not emit a refresh meta tag")
	}

	with, err := Layout(PageData{Title: "Refreshing", Body: "<p>ok</p>", MetaRefresh: 5})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if !strings.Contains(string(with), `<meta http-equiv="refresh" content="5">`) {
		t.Errorf("layout with MetaRefresh=5 should emit the refresh meta tag, got: %.300s", with)
	}
}
