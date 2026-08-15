package components

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This is the automatable slice of TODO.md's "audit keyboard navigation,
// focus order, labels, and contrast" and "audit screen-reader structure":
// missing alt text and unlabeled form controls are mechanically detectable
// from the markup itself, so — like TestNoScriptTagsOrInlineHandlersInTemplates
// above — this runs on every `go test ./...` instead of relying on a
// one-off manual pass someone has to remember to repeat after every
// template change. Focus order and actual screen-reader behavior still need
// a real audit; this only covers what a static scan can prove.

var (
	imgTagPattern   = regexp.MustCompile(`(?is)<img\b[^>]*>`)
	altAttrPattern  = regexp.MustCompile(`(?is)\salt\s*=\s*"[^"]*"`)
	labelForPattern = regexp.MustCompile(`(?is)<label\b[^>]*\sfor\s*=\s*"([^"]+)"`)
	labelledInput   = regexp.MustCompile(`(?is)<(input|select|textarea)\b([^>]*)>`)
	idAttrPattern   = regexp.MustCompile(`(?is)\sid\s*=\s*"([^"]+)"`)
	typeAttrPattern = regexp.MustCompile(`(?is)\stype\s*=\s*"([^"]+)"`)
)

// unlabeledInputTypes are form controls that either carry no visible
// value needing a label (hidden, submit, button) or are conventionally
// wrapped in their own <label>...</label> rather than referenced via
// label[for] (checkbox, radio) — flagging those would be a false positive
// against this codebase's actual markup style.
var unlabeledInputTypes = map[string]bool{
	"hidden": true, "submit": true, "button": true, "checkbox": true, "radio": true,
}

func TestTemplateImagesHaveAltText(t *testing.T) {
	walkTemplates(t, func(t *testing.T, path, body string) {
		for _, tag := range imgTagPattern.FindAllString(body, -1) {
			if !altAttrPattern.MatchString(tag) {
				t.Errorf("%s: <img> missing an alt attribute: %s", path, tag)
			}
		}
	})
}

func TestTemplateFormControlsHaveLabels(t *testing.T) {
	walkTemplates(t, func(t *testing.T, path, body string) {
		labeled := map[string]bool{}
		for _, m := range labelForPattern.FindAllStringSubmatch(body, -1) {
			labeled[m[1]] = true
		}
		for _, m := range labelledInput.FindAllStringSubmatch(body, -1) {
			attrs := m[2]
			if idm := idAttrPattern.FindStringSubmatch(attrs); idm != nil {
				id := idm[1]
				typ := ""
				if tm := typeAttrPattern.FindStringSubmatch(attrs); tm != nil {
					typ = strings.ToLower(tm[1])
				}
				if unlabeledInputTypes[typ] {
					continue
				}
				if !labeled[id] {
					t.Errorf("%s: <%s id=%q> has no matching <label for=%q>", path, m[1], id, id)
				}
			}
		}
	})
}

// TestHandlerSourceFormControlsHaveLabels covers the admin console's
// hand-built HTML string literals (handlers/admin.go, handlers/payments.go,
// handlers/wallets.go), the same gap TestNoScriptTagsInHandlerSource covers
// for script tags: those pages aren't .tmpl files, so the template walk
// above can't see them.
func TestHandlerSourceFormControlsHaveLabels(t *testing.T) {
	dir := filepath.Join("..", "handlers")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read handlers dir: %v", err)
	}
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
		labeled := map[string]bool{}
		for _, m := range labelForPattern.FindAllStringSubmatch(body, -1) {
			labeled[m[1]] = true
		}
		for _, m := range labelledInput.FindAllStringSubmatch(body, -1) {
			attrs := m[2]
			idm := idAttrPattern.FindStringSubmatch(attrs)
			if idm == nil {
				continue
			}
			id := idm[1]
			// Handler source builds ids with fmt.Sprintf (e.g. "value-%s"),
			// so an id containing a %-verb is a template, not a literal —
			// skip it; its rendered form is covered by the exact-match
			// check once it contains real data, which this static scan
			// cannot simulate.
			if strings.Contains(id, "%") {
				continue
			}
			typ := ""
			if tm := typeAttrPattern.FindStringSubmatch(attrs); tm != nil {
				typ = strings.ToLower(tm[1])
			}
			if unlabeledInputTypes[typ] {
				continue
			}
			if !labeled[id] {
				t.Errorf("%s: <%s id=%q> has no matching <label for=%q>", path, m[1], id, id)
			}
		}
	}
}

// TestLayoutHasSingleLandmarkStructure covers the mechanically-checkable
// slice of "verify semantic element usage": exactly one <main>, <header>,
// and <footer> per rendered page is what lets a screen reader's landmark
// navigation work at all — a duplicate or missing landmark is a real,
// automatable defect, unlike subjective "minimal class usage" style
// preferences, which is why this test exists and a generic class-count
// linter does not.
func TestLayoutHasSingleLandmarkStructure(t *testing.T) {
	html, err := Layout(PageData{Title: "Test page", Body: "<p>hello</p>"})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	out := string(html)
	for _, tag := range []string{"main", "header", "footer"} {
		open := regexp.MustCompile(`(?i)<` + tag + `[\s>]`)
		count := len(open.FindAllString(out, -1))
		if count != 1 {
			t.Errorf("rendered layout has %d <%s> element(s), want exactly 1", count, tag)
		}
	}
}

// TestTemplateTablesHaveHeaderStructure ensures every <table> in a
// template has a <thead> and a <tbody> — a table without them still
// renders visually but loses its exposed row/column header relationships
// for assistive technology.
func TestTemplateTablesHaveHeaderStructure(t *testing.T) {
	walkTemplates(t, func(t *testing.T, path, body string) {
		tableCount := strings.Count(strings.ToLower(body), "<table")
		if tableCount == 0 {
			return
		}
		theadCount := strings.Count(strings.ToLower(body), "<thead")
		tbodyCount := strings.Count(strings.ToLower(body), "<tbody")
		if theadCount < tableCount {
			t.Errorf("%s: %d <table> element(s) but only %d <thead>", path, tableCount, theadCount)
		}
		if tbodyCount < tableCount {
			t.Errorf("%s: %d <table> element(s) but only %d <tbody>", path, tableCount, tbodyCount)
		}
	})
}

func walkTemplates(t *testing.T, check func(t *testing.T, path, body string)) {
	t.Helper()
	entries, err := os.ReadDir("templates")
	if err != nil {
		t.Fatalf("read templates dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tmpl") {
			continue
		}
		path := filepath.Join("templates", e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		check(t, path, string(data))
	}
}
