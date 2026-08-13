package components

import (
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"strings"
)

// Pagination describes a server-side paginated listing. BaseURL carries the
// path and any existing query string without the page parameter.
type Pagination struct {
	Current int
	PerPage int
	Total   int
	BaseURL string
}

// Last returns the last page number.
func (p Pagination) Last() int {
	if p.PerPage < 1 {
		p.PerPage = 1
	}
	n := (p.Total + p.PerPage - 1) / p.PerPage
	if n < 1 {
		n = 1
	}
	return n
}

// Prev returns the previous page number (clamped to 1).
func (p Pagination) Prev() int {
	if p.Current <= 1 {
		return 1
	}
	return p.Current - 1
}

// Next returns the next page number (clamped to the last page).
func (p Pagination) Next() int {
	if p.Current >= p.Last() {
		return p.Last()
	}
	return p.Current + 1
}

// Pages returns a window of page numbers around the current page.
func (p Pagination) Pages() []int {
	last := p.Last()
	start := p.Current - 3
	if start < 1 {
		start = 1
	}
	end := start + 6
	if end > last {
		end = last
	}
	if end-start < 6 {
		start = end - 6
		if start < 1 {
			start = 1
		}
	}
	start = max(1, start)
	pages := make([]int, 0, end-start+1)
	for i := start; i <= end; i++ {
		pages = append(pages, i)
	}
	return pages
}

// URLFor builds a page link preserving the existing query string.
func (p Pagination) URLFor(page int) template.URL {
	if p.BaseURL == "" {
		return template.URL("#")
	}
	sep := "?"
	if strings.Contains(p.BaseURL, "?") {
		sep = "&"
	}
	return template.URL(fmt.Sprintf("%s%s%s=%s", p.BaseURL, sep, "page", url.QueryEscape(strconv.Itoa(page))))
}

// Paginate renders the pagination navigation.
func Paginate(p Pagination) (template.HTML, error) {
	if p.Total <= p.PerPage {
		return template.HTML(""), nil
	}
	if p.Current < 1 {
		p.Current = 1
	}
	return execute("pagination", p)
}
