// Package components renders HTML server-side from html/template fragments.
// Components are plain functions that take data and return template.HTML.
// No component queries the database directly.
package components

import (
	"bytes"
	"embed"
	"html/template"
)

//go:embed templates/*.tmpl
var tmplFS embed.FS

var base = template.Must(template.New("base").Funcs(template.FuncMap{
	"fieldid": FieldID,
}).ParseFS(tmplFS, "templates/*.tmpl"))

// execute renders the named template fragment with the given data.
func execute(name string, data any) (template.HTML, error) {
	var buf bytes.Buffer
	if err := base.ExecuteTemplate(&buf, name, data); err != nil {
		return "", err
	}
	return template.HTML(buf.String()), nil
}
