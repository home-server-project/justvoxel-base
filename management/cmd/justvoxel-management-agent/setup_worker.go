package main

import (
	"context"
	"log"
)

var startSetupWorker = func(s *server, operationID string, plan adminSetupNormalizedPlan, smbPassword []byte) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), setupWorkerTimeout)
		defer cancel()
		if err := executeSetupTransaction(ctx, s.operations, operationID, &plan, smbPassword); err != nil {
			s.operations.appendSetupWorkerErrorBestEffort(operationID, err)
			log.Printf("setup operation %s finished with error: %v", operationID, err)
		}
	}()
}
