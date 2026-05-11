# Tacklebox — TODO

Tracked items not yet implemented. Roughly ordered by value / blocking-ness.

## Features

### `tacklebox update <recipe.json>`
Re-pull the recipe's images and replay `bootc install` (or smarter
`bootc switch`) into existing per-env subtrees on a previously-built
media, without wiping the persist partition. Lets users refresh USB
media without re-formatting.

### `tacklebox add <env>` / `tacklebox remove <env>`
Mutate an existing media in place: add a fourth env to a built image,
or drop one. Today the only way is to rebuild from scratch.

### `tacklebox verify <image-or-device>`
Sanity-check a built image. Asserts:
- All BLS entries resolve to existing kernel + initrd in the ESP.
- Each env's ostree commit hash is **distinct from the others**
  (catches today's bootc-install content-collision bug — see below).
- ESP usage is under its partition size.
- Per-env `tbox-install/<env>/ostree/repo` is consistent (`ostree fsck`).
- `loader.conf` references an entry that exists.

Prerequisite for meaningful CI regression coverage — both the smoke
test and full E2E should call `tacklebox verify` after build.

### Per-stateroot greenboot / rollback
Each env boots independently with its own stateroot, so health-check
+ auto-rollback should be wired per-env, not globally.

### Persist-partition lifecycle
`TBOX_PERSIST` is formatted and `/home` is overlaid via the dracut
module, but there's no story for:
- Quota per env.
- Garbage collection when an env is removed.
- Migration if the recipe's env list changes between builds.

### USB pre-flight: unmount busy partitions
`final-attempt.log` shows `mkfs.vfat` failing because `/dev/sdb1` was
auto-mounted by the desktop. `internal/blockdev` should sweep
`/dev/<target>*` mounts before format. Only matters for `/dev/*`
targets, not loop images.

## Bugs

### Serial `bootc install` shares ostree commits across envs
**Reproduces with `examples/all-test.json`.** Each `bootc install
to-filesystem` runs from the correct container image, but both
`tbox-install/aurora` and `tbox-install/bazzite` end up with the
*same* ostree commit hash (bazzite content). The aurora container
itself contains genuine Aurora content when run standalone, so the
bug is in how bootc resolves the source image when serial installs
share `--mount type=bind,src=/var/lib/containers`. Either:
- Work around it on the tacklebox side (per-env scratch containers
  storage, copy image references into an env-local store before
  invoking bootc), or
- File upstream against bootc.

`tacklebox verify` (above) would have caught this in CI.

## CI / automated testing

The goal is regression coverage for the dracut module, partitioning,
bootloader wiring, and per-env install correctness, runnable on every
PR.

### Stage 1 — Lint + unit (every PR, ~2 min, ubuntu-latest)
- `go test ./...`
- `go vet ./...`
- `shellcheck src/dracut/95tbox-root/*.sh`
- `go build ./...`

### Stage 2 — Recipe schema check (every PR, seconds)
- Parse every `examples/*.json` through tacklebox's loader; fail on
  any rejection.

### Stage 3 — Disk-build smoke against fixture (every PR, ~5 min, ubuntu-latest)
- Build (or pull) a tiny fixture bootc image (e.g. minimal
  `quay.io/centos-bootc/centos-bootc:stream10`, or a purpose-built
  ~1 GB image we maintain in this repo).
- `tacklebox build fixtures/smoke.json` to a ~6 GB loop image.
- `tacklebox verify` against the result.
- Needs `sudo` + loop devices on the runner — both available on
  hosted Linux runners with the right setup.

### Stage 4 — QEMU boot smoke (every PR, ~3 min, ubuntu-latest with /dev/kvm)
- Boot the Stage 3 image headless under KVM, capture serial log.
- Grep for `tbox-root.service: ... finished` and `Welcome to`.
- On failure, upload the serial log + ESP contents as a CI artifact
  so triage doesn't require re-running locally.

### Stage 5 — Nightly full regression (self-hosted)
- Real `examples/all-test.json` build (bazzite + aurora + dakota,
  60 GB) + boot. Catches issues that only show up with real upstream
  images (e.g. today's bootc cross-env collision). Won't fit on free
  GHA runners — needs a self-hosted runner or an ephemeral cloud
  box spun up from the workflow.

### Workflow file
`.github/workflows/ci.yml` (stages 1–4) + `.github/workflows/nightly.yml`
(stage 5, `schedule:` + `workflow_dispatch:`).

### Test fixtures
- `fixtures/smoke.json` — single-env recipe pointing at the minimal
  bootc image, ~6 GB shared store, 1 GB ESP.
- Either pull the fixture image from a public registry, or build it
  in-repo and push to GHCR on main.
