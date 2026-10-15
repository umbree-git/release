package web

import "net/http"

func (s *Server) versionJSON(component string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := s.currentVersion(component)
		if err != nil {
			s.cfg.Log.Error("version.json", "err", err, "component", component)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(append(body, '\n'))
	}
}
