package health

import (
	"encoding/json"
	"net/http"
	"time"
)

// Response is the JSON body returned by liveness and readiness endpoints.
type Response struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

// LiveHandler returns 200 whenever the process is running.
func LiveHandler(w http.ResponseWriter, _ *http.Request) {
	respond(w, "ok")
}

// ReadyHandler returns 200 when the process is ready to accept traffic.
// Extend this to probe database connections, caches, or downstream dependencies.
func ReadyHandler(w http.ResponseWriter, _ *http.Request) {
	respond(w, "ready")
}

func respond(w http.ResponseWriter, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(Response{
		Status: status,
		Time:   time.Now().UTC().Format(time.RFC3339),
	})
}
