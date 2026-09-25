package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

type dataMigrationOrchestrationAPI interface {
	AdminDataMigrationPlan(ctx context.Context, session string, request api.AdminDataMigrationPlanRequest) (api.AdminDataMigrationPlanResponse, error)
	AdminDataMigrationApply(ctx context.Context, session string, request api.AdminDataMigrationApplyRequest) (api.AdminDataMigrationApplyResponse, error)
	AdminCurrentDataMigrationOperation(ctx context.Context, session string) (api.PersistentOperationResponse, error)
	AdminOperation(ctx context.Context, session, id string) (api.PersistentOperationResponse, error)
}

type dataMigrationApplyProof struct {
	PlanFingerprint    string
	MigrationConfirmed bool
	Confirmation       string
	PlayersConfirmed   bool
}

type dataMigrationOrchestrationError struct {
	StatusCode int
	Message    string
	Plan       api.AdminDataMigrationPlanResponse
}

func (e *dataMigrationOrchestrationError) Error() string {
	return e.Message
}

func dataMigrationPlanReviewed(ctx context.Context, client dataMigrationOrchestrationAPI, session string, request api.AdminDataMigrationPlanRequest) (api.AdminDataMigrationPlanResponse, error) {
	plan, err := client.AdminDataMigrationPlan(ctx, session, request)
	if err != nil {
		return plan, &dataMigrationOrchestrationError{
			StatusCode: dataMigrationErrorStatus(err),
			Message:    apiMessage(err, "Minecraft data migration planning is unavailable."),
			Plan:       plan,
		}
	}
	if !plan.OK || plan.Normalized == nil || plan.Requirements == nil {
		return plan, &dataMigrationOrchestrationError{
			StatusCode: http.StatusBadGateway,
			Message:    "Minecraft data migration returned an incomplete Review plan.",
			Plan:       plan,
		}
	}
	return plan, nil
}

func dataMigrationApplyReviewed(ctx context.Context, client dataMigrationOrchestrationAPI, session string, request api.AdminDataMigrationPlanRequest, proof dataMigrationApplyProof) (api.AdminDataMigrationPlanResponse, *api.PersistentOperation, error) {
	plan, err := dataMigrationPlanReviewed(ctx, client, session, request)
	if err != nil {
		return plan, nil, err
	}
	if proof.PlanFingerprint == "" || proof.PlanFingerprint != plan.PlanFingerprint {
		return plan, nil, &dataMigrationOrchestrationError{
			StatusCode: http.StatusConflict,
			Message:    "This migration plan changed since Review. Review the current target again before continuing.",
			Plan:       plan,
		}
	}
	if !proof.MigrationConfirmed {
		return plan, nil, &dataMigrationOrchestrationError{
			StatusCode: http.StatusBadRequest,
			Message:    "Confirm this reviewed Minecraft data migration before starting it.",
			Plan:       plan,
		}
	}
	if plan.Requirements.PlayersConfirmationRequired && !proof.PlayersConfirmed {
		return plan, nil, &dataMigrationOrchestrationError{
			StatusCode: http.StatusBadRequest,
			Message:    "Confirm that online players may be interrupted before starting migration.",
			Plan:       plan,
		}
	}
	if plan.Requirements.DestructiveConfirmationRequired && proof.Confirmation != plan.Requirements.ConfirmationPhrase {
		return plan, nil, &dataMigrationOrchestrationError{
			StatusCode: http.StatusBadRequest,
			Message:    "The destructive storage confirmation does not match the exact reviewed phrase.",
			Plan:       plan,
		}
	}
	result, err := client.AdminDataMigrationApply(ctx, session, api.AdminDataMigrationApplyRequest{
		PlanFingerprint:    plan.PlanFingerprint,
		Request:            request,
		MigrationConfirmed: true,
		Confirmation:       proof.Confirmation,
		PlayersConfirmed:   proof.PlayersConfirmed,
	})
	if err != nil {
		return plan, nil, &dataMigrationOrchestrationError{
			StatusCode: dataMigrationErrorStatus(err),
			Message:    apiMessage(err, "Could not start Minecraft data migration."),
			Plan:       plan,
		}
	}
	if !result.OK || result.Operation == nil || result.Operation.OperationType != "data_migration" {
		return plan, nil, &dataMigrationOrchestrationError{
			StatusCode: http.StatusBadGateway,
			Message:    "Minecraft data migration did not return a valid persistent operation.",
			Plan:       plan,
		}
	}
	return plan, result.Operation, nil
}

func dataMigrationCurrentOperation(ctx context.Context, client dataMigrationOrchestrationAPI, session string) (*api.PersistentOperation, error) {
	response, err := client.AdminCurrentDataMigrationOperation(ctx, session)
	if err != nil {
		return nil, err
	}
	if response.Operation == nil {
		return nil, nil
	}
	if response.Operation.OperationType != "data_migration" {
		return nil, &dataMigrationOrchestrationError{
			StatusCode: http.StatusBadGateway,
			Message:    "Minecraft data migration operation status is invalid.",
		}
	}
	return response.Operation, nil
}

func dataMigrationOperation(ctx context.Context, client dataMigrationOrchestrationAPI, session, id string) (*api.PersistentOperation, error) {
	response, err := client.AdminOperation(ctx, session, id)
	if err != nil {
		return nil, err
	}
	if response.Operation == nil || response.Operation.OperationType != "data_migration" {
		return nil, &dataMigrationOrchestrationError{
			StatusCode: http.StatusNotFound,
			Message:    "Minecraft data migration operation not found.",
		}
	}
	return response.Operation, nil
}

func dataMigrationErrorStatus(err error) int {
	var orchestrationErr *dataMigrationOrchestrationError
	if errors.As(err, &orchestrationErr) && orchestrationErr.StatusCode >= 400 && orchestrationErr.StatusCode <= 599 {
		return orchestrationErr.StatusCode
	}
	var responseErr *api.ResponseError
	if errors.As(err, &responseErr) && responseErr.StatusCode >= 400 && responseErr.StatusCode <= 599 {
		return responseErr.StatusCode
	}
	if errors.Is(err, api.ErrUnauthorized) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, api.ErrPasswordChangeRequired) {
		return http.StatusForbidden
	}
	return http.StatusBadGateway
}

func dataMigrationErrorMessage(err error, fallback string) string {
	var orchestrationErr *dataMigrationOrchestrationError
	if errors.As(err, &orchestrationErr) && orchestrationErr.Message != "" {
		return orchestrationErr.Message
	}
	return apiMessage(err, fallback)
}
