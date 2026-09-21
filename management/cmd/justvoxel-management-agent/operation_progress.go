package main

import (
	"errors"
	"time"
)

func (s *operationStore) updateProgress(id string, expected operationState, stage, status string) (operationJournal, error) {
	if !validOperationID(id) {
		return operationJournal{}, errOperationNotFound
	}
	if !validOperationState(expected) {
		return operationJournal{}, errors.New("invalid operation state")
	}
	if stage == "" || !operationStagePattern.MatchString(stage) {
		return operationJournal{}, errors.New("invalid operation stage")
	}
	if !validOperationStatus(status) {
		return operationJournal{}, errors.New("invalid operation status")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	journal, ok := s.operations[id]
	if !ok {
		return operationJournal{}, errOperationNotFound
	}
	if journal.State != expected {
		return operationJournal{}, errors.New("operation state changed while updating progress")
	}
	journal.Stage = stage
	journal.Status = status
	journal.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	if err := s.persist(journal); err != nil {
		return operationJournal{}, err
	}
	s.operations[id] = journal
	s.appendSetupJournalDiagnosticBestEffort(journal)
	return journal, nil
}
