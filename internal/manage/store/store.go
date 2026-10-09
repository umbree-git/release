package store

import (
	"errors"
	"time"
)

var (
	ErrNotFound       = errors.New("store: not found")
	ErrDuplicate      = errors.New("store: already catalogued")
	ErrBadState       = errors.New("store: illegal state")
	ErrBadValue       = errors.New("store: value outside the catalog vocabulary")
	ErrLedgerMismatch = errors.New("store: migrations ledger does not match this binary")
	errUnbuilt        = errors.New("store: not built")
)

const DBFile = "catalog.db"

type Store struct{}

type Migration struct {
	Version int
	Name    string
}

type LedgerReport struct {
	Applied []Migration
	Pending []Migration
}

func (r LedgerReport) IsCurrent() bool { return false }

type ReleaseVersion struct {
	ID             int64
	Component      string
	Channel        string
	Version        string
	Stamp          string
	ArtifactsJSON  string
	SumsKey        string
	MinisigKey     string
	State          string
	IsCurrent      bool
	Permanent      bool
	CreatedAt      time.Time
	PromotedAt     time.Time
	YankedAt       time.Time
	ExpiredAt      time.Time
	GatedPrunedAt  time.Time
	PublicPrunedAt time.Time
}

func Open(dataDir string) (*Store, error)                       { return nil, errUnbuilt }
func (s *Store) Close() error                                   { return nil }
func Migrations() []Migration                                   { return nil }
func (s *Store) AppliedMigrations() ([]Migration, error)        { return nil, errUnbuilt }
func CheckLedger(dataDir string) (LedgerReport, error)          { return LedgerReport{}, errUnbuilt }
func (s *Store) IssueNonce(nonce string, c, e time.Time) error  { return errUnbuilt }
func (s *Store) ConsumeNonce(nonce string, now time.Time) error { return errUnbuilt }
func (s *Store) InsertStaged(rv ReleaseVersion) (int64, error)  { return 0, errUnbuilt }
func (s *Store) Transition(id int64, from, to string, at time.Time) error {
	return errUnbuilt
}
func (s *Store) Get(id int64) (*ReleaseVersion, error) { return nil, errUnbuilt }
func (s *Store) ByStamp(component, channel, stamp string) (*ReleaseVersion, error) {
	return nil, errUnbuilt
}
func (s *Store) List(component, channel string, states ...string) ([]ReleaseVersion, error) {
	return nil, errUnbuilt
}
func (s *Store) Newest(component, channel string, states ...string) (*ReleaseVersion, error) {
	return nil, errUnbuilt
}
func Newer(a, b ReleaseVersion) bool        { return false }
func SortNewestFirst(rows []ReleaseVersion) {}
