package recipe

type SharedStore struct {
	Format      string `json:"format"`
	Compression string `json:"compression"`
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
	MediaName             string                `json:"media_name"`
	Size                  string                `json:"size"`
	SharedStore           SharedStore           `json:"shared_store"`
	BootableEnvironments []BootableEnvironment `json:"bootable_environments"`
	OfflinePayloads      []string              `json:"offline_payloads"`
}
