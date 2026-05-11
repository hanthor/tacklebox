package recipe

type SharedStore struct {
	Format      string `json:"format"`
	Compression string `json:"compression"`
}

// Partitions lets a recipe override the auto-computed partition layout.
// Any field left empty falls back to defaults: ESP=1G, Persist=2G, Store=
// total - ESP - Persist. Sizes accept the same forms as MediaRecipe.Size
// (e.g. "1G", "512M", "8192M").
type Partitions struct {
	ESP     string `json:"esp,omitempty"`
	Store   string `json:"store,omitempty"`
	Persist string `json:"persist,omitempty"`
}

type BootMode string

const (
	ModeLive       BootMode = "live"
	ModePersistent BootMode = "persistent"
)

type BootableEnvironment struct {
	ID      string     `json:"id"`
	Image   string     `json:"image"`
	Desktop string     `json:"desktop"`
	Backend string     `json:"backend"`
	Modes   []BootMode `json:"modes"`
}

type MediaRecipe struct {
	MediaName            string                `json:"media_name"`
	Size                 string                `json:"size"`
	SharedStore          SharedStore           `json:"shared_store"`
	Partitions           Partitions            `json:"partitions,omitempty"`
	BootableEnvironments []BootableEnvironment `json:"bootable_environments"`
	OfflinePayloads      []string              `json:"offline_payloads"`
}
