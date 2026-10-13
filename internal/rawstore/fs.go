// SPDX-License-Identifier: AGPL-3.0-only

package rawstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const tmpPrefix = ".ohs-tmp-"

type fsStore struct{ root string }

func openFS(root string) (Store, error) {
	root = filepath.Clean(root)
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("rawstore: create root: %w", err)
	}
	return &fsStore{root: root}, nil
}

// Ping checks the root is still a directory (a removed or unmounted volume is
// not ready, although listing a missing prefix would succeed).
func (s *fsStore) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	fi, err := os.Stat(s.root)
	if err != nil {
		return fmt.Errorf("rawstore: root: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("rawstore: root %s is not a directory", s.root)
	}
	return nil
}

func (s *fsStore) path(key string) string { return filepath.Join(s.root, filepath.FromSlash(key)) }

// Put writes to a temp file in the target directory, then os.Link makes it
// visible atomically; Link fails if the key already exists.
func (s *fsStore) Put(ctx context.Context, key string, r io.Reader) error {
	if err := checkKey(key); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dst := s.path(key)
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = io.Copy(tmp, &ctxReader{ctx: ctx, r: r})
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return ErrExists
		}
		return err
	}
	return nil
}

func (s *fsStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	f, err := os.Open(s.path(key))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if st, err := f.Stat(); err != nil || st.IsDir() {
		_ = f.Close() // opened read-only: nothing to lose, the answer is ErrNotFound
		return nil, ErrNotFound
	}
	return f, nil
}

func (s *fsStore) List(ctx context.Context, prefix string) ([]string, error) {
	if err := checkPrefix(prefix); err != nil {
		return nil, err
	}
	return s.list(ctx, prefix, false)
}

func (s *fsStore) Walk(ctx context.Context, prefix string, fn func(keys []string) error) error {
	if err := checkPrefix(prefix); err != nil {
		return err
	}
	return s.walk(ctx, prefix, false, fn)
}

// list walks the tree; includeTmp also returns crash leftovers (temp files).
func (s *fsStore) list(ctx context.Context, prefix string, includeTmp bool) ([]string, error) {
	var keys []string
	err := s.walk(ctx, prefix, includeTmp, func(page []string) error {
		keys = append(keys, page...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(keys)
	return keys, nil
}

func (s *fsStore) walk(ctx context.Context, prefix string, includeTmp bool, fn func([]string) error) error {
	// Start the walk at the deepest directory the prefix names.
	start := s.root
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		start = s.path(prefix[:i])
	}
	page := make([]string, 0, WalkPageSize)
	flush := func() error {
		if len(page) == 0 {
			return nil
		}
		sort.Strings(page)
		err := fn(page)
		page = make([]string, 0, WalkPageSize)
		return err
	}
	err := filepath.WalkDir(start, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if d.IsDir() || (!includeTmp && strings.HasPrefix(d.Name(), tmpPrefix)) {
			return nil
		}
		rel, err := filepath.Rel(s.root, p)
		if err != nil {
			return err
		}
		k := filepath.ToSlash(rel)
		if strings.HasPrefix(k, prefix) {
			page = append(page, k)
			if len(page) >= WalkPageSize {
				return flush()
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return flush()
}

func (s *fsStore) DeletePrefix(ctx context.Context, prefix string) error {
	if err := checkDeletePrefix(prefix); err != nil {
		return err
	}
	keys, err := s.list(ctx, prefix, true)
	if err != nil {
		return err
	}
	for _, k := range keys {
		if err := os.Remove(s.path(k)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		// Prune directories left empty, stopping at the root or the first non-empty one.
		for d := filepath.Dir(s.path(k)); d != s.root && strings.HasPrefix(d, s.root); d = filepath.Dir(d) {
			if os.Remove(d) != nil {
				break
			}
		}
	}
	return nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
