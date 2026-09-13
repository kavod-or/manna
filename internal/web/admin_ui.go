package web

import "net/http"

func (s *server) adminRedirect(writer http.ResponseWriter, request *http.Request) {
	http.Redirect(writer, request, "/admin/", http.StatusPermanentRedirect)
}

func (s *server) adminPage(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.page.ExecuteTemplate(writer, "admin.html", nil); err != nil {
		s.logger.Error("render admin page", "error", err)
	}
}

func (s *server) adminAsset(writer http.ResponseWriter, request *http.Request) {
	switch request.PathValue("asset") {
	case "admin.css", "admin.js":
		s.adminStatic.ServeHTTP(writer, request)
	default:
		http.NotFound(writer, request)
	}
}
