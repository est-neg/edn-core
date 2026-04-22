package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

var (
	readinessMu    sync.RWMutex
	readinessCheck func(context.Context) error
)

// Response is the JSON body returned by liveness and readiness endpoints.
type Response struct {
	Status string `json:"status"`
	Time   string `json:"time"`
}

// LiveHandler returns 200 whenever the process is running.
func LiveHandler(w http.ResponseWriter, _ *http.Request) {
	respond(w, http.StatusOK, "ok")
}

// SetReadinessCheck installs the dependency check used by ReadyHandler.
func SetReadinessCheck(check func(context.Context) error) {
	readinessMu.Lock()
	defer readinessMu.Unlock()
	readinessCheck = check
}

// ClearReadinessCheck removes any previously configured dependency check.
func ClearReadinessCheck() {
	SetReadinessCheck(nil)
}

// ReadyHandler returns 200 when the process is ready to accept traffic.
// If a readiness check is configured, it must succeed for the instance to stay ready.
func ReadyHandler(w http.ResponseWriter, r *http.Request) {
	readinessMu.RLock()
	check := readinessCheck
	readinessMu.RUnlock()

	if check != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		if err := check(ctx); err != nil {
			respond(w, http.StatusServiceUnavailable, "not_ready")
			return
		}
	}

	respond(w, http.StatusOK, "ready")
}

func respond(w http.ResponseWriter, code int, status string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(Response{
		Status: status,
		Time:   time.Now().UTC().Format(time.RFC3339),
	})
}
