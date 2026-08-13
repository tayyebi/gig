package components

import (
	"html/template"
	"regexp"
)

var fieldIDPattern = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// FieldID derives a stable, safe HTML id from a form field name.
func FieldID(name string) string {
	return "field-" + fieldIDPattern.ReplaceAllString(name, "-")
}

// ErrorItem is a single validation error with its field anchor.
type ErrorItem struct {
	Field   string
	Message string
	Anchor  string
}

// ErrorSummary renders a form validation error summary. Each error links to
// its field so mobile keyboard users can jump directly to the problem.
func ErrorSummary(errors map[string]string) (template.HTML, error) {
	if len(errors) == 0 {
		return template.HTML(""), nil
	}
	items := make([]ErrorItem, 0, len(errors))
	for field, msg := range errors {
		items = append(items, ErrorItem{Field: field, Message: msg, Anchor: FieldID(field)})
	}
	return execute("errorsummary", items)
}
