package install

// Pure-function tests for live.go. The actual InstallLive +
// ExtractEFIBinary functions need podman/mksquashfs/sudo and are
// covered by the verify-smoke CI job (and locally by examples/iso-smoke.json),
// not here.

// (intentionally empty for now; placeholder so the package has a test
// file and `go test ./...` doesn't skip it. Real coverage of live.go
// comes from the integration-test path — see cmd/tacklebox/integration_test.go.)
