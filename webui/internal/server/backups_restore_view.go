package server

// Presentation only: selected Restore remains authoritative through its instance API.
type backupsRestoreRequest struct{ BackupID, Mode string }
type backupsRestoreCurrent struct{ Version, VersionMode string }
type backupsRestoreMetadata struct {
	Minecraft struct{ ConfiguredVersion, VersionMode string }
}
type backupsRestoreNormalized struct {
	BackupID, CreatedAt, MetadataStatus, VersionRelation string
	Current                                              backupsRestoreCurrent
	Metadata                                             *backupsRestoreMetadata
}
type backupsRestoreRequirements struct {
	DestructiveConfirmationRequired, PlayersConfirmationRequired bool
	Online                                                       int
	Players                                                      []string
}
type backupsRestorePlan struct {
	OK              bool
	PlanFingerprint string
	Normalized      *backupsRestoreNormalized
	Requirements    *backupsRestoreRequirements
	Warnings        []struct{ Message string }
}
