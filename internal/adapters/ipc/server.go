package ipc

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Diaszano/quota-guardian/internal/application"
	"github.com/Diaszano/quota-guardian/internal/domain"
	"github.com/Diaszano/quota-guardian/internal/ports"
)

type Handler struct {
	service *application.Coordinator
	store   ports.SessionStore
}

func NewHandler(service *application.Coordinator, store ports.SessionStore) http.Handler {
	h := &Handler{service: service, store: store}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, err := strconv.Atoi(strings.TrimPrefix(r.RemoteAddr, "uid:"))
		if err != nil || !authorizedUID(uid) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		h.serve(w, r)
	})
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/health" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
		return
	}
	switch {
	case r.URL.Path == "/v1/sessions" && r.Method == http.MethodPost:
		h.run(w, r)
	case r.URL.Path == "/v1/sessions" && r.Method == http.MethodGet:
		if h.store == nil {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{"unavailable"})
			return
		}
		items, err := h.store.List(r.Context(), ports.SessionFilter{Limit: 100})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, items)
	case strings.HasPrefix(r.URL.Path, "/v1/sessions/"):
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/v1/sessions/"), "/")
		if len(parts) == 1 && r.Method == http.MethodGet {
			item, err := h.store.Get(r.Context(), parts[0])
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
			return
		}
		if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost && h.service != nil {
			if err := h.service.Cancel(r.Context(), parts[0]); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if len(parts) == 2 && parts[1] == "resume" && r.Method == http.MethodPost && h.service != nil {
			var in struct {
				OneShot bool `json:"one_shot"`
			}
			if err := decode(w, r, &in); err != nil {
				writeError(w, err)
				return
			}
			if err := h.service.Resume(r.Context(), parts[0], in.OneShot); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if len(parts) == 2 && parts[1] == "consent" && r.Method == http.MethodPost {
			var in struct {
				Revision int64          `json:"revision"`
				Consent  domain.Consent `json:"consent"`
			}
			if err := decode(w, r, &in); err != nil {
				writeError(w, err)
				return
			}
			item, err := h.store.SetConsent(r.Context(), parts[0], in.Revision, in.Consent)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, item)
			return
		}
		if len(parts) == 2 && parts[1] == "output" && r.Method == http.MethodGet && h.service != nil {
			buffer := h.service.Output(parts[0])
			if buffer == nil {
				writeJSON(w, http.StatusNotFound, errorResponse{"not found"})
				return
			}
			after := uint64(0)
			if raw := r.URL.Query().Get("after"); raw != "" {
				n, err := strconv.ParseUint(raw, 10, 64)
				if err != nil {
					writeJSON(w, http.StatusBadRequest, errorResponse{"invalid request"})
					return
				}
				after = n
			}
			records, dropped := buffer.Snapshot(after)
			writeJSON(w, http.StatusOK, map[string]any{"records": records, "dropped": dropped})
			return
		}
	}
	http.NotFound(w, r)
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SubmissionKey string `json:"submission_key"`
		Provider      string `json:"provider"`
		ContextID     string `json:"context_id"`
		Prompt        string `json:"prompt"`
		AutoResume    bool   `json:"auto_resume,omitempty"`
	}
	if err := decode(w, r, &in); err != nil {
		writeError(w, err)
		return
	}
	if in.SubmissionKey == "" || in.Provider == "" || in.ContextID == "" || in.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{"invalid request"})
		return
	}
	if h.service == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{"unavailable"})
		return
	}
	policy := domain.RetryPolicy{SafetyMargin: 30 * time.Second, BaseDelay: time.Minute, CapDelay: time.Hour, JitterFraction: .1, MaxRetries: 5, Deadline: 7 * 24 * time.Hour}
	consent := domain.Consent{Enabled: true, BackgroundAllowed: in.AutoResume, ApprovedAt: ptrTime(time.Now().UTC())}
	key := in.SubmissionKey
	item, err := h.service.Submit(r.Context(), ports.NewSession{SubmissionKey: &key, Provider: in.Provider, ContextID: in.ContextID, Mode: domain.ModeManaged, RetryPolicy: policy, Consent: consent}, in.Prompt)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, item)
}
func ptrTime(t time.Time) *time.Time { return &t }

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ports.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, ports.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ports.ErrUnsupported):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, ports.ErrBusy):
		status = http.StatusServiceUnavailable
	case errors.Is(err, domain.ErrConsentRequired):
		status = http.StatusConflict
	case strings.Contains(err.Error(), "request body too large"):
		status = http.StatusRequestEntityTooLarge
	case strings.Contains(err.Error(), "unknown field"), strings.Contains(err.Error(), "trailing request"):
		status = http.StatusBadRequest
	}
	message := "operation failed"
	if status == 400 || status == 413 {
		message = "invalid request"
	}
	if status == 409 {
		message = "conflict"
	}
	if status == 422 {
		message = "unsupported"
	}
	if status == 503 {
		message = "unavailable"
	}
	writeJSON(w, status, errorResponse{message})
}
