package store

import (
	"context"
	"sort"
	"time"
)

// What is where: a project's work travels through environments, and the
// question a release manager asks before shipping is what production is
// running and what is waiting behind it. A deployment says which work items it
// carried, so the answer is in the deliveries the project has already
// recorded.

// EnvironmentState is one environment type as it stands: the last successful
// deployment to it, and the work that deployment carried.
type EnvironmentState struct {
	Type string
	// Pipeline and Name are what the provider called the last deployment's
	// pipeline and environment.
	Pipeline string
	Name     string
	// LastDeployed is when it last took a successful deployment.
	LastDeployed time.Time
	// State is the state of the last deployment, successful or otherwise, so
	// an environment whose last attempt failed says so.
	State string
	// IssueKeys are the work items the last successful deployment carried,
	// and Waiting the ones that have reached the environment before it in
	// the order below but not this one.
	IssueKeys []string
	Waiting   []string
}

// EnvironmentOrder is the way work travels: earlier environments feed later
// ones, which is what makes "waiting" mean anything.
var EnvironmentOrder = []string{"development", "testing", "staging", "production"}

// ProjectEnvironments reads what each environment of a project is running.
// Only the deployments that name work the viewer can browse are read, so an
// environment shows what this person may know about.
func (s *Store) ProjectEnvironments(ctx context.Context, workspaceID, projectID, userID string) ([]EnvironmentState, error) {
	rows, err := s.Pool.Query(ctx, `
		WITH visible AS (
		  SELECT i.key FROM issues i
		  WHERE i.workspace_id=$1 AND i.project_id=$2 AND `+VisibleIssuePredicate("i", "$3")+`
		), latest AS (
		  SELECT DISTINCT ON (pipeline_id,environment_id,entity_sequence_number)
		         pipeline_id,environment_id,entity_sequence_number,issue_keys,state,environment_type,
		         COALESCE(payload->'environment'->>'displayName','') AS environment_name,occurred_at
		  FROM software_delivery_facts
		  WHERE workspace_id=$1 AND fact_type='deployment'
		  ORDER BY pipeline_id,environment_id,entity_sequence_number,update_sequence_number DESC
		)
		SELECT environment_type,pipeline_id,environment_name,state,occurred_at,issue_keys
		FROM latest
		WHERE EXISTS (SELECT 1 FROM visible v WHERE v.key=ANY(latest.issue_keys))
		ORDER BY environment_type,occurred_at DESC`, workspaceID, projectID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type deployment struct {
		pipeline, name, state string
		at                    time.Time
		keys                  []string
	}
	byType := map[string][]deployment{}
	for rows.Next() {
		var environmentType string
		var found deployment
		if err := rows.Scan(&environmentType, &found.pipeline, &found.name, &found.state, &found.at, &found.keys); err != nil {
			return nil, err
		}
		byType[environmentType] = append(byType[environmentType], found)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Every environment this project has deployed to, in the order work
	// travels, with anything the provider named that is not in that order
	// after it.
	order := append([]string{}, EnvironmentOrder...)
	for environmentType := range byType {
		if !containsString(order, environmentType) {
			order = append(order, environmentType)
		}
	}
	states := []EnvironmentState{}
	shipped := map[string]bool{}
	for _, environmentType := range order {
		deployments := byType[environmentType]
		if len(deployments) == 0 {
			continue
		}
		state := EnvironmentState{Type: environmentType}
		carried := map[string]bool{}
		for _, found := range deployments {
			if state.State == "" || found.at.After(state.LastDeployed) && state.State != "successful" {
				state.State, state.Pipeline, state.Name = found.state, found.pipeline, found.name
			}
			if found.state != "successful" {
				continue
			}
			if found.at.After(state.LastDeployed) {
				state.LastDeployed, state.Pipeline, state.Name, state.State = found.at, found.pipeline, found.name, found.state
			}
			for _, key := range found.keys {
				carried[key] = true
			}
		}
		for key := range carried {
			state.IssueKeys = append(state.IssueKeys, key)
		}
		sort.Strings(state.IssueKeys)
		// What reached an earlier environment and not this one is waiting to
		// be promoted into it.
		for key := range shipped {
			if !carried[key] {
				state.Waiting = append(state.Waiting, key)
			}
		}
		sort.Strings(state.Waiting)
		for key := range carried {
			shipped[key] = true
		}
		states = append(states, state)
	}
	return states, nil
}
