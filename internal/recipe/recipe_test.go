package recipe

import (
	"encoding/json"
	"testing"
)

func TestRecipeRoundtrip(t *testing.T) {
	raw := `{
		"media_name": "test",
		"size": "30G",
		"shared_store": {"format": "ext4", "compression": "zstd"},
		"bootable_environments": [
			{"id": "bluefin", "image": "ghcr.io/x/y", "modes": ["live", "persistent"]},
			{"id": "dakota", "image": "ghcr.io/a/b", "backend": "composefs", "modes": ["live"]}
		],
		"offline_payloads": ["ghcr.io/a/b"]
	}`
	var r MediaRecipe
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if r.MediaName != "test" || r.Size != "30G" {
		t.Errorf("top-level fields not parsed: %+v", r)
	}
	if len(r.BootableEnvironments) != 2 {
		t.Fatalf("want 2 environments, got %d", len(r.BootableEnvironments))
	}
	if r.BootableEnvironments[1].Backend != "composefs" {
		t.Errorf("backend override lost: %+v", r.BootableEnvironments[1])
	}
	modes := r.BootableEnvironments[0].Modes
	if len(modes) != 2 || modes[0] != ModeLive || modes[1] != ModePersistent {
		t.Errorf("modes parsed wrong: %v", modes)
	}
	if len(r.OfflinePayloads) != 1 {
		t.Errorf("offline_payloads lost: %v", r.OfflinePayloads)
	}
}
