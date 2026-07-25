package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tuna-os/tacklebox/internal/install"
	"github.com/tuna-os/tacklebox/internal/runner"
)

// customize-live exists so that publishing a live-overlay artifact and
// building an ISO run the SAME code path.
//
// tuna-os/tunaOS's live-overlay workflow used to reimplement the customize
// step in bash — `podman run --cap-add sys_admin ... bash ./customize-live.sh`
// — and the reimplementation silently diverged: it ran only the project's own
// script, while install.CustomizeLive additionally prepends the embedded
// src/live/baseline.sh (live user, DM autologin, live networking, sleep
// masking). The published overlay was therefore base + project script, NOT
// the live payload a CI-built ISO gets. Nothing compared the two, so the gap
// went unnoticed; the browser path masked it further by applying its own
// EnsureLiveUser/EnsureAutologin after grafting.
//
// Exposing the real implementation as a command makes that class of drift
// impossible: there is one definition of "make this image a live
// environment", and both consumers call it.
var customizeLiveCmd = &cobra.Command{
	Use:   "customize-live --image IMAGE --script PATH [--script PATH...] [--tag TAG]",
	Short: "Run the live customize pipeline (embedded baseline + given scripts) against an image",
	Long: `Runs the same live customization an ISO build performs: tacklebox's
embedded live baseline followed by each --script, in order, inside a
container of --image, committing the result.

Prints the resulting image ref on stdout. With --tag, the result is also
tagged as TAG (and labelled with any --label), which is what publishing a
live-overlay artifact wants.`,
	Args: cobra.NoArgs,
	RunE: runCustomizeLive,
}

var (
	clImage  string
	clScript []string
	clTag    string
	clLabel  []string
)

func init() {
	f := customizeLiveCmd.Flags()
	f.StringVar(&clImage, "image", "", "base image to customize (must already be in the podman store)")
	f.StringArrayVar(&clScript, "script", nil, "customize script to run after the embedded baseline; repeatable, order preserved")
	f.StringVar(&clTag, "tag", "", "tag the resulting image as this ref")
	f.StringArrayVar(&clLabel, "label", nil, "LABEL to set on the tagged image as key=value; repeatable, requires --tag")
	_ = customizeLiveCmd.MarkFlagRequired("image")
	rootCmd.AddCommand(customizeLiveCmd)
}

func runCustomizeLive(cmd *cobra.Command, _ []string) error {
	if len(clScript) == 0 {
		// CustomizeLive treats an empty script list as passthrough and skips
		// the baseline too, which would silently produce an un-customized
		// image. That is right for appliance recipes but never what someone
		// invoking this command directly means.
		return fmt.Errorf("at least one --script is required")
	}
	if len(clLabel) > 0 && clTag == "" {
		return fmt.Errorf("--label requires --tag")
	}
	for _, l := range clLabel {
		if !strings.Contains(l, "=") {
			return fmt.Errorf("--label %q must be key=value", l)
		}
	}

	derived, err := install.CustomizeLive(clImage, clScript)
	if err != nil {
		return err
	}

	if clTag != "" {
		if err := tagCustomized(derived, clTag, clLabel); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), clTag)
		return nil
	}
	fmt.Fprintln(cmd.OutOrStdout(), derived)
	return nil
}

// tagCustomized publishes derived as tag. Without labels a plain tag
// suffices; with them we must re-commit, since labels are image config and
// cannot be attached by tagging. Committing a created-but-never-started
// container carries the rootfs across unchanged.
func tagCustomized(derived, tag string, labels []string) error {
	podman := install.UserPodmanPrefix()
	run := func(args ...string) error {
		return runner.Run(podman[0], append(podman[1:], args...)...)
	}

	if len(labels) == 0 {
		if err := run("tag", derived, tag); err != nil {
			return fmt.Errorf("tag %s as %s: %w", derived, tag, err)
		}
		return nil
	}

	out, err := runner.Output(podman[0], append(podman[1:], "create", derived, "true")...)
	if err != nil {
		return fmt.Errorf("create container from %s: %w", derived, err)
	}
	ctr := strings.TrimSpace(string(out))
	defer func() { _ = run("rm", "-f", "--ignore", ctr) }()

	args := []string{"commit", "--quiet"}
	for _, l := range labels {
		args = append(args, "--change", "LABEL "+l)
	}
	args = append(args, ctr, tag)
	if err := run(args...); err != nil {
		return fmt.Errorf("commit %s as %s: %w", derived, tag, err)
	}
	return nil
}
