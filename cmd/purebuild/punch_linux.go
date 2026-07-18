package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// punchReader reads a file sequentially while punching holes behind the
// read position, so a large input (the customized-rootfs tar) releases its
// disk blocks as it is consumed instead of coexisting with its unpacked
// form. Logical size is preserved (KEEP_SIZE); the file is deleted after
// ingest anyway.
type punchReader struct {
	f        *os.File
	consumed int64
	punched  int64
}

const punchChunk = 512 << 20 // punch every 512 MiB

func (p *punchReader) Read(b []byte) (int, error) {
	n, err := p.f.Read(b)
	p.consumed += int64(n)
	if p.consumed-p.punched >= punchChunk {
		_ = unix.Fallocate(int(p.f.Fd()),
			unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE,
			p.punched, p.consumed-p.punched)
		p.punched = p.consumed
	}
	return n, err
}

func (p *punchReader) Close() error { return p.f.Close() }
