package intake

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/umbree-git/release/internal/register"
)

type signable interface {
	SigningBytes() ([]byte, error)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request, route string, p signable, sig, nonce string) bool {
	msg, err := p.SigningBytes()
	if err != nil {
		h.refuse(w, r, route, "payload does not canonicalise: "+err.Error())
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(sig)
	if err != nil || !ed25519.Verify(h.key, msg, raw) {
		h.refuse(w, r, route, "bad signature")
		return false
	}
	if err := h.store.ConsumeNonce(nonce, h.now()); err != nil {
		h.refuse(w, r, route, "nonce: "+err.Error())
		return false
	}
	return true
}

func (h *Handler) refuse(w http.ResponseWriter, r *http.Request, route, reason string) {
	h.log.Warn("intake: refused", "route", route, "reason", reason, "remote", r.RemoteAddr)
	writeError(w, http.StatusForbidden, RefusedMessage)
}

func decodeEnvelope[T any](w http.ResponseWriter, r *http.Request) (env register.Envelope[T], ok bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read the request body")
		return env, false
	}
	if len(body) > MaxBodyBytes {
		writeError(w, http.StatusUnprocessableEntity, "the request body is over "+strconv.Itoa(MaxBodyBytes)+" bytes")
		return env, false
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "the request body is not a signed envelope: "+err.Error())
		return env, false
	}
	return env, true
}

type windowLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	started time.Time
	count   int
}

func (l *windowLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.started.IsZero() || !now.Before(l.started.Add(l.window)) {
		l.started, l.count = now, 0
	}
	if l.count >= l.limit {
		return false
	}
	l.count++
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
