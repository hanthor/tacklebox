package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tuna-os/tacklebox/internal/recipe"
	"gopkg.in/yaml.v3"
)

func init() {
	recipeGenCmd.Flags().StringP("output", "o", "", "output file (default: stdout)")
	rootCmd.AddCommand(recipeGenCmd)
}

// yamlNormalize converts yaml.v3's map[any]any maps to map[string]any
// so json.Marshal works. Needed because the recipe struct uses json tags
// and we bridge YAML input → JSON → struct.
func yamlNormalize(v any) any {
	switch v := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(v))
		for k, val := range v {
			if ks, ok := k.(string); ok {
				m[ks] = yamlNormalize(val)
			}
		}
		return m
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, val := range v {
			m[k] = yamlNormalize(val)
		}
		return m
	case []any:
		a := make([]any, len(v))
		for i, val := range v {
			a[i] = yamlNormalize(val)
		}
		return a
	default:
		return v
	}
}

var recipeGenCmd = &cobra.Command{
	Use:   "recipe-gen [input.yaml|input.json]",
	Short: "Generate a tacklebox recipe from a simplified env list",
	Long: `Reads a YAML or JSON env-list and emits a full tacklebox recipe.

The input is a simplified format — only media_name, shared_store, and a
flat list of bootable environments (id, image, title). The output fills
in defaults (modes=["live"], default ISO target size, shared_store dedup
when multiple envs are present).

Example input (YAML):
  media_name: TunaOS Yellowfin
  shared_store:
    dedup: true
  bootable_environments:
    - id: gnome
      image: ghcr.io/tuna-os/yellowfin:gnome
      title: GNOME
    - id: gnome-hwe
      image: ghcr.io/tuna-os/yellowfin:gnome-hwe
      title: GNOME (HWE)

Any fields from the full recipe schema (size, default_boot, partitions,
etc.) can also be set — they pass through unchanged.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		inputPath := args[0]

		data, err := os.ReadFile(inputPath)
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}

		// Parse input. The recipe struct has json tags, so decode YAML
		// into a generic map first, then re-encode as JSON for the struct.
		var raw any
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse input: %w", err)
		}
		jsonData, err := json.Marshal(yamlNormalize(raw))
		if err != nil {
			return fmt.Errorf("convert to json: %w", err)
		}
		var r recipe.MediaRecipe
		if err := json.Unmarshal(jsonData, &r); err != nil {
			return fmt.Errorf("parse recipe: %w", err)
		}

		// Apply defaults.
		if r.Size == "" {
			// Estimate: 5 GB per env + 1 GB ESP + 2 GB persist.
			r.Size = fmt.Sprintf("%dG", len(r.BootableEnvironments)*5+3)
		}

		if r.SharedStore.Format == "" {
			r.SharedStore.Format = "ext4"
		}

		// Auto-enable dedup when >1 envs.
		if len(r.BootableEnvironments) > 1 && !r.SharedStore.Dedup {
			r.SharedStore.Dedup = true
		}

		// Default: live mode for all envs if unspecified.
		for i := range r.BootableEnvironments {
			if len(r.BootableEnvironments[i].Modes) == 0 {
				r.BootableEnvironments[i].Modes = []recipe.BootMode{"live"}
			}
		}

		// Default title from ID.
		for i := range r.BootableEnvironments {
			if r.BootableEnvironments[i].Title == "" {
				r.BootableEnvironments[i].Title = r.BootableEnvironments[i].ID
			}
		}

		// Default default_boot: first env.
		if r.DefaultBoot == "" && len(r.BootableEnvironments) > 0 {
			r.DefaultBoot = r.BootableEnvironments[0].ID
		}

		// Validate.
		if len(r.BootableEnvironments) == 0 {
			return fmt.Errorf("at least one bootable environment is required")
		}
		if r.MediaName == "" {
			return fmt.Errorf("media_name is required")
		}

		out, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal output: %w", err)
		}

		outputPath, _ := cmd.Flags().GetString("output")
		if outputPath != "" {
			if err := os.WriteFile(outputPath, append(out, '\n'), 0644); err != nil {
				return fmt.Errorf("write output: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Wrote %s\n", outputPath)
		} else {
			fmt.Println(string(out))
		}

		return nil
	},
}
