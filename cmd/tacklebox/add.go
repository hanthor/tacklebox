package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tuna-os/tacklebox/internal/install"
	"github.com/tuna-os/tacklebox/internal/recipe"
	"github.com/tuna-os/tacklebox/internal/runner"
)

var addCmd = &cobra.Command{
	Use:   "add RECIPE TARGET",
	Short: "Add one or more environments to an existing tacklebox media",
	Long: `Install additional bootable environment(s) onto an already-built media
WITHOUT reformatting or touching the other envs or TBOX_PERSIST.

RECIPE is a media recipe (the same format as 'build'/'update') describing the
env(s) to add. By default every env in RECIPE that is not already present is
added; restrict the set with --env (repeatable).

The new env's ostree content lands in a fresh tbox-install/<id> subtree, a
/EFI/<id> boot dir and BLS entries are written, and the embedded recipe.json
in EVERY env is updated so the boot-time update-all timer sees the new roster.
The existing default_boot env is preserved (adding never steals the default).

Block targets only — ISOs are immutable; rebuild to change their env set.

Examples:
  # Add every env from add.json that isn't already on the USB
  sudo tacklebox add add.json /dev/sdb

  # Add only the 'bazzite' env from a larger recipe to a loop image
  sudo tacklebox add recipes/all.json tacklebox.img --env bazzite
`,
	Args:         cobra.ExactArgs(2),
	SilenceUsage: true,
	RunE:         runAdd,
}

func init() {
	addCmd.Flags().BoolP("verbose", "v", false, "Stream subprocess output and command traces")
	addCmd.Flags().BoolP("yes", "y", false, "Skip the confirmation prompt")
	addCmd.Flags().StringSlice("env", nil, "Only add these env IDs from RECIPE (repeatable). Default: every env not already present.")
	rootCmd.AddCommand(addCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	verbose, _ := cmd.Flags().GetBool("verbose")
	runner.Verbose = verbose

	recipePath := args[0]
	targetArg := args[1]

	if err := validateMutationTarget(targetArg); err != nil {
		return err
	}

	r, err := loadRecipe(recipePath)
	if err != nil {
		return err
	}
	if len(r.BootableEnvironments) == 0 {
		return fmt.Errorf("recipe %s has no bootable_environments", recipePath)
	}

	outputBase, _ := cmd.Flags().GetString("output-base")
	if err := os.MkdirAll(outputBase, 0755); err != nil {
		return fmt.Errorf("create output directory %s: %w", outputBase, err)
	}
	install.SetStagingRoot(outputBase)
	defer install.CleanupStaging()

	var cleanups []func()
	addCleanup := func(f func()) { cleanups = append(cleanups, f) }
	defer func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}()

	storeMount, espMount, err := mountBlockMedia(targetArg, outputBase, addCleanup)
	if err != nil {
		return err
	}

	present, err := installedEnvs(storeMount)
	if err != nil {
		return err
	}
	presentSet := map[string]bool{}
	for _, id := range present {
		presentSet[id] = true
	}

	// Select which recipe envs to add.
	filter, _ := cmd.Flags().GetStringSlice("env")
	toAdd, err := selectEnvsToAdd(r.BootableEnvironments, presentSet, filter)
	if err != nil {
		return err
	}
	if len(toAdd) == 0 {
		fmt.Printf(">>> Nothing to add: all requested envs are already present on %s\n", targetArg)
		return nil
	}

	// Merge the new envs into the media's existing recipe so the new env's
	// embedded recipe.json (and the default_boot decision) reflect the full
	// roster. Fall back to the passed recipe as the base when the media has
	// no embedded recipe (older build).
	base, err := readPersistedRecipe(storeMount)
	if err != nil {
		return err
	}
	if base == nil {
		b := r
		b.BootableEnvironments = nil
		base = &b
	}
	merged := *base
	merged.BootableEnvironments = append(append([]recipe.BootableEnvironment{}, base.BootableEnvironments...), toAdd...)

	addIDs := make([]string, 0, len(toAdd))
	for _, e := range toAdd {
		addIDs = append(addIDs, e.ID)
	}
	assumeYes, _ := cmd.Flags().GetBool("yes")
	summary := fmt.Sprintf("About to ADD env(s) [%s] to %s (existing envs and TBOX_PERSIST untouched).",
		strings.Join(addIDs, ", "), targetArg)
	if err := confirmMutation(targetArg, summary, assumeYes); err != nil {
		return err
	}

	// Pre-flight: make sure the ESP has room before we start writing.
	if err := checkESPFit(espMount, len(toAdd)); err != nil {
		return err
	}

	timings := map[string]time.Duration{}
	track := func(name string, fn func() error) error {
		t0 := time.Now()
		err := fn()
		timings[name] = time.Since(t0)
		return err
	}
	start := time.Now()

	// Warm the container store for the new images (root's store; add is
	// block-only, so bootc install runs as root).
	for _, env := range toAdd {
		env := env
		if err := track("pull:"+env.ID, func() error { return install.Pull(env.Image) }); err != nil {
			return fmt.Errorf("pull %s: %w", env.Image, err)
		}
	}

	for _, env := range toAdd {
		if err := updateEnvBootc(env, merged, storeMount, espMount, track); err != nil {
			return fmt.Errorf("add %s: %w", env.ID, err)
		}
	}

	// Bring every pre-existing env's embedded recipe up to the new roster.
	if err := track("sync-recipe", func() error { return syncPersistedRecipe(storeMount, merged) }); err != nil {
		return fmt.Errorf("sync persisted recipe: %w", err)
	}

	printTimings(timings, time.Since(start))
	fmt.Printf(">>> Added %d env(s): %s\n", len(toAdd), strings.Join(addIDs, ", "))
	return nil
}

// selectEnvsToAdd resolves the recipe envs to install. With no filter it
// returns every recipe env not already present. With a filter it returns
// exactly those IDs, erroring if any is unknown to the recipe or already
// present (so a typo or a redundant add fails loudly rather than silently
// reinstalling — use `update` to refresh an existing env).
func selectEnvsToAdd(envs []recipe.BootableEnvironment, present map[string]bool, filter []string) ([]recipe.BootableEnvironment, error) {
	byID := map[string]recipe.BootableEnvironment{}
	for _, e := range envs {
		byID[e.ID] = e
	}

	if len(filter) == 0 {
		var out []recipe.BootableEnvironment
		for _, e := range envs {
			if !present[e.ID] {
				out = append(out, e)
			}
		}
		return out, nil
	}

	var out []recipe.BootableEnvironment
	var unknown, already []string
	for _, id := range filter {
		e, ok := byID[id]
		if !ok {
			unknown = append(unknown, id)
			continue
		}
		if present[id] {
			already = append(already, id)
			continue
		}
		out = append(out, e)
	}
	sort.Strings(unknown)
	sort.Strings(already)
	if len(unknown) > 0 {
		return nil, fmt.Errorf("recipe has no env(s): %s", strings.Join(unknown, ", "))
	}
	if len(already) > 0 {
		return nil, fmt.Errorf("env(s) already present (use `tacklebox update` to refresh): %s", strings.Join(already, ", "))
	}
	return out, nil
}
