package static

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"

	"umbree-release-r2-mirror/layout"
)

const (
	TemplatePath  = "tools/bootstrap.template.sh"
	ModulesDir    = "tools/modules"
	PubkeyPath    = "umbree-release.pub"
	SitePath      = "site/index.html"
	ServedChannel = "stable"
)

var (
	ErrMissingModule     = errors.New("static: @INCLUDE names a module that is not embedded")
	ErrUnexpandedInclude = errors.New("static: an @INCLUDE line survived expansion")
	ErrBadInput          = errors.New("static: refusing to render")
	ErrModuleHeredoc     = errors.New("static: a module has a heredoc, so its comment lines cannot be told from heredoc text")
)

var (
	includeRe      = regexp.MustCompile(`^@INCLUDE:([a-z0-9-]+)@$`)
	moduleHeaderRe = regexp.MustCompile(`^# (module|needs|since):`)
	pubkeyRe       = regexp.MustCompile(`^[A-Za-z0-9+/=]+$`)
	baseRe         = regexp.MustCompile(`^https://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._~/-]*)?$`)
)

type Renderer struct{ assets fs.FS }

func NewRenderer(assets fs.FS) Renderer { return Renderer{assets: assets} }

func records(text []byte) []string {
	if len(text) == 0 {
		return nil
	}
	lines := strings.Split(string(text), "\n")
	if text[len(text)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func ExpandIncludes(template []byte, module func(name string) ([]byte, error)) ([]byte, error) {
	var out bytes.Buffer
	for _, line := range records(template) {
		m := includeRe.FindStringSubmatch(line)
		if m == nil {
			out.WriteString(line + "\n")
			continue
		}
		body, err := module(m[1])
		if err != nil {
			return nil, fmt.Errorf("%w: @INCLUDE:%s@: %v", ErrMissingModule, m[1], err)
		}
		fmt.Fprintf(&out, "# BEGIN %s\n", m[1])
		for _, ml := range records(body) {
			if !moduleHeaderRe.MatchString(ml) {
				out.WriteString(ml + "\n")
			}
		}
		fmt.Fprintf(&out, "# END %s\n", m[1])
	}
	return out.Bytes(), nil
}

func (r Renderer) module(name string) ([]byte, error) {
	return fs.ReadFile(r.assets, ModulesDir+"/"+name+".sh")
}

func (r Renderer) PublicKeyLine() (string, error) {
	body, err := fs.ReadFile(r.assets, PubkeyPath)
	if err != nil {
		return "", err
	}
	key := ""
	for _, line := range records(body) {
		if strings.HasPrefix(line, "untrusted comment:") || strings.TrimSpace(line) == "" {
			continue
		}
		key = line
	}
	if !pubkeyRe.MatchString(key) {
		return "", fmt.Errorf("%w: no public key line in %s", ErrBadInput, PubkeyPath)
	}
	return key, nil
}

func checkInputs(component, floor, downloadsBase string) error {
	if !slices.Contains(layout.Components, component) {
		return fmt.Errorf("%w: unknown component %q", ErrBadInput, component)
	}
	if !layout.StableStampRe.MatchString(floor) {
		return fmt.Errorf("%w: the floor %q is not a stable stamp", ErrBadInput, floor)
	}
	if !baseRe.MatchString(downloadsBase) {
		return fmt.Errorf("%w: the downloads base %q is not a plain https URL", ErrBadInput, downloadsBase)
	}
	return nil
}

func (r Renderer) Bootstrap(component, floor, downloadsBase string) ([]byte, error) {
	if err := checkInputs(component, floor, downloadsBase); err != nil {
		return nil, err
	}
	pubkey, err := r.PublicKeyLine()
	if err != nil {
		return nil, err
	}
	template, err := fs.ReadFile(r.assets, TemplatePath)
	if err != nil {
		return nil, err
	}
	expanded, err := ExpandIncludes(template, r.module)
	if err != nil {
		return nil, err
	}
	out := string(expanded)
	for _, p := range [][2]string{
		{"@COMP@", component}, {"@PUBKEY@", pubkey}, {"@BRAND@", "UMBREE"}, {"@brand@", "umbree"},
		{"@CHANNEL@", ServedChannel}, {"@MIN_VERSION@", floor}, {"@DOWNLOADS_BASE@", downloadsBase}, {"@TEST_SEAM@", ""},
	} {
		out = strings.ReplaceAll(out, p[0], p[1])
	}
	if strings.Contains(out, "@INCLUDE:") {
		return nil, fmt.Errorf("%w in the %s bootstrap", ErrUnexpandedInclude, component)
	}
	return []byte(out), nil
}
