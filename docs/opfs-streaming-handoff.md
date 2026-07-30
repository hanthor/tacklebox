# Handoff: OPFS streaming and the wasm32 4 GiB ceiling (#156)

Status: **not started.** Diagnosis is solid, the cause of the memory is not.
Read the "What is NOT known" section before designing anything — the obvious
theory is already disproven.

---

## The failure

`tbox.wasm` is Go compiled to **wasm32**. Linear memory is a single 32-bit
address space, so ~4 GiB is the absolute ceiling — not a tunable, not a host
limit. Measured from JS (`instance.exports.mem.buffer.byteLength`) over one
build of `flounder:xfce`:

```
51 MB → 79 → 89 → 630 → 1788 → 3072 → 4094 MB → fatal
```

```
runtime: out of memory: cannot allocate 4194304-byte block (4252303360 in use)
fatal error: out of memory
exit code: 2
```

4,252,303,360 = 3.96 GiB. The failing allocation is **4 MiB**, which is
exactly `sliceChunk` in `opfsSliceReader.Read` — so the heap was already full
when a routine read buffer tipped it over. The allocation that failed is not
the allocation that matters.

Not the host: reproduced with 13 GB RAM free and 226 GB disk, and the
browser's own log line read `storage quota ~10.7 GB free`.

### Two shapes, same cause

- **Fatal** — a large enough allocation trips `fatal error: out of memory`
  and the module exits(2). `flounder:xfce` does this.
- **Silent wedge** — more common in CI. The heap parks near 4094 MB and the
  GC thrashes with **no console output, no `pageerror`, no panic**. The page
  simply never advances. This is why it read as a network stall for hours.
  A memory guard was added in `iso-builder/app/public/app.js`
  (`tboxWasmMB()` + `watchEngineMemory()`) so this now surfaces as a
  reported error rather than a frozen progress bar.

---

## What IS known

**Layer unpack already streams to OPFS.** `cmd/tbwasm/main.go` passes the
`opfsArena` directly to `c.Unpack(...)`, not the `MemStore`. Each file body
goes to OPFS via `arena.Put` in 1 MiB chunks.

**EROFS output already streams to OPFS.** `WriteErofs(root, store,
arenaWriter{sfsArena}, 0)` writes into a *second* arena. File data inside it
goes through `io.Copy(w, r)` — streaming.

**Reads from OPFS already chunk.** `opfsSliceReader.Read` pulls 4 MiB slices
via `blob.slice(...).arrayBuffer()`.

**So the obvious answer is wrong.** The `main.go` header comment still says:

> Memory model (MVP): MemStore holds unpacked file bodies and the EROFS is
> buffered before streaming — peak ≈ 2× unpacked rootfs.

That comment is **stale**. It describes an earlier design. Do not plan work
from it. (Fixing the comment is a freebie.)

**Where it dies:** stage label was `Authoring EROFS live root…`, i.e. inside
`purefs.WriteErofs`, after `GraftLiveOverlay` / `EnsureLiveUser` /
`EnsureAutologin` have run.

---

## What is NOT known

**Nobody has identified what holds ~4 GiB.** This is the single blocking
unknown, and every design below is speculative until it is answered.

Candidates inside `WriteErofs` (`internal/purefs/erofs.go`), none confirmed:

1. `inodes []*inode` — one struct per file in the tree.
2. `byPath map[string]*inode` — a **second** full copy of the path space, keyed
   by full path strings.
3. `in.dirData []byte` per directory — every directory's packed dirents, all
   retained simultaneously (`packDirents` result stored on the inode).
4. The `oci.Node` tree itself, built during unpack and still live: every file's
   name, mode, ownership, and a `Children map[string]*Node` per directory.

For a desktop image these are hundreds of thousands of entries. Maps and
per-node strings are the suspicious part, not file contents.

**Also unquantified:** wasm linear memory only ever **grows** — it is never
returned. So transient peaks are permanent. Fragmentation across many
short-lived 4 MiB read buffers may inflate the high-water mark well above
live-set size.

### First task for whoever picks this up

Instrument, do not theorise. Add allocation accounting around the phases and
report via the existing `tboxOnProgress`/console path:

```go
var ms runtime.MemStats
runtime.ReadMemStats(&ms)
fmt.Printf("tbox: phase=%s heap=%d MB objects=%d\n",
    phase, ms.HeapAlloc>>20, ms.HeapObjects)
```

Print after unpack, after each of the graft/liveuser/autologin steps, after
`mirror()` in `WriteErofs`, after `packDirents`, and during the data-block
loop. That converts this from guesswork into a number. Everything below is
premature until then.

---

## Design options

Ordered by confidence, not by effort.

### A. Publish pre-authored artifacts from CI *(highest confidence)*

Tracked as tuna-os/tunaOS#673. CI already builds these images; have it also
publish the authored EROFS rootfs (and optionally the customize overlay tar)
as OCI artifacts. The browser then streams a ready-made blob to disk instead
of unpacking 65 layers and authoring a filesystem in a 4 GiB address space.

- Sidesteps the ceiling entirely rather than fighting it.
- Makes browser and CI ISOs identical **by construction**, which is the actual
  goal of #673.
- Cost: the browser stops being a builder for those editions and becomes an
  assembler. Custom package/flatpak layering would need a different path.

### B. Compress the EROFS

`erofs.go` writes `FLAT_PLAIN` — uncompressed — so a 2.17 GB `marlin:gnome`
image becomes a **7.4 GB** ISO. EROFS supports lz4/zstd compressed clusters.

- Attacks the problem from both ends: smaller working set *and* far smaller
  ISOs (bandwidth matters, R2 grouping exists precisely to limit it).
- Does **not** on its own prove the 4 GiB peak goes away — that depends on the
  unknown above.
- Cost: implementing Z_EROFS compressed-cluster layout is real work.

### C. Reduce the in-memory tree

If instrumentation points at the inode/node structures:

- Drop `byPath` — it duplicates the whole path space; parent pointers already
  exist.
- Intern or elide `dirData`: regenerate each directory's dirents at write time
  instead of retaining all of them.
- Consider a disk-backed (OPFS) inode table for very large trees.

Cheapest if the tree is the culprit, useless if it is not. **Measure first.**

### D. Memory64 — *do not pursue*

Ruled out already, recorded so nobody re-investigates. Browsers do ship
Memory64 (Chrome M133; Wasm 3.0), **but Go cannot target it** — there is no
memory64 backend, and golang/go#63131 is about a *32-bit* `wasm32` for wasip1.
For a Go engine there is no "raise the limit" option.

---

## Constraints to respect

**Firefox is materially worse.** Go's js/wasm transport falls back to
`arrayBuffer()` when the browser lacks streaming response bodies — i.e.
Firefox — buffering **each layer body whole** into linear memory on top of
everything else. Any "largest buildable image" figure is Chromium-only.

**`await()` in `cmd/tbwasm/opfsstore.go` has no timeout.** It blocks on
`<-done` for a JS promise that may never settle — the same unbreakable-wait
class as the fetch bug fixed in #157, but in our own code. Worth fixing while
in here.

**Multi-extent ISO9660 is done** (#160). Files >4 GiB can now be *written* by
the pure-Go path, verified against a real 5 GiB image with xorriso. That was
the other web-builder blocker; #156 is the remaining one. They are
independent — a 7.4 GB ISO still cannot be assembled inside a 4 GiB address
space no matter how well it is written out.

---

## How to reproduce without a GitHub runner

Runners are frequently saturated; do not debug this through CI.

```bash
rsync -az --exclude node_modules --exclude .git app e2e himachal:/var/tmp/isob/

ssh himachal 'podman run --rm --shm-size=2g \
  -v /var/tmp/isob:/w:z -w /w/e2e \
  -e TBOX_E2E_FULL=1 -e TBOX_E2E_IMAGE=tuna-os/flounder:xfce \
  -e HOME=/w/home \
  mcr.microsoft.com/playwright:v1.62.0-noble \
  bash -lc "npm ci && npx playwright test --grep @full --timeout=3900000"'
```

`flounder:xfce` is the right test case: at 1.04 GB compressed it is the
*smallest* edition, and it still OOMs — so it fails fast and proves the
problem is not raw image size. The `@full` heartbeat prints
`[stage] <phase> wasm=<N> MB` every 30s, which is how the memory curve above
was captured.

`--shm-size=2g` matters; a small `/dev/shm` makes chromium fail for unrelated
reasons. `HOME` must be on the mounted volume because Chrome derives the OPFS
quota from that partition's free space.

Interactive alternative: `corral`'s `tuna-lab iso <ref>` builds an ISO and
boots it with a console. Note corral's qemu unit has **no `-serial`**, so
guest serial is not captured there — fine for looking at a desktop, useless
for a boot that prints nothing.

---

## Definition of done

`flounder:xfce` and `marlin:kde-cachyos` both build an ISO in the browser and
pass `@full` in `iso-builder`'s `full-matrix`, with peak `tboxWasmMB()`
reported and comfortably under 4096.
