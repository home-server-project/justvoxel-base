package main

// claimMinecraftMutation follows deletion's existing order: backup worker gate,
// operation mutex/current operation IDs, persistent claims, restore exclusion,
// maintenance. claimInstanceRestore owns the operation mutex until close, so
// process-tracked persistent locks can never be mistaken for a second owner's
// successful acquisition. Every acquisition is nonblocking.
func (s *server) claimMinecraftMutation() (func() error, error) {
	if !instanceBackupWorkerMu.TryLock() {
		return nil, errInstanceBusy
	}
	// Like Delete, Apply needs active mutation exclusion, without requiring an
	// independent runtime inventory (Original can exist without any instances).
	claim, err := s.claimInstanceRestore(true)
	if err != nil {
		instanceBackupWorkerMu.Unlock()
		return nil, err
	}
	return func() error {
		defer instanceBackupWorkerMu.Unlock()
		return claim.close()
	}, nil
}
