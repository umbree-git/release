package intake

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/umbree-git/release/internal/manage/catalog"
	"github.com/umbree-git/release/internal/manage/store"
	"github.com/umbree-git/release/internal/register"
)

const (
	NonceTTL        = 5 * time.Minute
	NonceLimit      = 30
	NonceWindow     = time.Minute
	MaxBodyBytes    = 1 << 20
	RefusedMessage  = "registration refused"
	RetentionBudget = 15 * time.Second
)

type AfterStage func(ctx context.Context, component, channel string) (string, error)

type Handler struct {
	store      *store.Store
	key        ed25519.PublicKey
	now        func() time.Time
	log        *slog.Logger
	limiter    *windowLimiter
	afterStage AfterStage
	budget     time.Duration
}

func (h *Handler) RetainAfterStage(fn AfterStage, budget time.Duration) {
	if budget <= 0 {
		budget = RetentionBudget
	}
	h.afterStage, h.budget = fn, budget
}

func (h *Handler) retainAfterStage(ctx context.Context, component, channel string) string {
	return ""
	if h.afterStage == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.budget)
	defer cancel()
	type outcome struct {
		summary string
		err     error
	}
	done := make(chan outcome, 1)
	go func() {
		summary, err := h.afterStage(ctx, component, channel)
		done <- outcome{summary, err}
	}()
	select {
	case o := <-done:
		if o.err != nil {
			h.log.Warn("intake: retention after registration failed", "component", component, "channel", channel, "err", o.err)
			return "failed: " + o.err.Error()
		}
		return o.summary
	case <-ctx.Done():
		h.log.Warn("intake: retention after registration did not finish", "component", component, "channel", channel, "budget", h.budget)
		return "did not finish within " + h.budget.String() + "; the nightly pass retries it"
	}
}

func New(st *store.Store, key ed25519.PublicKey, now func() time.Time, log *slog.Logger) *Handler {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Handler{store: st, key: key, now: now, log: log, limiter: &windowLimiter{limit: NonceLimit, window: NonceWindow}}
}

func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/releases/nonce", h.handleNonce)
	mux.HandleFunc("POST /api/v1/releases/register", h.handleRegister)
	mux.HandleFunc("POST /api/v1/releases/status", h.handleStatus)
}

func (h *Handler) handleNonce(w http.ResponseWriter, r *http.Request) {
	now := h.now()
	if !h.limiter.allow(now) {
		h.log.Warn("intake: nonce rate limited", "remote", r.RemoteAddr)
		writeError(w, http.StatusTooManyRequests, "too many nonce requests; retry in a minute")
		return
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		h.log.Error("intake: nonce: generate", "err", err)
		writeError(w, http.StatusInternalServerError, "could not issue a nonce")
		return
	}
	nonce := base64.RawURLEncoding.EncodeToString(raw)
	if err := h.store.IssueNonce(nonce, now, now.Add(NonceTTL)); err != nil {
		h.log.Error("intake: nonce: record", "err", err)
		writeError(w, http.StatusInternalServerError, "could not issue a nonce")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"nonce": nonce, "expires_in": int(NonceTTL.Seconds())})
}

func (h *Handler) handleRegister(w http.ResponseWriter, r *http.Request) {
	env, ok := decodeEnvelope[register.Payload](w, r)
	if !ok {
		return
	}
	p := env.Payload
	if msg, ok := validateRegistration(p); !ok {
		h.log.Warn("intake: register: invalid", "reason", msg, "component", p.Component, "stamp", p.Stamp)
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if !h.authenticate(w, r, "register", p, env.Sig, p.Nonce) {
		return
	}
	artifacts, err := json.Marshal(p.Artifacts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "could not record the artifact list")
		return
	}
	id, err := h.store.InsertStaged(store.ReleaseVersion{
		Component: p.Component, Channel: p.Channel, Version: p.Version, Stamp: p.Stamp,
		ArtifactsJSON: string(artifacts), SumsKey: p.SumsKey, MinisigKey: p.MinisigKey,
		CreatedAt: h.now(),
	})
	if err != nil {
		h.writeInsertError(w, p, err)
		return
	}
	h.log.Info("intake: staged", "id", id, "component", p.Component, "channel", p.Channel, "stamp", p.Stamp)
	status := register.RowStatus{ID: id, State: catalog.StateStaged, Stamp: p.Stamp, Version: p.Version}
	status.Retention = h.retainAfterStage(r.Context(), p.Component, p.Channel)
	writeJSON(w, http.StatusCreated, status)
}

func (h *Handler) writeInsertError(w http.ResponseWriter, p register.Payload, err error) {
	switch {
	case errors.Is(err, store.ErrDuplicate):
		h.log.Warn("intake: register: duplicate", "component", p.Component, "channel", p.Channel, "stamp", p.Stamp)
		writeError(w, http.StatusConflict, p.Component+" "+p.Stamp+" on "+p.Channel+" is already catalogued")
	case errors.Is(err, store.ErrBadValue):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		h.log.Error("intake: register: insert", "err", err)
		writeError(w, http.StatusInternalServerError, "could not record the row")
	}
}

func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	env, ok := decodeEnvelope[register.StatusQuery](w, r)
	if !ok {
		return
	}
	q := env.Payload
	if msg, ok := validateStatusQuery(q); !ok {
		writeError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	if !h.authenticate(w, r, "status", q, env.Sig, q.Nonce) {
		return
	}
	row, err := h.store.ByStamp(q.Component, q.Channel, q.Stamp)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no row for "+q.Component+" "+q.Stamp+" on "+q.Channel)
		return
	}
	if err != nil {
		h.log.Error("intake: status: read", "err", err)
		writeError(w, http.StatusInternalServerError, "could not read the row")
		return
	}
	writeJSON(w, http.StatusOK, register.RowStatus{ID: row.ID, State: row.State, Stamp: row.Stamp, Version: row.Version})
}
