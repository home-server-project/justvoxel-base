package main

import (
	"context"
	"fmt"
	"time"
)

// Reconcile only a recorded update failure, never an interrupted update or a retry.
// serve calls this before accepting requests, while the restored journal owns
// the existing shared maintenance locks.
func reconcileMinecraftUpdateFailureAtStartup(operations *operationStore) error {
	journal, err := operations.currentMinecraftMaintenance()
	if err != nil {
		return fmt.Errorf("read current Minecraft maintenance: %w", err)
	}
	if journal == nil || journal.State != operationNeedsAttention || journal.Stage != "update_failed" || !instanceIDPattern.MatchString(journal.InstanceID) {
		return nil
	}
	if journal.OperationType != operationTypeMinecraftContainerUpdate && journal.OperationType != operationTypeMinecraftUpdate {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	err = verifyMinecraftFailedUpdateSafeToRelease(ctx, journal.InstanceID)
	if err != nil {
		return fmt.Errorf("verify current Minecraft runtime for %s: %w", journal.InstanceID, err)
	}
	_, err = operations.transition(journal.OperationID, operationResolved, "resolved", "Previous update failed. Closed during startup reconciliation because the currently running committed runtime was independently verified and kept. No update was retried.")
	if err != nil {
		return fmt.Errorf("resolve previous Minecraft update failure: %w", err)
	}
	return nil
}
