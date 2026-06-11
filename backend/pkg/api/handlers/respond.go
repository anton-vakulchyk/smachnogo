// Package handlers implements the REST surface. Error envelope everywhere:
// {"error":{"code":"...","message":"..."}}.
package handlers

import (
	"encoding/json"
	"net/http"

	"go.uber.org/zap"

	"smachnogo/pkg/logging"
)

type errBody struct {
	Error errDetail `json:"error"`
}

type errDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errBody{Error: errDetail{Code: code, Message: msg}})
}

// writePaywall: 402 with the machine-readable reason and remaining count so
// the client picks the right paywall copy without probing-by-scanning.
func writePaywall(w http.ResponseWriter, reason string, remaining int) {
	msg := "subscribe to keep scanning"
	if reason == "window_expired" {
		msg = "your free week ended — your diary is intact; subscribe to keep scanning"
	}
	writeJSON(w, http.StatusPaymentRequired, map[string]any{
		"error": map[string]any{
			"code":            "PAYWALL",
			"message":         msg,
			"reason":          reason,
			"scans_remaining": remaining,
		},
	})
}

func writeInternal(w http.ResponseWriter, r *http.Request, err error, what string) {
	logging.From(r.Context()).Error(what, zap.Error(err))
	writeErr(w, http.StatusInternalServerError, "INTERNAL", "internal error")
}
