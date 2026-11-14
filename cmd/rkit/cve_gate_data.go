package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	cveGateFile      = "cve-gate.tsv"
	cveGateHeader    = "component\tkey\tvalue"
	gateKeySource    = "source"
	gateKeyGowork    = "gowork"
	gateKeyGo        = "go"
	gateKeyWaivers   = "waivers"
	gateKeyManifest  = "js-manifest"
	gateKeyVendorDir = "vendor-dir"
	goworkOff        = "off"
)

var gateArgKeys = []string{gateKeyGowork, gateKeyGo, gateKeyManifest, "js-root", gateKeyVendorDir, gateKeyWaivers}

type gateRow struct{ key, value string }

type gateEntry struct {
	source string
	rows   []gateRow
}

type cveGateData struct {
	order   []string
	entries map[string]gateEntry
}

func loadCVEGateData(repoDir string) (cveGateData, error) {
	f, err := os.Open(filepath.Join(repoDir, cveGateFile))
	if err != nil {
		return cveGateData{}, err
	}
	defer f.Close()
	return parseCVEGateData(f)
}

func parseCVEGateData(r io.Reader) (cveGateData, error) {
	d := cveGateData{entries: map[string]gateEntry{}}
	raw, err := io.ReadAll(r)
	if err != nil {
		return d, err
	}
	if bytes.ContainsRune(raw, '\r') {
		return d, fmt.Errorf("%s: carriage return refused (LF line endings only)", cveGateFile)
	}
	sc := bufio.NewScanner(bytes.NewReader(raw))
	seenHeader := false
	for n := 1; sc.Scan(); n++ {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !seenHeader {
			if line != cveGateHeader {
				return d, fmt.Errorf("%s:%d: header must be %q", cveGateFile, n, cveGateHeader)
			}
			seenHeader = true
			continue
		}
		if err := d.addRow(line); err != nil {
			return d, fmt.Errorf("%s:%d: %w", cveGateFile, n, err)
		}
	}
	if err := sc.Err(); err != nil {
		return d, err
	}
	if !seenHeader {
		return d, fmt.Errorf("%s: no header %q", cveGateFile, cveGateHeader)
	}
	return d, d.checkSources()
}

func (d *cveGateData) addRow(line string) error {
	fields := strings.Split(line, "\t")
	if len(fields) != 3 || slices.Contains(fields, "") {
		return fmt.Errorf("row without three fields (component, key, value): %q", line)
	}
	comp, key, value := fields[0], fields[1], fields[2]
	if err := checkGatePath(key, value); err != nil {
		return err
	}
	e, known := d.entries[comp]
	if !known {
		d.order = append(d.order, comp)
	}
	if err := e.add(key, value); err != nil {
		return fmt.Errorf("%s: %w", comp, err)
	}
	d.entries[comp] = e
	return nil
}

func checkGatePath(key, value string) error {
	if key != gateKeySource && !slices.Contains(gateArgKeys, key) {
		return fmt.Errorf("unknown key %q", key)
	}
	if path.IsAbs(value) || filepath.IsAbs(value) || strings.HasPrefix(value, `\`) {
		return fmt.Errorf("%s: absolute path %q refused", key, value)
	}
	for _, seg := range strings.FieldsFunc(value, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return fmt.Errorf("%s: path %q leaves its root (..)", key, value)
		}
	}
	return nil
}

func (e *gateEntry) add(key, value string) error {
	switch key {
	case gateKeySource:
		if e.source != "" {
			return fmt.Errorf("more than one source")
		}
		e.source = value
		return nil
	case gateKeyWaivers:
		if e.has(gateKeyWaivers) {
			return fmt.Errorf("more than one waivers")
		}
	case gateKeyVendorDir:
		if !e.has(gateKeyManifest) {
			return fmt.Errorf("vendor-dir before any js-manifest")
		}
	}
	e.rows = append(e.rows, gateRow{key: key, value: value})
	return nil
}

func (e gateEntry) has(key string) bool {
	return slices.ContainsFunc(e.rows, func(r gateRow) bool { return r.key == key })
}

func (d cveGateData) checkSources() error {
	for _, comp := range d.order {
		if d.entries[comp].source == "" {
			return fmt.Errorf("%s: %s has no source row", cveGateFile, comp)
		}
	}
	return nil
}

func (d cveGateData) checkComponents(want []string) error {
	got, need := slices.Sorted(slices.Values(d.order)), slices.Sorted(slices.Values(want))
	if !slices.Equal(got, need) {
		return fmt.Errorf("%s names components %s, rkit components lists %s",
			cveGateFile, strings.Join(got, ","), strings.Join(need, ","))
	}
	return nil
}

func (e gateEntry) args(repoDir, srcDir string) []string {
	var out []string
	for _, r := range e.rows {
		out = append(out, "--"+r.key, r.resolve(repoDir, srcDir))
	}
	return out
}

func (r gateRow) resolve(repoDir, srcDir string) string {
	switch {
	case r.key == gateKeyGowork && r.value == goworkOff:
		return goworkOff
	case r.key == gateKeyWaivers:
		return filepath.Join(repoDir, r.value)
	}
	return filepath.Join(srcDir, r.value)
}

func (e gateEntry) checkGoModules(srcDir string) error {
	found, err := goModuleDirs(srcDir)
	if err != nil {
		return err
	}
	var listed []string
	for _, r := range e.rows {
		if r.key == gateKeyGo {
			listed = append(listed, path.Clean(filepath.ToSlash(r.value)))
		}
	}
	for _, dir := range found {
		if !slices.Contains(listed, dir) {
			return fmt.Errorf("%s: go.mod at %s is not listed in %s", srcDir, dir, cveGateFile)
		}
	}
	for _, dir := range listed {
		if !slices.Contains(found, dir) {
			return fmt.Errorf("%s lists go %s, but %s has no go.mod there", cveGateFile, dir, srcDir)
		}
	}
	return nil
}

func goModuleDirs(srcDir string) ([]string, error) {
	var dirs []string
	err := filepath.WalkDir(srcDir, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() && p != srcDir && isGoIgnoredDir(de.Name()) {
			return filepath.SkipDir
		}
		if !de.IsDir() && de.Name() == "go.mod" {
			rel, err := filepath.Rel(srcDir, filepath.Dir(p))
			if err != nil {
				return err
			}
			dirs = append(dirs, filepath.ToSlash(rel))
		}
		return nil
	})
	return dirs, err
}

func isGoIgnoredDir(name string) bool {
	return name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}
