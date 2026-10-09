package static

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"umbree-release-r2-mirror/layout"
	"umbree-release-r2-mirror/manifest"
)

const maxManifestBytes = 64 << 10

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

type file struct {
	rel  string
	body []byte
	mode os.FileMode
}

func ExecRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (p *Publisher) Publish(ctx context.Context, component string) (string, error) {
	if p.Dest.Dir == "" {
		return "", ErrNoDest
	}
	m, err := p.manifest(ctx, component)
	if err != nil {
		return "", err
	}
	files, err := p.render(component, m)
	if err != nil {
		return "", err
	}
	if p.Dest.Remote() {
		err = p.copyRemote(ctx, component, files)
	} else {
		err = writeLocal(p.Dest.Dir, files)
	}
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.rel)
	}
	return fmt.Sprintf("republished %s %s to %s: %s", component, m.Stamp, p.Dest, strings.Join(names, " ")), nil
}

func (p *Publisher) manifest(ctx context.Context, component string) (manifest.Manifest, error) {
	key := manifest.Key(component + "/")
	body, err := p.Source.Get(ctx, key, maxManifestBytes)
	if err != nil {
		return manifest.Manifest{}, fmt.Errorf("read the public manifest %s: %w", key, err)
	}
	var m manifest.Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return manifest.Manifest{}, fmt.Errorf("%w: the public manifest %s does not parse: %v", ErrBadInput, key, err)
	}
	if m.Component != component || !layout.StableStampRe.MatchString(m.Stamp) {
		return manifest.Manifest{}, fmt.Errorf("%w: the public manifest %s names %q %q", ErrBadInput, key, m.Component, m.Stamp)
	}
	return m, nil
}

func (p *Publisher) render(component string, m manifest.Manifest) ([]file, error) {
	install, err := NewRenderer(p.Assets).Bootstrap(component, m.Stamp, p.DownloadsBase)
	if err != nil {
		return nil, err
	}
	js, err := VersionJS(component, m.Version, m.Stamp)
	if err != nil {
		return nil, err
	}
	pub, err := fs.ReadFile(p.Assets, PubkeyPath)
	if err != nil {
		return nil, err
	}
	site, err := fs.ReadFile(p.Assets, SitePath)
	if err != nil {
		return nil, err
	}
	return []file{
		{rel: component + "/install.sh", body: install, mode: 0o755},
		{rel: component + "/version.js", body: js, mode: 0o644},
		{rel: "umbree-release.pub", body: pub, mode: 0o644},
		{rel: "index.html", body: site, mode: 0o644},
	}, nil
}

func writeLocal(root string, files []file) error {
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return fmt.Errorf("the static dir %s is not a directory; it is the operator's to create: %v", root, err)
	}
	for _, f := range files {
		if err := writeAtomic(filepath.Join(root, f.rel), f.body, f.mode); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (p *Publisher) copyRemote(ctx context.Context, component string, files []file) error {
	stage, err := os.MkdirTemp("", "umbree-static-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := writeLocal(stage, files); err != nil {
		return err
	}
	batch, err := uploadBatch(stage, p.Dest.Dir, component, files)
	if err != nil {
		return err
	}
	run := p.Run
	if run == nil {
		run = ExecRunner
	}
	args := []string{"-q", "-b", batch, "-i", p.Dest.SSHKey, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + p.Dest.KnownHosts, "-o", "ConnectTimeout=15",
		p.Dest.Host}
	if out, err := run(ctx, "sftp", args...); err != nil {
		return fmt.Errorf("sftp to %s: %v: %s", p.Dest, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func uploadBatch(stage, dir, component string, files []file) (string, error) {
	if strings.ContainsAny(stage, "\"\\\n ") {
		return "", fmt.Errorf("the staging dir %q cannot be named in an sftp batch", stage)
	}
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	suffix := "." + hex.EncodeToString(nonce)
	var puts, renames strings.Builder
	fmt.Fprintf(&puts, "-mkdir %q\n", dir+"/"+component)
	for _, f := range files {
		final := dir + "/" + f.rel
		tmp := path.Join(path.Dir(final), "."+path.Base(final)+suffix)
		fmt.Fprintf(&puts, "put %q %q\n", filepath.Join(stage, f.rel), tmp)
		fmt.Fprintf(&renames, "rename %q %q\n", tmp, final)
	}
	batch := filepath.Join(stage, ".batch")
	return batch, os.WriteFile(batch, []byte(puts.String()+renames.String()), 0o600)
}
