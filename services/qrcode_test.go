package services

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestEncodeQRCodeSVGDoesNotPanic(t *testing.T) {
	payload := "ethereum:0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48/transfer?address=0x1234567890abcdef1234567890abcdef12345678&uint256=125000000"
	svg, err := EncodeQRCodeSVG(payload, 4)
	if err != nil {
		t.Fatalf("EncodeQRCodeSVG returned error: %v", err)
	}
	if svg == "" {
		t.Fatal("expected non-empty SVG output")
	}
}

func TestEncodeQRCodeSVGShortPayload(t *testing.T) {
	if _, err := EncodeQRCodeSVG("hello", 4); err != nil {
		t.Fatalf("unexpected error for short payload: %v", err)
	}
}

func TestEncodeQRCodeSVGTooLarge(t *testing.T) {
	huge := strings.Repeat("x", 500)
	if _, err := EncodeQRCodeSVG(huge, 4); err != ErrQRPayloadTooLarge {
		t.Fatalf("expected ErrQRPayloadTooLarge, got %v", err)
	}
}

// svgDoc is just enough structure to confirm the output is well-formed XML
// shaped like an SVG with a set of <rect> elements.
type svgDoc struct {
	XMLName xml.Name  `xml:"svg"`
	Rects   []svgRect `xml:"rect"`
	ViewBox string    `xml:"viewBox,attr"`
}

type svgRect struct {
	X      int `xml:"x,attr"`
	Y      int `xml:"y,attr"`
	Width  int `xml:"width,attr"`
	Height int `xml:"height,attr"`
}

func TestEncodeQRCodeSVGWellFormed(t *testing.T) {
	svg, err := EncodeQRCodeSVG("0xTREASURYADDRESS1234567890 amount=125.00 USDC", 4)
	if err != nil {
		t.Fatalf("EncodeQRCodeSVG error: %v", err)
	}
	var doc svgDoc
	if err := xml.Unmarshal([]byte(svg), &doc); err != nil {
		t.Fatalf("output is not well-formed XML/SVG: %v", err)
	}
	if doc.ViewBox == "" {
		t.Fatal("expected a viewBox attribute")
	}
	// First rect is the white background; the rest are dark modules.
	if len(doc.Rects) < 2 {
		t.Fatalf("expected background rect plus module rects, got %d rects", len(doc.Rects))
	}
}

// TestQRMatrixFinderPatterns spot-checks that the raw module grid (before
// SVG rendering) has valid finder-pattern structure in three corners: a
// solid 7x7 border, a light ring, and a solid 3x3 dark core, per
// ISO/IEC 18004's finder pattern definition.
func TestQRMatrixFinderPatterns(t *testing.T) {
	m, err := encodeQRMatrix([]byte("finder-pattern-check"))
	if err != nil {
		t.Fatalf("encodeQRMatrix error: %v", err)
	}

	checkFinder := func(left, top int) {
		t.Helper()
		for dy := 0; dy < 7; dy++ {
			for dx := 0; dx < 7; dx++ {
				want := dx == 0 || dx == 6 || dy == 0 || dy == 6 ||
					(dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4)
				got := m.modules[top+dy][left+dx]
				if got != want {
					t.Errorf("finder(%d,%d) at offset (%d,%d): got %v want %v", left, top, dx, dy, got, want)
				}
			}
		}
	}

	checkFinder(0, 0)
	checkFinder(m.size-7, 0)
	checkFinder(0, m.size-7)

	// The bottom-right corner is not a finder pattern; it should not
	// accidentally match the same solid-border shape.
	if m.modules[m.size-1][m.size-1] && m.modules[m.size-1][m.size-2] &&
		m.modules[m.size-2][m.size-1] && m.modules[m.size-7][m.size-7] {
		t.Error("bottom-right corner unexpectedly resembles a finder pattern")
	}
}

func TestQRVersionSelection(t *testing.T) {
	v, err := chooseQRVersion(10)
	if err != nil || v.version != 1 {
		t.Fatalf("expected version 1 for a 10-byte payload, got %+v, err=%v", v, err)
	}
	v, err = chooseQRVersion(120)
	if err != nil {
		t.Fatalf("unexpected error for 120-byte payload: %v", err)
	}
	if v.version < 5 {
		t.Fatalf("expected a larger version for 120 bytes, got %+v", v)
	}
}
