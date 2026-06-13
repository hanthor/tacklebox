# Tacklebox — TODO

Tracked items not yet implemented. Roughly ordered by value / blocking-ness.

## Features

### `tacklebox add <env>` / `tacklebox remove <env>`
Mutate an existing media in place: add a fourth env to a built image,
or drop one. Today the only way is to rebuild from scratch.

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

### Multi-env ISO: ARM64 support
`shared_store.dedup` ISOs currently assume x86_64 (sd-boot EFI binary).
ARM64 images need aarch64 sd-boot + OVMF for QEMU testing.

## Bugs

### Serial `bootc install` shares ostree commits across envs (block targets only)
Each `bootc install to-filesystem` runs from the correct container image,
but two different images can end up with the same ostree commit hash.
Reproduces with `examples/all-test.json`. `tacklebox verify` catches
this in CI.

Root cause likely in bootc's install-source resolution. Not yet fixed
upstream. ISO targets (Live mode) are unaffected.

## Done ✓

### `tacklebox update <recipe.json>` ✓
Re-pulls recipe images and replays `bootc install` into existing per-env
subtrees without reformatting. `cmd/tacklebox/update.go`.

### `tacklebox verify <image-or-device>` ✓
Sanity-checks a built image: BLS entries resolve, env ostree commits
are distinct, ESP usage fits, loader.conf references a valid entry.
`cmd/tacklebox/verify.go`. Called in CI on every PR.

### CI: full pipeline ✓
- Stage 1: lint + unit + shellcheck (every PR)
- Stage 2: recipe schema parse (every PR)
- Stage 3: 2-env block image build + verify + cache (every PR)
- Stage 4: QEMU boot smoke (block + ISO, every PR)
- ISO smoke: per-env + dedup ISO build, verify, dedup assertion, QEMU boot
- 6-env dedup scale test in iso-smoke (fixtures/iso-dedup-6env.json)

### Multi-image ISO dedup (`shared_store.dedup`) ✓
Pack multiple envs into one combined squashfs with file-level
deduplication. Tested at 2-env and 6-env scale. `internal/install/live.go`.

### `default_boot` BLS ordering ✓
Default env gets sort-key `00-tbox-<id>` (first in menu). ISO and block
targets both supported. `internal/install/bootloader.go`.

### `tacklebox recipe-gen` ✓
Generates tacklebox recipes from simplified YAML env-lists. Auto-defaults
dedup, size, modes, and default_boot. `cmd/tacklebox/recipe_gen.go`.

### Automatic initramfs preparation ✓
Probes images for required dracut modules, rebuilds when missing.
Cached by image ID. `internal/install/initramfs.go`.

### Build caches ✓
Initramfs cache + squashfs cache keyed by image ID. Incremental rebuilds
only pay for what changed. `cmd/tacklebox/build.go`.

### `tacklebox status` ✓
Inspects installed environments on a built media. `cmd/tacklebox/status.go`.

### `tacklebox update-all` ✓
Boot-time cross-env updater timer. Updates all envs from the persisted
recipe. `cmd/tacklebox/update_all.go` + `src/systemd/`.
