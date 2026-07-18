//go:build js && wasm

package main

import (
	"fmt"
	"github.com/tuna-os/tacklebox/internal/oci"
	"io"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
)

// opfsArena is a BlobStore over ONE append-only OPFS file: refs are
// "a:<offset>:<len>". This is the streaming answer to MemStore — the
// wasm heap holds chunk buffers and tree metadata only, while multi-GB
// image content lives in origin-private storage (the same pattern the
// Android/GrapheneOS web flashers use: stream, never hold).
//
// Writes go through a single FileSystemWritableFileStream kept open for
// the whole unpack (one open/close per image, not per blob — 45k blob
// opens would drown in async overhead). Reads slice the underlying File
// object; OPFS slices are cheap and lazily materialized.
type opfsArena struct {
	mu     sync.Mutex
	dir    js.Value // FileSystemDirectoryHandle
	handle js.Value // FileSystemFileHandle
	writer js.Value // FileSystemWritableFileStream (valid until Seal)
	file   js.Value // File snapshot for reads (refreshed on Seal)
	name   string
	off    int64
	sealed bool
}

// await blocks the calling goroutine on a JS promise.
func await(p js.Value) (js.Value, error) {
	done := make(chan struct{})
	var result js.Value
	var errv js.Value
	ok := true
	then := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			result = args[0]
		}
		close(done)
		return nil
	})
	catch := js.FuncOf(func(_ js.Value, args []js.Value) any {
		ok = false
		if len(args) > 0 {
			errv = args[0]
		}
		close(done)
		return nil
	})
	defer then.Release()
	defer catch.Release()
	p.Call("then", then).Call("catch", catch)
	<-done
	if !ok {
		return js.Value{}, fmt.Errorf("js: %s", jsErrString(errv))
	}
	return result, nil
}

func jsErrString(v js.Value) string {
	if v.Type() == js.TypeObject {
		if m := v.Get("message"); m.Type() == js.TypeString {
			return m.String()
		}
	}
	return v.String()
}

func newOpfsArena(name string) (*opfsArena, error) {
	nav := js.Global().Get("navigator")
	root, err := await(nav.Get("storage").Call("getDirectory"))
	if err != nil {
		return nil, fmt.Errorf("opfs unavailable: %w", err)
	}
	opts := js.Global().Get("Object").New()
	opts.Set("create", true)
	h, err := await(root.Call("getFileHandle", name, opts))
	if err != nil {
		return nil, err
	}
	w, err := await(h.Call("createWritable"))
	if err != nil {
		return nil, err
	}
	return &opfsArena{dir: root, handle: h, writer: w, name: name}, nil
}

func (a *opfsArena) Put(r io.Reader) (string, int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sealed {
		return "", 0, fmt.Errorf("arena sealed")
	}
	start := a.off
	buf := make([]byte, 1<<20)
	var n int64
	for {
		k, err := r.Read(buf)
		if k > 0 {
			u8 := js.Global().Get("Uint8Array").New(k)
			js.CopyBytesToJS(u8, buf[:k])
			if _, werr := await(a.writer.Call("write", u8)); werr != nil {
				return "", 0, werr
			}
			n += int64(k)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, err
		}
	}
	a.off += n
	return fmt.Sprintf("a:%d:%d", start, n), n, nil
}

// Seal flushes the writer so reads see all content. Put becomes invalid.
func (a *opfsArena) Seal() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sealed {
		return nil
	}
	if _, err := await(a.writer.Call("close")); err != nil {
		return err
	}
	f, err := await(a.handle.Call("getFile"))
	if err != nil {
		return err
	}
	a.file = f
	a.sealed = true
	return nil
}

func (a *opfsArena) Open(ref string) (io.ReadCloser, error) {
	parts := strings.Split(ref, ":")
	if len(parts) != 3 || parts[0] != "a" {
		return nil, fmt.Errorf("bad arena ref %q", ref)
	}
	off, _ := strconv.ParseInt(parts[1], 10, 64)
	ln, _ := strconv.ParseInt(parts[2], 10, 64)
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.sealed {
		// Reads before Seal (EnsureLiveUser mid-pipeline): flush via
		// close+reopen-append is not supported by OPFS streams, so
		// snapshot through a temporary seal-and-reopen is avoided by
		// keeping such reads out of the hot path; snapshot the file lazily.
		f, err := await(a.handle.Call("getFile"))
		if err != nil {
			return nil, err
		}
		a.file = f
	}
	slice := a.file.Call("slice", float64(off), float64(off+ln))
	return &opfsSliceReader{blob: slice, size: ln}, nil
}

func (a *opfsArena) Destroy() {
	opts := js.Global().Get("Object").New()
	_, _ = await(a.dir.Call("removeEntry", a.name, opts))
}

// opfsSliceReader reads a Blob slice in chunks via arrayBuffer().
type opfsSliceReader struct {
	blob js.Value
	size int64
	pos  int64
	buf  []byte
	bo   int
}

const sliceChunk = 4 << 20

func (r *opfsSliceReader) Read(p []byte) (int, error) {
	if r.bo >= len(r.buf) {
		if r.pos >= r.size {
			return 0, io.EOF
		}
		end := r.pos + sliceChunk
		if end > r.size {
			end = r.size
		}
		part := r.blob.Call("slice", float64(r.pos), float64(end))
		ab, err := await(part.Call("arrayBuffer"))
		if err != nil {
			return 0, err
		}
		u8 := js.Global().Get("Uint8Array").New(ab)
		r.buf = make([]byte, u8.Get("length").Int())
		js.CopyBytesToGo(r.buf, u8)
		r.bo = 0
		r.pos = end
	}
	n := copy(p, r.buf[r.bo:])
	r.bo += n
	return n, nil
}

func (r *opfsSliceReader) Close() error { return nil }

// hybridStore: reads dispatch by ref prefix; writes after the unpack
// (tree surgery like EnsureLiveUser — small files) land in memory.
type hybridStore struct {
	arena *opfsArena
	mem   *oci.MemStore
}

func (h *hybridStore) Put(r io.Reader) (string, int64, error) {
	ref, n, err := h.mem.Put(r)
	return "m/" + ref, n, err
}

func (h *hybridStore) Open(ref string) (io.ReadCloser, error) {
	if strings.HasPrefix(ref, "m/") {
		return h.mem.Open(strings.TrimPrefix(ref, "m/"))
	}
	return h.arena.Open(ref)
}

// arenaWriter appends raw bytes to the arena file (EROFS authoring).
type arenaWriter struct{ a *opfsArena }

func (w arenaWriter) Write(p []byte) (int, error) {
	u8 := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(u8, p)
	if _, err := await(w.a.writer.Call("write", u8)); err != nil {
		return 0, err
	}
	w.a.off += int64(len(p))
	return len(p), nil
}
