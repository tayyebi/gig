package services

import (
	"fmt"
	"strings"
)

// Package qrcode implements a from-scratch QR Code encoder (stdlib only, no
// third-party dependency) so deposit instruction pages can render a
// scannable code as plain inline SVG without any JavaScript (PLAN.md's
// zero-JS constraint). It supports byte-mode encoding at error-correction
// level L across versions 1-6, which comfortably covers the roughly
// 60-120 character payloads this app needs (an EVM address plus amount, or
// an `ethereum:` EIP-681 URI) without the bulk of a full versions-1-40,
// all-ECC-level implementation.

// qrVersionInfo describes the per-version codeword layout for ECC level L.
// Versions 1-5 use a single Reed-Solomon block; version 6 splits into two
// equal blocks (136 data codewords / 2 = 68 each), which is the first
// version where L still needs multiple blocks, so it doubles as a minimal
// interleaving smoke test without requiring the two-group block-size
// bookkeeping bigger versions need.
type qrVersionInfo struct {
	version            int
	totalDataCodewords int
	numBlocks          int
	eccPerBlock        int
}

var qrVersions = []qrVersionInfo{
	{1, 19, 1, 7},
	{2, 34, 1, 10},
	{3, 55, 1, 15},
	{4, 80, 1, 20},
	{5, 108, 1, 26},
	{6, 136, 2, 18},
}

// ErrQRPayloadTooLarge is returned when the payload does not fit in the
// largest supported version (6, ECC level L, byte mode).
var ErrQRPayloadTooLarge = fmt.Errorf("qrcode: payload too large for supported versions")

// qrMatrix is the working grid during encoding: modules holds the module
// colors (true = dark) and isFunction marks cells that are finder/timing/
// alignment/format patterns, which data placement and masking must skip.
type qrMatrix struct {
	size       int
	modules    [][]bool
	isFunction [][]bool
}

func newQRMatrix(size int) *qrMatrix {
	m := &qrMatrix{size: size}
	m.modules = make([][]bool, size)
	m.isFunction = make([][]bool, size)
	for i := range m.modules {
		m.modules[i] = make([]bool, size)
		m.isFunction[i] = make([]bool, size)
	}
	return m
}

func (m *qrMatrix) setFunction(x, y int, dark bool) {
	m.modules[y][x] = dark
	m.isFunction[y][x] = true
}

// EncodeQRCodeSVG encodes payload as a QR code (byte mode, ECC level L,
// smallest version 1-6 that fits) and renders it as a self-contained inline
// SVG string using explicit black/white colors, which stay legible and
// reliably scannable regardless of the page's light/dark theme (unlike the
// CSS custom properties in static/app.css, which are meant for themed UI
// chrome, not a fixed-contrast scannable code). moduleSize is the pixel size
// of one QR module; a 4-module quiet zone border is added per the spec.
func EncodeQRCodeSVG(payload string, moduleSize int) (string, error) {
	if moduleSize <= 0 {
		moduleSize = 4
	}
	m, err := encodeQRMatrix([]byte(payload))
	if err != nil {
		return "", err
	}
	return renderQRSVG(m, moduleSize), nil
}

func encodeQRMatrix(data []byte) (*qrMatrix, error) {
	ver, err := chooseQRVersion(len(data))
	if err != nil {
		return nil, err
	}

	bits := encodeQRDataBits(data, ver)
	codewords := bitsToBytes(bits)
	final := interleaveQRCodewords(codewords, ver)

	size := ver.version*4 + 17
	m := newQRMatrix(size)
	drawFinderPattern(m, 0, 0)
	drawFinderPattern(m, size-7, 0)
	drawFinderPattern(m, 0, size-7)
	drawTimingPatterns(m)
	if ver.version >= 2 {
		pos := ver.version*4 + 10
		drawAlignmentPattern(m, pos, pos)
	}
	m.setFunction(8, size-8, true) // dark module

	dataBits := bytesToBits(final)
	placeQRData(m, dataBits)

	bestMask, bestScore := -1, -1
	var bestModules [][]bool
	for mask := 0; mask < 8; mask++ {
		applyQRMask(m, mask)
		drawFormatInfo(m, mask)
		score := qrPenaltyScore(m)
		if bestMask == -1 || score < bestScore {
			bestScore = score
			bestMask = mask
			bestModules = cloneQRModules(m.modules)
		}
		applyQRMask(m, mask) // undo (masking twice with same pattern is idempotent XOR)
	}
	_ = bestMask
	m.modules = bestModules
	return m, nil
}

func cloneQRModules(src [][]bool) [][]bool {
	out := make([][]bool, len(src))
	for i, row := range src {
		out[i] = append([]bool(nil), row...)
	}
	return out
}

func chooseQRVersion(dataLen int) (qrVersionInfo, error) {
	for _, v := range qrVersions {
		headerBits := 4 + 8 // mode indicator + 8-bit length (valid for versions 1-9)
		if headerBits+dataLen*8 <= v.totalDataCodewords*8 {
			return v, nil
		}
	}
	return qrVersionInfo{}, ErrQRPayloadTooLarge
}

// encodeQRDataBits builds the byte-mode bit stream: mode indicator, 8-bit
// character count, the raw payload bytes, a terminator, bit-padding to a
// byte boundary, and 0xEC/0x11 pad bytes up to the version's capacity.
func encodeQRDataBits(data []byte, ver qrVersionInfo) []bool {
	var bits []bool
	appendBits := func(val uint32, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, (val>>uint(i))&1 != 0)
		}
	}
	appendBits(0b0100, 4) // byte mode
	appendBits(uint32(len(data)), 8)
	for _, b := range data {
		appendBits(uint32(b), 8)
	}

	capacityBits := ver.totalDataCodewords * 8
	for i := 0; i < 4 && len(bits) < capacityBits; i++ {
		bits = append(bits, false)
	}
	for len(bits)%8 != 0 {
		bits = append(bits, false)
	}
	padBytes := []byte{0xEC, 0x11}
	for i := 0; len(bits) < capacityBits; i++ {
		appendBits(uint32(padBytes[i%2]), 8)
	}
	return bits
}

func bitsToBytes(bits []bool) []byte {
	out := make([]byte, len(bits)/8)
	for i := range out {
		var b byte
		for j := 0; j < 8; j++ {
			b <<= 1
			if bits[i*8+j] {
				b |= 1
			}
		}
		out[i] = b
	}
	return out
}

func bytesToBits(data []byte) []bool {
	bits := make([]bool, 0, len(data)*8)
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			bits = append(bits, (b>>uint(i))&1 != 0)
		}
	}
	return bits
}

// interleaveQRCodewords splits data into ver.numBlocks equal blocks, appends
// Reed-Solomon error-correction codewords to each, and interleaves data
// codewords followed by interleaved EC codewords, per the QR spec's
// block-interleaving requirement (needed even at ECC level L once a version
// has more than one block).
func interleaveQRCodewords(data []byte, ver qrVersionInfo) []byte {
	blockSize := ver.totalDataCodewords / ver.numBlocks
	divisor := reedSolomonDivisor(ver.eccPerBlock)

	dataBlocks := make([][]byte, ver.numBlocks)
	eccBlocks := make([][]byte, ver.numBlocks)
	for i := 0; i < ver.numBlocks; i++ {
		block := data[i*blockSize : (i+1)*blockSize]
		dataBlocks[i] = block
		eccBlocks[i] = reedSolomonRemainder(block, divisor)
	}

	var out []byte
	for col := 0; col < blockSize; col++ {
		for _, b := range dataBlocks {
			out = append(out, b[col])
		}
	}
	for col := 0; col < ver.eccPerBlock; col++ {
		for _, b := range eccBlocks {
			out = append(out, b[col])
		}
	}
	return out
}

// --- GF(256) Reed-Solomon, per ISO/IEC 18004 (primitive polynomial 0x11D) ---

var qrGFExp [512]byte
var qrGFLog [256]byte

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		qrGFExp[i] = byte(x)
		qrGFLog[x] = byte(i)
		x <<= 1
		if x >= 256 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		qrGFExp[i] = qrGFExp[i-255]
	}
}

func qrGFMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return qrGFExp[int(qrGFLog[a])+int(qrGFLog[b])]
}

// reedSolomonDivisor computes the generator polynomial of the given degree
// (coefficients highest-degree first) via repeated polynomial
// multiplication by (x - alpha^i), per ISO/IEC 18004 Annex A.
func reedSolomonDivisor(degree int) []byte {
	coeffs := make([]byte, degree)
	coeffs[degree-1] = 1
	root := byte(1)
	for i := 0; i < degree; i++ {
		for j := 0; j < degree; j++ {
			coeffs[j] = qrGFMul(coeffs[j], root)
			if j+1 < degree {
				coeffs[j] ^= coeffs[j+1]
			}
		}
		root = qrGFMul(root, 2)
	}
	return coeffs
}

func reedSolomonRemainder(data []byte, divisor []byte) []byte {
	rem := make([]byte, len(divisor))
	for _, d := range data {
		factor := d ^ rem[0]
		copy(rem, rem[1:])
		rem[len(rem)-1] = 0
		for i, c := range divisor {
			rem[i] ^= qrGFMul(c, factor)
		}
	}
	return rem
}

// --- function pattern drawing ---

func drawFinderPattern(m *qrMatrix, left, top int) {
	for dy := -1; dy <= 7; dy++ {
		for dx := -1; dx <= 7; dx++ {
			x, y := left+dx, top+dy
			if x < 0 || x >= m.size || y < 0 || y >= m.size {
				continue
			}
			dark := dx >= 0 && dx <= 6 && dy >= 0 && dy <= 6 &&
				(dx == 0 || dx == 6 || dy == 0 || dy == 6 || (dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4))
			m.setFunction(x, y, dark)
		}
	}
}

func drawTimingPatterns(m *qrMatrix) {
	for i := 8; i < m.size-8; i++ {
		dark := i%2 == 0
		if !m.isFunction[6][i] {
			m.setFunction(i, 6, dark)
		}
		if !m.isFunction[i][6] {
			m.setFunction(6, i, dark)
		}
	}
}

func drawAlignmentPattern(m *qrMatrix, cx, cy int) {
	for dy := -2; dy <= 2; dy++ {
		for dx := -2; dx <= 2; dx++ {
			dark := dx == -2 || dx == 2 || dy == -2 || dy == 2 || (dx == 0 && dy == 0)
			m.setFunction(cx+dx, cy+dy, dark)
		}
	}
}

// drawFormatInfo draws (and overwrites on each mask trial) the two format
// information copies: ECC level L (indicator 01) and the given mask index,
// protected by the standard BCH(15,5) code and XOR mask, per ISO/IEC 18004
// section 8.9.
func drawFormatInfo(m *qrMatrix, mask int) {
	const eccLIndicator = 0b01
	data := uint32(eccLIndicator<<3 | mask)
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := ((data << 10) | rem) ^ 0x5412
	bits &= 0x7FFF

	getBit := func(i int) bool { return (bits>>uint(i))&1 != 0 }

	for i := 0; i <= 5; i++ {
		m.setFunction(8, i, getBit(i))
	}
	m.setFunction(8, 7, getBit(6))
	m.setFunction(8, 8, getBit(7))
	m.setFunction(7, 8, getBit(8))
	for i := 9; i < 15; i++ {
		m.setFunction(14-i, 8, getBit(i))
	}
	for i := 0; i < 8; i++ {
		m.setFunction(m.size-1-i, 8, getBit(i))
	}
	for i := 8; i < 15; i++ {
		m.setFunction(8, m.size-15+i, getBit(i))
	}
	m.setFunction(8, m.size-8, true)
}

// placeQRData walks the matrix in the standard boustrophedon column pairs
// (bottom to top, then top to bottom), skipping the vertical timing column
// and any function module, placing one data bit per free module.
func placeQRData(m *qrMatrix, bits []bool) {
	i := 0
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < m.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				upward := ((right + 1) & 2) == 0
				var y int
				if upward {
					y = m.size - 1 - vert
				} else {
					y = vert
				}
				if m.isFunction[y][x] {
					continue
				}
				var bit bool
				if i < len(bits) {
					bit = bits[i]
					i++
				}
				m.modules[y][x] = bit
			}
		}
	}
}

func qrMaskBit(mask, x, y int) bool {
	switch mask {
	case 0:
		return (x+y)%2 == 0
	case 1:
		return y%2 == 0
	case 2:
		return x%3 == 0
	case 3:
		return (x+y)%3 == 0
	case 4:
		return (x/3+y/2)%2 == 0
	case 5:
		return (x*y)%2+(x*y)%3 == 0
	case 6:
		return ((x*y)%2+(x*y)%3)%2 == 0
	default:
		return ((x+y)%2+(x*y)%3)%2 == 0
	}
}

func applyQRMask(m *qrMatrix, mask int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if m.isFunction[y][x] {
				continue
			}
			if qrMaskBit(mask, x, y) {
				m.modules[y][x] = !m.modules[y][x]
			}
		}
	}
}

// qrPenaltyScore implements the four ISO/IEC 18004 section 8.8.2 penalty
// rules used to pick the mask pattern with the least visually ambiguous
// result for scanners.
func qrPenaltyScore(m *qrMatrix) int {
	score := 0
	size := m.size

	runPenalty := func(get func(int) bool, n int) int {
		p := 0
		count := 1
		prev := get(0)
		for i := 1; i < n; i++ {
			v := get(i)
			if v == prev {
				count++
			} else {
				if count >= 5 {
					p += 3 + (count - 5)
				}
				count = 1
				prev = v
			}
		}
		if count >= 5 {
			p += 3 + (count - 5)
		}
		return p
	}
	for y := 0; y < size; y++ {
		yy := y
		score += runPenalty(func(x int) bool { return m.modules[yy][x] }, size)
	}
	for x := 0; x < size; x++ {
		xx := x
		score += runPenalty(func(y int) bool { return m.modules[y][xx] }, size)
	}

	for y := 0; y < size-1; y++ {
		for x := 0; x < size-1; x++ {
			v := m.modules[y][x]
			if m.modules[y][x+1] == v && m.modules[y+1][x] == v && m.modules[y+1][x+1] == v {
				score += 3
			}
		}
	}

	isFinderRun := func(get func(int) bool, start, n int) bool {
		pattern := []bool{true, false, true, true, true, false, true}
		before := []bool{false, false, false, false}
		after := []bool{false, false, false, false}
		for i := 0; i < 7; i++ {
			if start+i < 0 || start+i >= n || get(start+i) != pattern[i] {
				return false
			}
		}
		okBefore := true
		for i := 0; i < 4; i++ {
			idx := start - 1 - i
			if idx < 0 || get(idx) != before[i] {
				okBefore = false
				break
			}
		}
		okAfter := true
		for i := 0; i < 4; i++ {
			idx := start + 7 + i
			if idx >= n || get(idx) != after[i] {
				okAfter = false
				break
			}
		}
		return okBefore || okAfter
	}
	for y := 0; y < size; y++ {
		yy := y
		for x := 0; x <= size-7; x++ {
			if isFinderRun(func(i int) bool { return m.modules[yy][i] }, x, size) {
				score += 40
			}
		}
	}
	for x := 0; x < size; x++ {
		xx := x
		for y := 0; y <= size-7; y++ {
			if isFinderRun(func(i int) bool { return m.modules[i][xx] }, y, size) {
				score += 40
			}
		}
	}

	dark := 0
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if m.modules[y][x] {
				dark++
			}
		}
	}
	total := size * size
	percent := dark * 100 / total
	prev5 := percent - percent%5
	next5 := prev5 + 5
	a := prev5 - 50
	if a < 0 {
		a = -a
	}
	b := next5 - 50
	if b < 0 {
		b = -b
	}
	min := a
	if b < min {
		min = b
	}
	score += (min / 5) * 10

	return score
}

// renderQRSVG draws the matrix as plain inline SVG: a white background rect
// plus one <rect> per dark module (no third-party rendering, no JS), with a
// 4-module quiet zone border as required for reliable scanning.
func renderQRSVG(m *qrMatrix, moduleSize int) string {
	const quiet = 4
	dim := (m.size + quiet*2) * moduleSize
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label="QR code for deposit address">`,
		dim, dim, dim, dim)
	fmt.Fprintf(&sb, `<rect x="0" y="0" width="%d" height="%d" fill="#ffffff"/>`, dim, dim)
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if !m.modules[y][x] {
				continue
			}
			px := (x + quiet) * moduleSize
			py := (y + quiet) * moduleSize
			fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%d" height="%d" fill="#000000"/>`, px, py, moduleSize, moduleSize)
		}
	}
	sb.WriteString(`</svg>`)
	return sb.String()
}
