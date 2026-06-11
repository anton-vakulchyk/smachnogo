package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"

	"smachnogo/pkg/api/middleware"
	"smachnogo/pkg/applesign"
	"smachnogo/pkg/awsx"
	"smachnogo/pkg/config"
	"smachnogo/pkg/logging"
	"smachnogo/pkg/store"
)

// Apple: Sign in with Apple linking & recovery (M8). One endpoint, three
// outcomes — the client just says "this is me" and the server does the
// right thing:
//   - sub unlinked            → link it to the current user (backup)
//   - sub linked to current   → idempotent no-op
//   - sub linked to another   → RECOVER: copy that diary here, transfer the
//     subscription, repoint the link, delete the old account
type Apple struct {
	Cfg      *config.Config
	Store    *store.Store
	S3       *awsx.S3
	Cognito  *awsx.Cognito // nil in static-auth local dev — old-user deletion skipped
	Verifier applesign.Verifier
}

type appleReq struct {
	IdentityToken string `json:"identity_token"`
	Nonce         string `json:"nonce"` // raw value; token carries its SHA256
}

func (h *Apple) Link(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserID(r.Context())
	log := logging.From(r.Context())

	var req appleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IdentityToken == "" {
		writeErr(w, http.StatusBadRequest, "BAD_REQUEST", "identity_token required")
		return
	}

	id, err := h.Verifier.Verify(r.Context(), req.IdentityToken)
	if err != nil {
		log.Warn("apple token rejected", zap.Error(err))
		writeErr(w, http.StatusUnauthorized, "INVALID_APPLE_TOKEN", "Apple sign-in could not be verified")
		return
	}
	if !applesign.NonceMatches(id.Nonce, req.Nonce) {
		writeErr(w, http.StatusUnauthorized, "INVALID_APPLE_TOKEN", "nonce mismatch")
		return
	}

	profile, err := h.Store.GetProfile(r.Context(), userID)
	if err != nil {
		writeInternal(w, r, err, "read profile")
		return
	}
	// One Apple ID per diary: a user already backed up under a different
	// Apple ID must not silently re-home (sign out / delete account first).
	if profile.AppleSub != "" && profile.AppleSub != id.Sub {
		writeErr(w, http.StatusConflict, "APPLE_MISMATCH", "this diary is already backed up with a different Apple ID")
		return
	}

	owner, err := h.Store.GetAppleLink(r.Context(), id.Sub)
	if err != nil {
		writeInternal(w, r, err, "resolve apple link")
		return
	}
	now := time.Now().UTC()

	switch owner {
	case "":
		if err := h.Store.PutAppleLink(r.Context(), id.Sub, userID, now); err != nil {
			writeInternal(w, r, err, "create apple link")
			return
		}
		log.Info("apple link created")
		writeJSON(w, http.StatusOK, map[string]any{"status": "linked"})

	case userID:
		writeJSON(w, http.StatusOK, map[string]any{"status": "already_linked"})

	default:
		h.recover(w, r, owner, userID, id.Sub, now)
	}
}

// recover moves the diary owned by oldUser to newUser. Ordering matters:
// copy (idempotent) → transfer subscription → repoint link → best-effort
// delete of the old account LAST. A failure before the repoint leaves the
// link on the old user; the retry re-runs the same idempotent steps.
func (h *Apple) recover(w http.ResponseWriter, r *http.Request, oldUser, newUser, appleSub string, now time.Time) {
	log := logging.From(r.Context()).With(zap.String("recover_from", oldUser))

	oldProfile, err := h.Store.GetProfile(r.Context(), oldUser)
	if err != nil {
		writeInternal(w, r, err, "read old profile")
		return
	}
	copied, err := h.Store.CopyUserData(r.Context(), oldUser, newUser)
	if err != nil {
		writeInternal(w, r, err, "copy user data")
		return
	}
	// The copied profile carries the entitlement; the TXN owner item must
	// follow so future webhooks attribute to the new user.
	if oldProfile.OriginalTransactionID != "" {
		if _, err := h.Store.ClaimTransaction(r.Context(), oldProfile.OriginalTransactionID, newUser, now); err != nil {
			writeInternal(w, r, err, "transfer subscription")
			return
		}
	}
	if err := h.Store.PutAppleLink(r.Context(), appleSub, newUser, now); err != nil {
		writeInternal(w, r, err, "repoint apple link")
		return
	}

	// Old account cleanup. Best-effort: the diary is safe (copied) and the
	// link is repointed — a failure here only leaves an orphaned partition
	// (the old device's app self-heals: UserNotFound → fresh identity).
	if _, err := h.Store.DeleteUserData(r.Context(), oldUser); err != nil {
		log.Error("old partition cleanup failed", zap.Error(err))
	}
	if _, err := h.S3.DeletePrefix(r.Context(), "scans/"+oldUser+"/"); err != nil {
		log.Error("old photos cleanup failed", zap.Error(err))
	}
	if h.Cognito != nil {
		if err := h.Cognito.DeleteUserBySub(r.Context(), oldUser); err != nil {
			log.Error("old cognito user cleanup failed", zap.Error(err))
		}
	}

	log.Info("diary recovered", zap.Int("items_copied", copied))
	writeJSON(w, http.StatusOK, map[string]any{"status": "recovered", "items_copied": copied})
}
