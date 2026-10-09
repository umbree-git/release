package backendtest

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/umbree-git/release/internal/manage/backend"
)

type Call struct {
	Op  string
	Key string
	Src string
}

type Store struct {
	mu       sync.Mutex
	bucket   string
	objects  map[string][]byte
	peers    map[string]*Store
	failures map[string]error
	sizes    map[string]int64
	calls    []Call
	OnCall   func(c Call)
}

var (
	registryMu sync.Mutex
	registry   []*Store
)

func New(bucket string) *Store {
	s := &Store{bucket: bucket, objects: map[string][]byte{}, peers: map[string]*Store{}, failures: map[string]error{}, sizes: map[string]int64{}}
	registryMu.Lock()
	registry = append(registry, s)
	registryMu.Unlock()
	return s
}

func All() []*Store {
	registryMu.Lock()
	defer registryMu.Unlock()
	return append([]*Store(nil), registry...)
}

func (s *Store) Link(peers ...*Store) {
	for _, p := range peers {
		s.peers[p.bucket] = p
	}
}

func (s *Store) Bucket() string { return s.bucket }

func (s *Store) Seed(key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), body...)
}

func (s *Store) FailOn(op, key string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[op+" "+key] = err
}

func (s *Store) ReportSize(key string, size int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sizes[key] = size
}

func (s *Store) Body(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	return append([]byte(nil), b...), ok
}

func (s *Store) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Call(nil), s.calls...)
}

func (s *Store) Count(op string) int {
	n := 0
	for _, c := range s.Calls() {
		if c.Op == op {
			n++
		}
	}
	return n
}

func (s *Store) record(c Call) error {
	s.mu.Lock()
	s.calls = append(s.calls, c)
	err := s.failures[c.Op+" "+c.Key]
	hook := s.OnCall
	s.mu.Unlock()
	if hook != nil {
		hook(c)
	}
	return err
}

func (s *Store) Head(ctx context.Context, key string) (int64, error) {
	if err := s.record(Call{Op: "HEAD", Key: key}); err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return 0, fmt.Errorf("fake %s: HEAD %s: %w", s.bucket, key, backend.ErrNotFound)
	}
	if size, ok := s.sizes[key]; ok {
		return size, nil
	}
	return int64(len(b)), nil
}

func (s *Store) Get(ctx context.Context, key string, limit int64) ([]byte, error) {
	if err := s.record(Call{Op: "GET", Key: key}); err != nil {
		return nil, err
	}
	b, ok := s.Body(key)
	if !ok {
		return nil, fmt.Errorf("fake %s: GET %s: %w", s.bucket, key, backend.ErrNotFound)
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("fake %s: GET %s: body exceeds the %d-byte limit", s.bucket, key, limit)
	}
	return b, nil
}

func (s *Store) Put(ctx context.Context, key string, body []byte, contentType string) error {
	if err := s.record(Call{Op: "PUT", Key: key}); err != nil {
		return err
	}
	s.Seed(key, body)
	return nil
}

func (s *Store) Copy(ctx context.Context, srcBucket, srcKey, dstKey string) error {
	if err := s.record(Call{Op: "COPY", Key: dstKey, Src: srcBucket + "/" + srcKey}); err != nil {
		return err
	}
	src, ok := s.peers[srcBucket]
	if !ok {
		return fmt.Errorf("fake %s: COPY from unknown bucket %q", s.bucket, srcBucket)
	}
	body, ok := src.Body(srcKey)
	if !ok {
		return fmt.Errorf("fake %s: COPY %s: %w", s.bucket, srcKey, backend.ErrNotFound)
	}
	s.Seed(dstKey, body)
	return nil
}

func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := s.record(Call{Op: "LIST", Key: prefix}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys []string
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	if err := s.record(Call{Op: "DELETE", Key: key}); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, key)
	return nil
}

var (
	_ backend.Gated  = (*Store)(nil)
	_ backend.Public = (*Store)(nil)
)
