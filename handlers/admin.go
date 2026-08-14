package handlers

import (
	"net/http"

	"github.com/tayyebi/gig/components"
)

// adminHome is a placeholder landing page for the admin console. It exists so
// the "Admin" link shown to admin users resolves to a real, role-gated page
// instead of a 404; the full moderation, dispute, and payment consoles land
// in later phases.
func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) {
	body, err := components.EmptyState("Admin console",
		"The full admin console is not built yet. You are seeing this page because your account holds the admin role.")
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title: "Admin console",
		Body:  body,
	})
}
