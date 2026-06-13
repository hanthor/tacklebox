# Contributing to Tacklebox

Thank you for contributing to Tacklebox — a high-performance multi-boot orchestrator for `bootc`.

## Quick Start

### Prerequisites

- Go 1.22+
- `podman` and `bootc`
- `sgdisk` (from `gdisk` package)
- `mkfs.vfat`, `mkfs.ext4` (with verity support)
- `xz` (for compressed outputs)
- [`just`](https://github.com/casey/just) (task runner)

### Setup

```bash
git clone https://github.com/tuna-os/tacklebox.git
cd tacklebox
```

### Build

```bash
just build     # build the binary
```

## Development Workflow

### Building and Testing

```bash
# Build the tacklebox binary
just build

# Run unit tests
go test ./...

# Run the verify smoke test suite
just verify-smoke
```

### Provisioning a Test Drive

```bash
# Build a compressed distribution image
just build-xz

# Provision a test USB drive (⚠️ destructive)
just provision-usb device=/dev/sdX recipe=examples/multi-test.json
```

## Pre-Commit Checklist

Always run before every commit:

```bash
go fmt ./...        # format Go code
go vet ./...        # lint Go code
go test ./...       # run tests
```

Commits must be signed with DCO:

```bash
git commit -s -m "description"
```

## Pull Request Process

1. Fork the repository and create a feature branch
2. Run `go fmt ./... && go vet ./... && go test ./...` before pushing
3. Ensure your commits are signed (`git commit -s`)
4. Open a PR against the `main` branch
5. CI runs the verify smoke test suite
6. Address feedback from maintainers

## Architecture

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for detailed architecture:
- Code layout and package responsibilities
- Build flow and target interfaces
- Recipe schema and partition layout
- Initramfs module and boot-time integration

## Code Layout

```
cmd/tacklebox/       # CLI entry points (cobra subcommands)
internal/
  recipe/            # JSON recipe schema
  target/            # Target interface + block/ISO implementations
  install/           # per-env install backends (bootc, live, initramfs, bootloader)
  blockdev/          # sgdisk + mkfs wrappers
  runner/            # subprocess wrapper
src/
  dracut/95tbox-root/  # initramfs module for multi-tenancy at boot
  systemd/           # boot-time updater units
```

## Documentation

- [README.md](README.md) — project overview and usage
- [ARCHITECTURE.md](ARCHITECTURE.md) — code layout and design
- [TODO.md](TODO.md) — known issues and planned improvements

## Community

- [GitHub Issues](https://github.com/tuna-os/tacklebox/issues)
- Matrix: [#tunaos:reilly.asia](https://matrix.to/#/%23tunaos:reilly.asia)

## License

Tacklebox is licensed under [Apache 2.0](LICENSE).
