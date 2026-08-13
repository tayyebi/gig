package components

import "html/template"

// HomeData supplies the landing page content.
type HomeData struct {
	Categories []CategoryCard
}

// CategoryCard is a browse link for a category.
type CategoryCard struct {
	Name  string
	Slug  string
	Blurb string
}

// Home renders the landing page body.
func Home(d HomeData) (template.HTML, error) {
	return execute("home", d)
}
