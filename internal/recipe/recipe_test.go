package recipe

import (
	"encoding/json"
	"testing"
)

func TestRecipeRoundtrip(t *testing.T) {
	raw := `{
		"media_name": "test",
		"size": "30G",
		"shared_store": {"format": "ext4", "compression": "zstd", "dedup": true},
		"bootable_environments": [
			{"id": "bluefin", "image": "ghcr.io/x/y", "title": "Bluefin (GNOME)", "modes": ["live", "persistent"]},
			{"id": "dakota", "image": "ghcr.io/a/b", "backend": "composefs", "skip_initramfs_rebuild": true, "modes": ["live"]}
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
	if !r.SharedStore.Dedup {
		t.Errorf("shared_store.dedup lost: %+v", r.SharedStore)
	}
	if len(r.BootableEnvironments) != 2 {
		t.Fatalf("want 2 environments, got %d", len(r.BootableEnvironments))
	}
	if r.BootableEnvironments[1].Backend != "composefs" {
		t.Errorf("backend override lost: %+v", r.BootableEnvironments[1])
	}
	if r.BootableEnvironments[0].Title != "Bluefin (GNOME)" {
		t.Errorf("title lost: %+v", r.BootableEnvironments[0])
	}
	if !r.BootableEnvironments[1].SkipInitramfsRebuild {
		t.Errorf("skip_initramfs_rebuild lost: %+v", r.BootableEnvironments[1])
	}
	if r.BootableEnvironments[0].SkipInitramfsRebuild {
		t.Errorf("skip_initramfs_rebuild should default to false: %+v", r.BootableEnvironments[0])
	}
	modes := r.BootableEnvironments[0].Modes
	if len(modes) != 2 || modes[0] != ModeLive || modes[1] != ModePersistent {
		t.Errorf("modes parsed wrong: %v", modes)
	}
	if len(r.OfflinePayloads) != 1 {
		t.Errorf("offline_payloads lost: %v", r.OfflinePayloads)
	}
	if got := r.OfflinePayloads[0]; got.Source != "ghcr.io/a/b" || got.Ref != "ghcr.io/a/b" {
		t.Errorf("string offline payload = %+v", got)
	}
}

func TestOfflinePayloadSourceAndRef(t *testing.T) {
	var r MediaRecipe
	err := json.Unmarshal([]byte(`{"offline_payloads":[{"source":"localhost/os:dev","ref":"ghcr.io/example/os:stable"}]}`), &r)
	if err != nil {
		t.Fatal(err)
	}
	got := r.OfflinePayloads[0]
	if got.Source != "localhost/os:dev" || got.Ref != "ghcr.io/example/os:stable" {
		t.Errorf("payload = %+v", got)
	}
}
