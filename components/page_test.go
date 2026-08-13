package components

import (
	"strings"
	"testing"
)

func TestLayoutSetsLanguageAndDirection(t *testing.T) {
	html, err := Layout(PageData{
		Lang:  "en",
		Dir:   "ltr",
		Title: "Test",
		Body:  "<p>hello</p>",
	})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	out := string(html)
	if !strings.Contains(out, `<html lang="en" dir="ltr">`) {
		t.Errorf("expected lang/dir on html element, got:\n%s", out)
	}
}

func TestLayoutDefaults(t *testing.T) {
	html, err := Layout(PageData{Title: "T"})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if !strings.Contains(string(html), `lang="en"`) {
		t.Errorf("expected default lang=en")
	}
	if !strings.Contains(string(html), `dir="ltr"`) {
		t.Errorf("expected default dir=ltr")
	}
}

func TestLayoutContainsNoScript(t *testing.T) {
	html, err := Layout(PageData{Title: "T", Body: "<form><input name=\"q\"></form>"})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if strings.Contains(string(html), "<script") {
		t.Errorf("layout must never emit script elements")
	}
}

func TestLayoutEscapesUserData(t *testing.T) {
	html, err := Layout(PageData{Title: `<img src=x onerror=alert(1)>`})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if strings.Contains(string(html), "<img") {
		t.Errorf("user data must be HTML-escaped")
	}
}

func TestMetaRefreshWhenRequested(t *testing.T) {
	html, err := Layout(PageData{Title: "T", MetaRefresh: 5})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if !strings.Contains(string(html), `<meta http-equiv="refresh" content="5">`) {
		t.Errorf("expected meta refresh tag")
	}

	plain, err := Layout(PageData{Title: "T"})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if strings.Contains(string(plain), `http-equiv="refresh"`) {
		t.Errorf("meta refresh must only appear when requested")
	}
}

func TestErrorSummaryRendersFieldLinks(t *testing.T) {
	html, err := ErrorSummary(map[string]string{"email": "is invalid"})
	if err != nil {
		t.Fatalf("ErrorSummary: %v", err)
	}
	out := string(html)
	if !strings.Contains(out, "is invalid") {
		t.Errorf("expected error message")
	}
	if !strings.Contains(out, `href="#field-email"`) {
		t.Errorf("expected field link to #field-email, got:\n%s", out)
	}
}

func TestErrorSummaryEmpty(t *testing.T) {
	html, err := ErrorSummary(nil)
	if err != nil {
		t.Fatalf("ErrorSummary: %v", err)
	}
	if string(html) != "" {
		t.Errorf("expected empty output, got %q", html)
	}
}
