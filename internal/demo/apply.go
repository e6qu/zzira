package demo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Result is what applying a scenario produced: the workspace it built and the
// credentials a person can sign in with.
type Result struct {
	WorkspaceID string
	Slug        string
	// Tokens are API tokens by email, for the people the scenario declared.
	Tokens map[string]string
	// Passwords are sign-in passwords by email.
	Passwords map[string]string
}

// Applier builds a site from a scenario. Everything it writes goes through the
// ordinary command layer, so permissions, the action log, notifications and
// search behave exactly as they do for a person; only the timestamps are the
// scenario's rather than now.
type Applier struct {
	Store    *store.Store
	Commands *commands.Service
	Clock    Clock

	workspaceID string
	admin       string
	people      map[string]string
	groups      map[string]string
	projects    map[string]*models.Project
	desks       map[string]string
	requestType map[string]string
	versions    map[string]*models.Version
	sprints     map[string]*models.Sprint
	boards      map[string]*models.Board
	fields      map[string]string
	items       map[string]*models.Issue
	spaces      map[string]string
	stamps      []stamp
	ordinal     int
}

// stamp records that everything written between two action sequences happened
// at a moment in the scenario's history.
type stamp struct {
	from, to int64
	at       time.Time
}

// Apply builds the scenario's site and returns how to sign in to it.
func Apply(ctx context.Context, st *store.Store, cmds *commands.Service, scenario *Scenario, clock Clock) (*Result, error) {
	if err := scenario.Validate(); err != nil {
		return nil, err
	}
	applier := &Applier{
		Store: st, Commands: cmds, Clock: clock,
		people: map[string]string{}, groups: map[string]string{}, projects: map[string]*models.Project{},
		desks: map[string]string{}, requestType: map[string]string{}, versions: map[string]*models.Version{},
		sprints: map[string]*models.Sprint{}, boards: map[string]*models.Board{}, fields: map[string]string{},
		items: map[string]*models.Issue{}, spaces: map[string]string{},
	}
	return applier.run(ctx, scenario)
}

func (a *Applier) run(ctx context.Context, scenario *Scenario) (*Result, error) {
	result := &Result{Slug: scenario.Site.Slug, Tokens: map[string]string{}, Passwords: map[string]string{}}
	if err := a.workspace(ctx, scenario.Site); err != nil {
		return nil, err
	}
	result.WorkspaceID = a.workspaceID
	if err := a.accounts(ctx, scenario, result); err != nil {
		return nil, err
	}
	if err := a.hierarchy(ctx, scenario.Hierarchy); err != nil {
		return nil, err
	}
	if err := a.customFields(ctx, scenario.CustomFields); err != nil {
		return nil, err
	}
	for _, project := range scenario.Projects {
		if err := a.project(ctx, project); err != nil {
			return nil, fmt.Errorf("project %s: %w", project.ID, err)
		}
	}
	for _, item := range scenario.WorkItems {
		if err := a.workItem(ctx, item); err != nil {
			return nil, fmt.Errorf("work item %s: %w", item.ID, err)
		}
	}
	// Sprints start and close once their work is in them, and versions ship
	// once the work that went into them is done, as they do in a real site.
	if err := a.startSprints(ctx, scenario); err != nil {
		return nil, err
	}
	if err := a.releaseVersions(ctx, scenario); err != nil {
		return nil, err
	}
	if err := a.deployments(ctx, scenario.Deployments); err != nil {
		return nil, err
	}
	if scenario.Service != nil {
		if err := a.service(ctx, scenario.Service); err != nil {
			return nil, err
		}
	}
	if scenario.Wiki != nil {
		if err := a.wiki(ctx, scenario.Wiki); err != nil {
			return nil, err
		}
	}
	if err := a.filtersAndDashboards(ctx, scenario); err != nil {
		return nil, err
	}
	if err := a.retime(ctx); err != nil {
		return nil, fmt.Errorf("retime the history: %w", err)
	}
	return result, nil
}

// at runs one piece of the scenario and records when it happened, so the
// retiming pass can move its action log entries into the past.
func (a *Applier) at(ctx context.Context, day int, write func() error) error {
	before, err := a.sequence(ctx)
	if err != nil {
		return err
	}
	if err := write(); err != nil {
		return err
	}
	after, err := a.sequence(ctx)
	if err != nil {
		return err
	}
	if after > before {
		a.ordinal++
		a.stamps = append(a.stamps, stamp{from: before, to: after, at: a.Clock.At(day, a.ordinal)})
	}
	return nil
}

// sequence reads the workspace's action sequence.
func (a *Applier) sequence(ctx context.Context) (int64, error) {
	var seq int64
	err := a.Store.Pool.QueryRow(ctx, `SELECT COALESCE(max(seq),0) FROM actions WHERE workspace_id=$1`, a.workspaceID).Scan(&seq)
	return seq, err
}

// workspace finds or creates the site the scenario describes.
func (a *Applier) workspace(ctx context.Context, site Site) error {
	if id, err := a.Store.WorkspaceBySlug(ctx, site.Slug); err == nil && id != "" {
		a.workspaceID = id
		return nil
	}
	id := store.NewID("ws")
	if _, err := a.Store.Pool.Exec(ctx,
		`INSERT INTO workspaces(id,slug,name,seq) VALUES($1,$2,$3,0)`, id, site.Slug, site.Name); err != nil {
		return fmt.Errorf("create the site: %w", err)
	}
	a.workspaceID = id
	return nil
}

// accounts creates the people and groups, and the credentials for signing in.
func (a *Applier) accounts(ctx context.Context, scenario *Scenario, result *Result) error {
	for _, person := range scenario.People {
		password := person.Password
		if password == "" {
			password = "demo1234"
		}
		hash, err := authn.HashPassword(password)
		if err != nil {
			return err
		}
		// A person who already has an account keeps it, so a scenario can be
		// applied to a site someone has already signed in to.
		userID, _, _, err := a.Store.UserByEmail(ctx, person.Email)
		if err != nil || userID == "" {
			created, createErr := a.Store.CreateUser(ctx, store.NewID("usr"), person.Email, hash, person.DisplayName)
			if createErr != nil {
				return fmt.Errorf("create %s: %w", person.Email, createErr)
			}
			userID = created.ID
		}
		user := &models.User{ID: userID}
		a.people[person.ID] = user.ID
		result.Passwords[person.Email] = password
		// A customer has no seat on the site; everyone else is a member, and
		// the first administrator applies the rest of the scenario.
		if !person.Customer {
			role := person.Role
			if role == "" {
				role = "member"
			}
			if err := a.Store.AddMember(ctx, a.workspaceID, user.ID, role); err != nil {
				return err
			}
			if role == "admin" && a.admin == "" {
				a.admin = user.ID
			}
		}
		plain, hashed, err := authn.NewAPIToken()
		if err != nil {
			return err
		}
		if err := a.Store.CreateAPIToken(ctx, store.NewID("tok"), user.ID, hashed, "demo"); err != nil {
			return err
		}
		result.Tokens[person.Email] = plain
		if person.Customer {
			// A customer reaches the portal without a seat on the site.
			if err := a.Store.EnrollServiceCustomer(ctx, a.workspaceID, userID); err != nil {
				return fmt.Errorf("enrol %s as a customer: %w", person.Email, err)
			}
		}
	}
	if a.admin == "" {
		return fmt.Errorf("the scenario needs one person with the admin role")
	}
	for _, group := range scenario.Groups {
		created, err := a.Store.CreateWikiGroup(ctx, a.workspaceID, a.admin, group.Name)
		if err != nil {
			return fmt.Errorf("create group %s: %w", group.Name, err)
		}
		a.groups[group.ID] = created.ID
	}
	for _, person := range scenario.People {
		for _, group := range person.Groups {
			if err := a.Store.SetWikiGroupMembership(ctx, a.workspaceID, a.admin, a.groups[group], a.people[person.ID], true); err != nil {
				return fmt.Errorf("add %s to %s: %w", person.Email, group, err)
			}
		}
	}
	return nil
}

// hierarchy adds the levels above Epic and puts work types on them, which is
// what Jira's work type hierarchy settings do.
func (a *Applier) hierarchy(ctx context.Context, levels []HierarchyLevel) error {
	for _, level := range levels {
		if _, err := a.Store.AddHierarchyLevel(ctx, a.workspaceID, a.admin, level.Name); err != nil {
			return fmt.Errorf("hierarchy level %s: %w", level.Name, err)
		}
		for _, workType := range level.WorkTypes {
			if _, err := a.Store.IssueTypeByIDOrName(ctx, a.workspaceID, workType); err != nil {
				if _, err := a.Store.CreateIssueType(ctx, a.workspaceID, workType,
					fmt.Sprintf("Work at the %s level.", level.Name), "standard", nil); err != nil {
					return fmt.Errorf("work type %s: %w", workType, err)
				}
			}
			if _, err := a.Store.SetWorkTypeHierarchyLevel(ctx, a.workspaceID, a.admin, workType, level.Level); err != nil {
				return fmt.Errorf("put %s on %s: %w", workType, level.Name, err)
			}
		}
	}
	return nil
}

// customFields registers the fields the work items use.
func (a *Applier) customFields(ctx context.Context, fields []CustomField) error {
	for _, field := range fields {
		seq, err := a.Store.NextCustomFieldNumber(ctx)
		if err != nil {
			return err
		}
		id := fmt.Sprintf("customfield_%d", seq)
		typeKey, known := models.CustomFieldTypeKeys[field.Type]
		if !known {
			return fmt.Errorf("custom field %s has the unknown type %q", field.ID, field.Type)
		}
		if _, err := a.Store.CreateWorkspaceCustomFieldOfKind(ctx, a.workspaceID, id, field.Name, field.Type, typeKey, ""); err != nil {
			return fmt.Errorf("create field %s: %w", field.Name, err)
		}
		a.fields[field.ID] = id
		if len(field.Options) > 0 {
			contexts, err := a.Store.CustomFieldContexts(ctx, a.workspaceID, id, nil)
			if err != nil {
				return err
			}
			if len(contexts) == 0 {
				continue
			}
			if _, err := a.Store.CreateCustomFieldOptions(ctx, a.workspaceID, a.admin, id, contexts[0].ID, field.Options); err != nil {
				return fmt.Errorf("add options to %s: %w", field.Name, err)
			}
		}
	}
	return nil
}

// project creates a project with its components, versions, board and sprints.
func (a *Applier) project(ctx context.Context, project Project) error {
	created, err := a.Commands.CreateProject(ctx, a.admin, a.workspaceID, commands.CreateProjectInput{
		Key: project.Key, Name: project.Name, Description: project.Description,
		LeadAccountID: a.people[project.Lead], ProjectTypeKey: project.Type, ProjectTemplateKey: project.Template,
	})
	if err != nil {
		var invalid *commands.ProjectValidationError
		if errors.As(err, &invalid) {
			return fmt.Errorf("%s: %v", err, invalid.Fields)
		}
		return err
	}
	a.projects[project.ID] = created
	for _, component := range project.Components {
		if _, err := a.Store.CreateComponent(ctx, a.workspaceID, a.admin, store.ComponentInput{
			ProjectIDOrKey: created.ID, Name: component.Name, LeadAccountID: a.people[component.Lead],
		}); err != nil {
			return fmt.Errorf("component %s: %w", component.Name, err)
		}
	}
	for _, version := range project.Versions {
		update := store.VersionUpdate{Name: &version.Name}
		if version.Description != "" {
			update.Description = &version.Description
		}
		if version.StartDay != nil {
			day := a.Clock.Day(*version.StartDay)
			update.StartDate = &day
		}
		if version.ReleaseDay != nil {
			day := a.Clock.Day(*version.ReleaseDay)
			update.ReleaseDate = &day
		}
		saved, err := a.Store.SaveVersion(ctx, a.workspaceID, a.admin, created.ID, "", update)
		if err != nil {
			return fmt.Errorf("version %s: %w", version.Name, err)
		}
		a.versions[version.ID] = saved
	}
	if project.Board != nil {
		if err := a.board(ctx, created, *project.Board); err != nil {
			return err
		}
	}
	if project.ServiceDesk != nil {
		if err := a.serviceDesk(ctx, project, created); err != nil {
			return err
		}
	}
	return nil
}

// board keeps the board a software project starts with, or creates one, and
// runs its sprints.
func (a *Applier) board(ctx context.Context, project *models.Project, declared Board) error {
	boards, err := a.Store.BoardsByProject(ctx, a.workspaceID, project.ID)
	if err != nil {
		return err
	}
	var board *models.Board
	if len(boards) > 0 {
		board = boards[0]
	} else {
		if board, err = a.Store.CreateBoard(ctx, a.admin, a.workspaceID, store.BoardCreate{
			Name: declared.Name, Type: declared.Type, ProjectID: project.ID,
		}); err != nil {
			return fmt.Errorf("board %s: %w", declared.Name, err)
		}
	}
	a.boards[declared.ID] = board
	for _, sprint := range declared.Sprints {
		created, _, err := a.Store.CreateSprint(ctx, a.admin, a.workspaceID, board.ID, sprint.Name, sprint.Goal)
		if err != nil {
			return fmt.Errorf("sprint %s: %w", sprint.Name, err)
		}
		a.sprints[sprint.ID] = created
	}
	return nil
}

// startSprints moves each sprint into the state the scenario declares, once
// its work is in it: Jira starts a sprint with its scope, and completing one
// moves unfinished work out.
func (a *Applier) startSprints(ctx context.Context, scenario *Scenario) error {
	for _, project := range scenario.Projects {
		if project.Board == nil {
			continue
		}
		for _, sprint := range project.Board.Sprints {
			state := sprint.State
			if state == "" || state == "future" {
				continue
			}
			created := a.sprints[sprint.ID]
			update := store.SprintUpdate{Name: sprint.Name, Goal: sprint.Goal, State: "active"}
			if sprint.StartDay != nil {
				start := a.Clock.At(*sprint.StartDay, 0)
				update.StartDate = &start
			}
			if sprint.EndDay != nil {
				end := a.Clock.At(*sprint.EndDay, 0)
				update.EndDate = &end
			}
			day := 0
			if sprint.StartDay != nil {
				day = *sprint.StartDay
			}
			if err := a.at(ctx, day, func() error {
				_, _, err := a.Store.UpdateSprint(ctx, a.admin, a.workspaceID, created.ID, update)
				return err
			}); err != nil {
				return fmt.Errorf("start sprint %s: %w", sprint.Name, err)
			}
			if state != "closed" {
				continue
			}
			closing := update
			closing.State = "closed"
			closeDay := 0
			if sprint.EndDay != nil {
				closeDay = *sprint.EndDay
			}
			if err := a.at(ctx, closeDay, func() error {
				_, _, err := a.Store.UpdateSprint(ctx, a.admin, a.workspaceID, created.ID, closing)
				return err
			}); err != nil {
				return fmt.Errorf("close sprint %s: %w", sprint.Name, err)
			}
		}
	}
	return nil
}

// releaseVersions ships the versions the scenario marks released, so release
// reports and the release hub have history.
func (a *Applier) releaseVersions(ctx context.Context, scenario *Scenario) error {
	for _, project := range scenario.Projects {
		for _, version := range project.Versions {
			if !version.Released {
				continue
			}
			saved := a.versions[version.ID]
			released := true
			day := 0
			if version.ReleaseDay != nil {
				day = *version.ReleaseDay
			}
			if err := a.at(ctx, day, func() error {
				_, err := a.Store.SaveVersion(ctx, a.workspaceID, a.admin, a.projects[project.ID].ID, saved.ID,
					store.VersionUpdate{Released: &released})
				return err
			}); err != nil {
				return fmt.Errorf("release %s: %w", version.Name, err)
			}
		}
	}
	return nil
}

// workItem raises one piece of work and replays everything that happened to it.
func (a *Applier) workItem(ctx context.Context, item WorkItem) error {
	project := a.projects[item.Project]
	fields := map[string]json.RawMessage{}
	for field, value := range item.Fields {
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		fields[a.fields[field]] = encoded
	}
	if len(item.Components) > 0 {
		names := make([]map[string]string, 0, len(item.Components))
		for _, component := range item.Components {
			names = append(names, map[string]string{"name": component})
		}
		encoded, err := json.Marshal(names)
		if err != nil {
			return err
		}
		fields["components"] = encoded
	}
	if len(item.FixVersions) > 0 {
		refs := make([]map[string]string, 0, len(item.FixVersions))
		for _, version := range item.FixVersions {
			refs = append(refs, map[string]string{"id": a.versions[version].ID})
		}
		encoded, err := json.Marshal(refs)
		if err != nil {
			return err
		}
		fields["fixVersions"] = encoded
	}
	reporter := a.people[item.Reporter]
	actor := reporter
	if actor == "" {
		actor = a.admin
	}
	parent := ""
	if item.Parent != "" {
		parent = a.items[item.Parent].ID
	}
	var created *models.Issue
	if err := a.at(ctx, item.CreatedDay, func() error {
		issue, _, err := a.Commands.CreateIssue(ctx, commands.CreateIssueInput{
			ActorID: actor, ReporterID: reporter, WorkspaceID: a.workspaceID, ProjectIDOrKey: project.ID,
			Summary: item.Summary, Description: item.Description, IssueTypeID: item.Type,
			AssigneeID: a.people[item.Assignee], PriorityID: item.Priority, Labels: item.Labels,
			ParentIDOrKey: parent, Fields: fields,
		})
		created = issue
		return err
	}); err != nil {
		return err
	}
	a.items[item.ID] = created
	if item.Sprint != "" {
		sprint := a.sprints[item.Sprint]
		if err := a.at(ctx, item.CreatedDay, func() error {
			rank, err := a.Store.NextSprintRank(ctx, sprint.ID)
			if err != nil {
				return err
			}
			_, err = a.Store.AddIssueToSprint(ctx, a.admin, a.workspaceID, sprint.ID, created.ID, rank)
			return err
		}); err != nil {
			return fmt.Errorf("put %s in %s: %w", item.ID, item.Sprint, err)
		}
	}
	for _, event := range item.Events {
		if err := a.event(ctx, created, event); err != nil {
			return fmt.Errorf("%s event: %w", event.Kind, err)
		}
	}
	return nil
}

// event replays one thing that happened to a work item.
func (a *Applier) event(ctx context.Context, issue *models.Issue, event Event) error {
	actor := a.people[event.Actor]
	if actor == "" {
		actor = a.admin
	}
	return a.at(ctx, event.Day, func() error {
		switch event.Kind {
		case "transition":
			return a.transition(ctx, actor, issue, event)
		case "comment":
			_, _, err := a.Commands.AddComment(ctx, commands.AddCommentInput{
				ActorID: actor, WorkspaceID: a.workspaceID, IssueIDOrKey: issue.ID, Body: adf.ParagraphDoc(event.Body),
			})
			return err
		case "worklog":
			_, _, err := a.Commands.AddWorklog(ctx, actor, a.workspaceID, issue.ID, adf.ParagraphDoc(event.Body), event.Seconds)
			return err
		case "assign":
			assignee := a.people[event.Assignee]
			_, _, err := a.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{
				ActorID: actor, WorkspaceID: a.workspaceID, IssueIDOrKey: issue.ID, AssigneeID: &assignee,
			})
			return err
		case "link":
			target := a.items[event.Target]
			linkType, err := a.linkTypeID(ctx, event.LinkType)
			if err != nil {
				return err
			}
			_, _, err = a.Commands.LinkIssue(ctx, actor, a.workspaceID, issue.ID, linkType, target.ID)
			return err
		case "watch":
			_, err := a.Commands.SetWatching(ctx, actor, a.workspaceID, issue.ID, true)
			return err
		case "vote":
			_, err := a.Commands.SetVoting(ctx, actor, a.workspaceID, issue.ID, true)
			return err
		}
		return fmt.Errorf("unknown event kind %q", event.Kind)
	})
}

// transition moves work to a status by running the workflow transition that
// leads there, so conditions, validators and post functions all run.
func (a *Applier) transition(ctx context.Context, actor string, issue *models.Issue, event Event) error {
	current, err := a.Store.IssueByIDOrKey(ctx, a.workspaceID, issue.ID)
	if err != nil {
		return err
	}
	workflow, err := a.Store.WorkflowForProjectAndIssueType(ctx, current.ProjectID, current.IssueType.ID)
	if err != nil {
		return err
	}
	target := strings.ToLower(strings.TrimSpace(event.Status))
	for _, transition := range workflow.Available(current.Status.ID) {
		status, statusErr := a.Store.StatusByIDForProject(ctx, transition.To, current.ProjectID)
		if statusErr != nil {
			continue
		}
		if strings.ToLower(status.Name) != target {
			continue
		}
		// A transition can only carry a resolution when its screen asks for
		// one; otherwise the status decides, and a named resolution is
		// recorded afterwards the way a person would set it.
		update := store.IssueUpdate{}
		asksResolution := slices.Contains(transition.ScreenFields(), "resolution")
		if event.Resolution != "" && asksResolution {
			resolution := event.Resolution
			update.ResolutionID = &resolution
		}
		if _, _, err := a.Commands.TransitionIssueWithUpdate(ctx, actor, a.workspaceID, issue.ID, transition.ID, update); err != nil {
			return err
		}
		if event.Resolution == "" || asksResolution {
			return nil
		}
		resolution := event.Resolution
		_, _, err := a.Commands.UpdateIssue(ctx, commands.UpdateIssueInput{
			ActorID: actor, WorkspaceID: a.workspaceID, IssueIDOrKey: issue.ID, ResolutionID: &resolution,
		})
		return err
	}
	return fmt.Errorf("no transition from %s to %q", current.Status.Name, event.Status)
}

// linkTypeID finds a link type by its outward name, such as "blocks".
func (a *Applier) linkTypeID(ctx context.Context, name string) (string, error) {
	types, err := a.Store.LinkTypes(ctx, a.workspaceID)
	if err != nil {
		return "", err
	}
	for _, linkType := range types {
		if strings.EqualFold(linkType.Outward, name) || strings.EqualFold(linkType.Name, name) {
			return linkType.ID, nil
		}
	}
	return "", fmt.Errorf("no link type called %q", name)
}

// deployments records deliveries, which the delivery report counts.
func (a *Applier) deployments(ctx context.Context, declared []Deployment) error {
	if len(declared) == 0 {
		return nil
	}
	deployments := make([]models.SoftwareDeployment, 0, len(declared))
	for index, deployment := range declared {
		keys := make([]string, 0, len(deployment.WorkItems))
		for _, item := range deployment.WorkItems {
			keys = append(keys, a.items[item].Key)
		}
		at := a.Clock.At(deployment.Day, index)
		payload, err := json.Marshal(map[string]any{
			"deploymentSequenceNumber": index + 1, "updateSequenceNumber": index + 1,
			"displayName": fmt.Sprintf("%s #%d", deployment.Pipeline, index+1), "state": deployment.State,
			"lastUpdated":  at.Format(time.RFC3339),
			"pipeline":     map[string]any{"id": deployment.Pipeline, "displayName": deployment.Pipeline, "url": "https://ci.example.test/" + deployment.Pipeline},
			"environment":  map[string]any{"id": deployment.Environment, "displayName": deployment.Environment, "type": deployment.Type},
			"associations": []any{map[string]any{"associationType": "issueKeys", "values": keys}},
		})
		if err != nil {
			return err
		}
		deployments = append(deployments, models.SoftwareDeployment{
			Payload: payload, Properties: json.RawMessage(`{}`),
			PipelineID: deployment.Pipeline, EnvironmentID: deployment.Environment,
			DeploymentSequenceNumber: int64(index + 1), UpdateSequenceNumber: int64(index + 1),
			IssueKeys: keys, DisplayName: fmt.Sprintf("%s #%d", deployment.Pipeline, index+1),
			URL:   fmt.Sprintf("https://ci.example.test/%s/%d", deployment.Pipeline, index+1),
			State: deployment.State, EnvironmentName: deployment.Environment,
			EnvironmentType: deployment.Type, LastUpdated: at,
		})
	}
	return a.Store.UpsertSoftwareDeployments(ctx, a.workspaceID, deployments)
}

// filtersAndDashboards saves the searches and dashboards people keep.
func (a *Applier) filtersAndDashboards(ctx context.Context, scenario *Scenario) error {
	for _, filter := range scenario.Filters {
		owner := a.people[filter.Owner]
		if owner == "" {
			owner = a.admin
		}
		if _, err := a.Store.CreateFilter(ctx, store.NewID("flt"), a.workspaceID, filter.Name, filter.JQL, filter.Description, owner); err != nil {
			return fmt.Errorf("filter %s: %w", filter.Name, err)
		}
	}
	return nil
}

// serviceDesk sets up a service project's portal: its agents and the requests
// customers can raise.
func (a *Applier) serviceDesk(ctx context.Context, declared Project, project *models.Project) error {
	desks, err := a.Store.ServiceDesks(ctx, a.workspaceID)
	if err != nil {
		return err
	}
	deskID := ""
	for _, desk := range desks {
		if desk.ProjectID == project.ID {
			deskID = desk.ID
			break
		}
	}
	if deskID == "" {
		return fmt.Errorf("the service project %s has no desk", declared.Key)
	}
	a.desks[declared.ID] = deskID
	for _, agent := range declared.ServiceDesk.Agents {
		if err := a.Store.SetServiceDeskAgent(ctx, a.workspaceID, a.admin, deskID, a.people[agent], true); err != nil {
			return fmt.Errorf("agent %s: %w", agent, err)
		}
	}
	types, err := a.Store.ServiceRequestTypes(ctx, a.workspaceID, deskID, "")
	if err != nil {
		return err
	}
	// A request type is raised as a work type of the project; the desk's own
	// types come from its project, so the first standard one carries them.
	workTypes, err := a.Store.ProjectIssueTypes(ctx, a.workspaceID, project.ID, nil)
	if err != nil {
		return err
	}
	workTypeID := ""
	for _, workType := range workTypes {
		if !workType.Subtask {
			workTypeID = workType.ID
			break
		}
	}
	if workTypeID == "" {
		return fmt.Errorf("the service project %s offers no work type", declared.Key)
	}
	seeded := map[string]string{}
	for _, requestType := range types {
		seeded[strings.ToLower(requestType.Name)] = requestType.ID
	}
	for _, requestType := range declared.ServiceDesk.RequestTypes {
		if id, ok := seeded[strings.ToLower(requestType.Name)]; ok {
			a.requestType[requestType.ID] = id
			continue
		}
		created, err := a.Store.CreateServiceRequestType(ctx, a.workspaceID, deskID, requestType.Name, requestType.Description, "", workTypeID)
		if err != nil {
			return fmt.Errorf("request type %s: %w", requestType.Name, err)
		}
		a.requestType[requestType.ID] = created.ID
	}
	return nil
}

// service raises the requests customers made, replays what happened to each,
// and records the satisfaction they left.
func (a *Applier) service(ctx context.Context, declared *Service) error {
	for _, organization := range declared.Organizations {
		created, err := a.Store.CreateServiceOrganization(ctx, a.workspaceID, organization.Name)
		if err != nil {
			return fmt.Errorf("organization %s: %w", organization.Name, err)
		}
		members := make([]string, 0, len(organization.Members))
		for _, member := range organization.Members {
			members = append(members, a.people[member])
		}
		if len(members) > 0 {
			if err := a.Store.SetServiceOrganizationUsers(ctx, a.workspaceID, created.ID, members, true); err != nil {
				return fmt.Errorf("organization %s members: %w", organization.Name, err)
			}
		}
	}
	for _, request := range declared.Requests {
		deskID := a.desks[request.Project]
		if deskID == "" {
			return fmt.Errorf("request %s names the project %s, which has no service desk", request.ID, request.Project)
		}
		customer := a.people[request.Customer]
		var raised *models.ServiceRequest
		if err := a.at(ctx, request.CreatedDay, func() error {
			created, err := a.Commands.CreateServiceRequest(ctx, commands.CreateServiceRequestInput{
				ActorID: customer, WorkspaceID: a.workspaceID, ServiceDeskID: deskID,
				RequestTypeID: a.requestType[request.RequestType], CustomerID: customer, Channel: "portal",
				Summary: request.Summary, Description: request.Description,
			})
			raised = created
			return err
		}); err != nil {
			return fmt.Errorf("request %s: %w", request.ID, err)
		}
		issue, err := a.Store.IssueByIDOrKey(ctx, a.workspaceID, raised.Issue.ID)
		if err != nil {
			return err
		}
		a.items[request.ID] = issue
		for _, event := range request.Events {
			if err := a.event(ctx, issue, event); err != nil {
				return fmt.Errorf("request %s %s: %w", request.ID, event.Kind, err)
			}
		}
		if request.Satisfaction > 0 {
			day := request.CreatedDay
			if len(request.Events) > 0 {
				day = request.Events[len(request.Events)-1].Day
			}
			if err := a.at(ctx, day, func() error {
				_, err := a.Store.PutServiceRequestFeedback(ctx, a.workspaceID, issue.ID, customer, "csat", request.Satisfaction, request.Feedback)
				return err
			}); err != nil {
				return fmt.Errorf("request %s feedback: %w", request.ID, err)
			}
		}
	}
	return nil
}

// wiki builds the knowledge base: spaces, their page trees, blog posts and
// the comments people left.
func (a *Applier) wiki(ctx context.Context, declared *Wiki) error {
	for _, space := range declared.Spaces {
		created, err := a.Store.CreateWikiSpaceFull(ctx, a.workspaceID, a.admin, store.CreateWikiSpaceInput{
			Key: space.Key, Name: space.Name, Description: space.Description,
		})
		if err != nil {
			return fmt.Errorf("space %s: %w", space.Key, err)
		}
		a.spaces[space.Key] = created.ID
		if err := a.pages(ctx, created.ID, "", space.Pages); err != nil {
			return fmt.Errorf("space %s: %w", space.Key, err)
		}
		for _, post := range space.BlogPosts {
			author := a.people[post.Author]
			if author == "" {
				author = a.admin
			}
			if err := a.at(ctx, post.CreatedDay, func() error {
				_, err := a.Store.SaveWikiBlogPost(ctx, a.workspaceID, author, models.WikiBlogPost{
					SpaceID: created.ID, Title: post.Title, Status: "current",
					Body: models.WikiBody{Representation: "storage", Value: post.Body},
				})
				return err
			}); err != nil {
				return fmt.Errorf("blog post %s: %w", post.Title, err)
			}
		}
	}
	return nil
}

// pages writes one level of a space's page tree and then its children.
func (a *Applier) pages(ctx context.Context, spaceID, parentID string, pages []Page) error {
	for _, page := range pages {
		author := a.people[page.Author]
		if author == "" {
			author = a.admin
		}
		var saved *models.WikiPage
		if err := a.at(ctx, page.CreatedDay, func() error {
			created, err := a.Commands.SaveWikiPage(ctx, a.workspaceID, author, models.WikiPage{
				SpaceID: spaceID, ParentID: parentID, Title: page.Title, Status: "current",
				Body: models.WikiBody{Representation: "storage", Value: page.Body},
			})
			saved = created
			return err
		}); err != nil {
			return fmt.Errorf("page %s: %w", page.Title, err)
		}
		for _, comment := range page.Comments {
			commenter := a.people[comment.Author]
			if commenter == "" {
				commenter = a.admin
			}
			if err := a.at(ctx, comment.Day, func() error {
				_, err := a.Commands.CreateWikiFooterComment(ctx, a.workspaceID, commenter, models.WikiFooterComment{
					PageID: saved.ID, Body: models.WikiBody{Representation: "storage", Value: comment.Body},
				})
				return err
			}); err != nil {
				return fmt.Errorf("comment on %s: %w", page.Title, err)
			}
		}
		if err := a.pages(ctx, spaceID, saved.ID, page.Children); err != nil {
			return err
		}
	}
	return nil
}

// retime moves the history the scenario just wrote into the past. The action
// log is the record of what happened, so it is retimed first and the rows it
// produced take their timestamps from it: a work item was created when its
// creation was logged and resolved when its resolution was.
func (a *Applier) retime(ctx context.Context) error {
	tx, err := a.Store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, stamp := range a.stamps {
		if _, err := tx.Exec(ctx, `UPDATE actions SET created_at=$4
			WHERE workspace_id=$1 AND seq>$2 AND seq<=$3`, a.workspaceID, stamp.from, stamp.to, stamp.at); err != nil {
			return err
		}
	}
	// Each row takes the time of the action that made it.
	for _, statement := range []string{
		`UPDATE issues i SET created_at=first.created_at
		   FROM (SELECT entity_id, min(created_at) AS created_at FROM actions
		         WHERE workspace_id=$1 AND entity_type='issue' GROUP BY entity_id) first
		  WHERE i.workspace_id=$1 AND i.id=first.entity_id`,
		`UPDATE issues i SET updated_at=last.updated_at
		   FROM (SELECT entity_id, max(created_at) AS updated_at FROM actions
		         WHERE workspace_id=$1 AND entity_type='issue' GROUP BY entity_id) last
		  WHERE i.workspace_id=$1 AND i.id=last.entity_id`,
		`UPDATE issues i SET resolved_at=resolved.at
		   FROM (SELECT entity_id, min(created_at) AS at FROM actions
		         WHERE workspace_id=$1 AND entity_type='issue'
		           AND COALESCE(payload->'diff'->'resolution'->>'to','')<>'' GROUP BY entity_id) resolved
		  WHERE i.workspace_id=$1 AND i.id=resolved.entity_id AND i.resolution_id IS NOT NULL`,
		`UPDATE comments c SET created_at=a.created_at
		   FROM actions a WHERE a.workspace_id=$1 AND a.entity_type='comment' AND a.entity_id=c.id`,
		`UPDATE worklogs w SET created_at=a.created_at
		   FROM actions a WHERE a.workspace_id=$1 AND a.entity_type='worklog' AND a.entity_id=w.id`,
		`UPDATE wiki_pages p SET created_at=a.created_at
		   FROM actions a WHERE a.workspace_id=$1 AND a.entity_type='wiki_page' AND a.entity_id=p.id::text`,
		`UPDATE wiki_blog_posts b SET created_at=a.created_at
		   FROM actions a WHERE a.workspace_id=$1 AND a.entity_type='wiki_blogpost' AND a.entity_id=b.id::text`,
		`UPDATE service_requests r SET created_at=i.created_at
		   FROM issues i WHERE r.workspace_id=$1 AND i.id=r.issue_id`,
	} {
		if _, err := tx.Exec(ctx, statement, a.workspaceID); err != nil {
			return fmt.Errorf("%s: %w", strings.SplitN(statement, "\n", 2)[0], err)
		}
	}
	return tx.Commit(ctx)
}
