package httpapi

import (
	"embed"
	"net/http"
)

//go:embed dashboard.html
var dashboardFiles embed.FS

func dashboard(w http.ResponseWriter, _ *http.Request) {
	data, err := dashboardFiles.ReadFile("dashboard.html")
	if err != nil {
		http.Error(w, "dashboard unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
