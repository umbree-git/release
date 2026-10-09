package manifest

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

const (
	FileName    = "latest.json"
	SumsName    = "SHA256SUMS.txt"
	MinisigName = "SHA256SUMS.txt.minisig"
)

type Manifest struct {
	Component  string   `json:"component"`
	Minisig    string   `json:"minisig"`
	Path       string   `json:"path"`
	SHA256Sums string   `json:"sha256sums"`
	Stamp      string   `json:"stamp"`
	Updated    string   `json:"updated"`
	Version    string   `json:"version"`
	Zips       []string `json:"zips"`
}

func Build(component, prefix, version, stamp string, zips []string, updated time.Time) Manifest {
	base := prefix + stamp
	sorted := slices.Clone(zips)
	slices.Sort(sorted)
	return Manifest{
		Component:  component,
		Version:    version,
		Stamp:      stamp,
		Path:       base,
		Zips:       sorted,
		SHA256Sums: base + "/" + SumsName,
		Minisig:    base + "/" + MinisigName,
		Updated:    updated.UTC().Format(time.RFC3339),
	}
}

func Key(prefix string) string { return prefix + FileName }

func (m Manifest) Encode() ([]byte, error) {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", FileName, err)
	}
	return append(body, '\n'), nil
}
