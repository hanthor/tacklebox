package purefs

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/diskfs/go-diskfs/backend"
	"github.com/diskfs/go-diskfs/filesystem/fat32"
)

// memStorage is an in-memory backend.Storage so the FAT32 ESP can be
// authored with no filesystem at all — required under GOOS=js, and it
// keeps native builds from needing a scratch file.
type memStorage struct {
	buf []byte
	off int64
}

func newMemStorage(size int64) *memStorage { return &memStorage{buf: make([]byte, size)} }

func (m *memStorage) Read(p []byte) (int, error) {
	if m.off >= int64(len(m.buf)) {
		return 0, io.EOF
	}
	n := copy(p, m.buf[m.off:])
	m.off += int64(n)
	return n, nil
}

func (m *memStorage) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(m.buf)) {
		return 0, io.EOF
	}
	return copy(p, m.buf[off:]), nil
}

func (m *memStorage) WriteAt(p []byte, off int64) (int, error) {
	if need := off + int64(len(p)); need > int64(len(m.buf)) {
		grown := make([]byte, need)
		copy(grown, m.buf)
		m.buf = grown
	}
	return copy(m.buf[off:], p), nil
}

func (m *memStorage) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		m.off = off
	case io.SeekCurrent:
		m.off += off
	case io.SeekEnd:
		m.off = int64(len(m.buf)) + off
	}
	return m.off, nil
}

func (m *memStorage) Close() error { return nil }

func (m *memStorage) Stat() (fs.FileInfo, error) { return memInfo{size: int64(len(m.buf))}, nil }

func (m *memStorage) Sys() (*os.File, error) { return nil, backend.ErrNotSuitable }

func (m *memStorage) Writable() (backend.WritableFile, error) { return m, nil }

func (m *memStorage) Path() string { return "" }

type memInfo struct{ size int64 }

func (i memInfo) Name() string       { return "esp.img" }
func (i memInfo) Size() int64        { return i.size }
func (i memInfo) Mode() fs.FileMode  { return 0o644 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }

// BuildEspBytes authors a FAT32 ESP fully in memory and returns its
// contents. Sizing mirrors WriteEsp: content + FAT overhead headroom.
func BuildEspBytes(files []EspFile) ([]byte, error) {
	var content int64
	for _, f := range files {
		sz, err := sourceSize(f.Source)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		content += sz
	}
	size := content + content/10 + 64*1024*1024
	size = (size + 1024*1024 - 1) / (1024 * 1024) * (1024 * 1024)

	st := newMemStorage(size)
	fsys, err := fat32.Create(st, size, 0, 512, "ESP", true)
	if err != nil {
		return nil, fmt.Errorf("fat32 create: %w", err)
	}
	if err := populateEsp(fsys, files); err != nil {
		return nil, err
	}
	return st.buf, nil
}

func sourceSize(src func() (io.ReadCloser, error)) (int64, error) {
	rc, err := src()
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return io.Copy(io.Discard, rc)
}
