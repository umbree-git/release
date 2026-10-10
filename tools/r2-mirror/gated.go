package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"umbree-release-r2-mirror/layout"
	"umbree-release-r2-mirror/r2"
)

type receiptObject struct {
	Key    string `json:"key"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type gatedReceipt struct {
	Component string          `json:"component"`
	Channel   string          `json:"channel"`
	Stamp     string          `json:"stamp"`
	Version   string          `json:"version"`
	Objects   []receiptObject `json:"objects"`
}

func newReceiptObject(key string, body []byte) receiptObject {
	sum := sha256.Sum256(body)
	return receiptObject{Key: key, Size: int64(len(body)), SHA256: hex.EncodeToString(sum[:])}
}

func (c config) isGated() bool { return c.store == storeGated }

func (c config) validateGated() error {
	if _, err := layout.GatedKey(c.comp, c.channel, c.stamp, sumsName); err != nil {
		return err
	}
	if err := missingFlag("bucket", c.bucket); err != nil {
		return fmt.Errorf("%w: the gated store has no default bucket", err)
	}
	if c.dryRun {
		return nil
	}
	return missingFlag("receipt", c.receipt)
}

func gatedKeys(cfg config, artifacts []string) ([]string, error) {
	keys := make([]string, 0, len(artifacts))
	for _, name := range artifacts {
		key, err := layout.GatedKey(cfg.comp, cfg.channel, cfg.stamp, name)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func stageGated(ctx context.Context, cfg config, out io.Writer, client *r2.Client, artifacts, keys []string) error {
	objects, err := uploadAll(ctx, out, client, cfg.stageDir, artifacts, keys)
	if err != nil {
		return err
	}
	receipt := gatedReceipt{Component: cfg.comp, Channel: cfg.channel, Stamp: cfg.stamp, Version: cfg.version, Objects: objects}
	if err := writeReceipt(cfg.receipt, receipt); err != nil {
		return err
	}
	fmt.Fprintf(out, "✓ staged %s %s to the gated store: %d objects, no manifest; receipt %s\n", cfg.comp, cfg.stamp, len(objects), cfg.receipt)
	return nil
}

func writeReceipt(path string, receipt gatedReceipt) error {
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("encode receipt: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".receipt-*")
	if err != nil {
		return fmt.Errorf("write receipt %q: %w", path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write receipt %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write receipt %q: %w", path, err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return fmt.Errorf("write receipt %q: %w", path, err)
	}
	return os.Rename(tmp.Name(), path)
}
