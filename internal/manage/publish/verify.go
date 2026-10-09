package publish

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/umbree-git/release/internal/manage/backend"
	"github.com/umbree-git/release/internal/manage/sums"
	"github.com/umbree-git/release/internal/register"
)

func (r *Run) verifyAll(ctx context.Context, st *stream, arts []register.Artifact) error {
	bodies := map[string][]byte{}
	for _, a := range arts {
		body, err := verifyOne(ctx, r.d.Gated, a)
		if err != nil {
			return err
		}
		if a.Key == r.row.SumsKey || a.Key == r.row.MinisigKey {
			bodies[a.Key] = body
		}
		st.send(Event{Step: "verify", Key: a.Key, Bytes: a.Size, Status: "ok"})
	}
	return r.verifySums(bodies[r.row.SumsKey], bodies[r.row.MinisigKey], arts)
}

func verifyOne(ctx context.Context, gated backend.Gated, a register.Artifact) ([]byte, error) {
	if a.Size <= 0 {
		return nil, fmt.Errorf("verify %s: the catalog records %d bytes", a.Key, a.Size)
	}
	size, err := gated.Head(ctx, a.Key)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", a.Key, err)
	}
	if size != a.Size {
		return nil, fmt.Errorf("verify %s: the gated store holds %d bytes, the catalog says %d", a.Key, size, a.Size)
	}
	body, err := gated.Get(ctx, a.Key, a.Size)
	if err != nil {
		return nil, fmt.Errorf("verify %s: %w", a.Key, err)
	}
	if got := sha256Hex(body); got != a.SHA256 {
		return nil, fmt.Errorf("verify %s: sha256 is %s, the catalog says %s", a.Key, got, a.SHA256)
	}
	return body, nil
}

func (r *Run) verifySums(sumsBody, signature []byte, arts []register.Artifact) error {
	if sumsBody == nil || signature == nil {
		return errors.New("verify: the catalog does not list both " + register.SumsName + " and its signature")
	}
	if err := sums.Verify(r.d.Key, sumsBody, signature); err != nil {
		return fmt.Errorf("verify %s: %w", r.row.MinisigKey, err)
	}
	listed, err := sums.Parse(sumsBody)
	if err != nil {
		return fmt.Errorf("verify %s: %w", r.row.SumsKey, err)
	}
	want := map[string]string{}
	for _, a := range arts {
		if strings.HasSuffix(a.Key, ".zip") {
			want[path.Base(a.Key)] = a.SHA256
		}
	}
	if err := sums.Agree(listed, want); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	return nil
}

func (r *Run) copyAll(ctx context.Context, st *stream, arts []register.Artifact) error {
	for _, a := range arts {
		dst := publicKey(r.row.Component, r.row.Stamp, a.Key)
		size, err := r.d.Public.Head(ctx, dst)
		switch {
		case err == nil && size == a.Size:
			st.send(Event{Step: "copy", Key: dst, Bytes: size, Status: "present"})
			continue
		case err != nil && !errors.Is(err, backend.ErrNotFound):
			return fmt.Errorf("copy %s: %w", dst, err)
		}
		if err := r.d.Public.Copy(ctx, r.d.Gated.Bucket(), a.Key, dst); err != nil {
			return fmt.Errorf("copy %s: %w", dst, err)
		}
		size, err = r.d.Public.Head(ctx, dst)
		if err != nil {
			return fmt.Errorf("copy %s: %w", dst, err)
		}
		if size != a.Size {
			return fmt.Errorf("copy %s: the public store holds %d bytes after the copy, want %d", dst, size, a.Size)
		}
		st.send(Event{Step: "copy", Key: dst, Bytes: size, Status: "ok"})
	}
	return nil
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
