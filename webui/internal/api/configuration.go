package api

type AdminConfigurationChangeRequest struct {
	WhitelistEnabled   *bool  `json:"whitelist_enabled,omitempty"`
	GameMode           string `json:"game_mode"`
	JavaMemory         string `json:"java_memory"`
	ContainerMemory    string `json:"container_memory"`
	JavaPort           int    `json:"java_port"`
	BedrockEnabled     bool   `json:"bedrock_enabled"`
	BedrockPort        int    `json:"bedrock_port"`
	Timezone           string `json:"timezone"`
	MaxPlayers         int    `json:"max_players"`
	MOTD               string `json:"motd"`
	ImageTag           string `json:"image_tag"`
	VersionPolicy      string `json:"version_policy"`
	Version            string `json:"version"`
	BackupKeep         int    `json:"backup_keep"`
	BackupSchedule     string `json:"backup_schedule"`
	BackupTimerEnabled bool   `json:"backup_timer_enabled"`
	ConfirmPlayers     bool   `json:"confirm_players"`
}

type AdminConfigurationChange struct {
	Field           string `json:"field"`
	Label           string `json:"label"`
	Before          string `json:"before"`
	After           string `json:"after"`
	RestartRequired bool   `json:"restart_required"`
}

type AdminConfigurationChangeResponse struct {
	OK                    bool                        `json:"ok"`
	Error                 string                      `json:"error,omitempty"`
	Changes               []AdminConfigurationChange  `json:"changes"`
	Warnings              []string                    `json:"warnings"`
	CompatibilityBlocked  bool                        `json:"compatibility_blocked"`
	RestartRequired       bool                        `json:"restart_required"`
	MemoryRestartRequired bool                        `json:"memory_restart_required"`
	MemoryRemainingMiB    int                         `json:"memory_remaining_mib"`
	Proposed              AdminConfigurationDiscovery `json:"proposed"`
	Applied               bool                        `json:"applied"`
	ConfirmationRequired  bool                        `json:"confirmation_required"`
	Online                int                         `json:"online"`
	Players               []string                    `json:"players"`
	Restarted             bool                        `json:"restarted"`
	RestartDeferred       bool                        `json:"restart_deferred"`
	Message               string                      `json:"message,omitempty"`
}
