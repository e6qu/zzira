package commands

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/e6qu/zzira/internal/models"
)

// deploymentEnvironmentTypes are the environment types Jira deployments use.
var deploymentEnvironmentTypes = []string{"unmapped", "development", "testing", "staging", "production"}

// SetServiceDeploymentGate connects a deployment provider, an installed app or
// "*" for any, to a service desk and gates the chosen environment types; no
// types turns gating off.
func (s *Service) SetServiceDeploymentGate(ctx context.Context, actorID, workspaceID, serviceDeskID, providerKey string, environmentTypes []string) error {
	if err := s.requireServiceDeskAdmin(ctx, workspaceID, serviceDeskID, actorID); err != nil {
		return err
	}
	for _, environmentType := range environmentTypes {
		if !slices.Contains(deploymentEnvironmentTypes, environmentType) {
			return fmt.Errorf("environment type %q is not a Jira deployment environment type", environmentType)
		}
	}
	providerKey = strings.TrimSpace(providerKey)
	if len(environmentTypes) > 0 && providerKey != "*" {
		installations, err := s.Store.AppInstallations(ctx, workspaceID)
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(installations, func(installation *models.AppInstallation) bool { return installation.Key == providerKey }) {
			return fmt.Errorf("deployment provider %q is not an installed app", providerKey)
		}
	}
	return s.Store.SetServiceDeploymentGate(ctx, workspaceID, serviceDeskID, providerKey, environmentTypes)
}

// GateDeployment opens the change request a gating service desk requires for
// a deployment, once per deployment. The provider is the submitting app; any
// submitter matches a desk gating every provider. A gate that cannot open its
// change request is recorded, so the deployment's gating status is invalid.
func (s *Service) GateDeployment(ctx context.Context, workspaceID, submitterID string, deployment models.SoftwareDeployment) error {
	if _, gated, err := s.Store.DeploymentGating(ctx, workspaceID, deployment.PipelineID, deployment.EnvironmentID, deployment.DeploymentSequenceNumber); err != nil || gated {
		return err
	}
	installations, err := s.Store.AppInstallations(ctx, workspaceID)
	if err != nil {
		return err
	}
	providerKey := ""
	for _, installation := range installations {
		if installation.PrincipalID == submitterID {
			providerKey = installation.Key
		}
	}
	desks, err := s.Store.DeploymentGates(ctx, workspaceID, providerKey, deployment.EnvironmentType)
	if err != nil || len(desks) == 0 {
		return err
	}
	issueID, gateErr := s.openDeploymentChangeRequest(ctx, workspaceID, desks[0], submitterID, deployment)
	recordErr := s.Store.RecordDeploymentGating(ctx, workspaceID, deployment.PipelineID, deployment.EnvironmentID, deployment.DeploymentSequenceNumber, issueID)
	return errors.Join(gateErr, recordErr)
}

func (s *Service) openDeploymentChangeRequest(ctx context.Context, workspaceID, serviceDeskID, submitterID string, deployment models.SoftwareDeployment) (string, error) {
	desk, err := s.Store.ServiceDesk(ctx, workspaceID, serviceDeskID)
	if err != nil {
		return "", err
	}
	requestTypes, err := s.Store.ServiceRequestTypes(ctx, workspaceID, desk.ID, "")
	if err != nil {
		return "", err
	}
	changeTypeID := ""
	for _, requestType := range requestTypes {
		words := strings.FieldsFunc(strings.ToLower(requestType.Name), func(value rune) bool { return !unicode.IsLetter(value) && !unicode.IsDigit(value) })
		if slices.Contains(words, "change") {
			changeTypeID = requestType.ID
			break
		}
	}
	if changeTypeID == "" {
		return "", fmt.Errorf("service desk %s has no change request type", desk.ProjectKey)
	}
	project, err := s.Store.ProjectByIDOrKey(ctx, workspaceID, desk.ProjectID)
	if err != nil {
		return "", err
	}
	actorID := project.LeadAccountID
	if actorID == "" {
		actorID = submitterID
	}
	if err := s.Store.EnrollServiceCustomer(ctx, workspaceID, submitterID); err != nil {
		return "", err
	}
	environment := deployment.EnvironmentName
	if environment == "" {
		environment = deployment.EnvironmentID
	}
	summary := "Deploy " + deployment.DisplayName + " to " + environment
	if len(summary) > 255 {
		summary = summary[:255]
	}
	description := "Deployment gating holds this deployment until the change is approved."
	if deployment.URL != "" {
		description += "\n\nDeployment: " + deployment.URL
	}
	if len(deployment.IssueKeys) > 0 {
		description += "\n\nWork items: " + strings.Join(deployment.IssueKeys, ", ")
	}
	request, err := s.CreateServiceRequest(ctx, CreateServiceRequestInput{
		ActorID: actorID, WorkspaceID: workspaceID, ServiceDeskID: desk.ID, RequestTypeID: changeTypeID,
		CustomerID: submitterID, Channel: "api", Summary: summary, Description: description,
	})
	if err != nil {
		return "", err
	}
	return request.Issue.ID, nil
}

// DeploymentGatingStatus is a deployment's gating status and the change
// request deciding it: allowed when ungated; invalid when gating failed;
// prevented once an approval is declined; awaiting while approvals are
// pending; allowed once approved, or completed without approvals.
func (s *Service) DeploymentGatingStatus(ctx context.Context, workspaceID string, deployment models.SoftwareDeployment) (string, *models.Issue, error) {
	issueID, gated, err := s.Store.DeploymentGating(ctx, workspaceID, deployment.PipelineID, deployment.EnvironmentID, deployment.DeploymentSequenceNumber)
	if err != nil {
		return "", nil, err
	}
	if !gated {
		return "allowed", nil, nil
	}
	if issueID == "" {
		return "invalid", nil, nil
	}
	issue, err := s.Store.IssueByIDOrKey(ctx, workspaceID, issueID)
	if err != nil {
		return "invalid", nil, nil
	}
	approvals, err := s.Store.ServiceApprovals(ctx, issueID)
	if err != nil {
		return "", nil, err
	}
	pending, approved := false, false
	for _, approval := range approvals {
		switch approval.FinalDecision {
		case "declined":
			return "prevented", issue, nil
		case "pending":
			pending = true
		case "approved":
			approved = true
		}
	}
	if !pending && (approved || strings.EqualFold(issue.Status.Category, "done")) {
		return "allowed", issue, nil
	}
	return "awaiting", issue, nil
}
