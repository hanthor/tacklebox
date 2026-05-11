package main

import (
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "tacklebox",
	Short: "Tacklebox creates updatable, multi-boot USB drives from bootc images.",
	Long:  `Tacklebox is an orchestrator for bootc that provisions multi-tenant bootable media with shared storage and unified systemd-boot management.`,
}

func main() {
	// Cobra prints the "Error: ..." line itself; we only need to set the
	// non-zero exit status here.
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringP("output-base", "b", "/var/mnt/data/tacklebox-build", "Base directory for build artifacts")
}
