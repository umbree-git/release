package static

import (
	"context"
	"errors"
	"io/fs"
)

const (
	TemplatePath = "tools/bootstrap.template.sh"
	ModulesDir   = "tools/modules"
	PubkeyPath   = "umbree-release.pub"
	SitePath     = "site/index.html"
)

var (
	ErrNoDest            = errors.New("static: not built")
	ErrNeedSSHKey        = errors.New("static: not built")
	ErrMissingModule     = errors.New("static: not built")
	ErrUnexpandedInclude = errors.New("static: not built")
	errNotBuilt          = errors.New("static: not built")
)

type Renderer struct{ assets fs.FS }

func NewRenderer(assets fs.FS) Renderer { return Renderer{assets: assets} }

func (r Renderer) Bootstrap(component, floor, downloadsBase string) ([]byte, error) {
	return nil, errNotBuilt
}

func ExpandIncludes(template []byte, module func(string) ([]byte, error)) ([]byte, error) {
	return nil, errNotBuilt
}

func VersionJS(component, version, stamp string) ([]byte, error) { return nil, errNotBuilt }

type Dest struct{ Dir, Host, SSHKey, KnownHosts string }

func (d Dest) Remote() bool { return d.Host != "" }

func ParseDest(dest, sshKey, knownHosts string) (Dest, error) { return Dest{}, errNotBuilt }

type ManifestSource interface {
	Get(ctx context.Context, key string, limit int64) ([]byte, error)
}

type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

type Publisher struct {
	Assets        fs.FS
	Source        ManifestSource
	DownloadsBase string
	Dest          Dest
	Run           Runner
}

func (p *Publisher) Publish(ctx context.Context, component string) (string, error) {
	return "", errNotBuilt
}
