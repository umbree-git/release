package manifest

import "time"

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
	return Manifest{}
}

func Key(prefix string) string { return "" }

func (m Manifest) Encode() ([]byte, error) { return nil, nil }
