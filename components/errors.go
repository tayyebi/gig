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

// ErrorSummary renders a form validation error summary. Each error links to
// its field so mobile keyboard users can jump directly to the problem.
func ErrorSummary(errors map[string]string) (template.HTML, error) {
	if len(errors) == 0 {
		return template.HTML(""), nil
	}
	return execute("errorsummary", errors)
}
