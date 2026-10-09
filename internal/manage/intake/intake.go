package intake

import (
	"crypto/ed25519"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/umbree-git/release/internal/manage/store"
)

const (
	NonceTTL       = 5 * time.Minute
	NonceLimit     = 30
	NonceWindow    = time.Minute
	MaxBodyBytes   = 1 << 20
	RefusedMessage = "registration refused"
)

type Handler struct{}

func New(st *store.Store, key ed25519.PublicKey, now func() time.Time, log *slog.Logger) *Handler {
	return &Handler{}
}

func (h *Handler) Routes(mux *http.ServeMux) {}

func ReleaseKey() (ed25519.PublicKey, error) { return nil, errors.New("intake: not built") }
