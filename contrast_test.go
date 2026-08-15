package main

import (
	"bufio"
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// This is the automatable slice of TODO.md's "audit keyboard navigation,
// focus order, labels, and contrast": WCAG 2.1 contrast ratio is a
// mechanical function of two colors, so it doesn't need a real browser or
// a human to check — unlike focus order or screen-reader behavior, which
// still need a real audit. Parses the color custom properties straight out
// of static/app.css so this test breaks if a future palette change
// regresses contrast, rather than relying on someone remembering to
// re-check by eye.

var cssVarPattern = regexp.MustCompile(`^\s*(--color-[a-z-]+)\s*:\s*(#[0-9a-fA-F]{6})\s*;`)

func loadCSSColors(t *testing.T) map[string]string {
	t.Helper()
	data, err := staticFS.ReadFile("static/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	colors := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		if m := cssVarPattern.FindStringSubmatch(scanner.Text()); m != nil {
			colors[m[1]] = m[2]
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan app.css: %v", err)
	}
	return colors
}

// relativeLuminance implements the WCAG 2.1 formula (§1.4.3).
func relativeLuminance(hex string) float64 {
	r, _ := strconv.ParseInt(hex[1:3], 16, 32)
	g, _ := strconv.ParseInt(hex[3:5], 16, 32)
	b, _ := strconv.ParseInt(hex[5:7], 16, 32)
	lin := func(c int64) float64 {
		v := float64(c) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrastRatio implements the WCAG 2.1 formula (§1.4.3): (L1+0.05)/(L2+0.05)
// with L1 the lighter of the two relative luminances.
func contrastRatio(hexA, hexB string) float64 {
	la, lb := relativeLuminance(hexA), relativeLuminance(hexB)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestColorContrastMeetsWCAG_AA checks every foreground/background pair the
// stylesheet actually composites together (text-on-surface, button labels,
// status banners) against WCAG AA: 4.5:1 for normal text, 3:1 for large
// text/UI components (PLAN.md section 6, "Accessibility... contrast").
func TestColorContrastMeetsWCAG_AA(t *testing.T) {
	colors := loadCSSColors(t)
	need := func(name string) string {
		v, ok := colors[name]
		if !ok {
			t.Fatalf("app.css defines no %s custom property", name)
		}
		return v
	}

	cases := []struct {
		name           string
		fg, bg         string
		minRatio       float64
		largeTextOrUI  bool
	}{
		{"body text on page background", need("--color-text"), need("--color-bg"), 4.5, false},
		{"body text on surface", need("--color-text"), need("--color-surface"), 4.5, false},
		{"body text on alt surface", need("--color-text"), need("--color-surface-alt"), 4.5, false},
		{"muted text on page background", need("--color-muted"), need("--color-bg"), 4.5, false},
		{"primary button label", need("--color-on-primary"), need("--color-primary"), 4.5, false},
		{"primary link/accent on page background", need("--color-primary"), need("--color-bg"), 3.0, true},
		{"danger text on danger banner", need("--color-danger"), need("--color-danger-bg"), 4.5, false},
		{"success text on success banner", need("--color-success"), need("--color-success-bg"), 4.5, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ratio := contrastRatio(c.fg, c.bg)
			if ratio < c.minRatio {
				t.Errorf("%s: contrast %.2f:1 (%s on %s), want >= %.1f:1 for WCAG AA", c.name, ratio, c.fg, c.bg, c.minRatio)
			}
		})
	}
}
