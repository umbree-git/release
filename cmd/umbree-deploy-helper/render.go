//go:build linux

package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var upstreamServer = regexp.MustCompile(`(upstream umbree_manage \{ *server )127\.0\.0\.1:[0-9]+(; *\})`)

func (h *host) template(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(h.path(templateRel), name))
	return string(data), err
}

func (h *host) renderUnit(name string, cfg *config) (string, error) {
	tpl, err := h.template(name)
	if err != nil {
		return "", err
	}
	text := strings.NewReplacer(
		"<SERVICE_USER>", cfg.serviceUser, "<MANAGE_ENV_FILE>", cfg.envFile,
		"<MANAGE_BIN_DIR>", h.binDir(), "<MANAGE_DATA_DIR>", cfg.dataDir,
		"<STATIC_DIR>", cfg.staticDir,
	).Replace(tpl)
	return text, checkUnit(name, text, allowedKeys(name, tpl), cfg.serviceUser)
}

func (h *host) renderVhost(cfg *config) (string, error) {
	text, err := h.template(vhostName)
	if err != nil {
		return "", err
	}
	if !upstreamServer.MatchString(text) {
		return "", fmt.Errorf("%s template has no upstream umbree_manage line", vhostName)
	}
	text = upstreamServer.ReplaceAllString(text, "${1}"+cfg.listen+"${2}")
	text = strings.ReplaceAll(text, "<STATIC_DIR>", cfg.staticDir)
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if m := placeholder.FindString(line); m != "" {
			return "", fmt.Errorf("%s keeps an unfilled placeholder %s", vhostName, m)
		}
	}
	return text, nil
}

func (h *host) renderAll(cfg *config) (map[string]string, error) {
	out := map[string]string{}
	for _, u := range unitNames {
		text, err := h.renderUnit(u, cfg)
		if err != nil {
			return nil, err
		}
		out[u] = text
	}
	text, err := h.renderVhost(cfg)
	if err != nil {
		return nil, err
	}
	out[vhostName] = text
	return out, nil
}

func (h *host) writeChanged(rendered map[string]string) ([]artefact, error) {
	var replaced []artefact
	for _, a := range h.renderedArtefacts() {
		if cur, err := os.ReadFile(a.dest()); err == nil && string(cur) == rendered[a.name] {
			for _, kind := range []string{"prev", "absent"} {
				if err := os.Remove(h.marker(a, kind)); err != nil && !os.IsNotExist(err) {
					return replaced, err
				}
			}
			h.say("%s unchanged", a.dest())
			continue
		}
		replaced = append(replaced, a)
		if err := writeAtomic(a.dir, a.name, []byte(rendered[a.name]), a.mode); err != nil {
			return replaced, fmt.Errorf("write %s: %w", a.dest(), err)
		}
		h.say("%s replaced", a.dest())
	}
	return replaced, nil
}

func render(h *host, cfg *config) error {
	rendered, err := h.renderAll(cfg)
	if err != nil {
		return err
	}
	replaced, err := h.writeChanged(rendered)
	if err == nil {
		err = h.recordChanges(replaced)
	}
	if err != nil {
		return h.renderFailed(replaced, err)
	}
	if len(replaced) == 0 {
		h.ok("rendered output matches the installed units and vhost; nothing written")
		return nil
	}
	if out, err := h.exec("NGINX", "-t"); err != nil {
		return h.renderFailed(replaced, fmt.Errorf("nginx -t: %v: %s", err, tail(out)))
	}
	verify := []string{"verify"}
	for _, u := range unitNames {
		verify = append(verify, filepath.Join(h.unitDir, u))
	}
	if out, err := h.exec("SYSTEMD_ANALYZE", verify...); err != nil {
		return h.renderFailed(replaced, fmt.Errorf("systemd-analyze verify: %v: %s", err, tail(out)))
	}
	h.ok("rendered %d changed file(s)", len(replaced))
	return nil
}

func (h *host) renderFailed(arts []artefact, cause error) error {
	if err := h.restoreFiles(arts); err != nil {
		return fmt.Errorf("%v; and restoring failed: %v", cause, err)
	}
	return fmt.Errorf("%v; the previous units and vhost are back", cause)
}

func tail(out []byte) string {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	return strings.Join(lines, " | ")
}
