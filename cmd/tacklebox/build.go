package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tuna-os/tacklebox/internal/blockdev"
	"github.com/tuna-os/tacklebox/internal/install"
	"github.com/tuna-os/tacklebox/internal/recipe"
	"github.com/tuna-os/tacklebox/internal/runner"
)

var buildCmd = &cobra.Command{
	Use:   "build RECIPE [TARGET]",
	Short: "Build a multi-boot image from a recipe",
	Long: `Build a multi-boot image, or provision a real disk, from a recipe.

If TARGET is omitted, a sparse raw image is created at <output-base>/tacklebox.img.
If TARGET begins with /dev/, it is treated as a real block device and partitioned in place.

Examples:
  # Build a raw image file
  tacklebox build examples/multi-test.json

  # Provision a USB stick (DESTRUCTIVE)
  sudo tacklebox build examples/multi-test.json /dev/sda

  # Build and compress for distribution
  tacklebox build recipe.json --xz -b /tmp/dist
`,
	Args:         cobra.RangeArgs(1, 2),
	SilenceUsage: true,
	RunE:         runBuild,
}

func runBuild(cmd *cobra.Command, args []string) error {
	verbose, _ := cmd.Flags().GetBool("verbose")
	runner.Verbose = verbose

	recipePath := args[0]
	data, err := os.ReadFile(recipePath)
	if err != nil {
		return fmt.Errorf("read recipe %s: %w", recipePath, err)
	}

	var r recipe.MediaRecipe
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("parse recipe %s: %w", recipePath, err)
	}
	if len(r.BootableEnvironments) == 0 {
		return fmt.Errorf("recipe %s has no bootable_environments", recipePath)
	}
	if r.Size == "" {
		return fmt.Errorf("recipe %s missing size", recipePath)
	}

	// Validate the target argument shape before doing any filesystem work,
	// so a typo like `tacklebox build recipe.json sdaX` fails instantly
	// instead of after creating an output directory.
	if len(args) == 2 && !strings.HasPrefix(args[1], "/dev/") {
		return fmt.Errorf("target %q does not look like a block device (must start with /dev/)", args[1])
	}

	outputBase, _ := cmd.Flags().GetString("output-base")
	if err := os.MkdirAll(outputBase, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outputBase, err)
	}

	// Cleanup stack runs in LIFO order, including on SIGINT.
	var cleanups []func()
	addCleanup := func(f func()) { cleanups = append(cleanups, f) }
	runCleanups := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
		cleanups = nil
	}
	defer runCleanups()

	// SIGINT/SIGTERM: run cleanups then exit non-zero so leftover loop devices
	// and mounts don't accumulate when the user cancels mid-build.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig, ok := <-sigCh
		if !ok {
			return
		}
		fmt.Fprintf(os.Stderr, "\n>>> Caught %s, cleaning up...\n", sig)
		runCleanups()
		os.Exit(130)
	}()
	defer signal.Stop(sigCh)
	defer close(sigCh)

	var targetDevice string
	var isBlockDevice bool

	if len(args) == 2 {
		targetDevice = args[1]
		isBlockDevice = true
		fmt.Printf(">>> Target is a block device: %s\n", targetDevice)
		assumeYes, _ := cmd.Flags().GetBool("yes")
		if err := confirmDestructive(targetDevice, assumeYes); err != nil {
			return err
		}
	}

	fmt.Printf(">>> Building media: %s (%s)\n", r.MediaName, r.Size)
	fmt.Printf(">>> Output directory: %s\n", outputBase)

	timings := map[string]time.Duration{}
	track := func(name string, fn func() error) error {
		t0 := time.Now()
		err := fn()
		timings[name] = time.Since(t0)
		return err
	}
	buildStart := time.Now()

	var imagePath string
	var loopDev string

	if !isBlockDevice {
		imagePath = filepath.Join(outputBase, "tacklebox.img")

		// Pre-flight: make sure we won't blow up halfway through `truncate`
		// because the filesystem can't hold the image. truncate creates a
		// sparse file so this only catches gross misconfigurations, but
		// without it the failure surfaces much later as cryptic mkfs errors.
		if needed, ok := parseSize(r.Size); ok {
			if free, err := freeBytes(outputBase); err == nil && free < needed/2 {
				fmt.Fprintf(os.Stderr,
					">>> WARNING: only %d MiB free under %s; recipe asks for %s (sparse, but installs will write real data)\n",
					free/(1024*1024), outputBase, r.Size)
			}
		} else {
			return fmt.Errorf("unrecognised size %q in recipe (expected e.g. 32G, 16384M)", r.Size)
		}

		fmt.Printf(">>> Creating raw image: %s\n", imagePath)
		if err := runner.Run("truncate", "-s", r.Size, imagePath); err != nil {
			return fmt.Errorf("create sparse file %s: %w", imagePath, err)
		}

		loopDevBytes, err := runner.Output("sudo", "losetup", "--find", "--show", "--partscan", imagePath)
		if err != nil {
			return fmt.Errorf("setup loop device for %s: %w", imagePath, err)
		}
		loopDev = strings.TrimSpace(string(loopDevBytes))
		targetDevice = loopDev
		addCleanup(func() {
			fmt.Printf(">>> Detaching loop device: %s\n", loopDev)
			runner.Run("sudo", "losetup", "-d", loopDev)
		})
	}

	// Rough sanity check on store sizing. ostree stateroots are large the
	// first time they're populated (~10 GiB observed for bluefin/bazzite);
	// composefs deployments are more compact (~3-5 GiB). These estimates
	// are deliberately conservative — better to warn unnecessarily than to
	// let a build burn 4 minutes only to OOM on env #N.
	if needed, store, ok := estimateStoreUsage(r); ok && needed > store {
		fmt.Fprintf(os.Stderr,
			">>> WARNING: %d environments may need ~%d GiB of store, but recipe layout only allocates ~%d GiB.\n"+
				">>>          Consider increasing recipe size (current: %s).\n",
			len(r.BootableEnvironments), needed>>30, store>>30, r.Size)
	}

	// Partition + format. Layout is derived from the recipe's total size so
	// a 30G recipe doesn't share a 20G store with a 5G recipe — that was a
	// previous foot-gun where bootc installs would silently OOM the store
	// partition on the second environment.
	//
	//   p1 TBOX_ESP    : 1 GiB           (bootloader + per-env kernel/initrd)
	//   p2 TBOX_STORE  : total - 1 - 2   (shared bootc installs)
	//   p3 TBOX_PERSIST: remainder       (~2 GiB+ for persistent overlays)
	//
	// sgdisk's `+SIZE` syntax means "this length starting at default" so we
	// can express STORE as an explicit length rather than an end-position.
	partitions, err := computePartitions(r)
	if err != nil {
		return err
	}
	if err := track("partition", func() error { return blockdev.FormatDisk(targetDevice, partitions) }); err != nil {
		return err
	}
	if err := track("mkfs", func() error { return blockdev.CreateFilesystems(targetDevice, partitions) }); err != nil {
		return err
	}

	espMount := filepath.Join(outputBase, "mount-esp")
	storeMount := filepath.Join(outputBase, "mount-store")
	os.MkdirAll(espMount, 0755)
	os.MkdirAll(storeMount, 0755)

	espDev := blockdev.PartitionPath(targetDevice, 1)
	storeDev := blockdev.PartitionPath(targetDevice, 2)

	// Wait for the kernel to expose the partition nodes. Without this, mount
	// can race the partition rescan after mkfs on busy hosts (--timeout
	// caps the wait at 10 s so we fail fast on real misconfigurations).
	runner.Run("udevadm", "settle", "--timeout=10")

	fmt.Printf(">>> Mounting ESP to %s\n", espMount)
	if err := runner.Run("sudo", "mount", espDev, espMount); err != nil {
		return fmt.Errorf("mount ESP %s: %w", espDev, err)
	}
	addCleanup(func() { runner.Run("sudo", "umount", espMount) })

	fmt.Printf(">>> Mounting STORE to %s\n", storeMount)
	if err := runner.Run("sudo", "mount", storeDev, storeMount); err != nil {
		return fmt.Errorf("mount STORE %s: %w", storeDev, err)
	}
	addCleanup(func() { runner.Run("sudo", "umount", storeMount) })

	if err := track("bootloader", func() error { return install.SetupBootloader(espMount) }); err != nil {
		return err
	}

	// Pre-pull all images in parallel. The actual `bootc install` step still
	// runs sequentially per environment (it shares /var/lib/containers locking
	// and would race), but pulling in parallel overlaps the network-bound
	// portion of the build, which is the dominant cost on a fresh host.
	if err := track("pre-pull (parallel)", func() error { return prePullImages(r.BootableEnvironments) }); err != nil {
		return err
	}

	// Install + boot file extraction per environment.
	for _, env := range r.BootableEnvironments {
		backend := install.Backend(env.Backend)
		if backend == "" {
			detected, err := install.DetectBackend(env.Image)
			if err != nil {
				return err
			}
			backend = detected
		}

		envRoot := filepath.Join(storeMount, "tbox-install", env.ID)
		runner.Run("sudo", "rm", "-rf", envRoot)
		if err := runner.Run("sudo", "mkdir", "-p", envRoot); err != nil {
			return fmt.Errorf("create env root for %s: %w", env.ID, err)
		}
		fmt.Printf(">>> Installing %s (backend=%s)\n", env.ID, backend)
		if err := track("install:"+env.ID, func() error {
			return install.PullAndInstall(env.Image, envRoot, env.ID, backend)
		}); err != nil {
			return fmt.Errorf("install %s: %w", env.ID, err)
		}

		bootDir := filepath.Join(espMount, "EFI", env.ID)
		if err := runner.Run("sudo", "mkdir", "-p", bootDir); err != nil {
			return fmt.Errorf("create boot dir %s: %w", bootDir, err)
		}
		var kver string
		if err := track("extract:"+env.ID, func() error {
			var err error
			kver, err = install.ExtractBootFiles(env.Image, bootDir)
			return err
		}); err != nil {
			return fmt.Errorf("extract boot files for %s: %w", env.ID, err)
		}

		kernelRelPath := filepath.Join("/EFI", env.ID, "vmlinuz")
		initrdRelPath := filepath.Join("/EFI", env.ID, "initrd.img")

		for _, mode := range env.Modes {
			title := fmt.Sprintf("%s (%s)", env.ID, mode)
			id := fmt.Sprintf("%s-%s", env.ID, mode)

			options := fmt.Sprintf("root=LABEL=TBOX_STORE rw console=ttyS0 tacklebox.root=tbox-install/%s", env.ID)
			if mode == recipe.ModeLive {
				options += " rd.live.overlay=tmpfs"
			} else {
				options += " tacklebox.persist=LABEL=TBOX_PERSIST"
			}

			if backend == install.BackendOstree {
				options += fmt.Sprintf(" ostree=/ostree/boot.1/%s/current/0", env.ID)
			} else {
				options += " rootflags=subvol=containers/storage/overlay/default/diff"
			}

			if err := install.WriteBLSEntry(espMount, id, title, kernelRelPath, initrdRelPath, options); err != nil {
				return err
			}
		}
		fmt.Printf(">>> Finished environment: %s (kernel=%s)\n", env.ID, kver)
	}

	if !isBlockDevice {
		fmt.Printf(">>> Tacklebox build complete: %s\n", imagePath)
		xz, _ := cmd.Flags().GetBool("xz")
		if xz {
			fmt.Printf(">>> Compressing image to %s.xz...\n", imagePath)
			if err := runner.Run("xz", "-T0", "-k", imagePath); err != nil {
				return fmt.Errorf("compress image %s: %w", imagePath, err)
			}
			fmt.Printf(">>> Compression complete: %s.xz\n", imagePath)
		}
	} else {
		fmt.Printf(">>> Tacklebox provisioning complete: %s\n", targetDevice)
	}

	printTimings(timings, time.Since(buildStart))
	return nil
}

func printTimings(t map[string]time.Duration, total time.Duration) {
	// Stable column layout. Sort by descending duration so the cost centres
	// stand out without the reader having to scan the whole list.
	type row struct {
		name string
		d    time.Duration
	}
	rows := make([]row, 0, len(t))
	for k, v := range t {
		rows = append(rows, row{k, v})
	}
	// Simple insertion sort — list is tiny.
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0 && rows[j-1].d < rows[j].d; j-- {
			rows[j-1], rows[j] = rows[j], rows[j-1]
		}
	}
	fmt.Println(">>> Phase timings:")
	for _, r := range rows {
		fmt.Printf("    %-24s %8s  (%4.1f%%)\n", r.name, r.d.Round(time.Millisecond), 100*float64(r.d)/float64(total))
	}
	fmt.Printf("    %-24s %8s\n", "TOTAL", total.Round(time.Millisecond))
}

// confirmDestructive prints a summary of the target and requires the user to
// type 'yes' before continuing — unless --yes is set or stdin isn't a tty
// (CI / scripts). This prevents the classic `sudo tacklebox build x /dev/sda`
// typo from nuking the wrong disk.
func confirmDestructive(target string, assumeYes bool) error {
	if assumeYes {
		fmt.Printf(">>> --yes set, skipping destructive confirmation for %s\n", target)
		return nil
	}
	// Best-effort summary: lsblk if available, otherwise nothing.
	if out, err := runner.Output("lsblk", "-o", "NAME,SIZE,TYPE,MODEL,LABEL,MOUNTPOINT", target); err == nil {
		fmt.Println(string(out))
	}

	// Detect non-interactive stdin (e.g. running in CI) and refuse unless --yes.
	fi, err := os.Stdin.Stat()
	if err != nil || (fi.Mode()&os.ModeCharDevice) == 0 {
		return fmt.Errorf("refusing to destroy %s without --yes (stdin is not a terminal)", target)
	}

	fmt.Printf(">>> About to ERASE %s and write a new partition table. Type 'yes' to continue: ", target)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}
	if strings.TrimSpace(strings.ToLower(line)) != "yes" {
		return fmt.Errorf("aborted by user")
	}
	return nil
}

// prePullImages pulls all unique images concurrently. Errors are aggregated so
// the user sees every failure in one go instead of fixing them one-by-one.
func prePullImages(envs []recipe.BootableEnvironment) error {
	seen := make(map[string]struct{}, len(envs))
	var unique []string
	for _, e := range envs {
		if _, dup := seen[e.Image]; dup {
			continue
		}
		seen[e.Image] = struct{}{}
		unique = append(unique, e.Image)
	}
	if len(unique) == 0 {
		return nil
	}

	fmt.Printf(">>> Pre-pulling %d image(s) in parallel\n", len(unique))
	var wg sync.WaitGroup
	errs := make([]error, len(unique))
	for i, img := range unique {
		wg.Add(1)
		go func(i int, img string) {
			defer wg.Done()
			errs[i] = install.Pull(img)
		}(i, img)
	}
	wg.Wait()

	var failed []string
	for i, err := range errs {
		if err != nil {
			failed = append(failed, fmt.Sprintf("  - %s: %v", unique[i], err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("pre-pull failed for %d image(s):\n%s", len(failed), strings.Join(failed, "\n"))
	}
	return nil
}

const (
	espBytes     uint64 = 1 << 30 // 1 GiB
	persistBytes uint64 = 2 << 30 // 2 GiB reserved at the end
	// Minimum store size we'll accept. Anything smaller can't hold even a
	// single bootc deployment.
	minStoreBytes uint64 = 3 << 30 // 3 GiB
)

// computePartitions derives the disk layout from the recipe's total size.
// STORE gets total - ESP - PERSIST so larger recipes automatically get
// larger shared stores.
func computePartitions(r recipe.MediaRecipe) ([]blockdev.Partition, error) {
	total, ok := parseSize(r.Size)
	if !ok {
		return nil, fmt.Errorf("unrecognised size %q in recipe", r.Size)
	}
	if total < espBytes+persistBytes+minStoreBytes {
		return nil, fmt.Errorf(
			"recipe size %s is too small: need at least %d GiB (1 ESP + %d store + %d persist)",
			r.Size, (espBytes+persistBytes+minStoreBytes)>>30, minStoreBytes>>30, persistBytes>>30)
	}
	storeBytes := total - espBytes - persistBytes
	// Round down to whole GiB so the partition table is tidy.
	storeGiB := storeBytes >> 30

	return []blockdev.Partition{
		{Number: 1, Label: "TBOX_ESP", Size: "+1G", Type: "ef00", FS: "vfat"},
		{Number: 2, Label: "TBOX_STORE", Size: fmt.Sprintf("+%dG", storeGiB), Type: "8300", FS: r.SharedStore.Format},
		{Number: 3, Label: "TBOX_PERSIST", Size: "0", Type: "8300", FS: "ext4"},
	}, nil
}

// Rough per-env disk usage estimates (observed empirically — see commit
// notes). Treat anything without an explicit backend as ostree (the larger
// number) so we err on the side of warning.
const (
	ostreeEnvBytes    uint64 = 10 << 30
	composefsEnvBytes uint64 = 5 << 30
)

// estimateStoreUsage returns (estimated bytes needed, store bytes available,
// ok). If the recipe is malformed it returns ok=false and the caller skips
// the pre-flight warning.
func estimateStoreUsage(r recipe.MediaRecipe) (uint64, uint64, bool) {
	total, ok := parseSize(r.Size)
	if !ok || total < espBytes+persistBytes {
		return 0, 0, false
	}
	store := total - espBytes - persistBytes

	// We treat unknown / empty backend as ostree to match the DetectBackend
	// fallback bias and to err on the side of warning more (ostree estimate
	// is larger than composefs).
	var needed uint64
	for _, e := range r.BootableEnvironments {
		if e.Backend == string(install.BackendComposefs) {
			needed += composefsEnvBytes
		} else {
			needed += ostreeEnvBytes
		}
	}
	return needed, store, true
}

// parseSize accepts forms like "32G", "16384M", "1T", "500K" (decimal G=2^30 here
// to match `truncate -s` conventions).
func parseSize(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	unit := uint64(1)
	digits := s
	switch s[len(s)-1] {
	case 'K', 'k':
		unit = 1 << 10
		digits = s[:len(s)-1]
	case 'M', 'm':
		unit = 1 << 20
		digits = s[:len(s)-1]
	case 'G', 'g':
		unit = 1 << 30
		digits = s[:len(s)-1]
	case 'T', 't':
		unit = 1 << 40
		digits = s[:len(s)-1]
	}
	var n uint64
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + uint64(c-'0')
	}
	if n == 0 {
		return 0, false
	}
	return n * unit, true
}

func freeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func init() {
	buildCmd.Flags().Bool("xz", false, "Compress the final image with xz")
	buildCmd.Flags().BoolP("verbose", "v", false, "Stream subprocess output and command traces")
	buildCmd.Flags().BoolP("yes", "y", false, "Skip destructive confirmation when TARGET is a /dev/* device")
	rootCmd.AddCommand(buildCmd)
}
