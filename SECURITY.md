# Security Policy

## Supported Versions

Tacklebox is published as a Go binary and container image (`ghcr.io/tuna-os/tacklebox`).
Only the most recent tagged release and `latest` container image are actively supported.

| Version | Status |
|---------|--------|
| Latest release / `latest` tag | ✅ Supported |
| Older releases | ❌ Unsupported |

## Reporting a Vulnerability

**Please do not report security vulnerabilities through public GitHub issues.**

Tacklebox operates with root privileges and writes to block devices — security
vulnerabilities could result in data loss or system compromise.

Instead, report them privately via GitHub Security Advisories:

1. Go to the [Security tab](https://github.com/tuna-os/tacklebox/security)
2. Click **Report a vulnerability**
3. Provide a detailed description of the issue, including:
   - Steps to reproduce
   - Affected versions
   - Potential impact (data loss, privilege escalation, etc.)

You can expect:
- **Acknowledgment** within 48 hours
- **Status update** within 5 business days
- **Resolution timeline** based on severity

## Security Model

Tacklebox is a system-level tool that:
- Runs with **root privileges** (required for block device access, mount, sgdisk)
- Creates and modifies **partition tables** on physical block devices
- Executes **bootc** as a subprocess to install bootable environments
- Runs **podman** for initramfs preparation
- Writes **bootloader entries** (systemd-boot / BLS) to the ESP

### Trust Boundaries

1. **Recipe input**: JSON recipes specify images and configuration. Malicious recipes
   could install untrusted container images onto bootable media.
2. **Container images**: Tacklebox pulls and runs container images from registries.
   Image integrity depends on registry trust and optional signing.
3. **Block device access**: Tacklebox writes directly to block devices. Ensure target
   devices are correctly specified to avoid accidental destruction.

### Mitigations

- Container images are pinned by digest in CI verification
- GitHub Actions workflows run with limited permissions
- CI container images built from pinned base images
- Go's memory safety eliminates common buffer overflow classes

## Supply Chain Security

- Go modules pinned with checksums in `go.sum`
- Container images built in CI with provenance attestation
- GitHub Actions pinned to commit SHAs
- Binary releases built via GitHub Actions with attestation

## Disclosure Policy

We follow coordinated disclosure:
1. Reporter submits vulnerability privately
2. We investigate and develop a fix
3. Fix is released as a new version
4. Advisory is published after the fix is available

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for internal design details.
