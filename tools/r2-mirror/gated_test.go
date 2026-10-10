package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type recordedPut struct {
	key  string
	body []byte
}

type recordingDoer struct {
	puts   []recordedPut
	failOn string
}

func (d *recordingDoer) Do(req *http.Request) (*http.Response, error) {
	key := strings.TrimPrefix(req.URL.Path, "/")
	_, key, _ = strings.Cut(key, "/")
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	status := http.StatusOK
	if req.Method != http.MethodPut || key == d.failOn {
		status = http.StatusInternalServerError
	} else {
		d.puts = append(d.puts, recordedPut{key: key, body: body})
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
}

func stageFixture(t *testing.T) string {
	t.Helper()
	stage := t.TempDir()
	files := map[string]string{
		"umbree-darwin-arm64.zip": "zip-darwin",
		"umbree-linux-amd64.zip":  "zip-linux-amd64-longer",
		"SHA256SUMS.txt":          "sums",
		"SHA256SUMS.txt.minisig":  "sig",
		"release-notes.md":        "notes",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(stage, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return stage
}

func credsFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "r2.toml")
	if err := os.WriteFile(p, []byte("access_key_id = \"AKID\"\nsecret_access_key = \"SECRET\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func gatedConfig(t *testing.T) config {
	t.Helper()
	return config{
		account:  "acct",
		bucket:   "gated-test",
		stageDir: stageFixture(t),
		comp:     "umbree",
		channel:  "production",
		version:  "0.1.8",
		stamp:    stableStamp,
		creds:    credsFixture(t),
		store:    "gated",
		receipt:  filepath.Join(t.TempDir(), "receipt.json"),
	}
}

func TestGatedPlanHasNoManifest(t *testing.T) {
	keys, err := uploadPlan(config{comp: "umbree", channel: "production", stamp: stableStamp, store: "gated"}, arts)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != len(arts) {
		t.Fatalf("gated plan = %v, want one key per artifact", keys)
	}
	for i, k := range keys {
		if strings.HasSuffix(k, "latest.json") {
			t.Fatalf("gated plan names a manifest: %q", k)
		}
		if want := "umbree/production/" + stableStamp + "/" + arts[i]; k != want {
			t.Fatalf("gated key %d = %q, want %q", i, k, want)
		}
	}
}

func TestGatedUploadPutsArtifactsOnly(t *testing.T) {
	cfg := gatedConfig(t)
	d := &recordingDoer{}
	if err := execute(context.Background(), cfg, io.Discard, d); err != nil {
		t.Fatal(err)
	}
	if len(d.puts) != 4 {
		t.Fatalf("PUT count = %d (%v), want 4 artifacts", len(d.puts), d.puts)
	}
	for _, p := range d.puts {
		if !strings.HasPrefix(p.key, "umbree/production/"+stableStamp+"/") {
			t.Errorf("PUT key %q is outside the gated prefix", p.key)
		}
		if strings.HasSuffix(p.key, "latest.json") {
			t.Errorf("PUT a manifest: %q", p.key)
		}
	}
}

type receiptFile struct {
	Component string `json:"component"`
	Channel   string `json:"channel"`
	Stamp     string `json:"stamp"`
	Version   string `json:"version"`
	Objects   []struct {
		Key    string `json:"key"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"objects"`
}

func TestGatedReceiptMatchesBytes(t *testing.T) {
	cfg := gatedConfig(t)
	d := &recordingDoer{}
	if err := execute(context.Background(), cfg, io.Discard, d); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfg.receipt)
	if err != nil {
		t.Fatalf("no receipt: %v", err)
	}
	var r receiptFile
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("receipt is not JSON: %v\n%s", err, raw)
	}
	if r.Component != "umbree" || r.Channel != "production" || r.Stamp != stableStamp || r.Version != "0.1.8" {
		t.Fatalf("receipt header = %+v", r)
	}
	if len(r.Objects) != len(d.puts) {
		t.Fatalf("receipt lists %d objects, %d were PUT", len(r.Objects), len(d.puts))
	}
	for i, p := range d.puts {
		sum := sha256.Sum256(p.body)
		o := r.Objects[i]
		if o.Key != p.key || o.Size != int64(len(p.body)) || o.SHA256 != hex.EncodeToString(sum[:]) {
			t.Errorf("receipt object %d = %+v, PUT %q (%d bytes, %x)", i, o, p.key, len(p.body), sum)
		}
	}
}

func TestGatedReceiptNotWrittenOnFailure(t *testing.T) {
	cfg := gatedConfig(t)
	d := &recordingDoer{failOn: "umbree/production/" + stableStamp + "/umbree-darwin-arm64.zip"}
	if err := execute(context.Background(), cfg, io.Discard, d); err == nil {
		t.Fatal("a failed PUT was reported as success")
	}
	if _, err := os.Stat(cfg.receipt); !os.IsNotExist(err) {
		t.Fatalf("receipt exists after a failed upload (stat err %v)", err)
	}
}

func TestGatedRequiresBucket(t *testing.T) {
	for _, dry := range []bool{false, true} {
		cfg := gatedConfig(t)
		cfg.bucket, cfg.dryRun = "", dry
		err := cfg.validate()
		if err == nil || !strings.Contains(err.Error(), "--bucket") {
			t.Errorf("dryRun=%v: gated with no bucket: err = %v, want a refusal naming --bucket", dry, err)
		}
	}
	cfg := gatedConfig(t)
	cfg.receipt = ""
	if err := cfg.validate(); err == nil || !strings.Contains(err.Error(), "--receipt") {
		t.Errorf("gated with no receipt: err = %v, want a refusal naming --receipt", err)
	}
}

func TestGatedRefusesChannelAndStamp(t *testing.T) {
	for _, c := range []struct{ channel, stamp, want string }{
		{"stable", stableStamp, `unknown channel "stable"`},
		{"", stableStamp, `unknown channel ""`},
		{"beta", betaStamp, `channel "beta" is held`},
		{"production", betaStamp, `stamp "` + betaStamp + `"`},
	} {
		cfg := gatedConfig(t)
		cfg.channel, cfg.stamp = c.channel, c.stamp
		err := cfg.validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("channel=%q stamp=%q: err = %v, want %q", c.channel, c.stamp, err, c.want)
		}
	}
}

func TestGatedDryRunPrintsKeysAndNoManifest(t *testing.T) {
	cfg := gatedConfig(t)
	cfg.dryRun, cfg.creds, cfg.account = true, "", ""
	var out bytes.Buffer
	d := &recordingDoer{}
	if err := execute(context.Background(), cfg, &out, d); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, f := range []string{"SHA256SUMS.txt", "SHA256SUMS.txt.minisig", "umbree-darwin-arm64.zip", "umbree-linux-amd64.zip"} {
		if !strings.Contains(text, "umbree/production/"+stableStamp+"/"+f) {
			t.Errorf("dry run does not list %s:\n%s", f, text)
		}
	}
	if strings.Contains(text, "latest.json") {
		t.Errorf("dry run names a manifest:\n%s", text)
	}
	if !strings.Contains(text, "no manifest (gated store)") {
		t.Errorf("dry run does not say there is no manifest:\n%s", text)
	}
	if len(d.puts) != 0 {
		t.Errorf("dry run PUT %d objects", len(d.puts))
	}
	if _, err := os.Stat(cfg.receipt); !os.IsNotExist(err) {
		t.Errorf("dry run wrote a receipt (stat err %v)", err)
	}
}

func TestUnknownStoreRefused(t *testing.T) {
	cfg := gatedConfig(t)
	cfg.store = "private"
	err := cfg.validate()
	if err == nil || !strings.Contains(err.Error(), `unknown store "private"`) {
		t.Fatalf("err = %v, want unknown store \"private\"", err)
	}
}

func TestPublicStoreUnchanged(t *testing.T) {
	for _, store := range []string{"", "public"} {
		cfg := config{comp: "umbree", channel: "stable", stamp: stableStamp, store: store}
		keys, err := uploadPlan(cfg, arts)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(keys, plannedKeys(cfg, arts)) {
			t.Fatalf("store %q plan = %v, want the public plan", store, keys)
		}
	}
	cfg := gatedConfig(t)
	cfg.store, cfg.channel, cfg.receipt, cfg.bucket = "public", "stable", "", "umbree-downloads"
	d := &recordingDoer{}
	if err := execute(context.Background(), cfg, io.Discard, d); err != nil {
		t.Fatal(err)
	}
	if len(d.puts) != 5 {
		t.Fatalf("public PUT count = %d, want 4 artifacts + manifest", len(d.puts))
	}
	if last := d.puts[len(d.puts)-1].key; last != "umbree/latest.json" {
		t.Fatalf("public last PUT = %q, want umbree/latest.json", last)
	}
	for _, p := range d.puts[:4] {
		if strings.HasSuffix(p.key, "latest.json") {
			t.Fatalf("manifest PUT before the artifacts: %q", p.key)
		}
	}
}
