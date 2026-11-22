// Package store holds the configured pack sizes so they can be changed at
// runtime without code changes, optionally persisting them to a JSON file.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"pack-calculator/internal/packing"
)

// PackStore is a concurrency-safe holder for the current pack sizes.
type PackStore struct {
	mu    sync.RWMutex
	sizes []int
	path  string // optional persistence file; empty means in-memory only
}

type fileFormat struct {
	PackSizes []int `json:"packSizes"`
}

// New creates a store. If path is set and the file exists, the sizes saved
// there take precedence over defaults; otherwise defaults are used.
func New(defaults []int, path string) (*PackStore, error) {
	sizes := defaults
	if path != "" {
		saved, err := load(path)
		switch {
		case err == nil:
			sizes = saved
		case errors.Is(err, fs.ErrNotExist):
			// First run: fall back to defaults.
		default:
			return nil, err
		}
	}
	normalized, err := packing.NormalizePackSizes(sizes)
	if err != nil {
		return nil, fmt.Errorf("invalid pack sizes: %w", err)
	}
	return &PackStore{sizes: normalized, path: path}, nil
}

// Get returns a copy of the current pack sizes in ascending order.
func (s *PackStore) Get() []int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.sizes)
}

// Set validates and replaces the pack sizes, persisting them if configured.
// It returns the normalised (sorted, de-duplicated) sizes.
func (s *PackStore) Set(sizes []int) ([]int, error) {
	normalized, err := packing.NormalizePackSizes(sizes)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		if err := save(s.path, normalized); err != nil {
			return nil, fmt.Errorf("saving pack sizes: %w", err)
		}
	}
	s.sizes = normalized
	return slices.Clone(normalized), nil
}

func load(path string) ([]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return f.PackSizes, nil
}

// save writes atomically (temp file + rename) so a crash never leaves a
// half-written config behind.
func save(path string, sizes []int) error {
	data, err := json.MarshalIndent(fileFormat{PackSizes: sizes}, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".packsizes-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
