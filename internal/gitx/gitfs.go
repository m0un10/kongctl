package gitx

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

// FS exposes a git ref (or the index when ref is "") as a read-only fs.FS,
// so compose can read from history exactly as it reads from the worktree.
type FS struct {
	Git Git
	Ref string
	Ctx context.Context
}

// NewFS builds an FS over ref; "" means the index.
func NewFS(ctx context.Context, g Git, ref string) *FS {
	return &FS{Git: g, Ref: ref, Ctx: ctx}
}

var _ fs.ReadDirFS = (*FS)(nil)
var _ fs.ReadFileFS = (*FS)(nil)
var _ fs.StatFS = (*FS)(nil)

func clean(name string) string {
	name = path.Clean(name)
	if name == "/" {
		return "."
	}
	return strings.TrimPrefix(name, "/")
}

// Open opens a file (directories are supported for ReadDir only).
func (f *FS) Open(name string) (fs.File, error) {
	name = clean(name)
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	data, err := f.ReadFile(name)
	if err == nil {
		return &memFile{name: path.Base(name), data: bytes.NewReader(data), size: int64(len(data))}, nil
	}
	if entries, derr := f.ReadDir(name); derr == nil {
		return &dirFile{name: path.Base(name), entries: entries}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: err}
}

// ReadFile reads a blob.
func (f *FS) ReadFile(name string) ([]byte, error) {
	name = clean(name)
	data, err := f.Git.ReadBlob(f.Ctx, f.Ref, name)
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: err}
	}
	return data, nil
}

// ReadDir lists a tree.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	name = clean(name)
	entries, err := f.Git.ListTree(f.Ctx, f.Ref, name)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, dirEntry{name: e.Name, dir: e.IsDir})
	}
	return out, nil
}

// Stat reports whether name is a file or directory.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	name = clean(name)
	if name == "." {
		return dirEntry{name: ".", dir: true}, nil
	}
	if data, err := f.Git.ReadBlob(f.Ctx, f.Ref, name); err == nil {
		return fileInfo{name: path.Base(name), size: int64(len(data))}, nil
	}
	if _, err := f.Git.ListTree(f.Ctx, f.Ref, name); err == nil {
		return dirEntry{name: path.Base(name), dir: true}, nil
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

type fileInfo struct {
	name string
	size int64
}

func (i fileInfo) Name() string       { return i.name }
func (i fileInfo) Size() int64        { return i.size }
func (i fileInfo) Mode() fs.FileMode  { return 0o644 }
func (i fileInfo) ModTime() time.Time { return time.Time{} }
func (i fileInfo) IsDir() bool        { return false }
func (i fileInfo) Sys() any           { return nil }

type dirEntry struct {
	name string
	dir  bool
}

func (d dirEntry) Name() string               { return d.name }
func (d dirEntry) IsDir() bool                { return d.dir }
func (d dirEntry) Type() fs.FileMode          { return d.Mode().Type() }
func (d dirEntry) Info() (fs.FileInfo, error) { return d, nil }
func (d dirEntry) Size() int64                { return 0 }
func (d dirEntry) Mode() fs.FileMode {
	if d.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (d dirEntry) ModTime() time.Time { return time.Time{} }
func (d dirEntry) Sys() any           { return nil }

type memFile struct {
	name string
	data *bytes.Reader
	size int64
}

func (m *memFile) Stat() (fs.FileInfo, error) { return fileInfo{name: m.name, size: m.size}, nil }
func (m *memFile) Read(p []byte) (int, error) { return m.data.Read(p) }
func (m *memFile) Close() error               { return nil }

type dirFile struct {
	name    string
	entries []fs.DirEntry
	pos     int
}

func (d *dirFile) Stat() (fs.FileInfo, error) { return dirEntry{name: d.name, dir: true}, nil }
func (d *dirFile) Read([]byte) (int, error)   { return 0, io.EOF }
func (d *dirFile) Close() error               { return nil }
func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if d.pos >= len(d.entries) {
		if n <= 0 {
			return nil, nil
		}
		return nil, io.EOF
	}
	end := len(d.entries)
	if n > 0 && d.pos+n < end {
		end = d.pos + n
	}
	out := d.entries[d.pos:end]
	d.pos = end
	return out, nil
}
