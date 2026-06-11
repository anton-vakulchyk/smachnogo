package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// UpdateMe's validation runs before any store access, so a nil-store
// handler exercises every rejection path.
func TestUpdateMeValidation(t *testing.T) {
	h := &Users{}

	cases := []struct {
		name string
		body string
		want int
	}{
		{"not json", "nope", http.StatusBadRequest},
		{"missing limits", `{}`, http.StatusBadRequest},
		{"unknown field", `{"limits":{"steps":10000}}`, http.StatusBadRequest},
		{"zero value", `{"limits":{"calories_kcal":0}}`, http.StatusBadRequest},
		{"negative", `{"limits":{"sugar_g":-5}}`, http.StatusBadRequest},
		{"absurd", `{"limits":{"sodium_mg":1000001}}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("PATCH", "/v1/users/me", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			h.UpdateMe(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d (body %s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}
