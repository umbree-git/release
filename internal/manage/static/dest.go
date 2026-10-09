package static

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrNoDest     = errors.New("static: no --static-dest is configured, so nothing republishes the static surface")
	ErrNeedSSHKey = errors.New("static: a remote --static-dest needs --static-ssh-key, a key restricted to the static dir on that host")
	ErrBadDest    = errors.New("static: --static-dest must be an absolute directory or <host>:<absolute directory>")
)

var (
	hostRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._@-]*$`)
	dirRe  = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
)

type Dest struct {
	Dir        string
	Host       string
	SSHKey     string
	KnownHosts string
}

func (d Dest) Remote() bool { return d.Host != "" }

func (d Dest) String() string {
	if d.Remote() {
		return d.Host + ":" + d.Dir
	}
	return d.Dir
}

func cleanAbsolute(p string) bool { return dirRe.MatchString(p) && filepath.Clean(p) == p }

func ParseDest(dest, sshKey, knownHosts string) (Dest, error) {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return Dest{}, ErrNoDest
	}
	if strings.HasPrefix(dest, "/") {
		if !cleanAbsolute(dest) {
			return Dest{}, fmt.Errorf("%w (got %q)", ErrBadDest, dest)
		}
		return Dest{Dir: dest}, nil
	}
	host, dir, ok := strings.Cut(dest, ":")
	if !ok || !hostRe.MatchString(host) || !cleanAbsolute(dir) {
		return Dest{}, fmt.Errorf("%w (got %q)", ErrBadDest, dest)
	}
	if strings.TrimSpace(sshKey) == "" {
		return Dest{}, ErrNeedSSHKey
	}
	if !cleanAbsolute(sshKey) || !cleanAbsolute(knownHosts) {
		return Dest{}, fmt.Errorf("%w: the ssh key %q and the known-hosts file %q must be clean absolute paths", ErrBadDest, sshKey, knownHosts)
	}
	return Dest{Dir: dir, Host: host, SSHKey: sshKey, KnownHosts: knownHosts}, nil
}
