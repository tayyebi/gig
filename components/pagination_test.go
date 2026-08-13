package components

import (
	"strings"
	"testing"
)

func TestPaginationPagesWindow(t *testing.T) {
	p := Pagination{Current: 5, PerPage: 10, Total: 250}
	want := []int{2, 3, 4, 5, 6, 7, 8}
	pages := p.Pages()
	if len(pages) != len(want) {
		t.Fatalf("Pages() = %v, want %v", pages, want)
	}
	for i := range want {
		if pages[i] != want[i] {
			t.Errorf("Pages()[%d] = %d, want %d", i, pages[i], want[i])
		}
	}
}

func TestPaginationLast(t *testing.T) {
	cases := []struct {
		total int
		per   int
		last  int
	}{
		{0, 10, 1},
		{10, 10, 1},
		{11, 10, 2},
		{250, 10, 25},
	}
	for _, c := range cases {
		if got := (Pagination{PerPage: c.per, Total: c.total}).Last(); got != c.last {
			t.Errorf("Last(%d/%d) = %d, want %d", c.total, c.per, got, c.last)
		}
	}
}

func TestPaginationURLFor(t *testing.T) {
	p := Pagination{BaseURL: "/search?q=design&sort=new"}
	u := string(p.URLFor(3))
	if !strings.Contains(u, "?q=design&sort=new&page=3") {
		t.Errorf("URLFor(3) = %q", u)
	}
}

func TestPaginationHiddenWhenSinglePage(t *testing.T) {
	html, err := Paginate(Pagination{Current: 1, PerPage: 10, Total: 5, BaseURL: "/search"})
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	if string(html) != "" {
		t.Errorf("pagination must be hidden for a single page")
	}
}

func TestPaginateRendersNav(t *testing.T) {
	html, err := Paginate(Pagination{Current: 2, PerPage: 10, Total: 100, BaseURL: "/search"})
	if err != nil {
		t.Fatalf("Paginate: %v", err)
	}
	out := string(html)
	for _, want := range []string{`aria-label="Pagination"`, `rel="prev"`, `rel="next"`, `aria-current="page"`} {
		if !strings.Contains(out, want) {
			t.Errorf("pagination output missing %q:\n%s", want, out)
		}
	}
}
