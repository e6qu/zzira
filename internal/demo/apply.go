package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"golang.org/x/sync/errgroup"

	"github.com/e6qu/zzira/internal/adf"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/automation"
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
	// assets maps a scenario asset id to the object the site holds.
	assets map[string]string
	// deskForms are the request type forms waiting for their desk's Assets
	// inventory, which is built after the desks are.
	deskForms []deskForm
	versions  map[string]*models.Version
	sprints   map[string]*models.Sprint
	boards    map[string]*models.Board
	fields    map[string]string
	// fieldTypes is what each declared custom field is, because a value is
	// written differently for each kind: an option is named by its value,
	// and everything else is written as it stands.
	fieldTypes map[string]string
	items      map[string]*models.Issue
	// existing indexes the work already on the site by project and summary,
	// so a re-run keeps it rather than raising it twice.
	existing map[string]string
	spaces   map[string]string
	// ordinals count the writes each day already carries, so a day's history
	// reads in the order it happened and stays inside that day. A single
	// counter across the whole scenario spread three years of writes seven
	// minutes apart, which put the last months of it in the future.
	ordinals map[int]int
	// mutex guards what several projects' histories touch at once: the work
	// they have raised, and the ordinal that orders a day.
	mutex sync.Mutex
}

// stamp records that everything written between two action sequences happened
// at a moment in the scenario's history.
// Apply builds the scenario's site and returns how to sign in to it.
//
// slug names the workspace to build into, and is the scenario's own slug
// unless the operator named another. A deployment serves exactly one
// workspace, so seeding it means naming that one: a scenario applied to a
// slug the server does not serve builds a company nobody can see. The site
// keeps the scenario's display name either way.
func Apply(ctx context.Context, st *store.Store, cmds *commands.Service, scenario *Scenario, clock Clock, slug string) (*Result, error) {
	if err := scenario.Validate(); err != nil {
		return nil, err
	}
	if slug == "" {
		return nil, errors.New("name the workspace to apply the scenario to")
	}
	applier := &Applier{
		Store: st, Commands: cmds, Clock: clock,
		people: map[string]string{}, groups: map[string]string{}, projects: map[string]*models.Project{},
		desks: map[string]string{}, requestType: map[string]string{}, assets: map[string]string{},
		versions: map[string]*models.Version{},
		sprints:  map[string]*models.Sprint{}, boards: map[string]*models.Board{}, fields: map[string]string{},
		fieldTypes: map[string]string{},
		items:      map[string]*models.Issue{}, existing: map[string]string{}, spaces: map[string]string{},
		ordinals: map[int]int{},
	}
	return applier.run(ctx, scenario, slug)
}

func (a *Applier) run(ctx context.Context, scenario *Scenario, slug string) (*Result, error) {
	result := &Result{Slug: slug, Tokens: map[string]string{}, Passwords: map[string]string{}}
	if err := a.workspace(ctx, scenario.Site, slug); err != nil {
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
	if err := a.readExistingWork(ctx); err != nil {
		return nil, err
	}
	// Work a person wrote can refer to anything -- a parent in another
	// project, a link to another piece of work -- so it is applied in the
	// order it was written. Generated work refers only to its own project,
	// so each project's years of history are applied alongside the others'.
	// Inside a project the order still holds, which is what keeps its keys
	// climbing with its history.
	curated, generated := []WorkItem{}, map[string][]WorkItem{}
	order := []string{}
	for _, item := range scenario.WorkItems {
		if !item.Generated {
			curated = append(curated, item)
			continue
		}
		if _, seen := generated[item.Project]; !seen {
			order = append(order, item.Project)
		}
		generated[item.Project] = append(generated[item.Project], item)
	}
	for _, item := range curated {
		if err := a.workItem(ctx, item); err != nil {
			return nil, fmt.Errorf("work item %s: %w", item.ID, err)
		}
	}
	// The desk's queue is its own project, so it fills while the delivery
	// teams' years are being written: separate projects, separate keys, and
	// the same site.
	history, historyCtx := errgroup.WithContext(ctx)
	history.Go(func() error { return a.generatedWork(historyCtx, order, generated) })
	if scenario.Service != nil {
		history.Go(func() error { return a.service(historyCtx, scenario.Service) })
	}
	if err := history.Wait(); err != nil {
		return nil, err
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
	if err := a.commits(ctx, scenario.Commits); err != nil {
		return nil, err
	}

	if scenario.Wiki != nil {
		if err := a.wiki(ctx, scenario.Wiki); err != nil {
			return nil, err
		}
	}
	if err := a.teamsAndPlans(ctx, scenario); err != nil {
		return nil, err
	}
	if err := a.automationRules(ctx, scenario.Automation); err != nil {
		return nil, err
	}
	if err := a.filtersAndDashboards(ctx, scenario); err != nil {
		return nil, err
	}
	if err := a.translations(ctx, scenario.Translations); err != nil {
		return nil, err
	}
	if err := a.retime(ctx); err != nil {
		return nil, fmt.Errorf("retime the history: %w", err)
	}
	return result, nil
}

// atMinute runs one piece of the scenario at a time of day: the same spread
// through the working day as everything else, plus however far into the day
// the event said it happened. An outage found at nine and over by ten is fifty
// minutes of recovery, and a day is too coarse to say that.
func (a *Applier) atMinute(ctx context.Context, day, minutes int, write func(context.Context) error) error {
	if minutes <= 0 {
		return a.at(ctx, day, write)
	}
	a.mutex.Lock()
	ordinal := a.ordinals[day]
	a.ordinals[day] = ordinal + 1
	a.mutex.Unlock()
	return write(store.WithActionTime(ctx, a.Clock.At(day, ordinal).Add(time.Duration(minutes)*time.Minute)))
}

// at runs one piece of the scenario and records when it happened, so the
// retiming pass can move its action log entries into the past.
func (a *Applier) at(ctx context.Context, day int, write func(context.Context) error) error {
	a.mutex.Lock()
	ordinal := a.ordinals[day]
	a.ordinals[day] = ordinal + 1
	a.mutex.Unlock()
	// The write carries the moment it happened into the action log, so the
	// history reads correctly as it is written. Bracketing each write with
	// the workspace's action sequence and rewriting the range afterwards
	// cost two queries a write and could never be done with more than one
	// write in flight.
	return write(store.WithActionTime(ctx, a.Clock.At(day, ordinal)))
}

// workspace finds or creates the site the scenario describes, under the slug
// the operator asked for. A site that is already there keeps its name -- the
// scenario is being poured into someone's workspace, not renaming it.
func (a *Applier) workspace(ctx context.Context, site Site, slug string) error {
	if id, err := a.Store.WorkspaceBySlug(ctx, slug); err == nil && id != "" {
		a.workspaceID = id
		return nil
	}
	id := store.NewID("ws")
	if _, err := a.Store.Pool.Exec(ctx,
		`INSERT INTO workspaces(id,slug,name,seq) VALUES($1,$2,$3,0)`, id, slug, site.Name); err != nil {
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
	existingGroups, err := a.Store.WikiGroups(ctx, a.workspaceID, a.admin, "")
	if err != nil {
		return fmt.Errorf("read the groups: %w", err)
	}
	for _, group := range scenario.Groups {
		// A group name is unique in the directory, so a group the scenario
		// already made is that group.
		if id := groupNamed(existingGroups, group.Name); id != "" {
			a.groups[group.ID] = id
			continue
		}
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

// groupNamed finds a group by the name a scenario gives it.
func groupNamed(groups []store.WikiGroup, name string) string {
	for _, group := range groups {
		if group.Name == name {
			return group.ID
		}
	}
	return ""
}

// hierarchy adds the levels above Epic and puts work types on them, which is
// what Jira's work type hierarchy settings do.
func (a *Applier) hierarchy(ctx context.Context, levels []HierarchyLevel) error {
	existing, err := a.Store.HierarchyLevels(ctx, a.workspaceID)
	if err != nil {
		return fmt.Errorf("read the work type hierarchy: %w", err)
	}
	for _, level := range levels {
		// A level name is unique in the hierarchy, so a level the scenario
		// already added is that level; its work types are set below either
		// way, which is what makes an edited scenario take effect.
		if !slices.ContainsFunc(existing, func(l store.HierarchyLevel) bool { return l.Name == level.Name }) {
			if _, err := a.Store.AddHierarchyLevel(ctx, a.workspaceID, a.admin, level.Name); err != nil {
				return fmt.Errorf("hierarchy level %s: %w", level.Name, err)
			}
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

// customFields registers the fields the work items use. A field the scenario
// already made is that field: two fields of the same name would be two
// different columns on the same work, and the work raised last time is on the
// first one.
func (a *Applier) customFields(ctx context.Context, fields []CustomField) error {
	existing, err := a.Store.CustomFieldsForWorkspace(ctx, a.workspaceID)
	if err != nil {
		return fmt.Errorf("read the custom fields: %w", err)
	}
	for _, field := range fields {
		a.fieldTypes[field.ID] = field.Type
		if made := customFieldNamed(existing, field.Name); made != "" {
			a.fields[field.ID] = made
			continue
		}
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
		// A field is created without a context, and a field with no context
		// is in no project: its options have nowhere to live and no form
		// offers it. Every declared field gets one covering the site.
		contexts, err := a.Store.CustomFieldContexts(ctx, a.workspaceID, id, nil)
		if err != nil {
			return err
		}
		if len(contexts) == 0 {
			made, err := a.Store.CreateCustomFieldContext(ctx, a.workspaceID, a.admin, id,
				field.Name+" context", "Every project and work type", nil, nil)
			if err != nil {
				return fmt.Errorf("give %s a context: %w", field.Name, err)
			}
			contexts = []*models.CustomFieldContext{made}
		}
		if len(field.Options) > 0 {
			if _, err := a.Store.CreateCustomFieldOptions(ctx, a.workspaceID, a.admin, id, contexts[0].ID, field.Options); err != nil {
				return fmt.Errorf("add options to %s: %w", field.Name, err)
			}
		}
	}
	return nil
}

// encodeFieldValue writes a scenario's field value the way the field reads it.
// An option field names its option by value -- which is what a scenario says,
// "Several customers" rather than an id nobody wrote down -- and a bare string
// would otherwise be read as an option id and rejected.
func (a *Applier) encodeFieldValue(field, value string) (json.RawMessage, error) {
	switch a.fieldTypes[field] {
	case models.CustomFieldSelect:
		return json.Marshal(map[string]string{"value": value})
	case models.CustomFieldMultiSelect:
		chosen := []map[string]string{}
		for _, part := range strings.Split(value, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				chosen = append(chosen, map[string]string{"value": trimmed})
			}
		}
		return json.Marshal(chosen)
	case models.CustomFieldCascadingSelect:
		parent, child, _ := strings.Cut(value, ":")
		cascade := map[string]any{"value": strings.TrimSpace(parent)}
		if child = strings.TrimSpace(child); child != "" {
			cascade["child"] = map[string]string{"value": child}
		}
		return json.Marshal(cascade)
	default:
		return json.Marshal(value)
	}
}

// customFieldNamed finds a custom field by the name a scenario gives it.
func customFieldNamed(fields []*models.CustomField, name string) string {
	for _, field := range fields {
		if field.Name == name {
			return field.ID
		}
	}
	return ""
}

// project creates a project with its components, versions, board and sprints,
// or takes the ones already there. A scenario is applied more than once -- the
// README invites it: change the document, run the mode again -- and a project
// key, a component name and a version name are each unique within a site, so
// building them a second time could only fail. Keeping what is there is the
// same rule the site, the accounts and the board already follow.
func (a *Applier) project(ctx context.Context, project Project) error {
	created, err := a.Store.ProjectByKey(ctx, a.workspaceID, project.Key)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("look for the %s project: %w", project.Key, err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		created, err = a.Commands.CreateProject(ctx, a.admin, a.workspaceID, commands.CreateProjectInput{
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
	}
	a.projects[project.ID] = created
	existingComponents, err := a.Store.Components(ctx, a.workspaceID, created.ID, "", "")
	if err != nil {
		return fmt.Errorf("read the components of %s: %w", project.Key, err)
	}
	for _, component := range project.Components {
		if slices.ContainsFunc(existingComponents, func(c *models.ProjectComponent) bool { return c.Name == component.Name }) {
			continue
		}
		if _, err := a.Store.CreateComponent(ctx, a.workspaceID, a.admin, store.ComponentInput{
			ProjectIDOrKey: created.ID, Name: component.Name, LeadAccountID: a.people[component.Lead],
		}); err != nil {
			return fmt.Errorf("component %s: %w", component.Name, err)
		}
	}
	existingVersions, err := a.Store.ProjectVersions(ctx, created.ID)
	if err != nil {
		return fmt.Errorf("read the versions of %s: %w", project.Key, err)
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
		id := ""
		if existing := versionNamed(existingVersions, version.Name); existing != nil {
			id = existing.ID
		}
		saved, err := a.Store.SaveVersion(ctx, a.workspaceID, a.admin, created.ID, id, update)
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
	// What this project counts as an incident, which is what its time to
	// restore is measured over.
	if query := strings.TrimSpace(project.IncidentJQL); query != "" {
		settings, err := a.Store.DORASettingsFor(ctx, a.workspaceID, created.ID)
		if err != nil {
			return fmt.Errorf("read the delivery settings of %s: %w", project.Key, err)
		}
		settings.IncidentJQL = query
		if err := a.Store.SaveDORASettings(ctx, a.workspaceID, a.admin, created.ID, settings); err != nil {
			return fmt.Errorf("say what an incident is in %s: %w", project.Key, err)
		}
	}
	return nil
}

// versionNamed finds a version by the name a scenario gives it.
func versionNamed(versions []*models.Version, name string) *models.Version {
	for _, version := range versions {
		if version.Name == name {
			return version
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
	existing, err := a.Store.SprintsByBoard(ctx, board.ID)
	if err != nil {
		return fmt.Errorf("read the sprints of %s: %w", board.Name, err)
	}
	for _, sprint := range declared.Sprints {
		// A sprint the scenario already ran on this board is that sprint, not
		// a second one with the same name: re-running must not leave a board
		// with two "Sprint 1"s, one of them closed.
		if found := sprintNamed(existing, sprint.Name); found != nil {
			a.sprints[sprint.ID] = found
			continue
		}
		created, _, err := a.Store.CreateSprint(ctx, a.admin, a.workspaceID, board.ID, sprint.Name, sprint.Goal)
		if err != nil {
			return fmt.Errorf("sprint %s: %w", sprint.Name, err)
		}
		a.sprints[sprint.ID] = created
	}
	return nil
}

// sprintNamed finds a sprint by the name a scenario gives it.
func sprintNamed(sprints []*models.Sprint, name string) *models.Sprint {
	for _, sprint := range sprints {
		if sprint.Name == name {
			return sprint
		}
	}
	return nil
}

// startSprints moves each sprint into the state the scenario declares, once
// its work is in it: Jira starts a sprint with its scope, and completing one
// moves unfinished work out.
// sprintStartDay is when a sprint began, for ordering; one with no start day
// sorts first, as it is the oldest thing a scenario can say about it.
func sprintStartDay(sprint Sprint) int {
	if sprint.StartDay == nil {
		return -1 << 30
	}
	return *sprint.StartDay
}

func (a *Applier) startSprints(ctx context.Context, scenario *Scenario) error {
	for _, project := range scenario.Projects {
		if project.Board == nil {
			continue
		}
		// Sprints run in the order they happened, whatever order the scenario
		// lists them in: a site with parallel sprints off allows one active
		// sprint at a time, so starting an older sprint while a newer one is
		// still running is refused -- correctly.
		ordered := append([]Sprint(nil), project.Board.Sprints...)
		sort.SliceStable(ordered, func(first, second int) bool {
			return sprintStartDay(ordered[first]) < sprintStartDay(ordered[second])
		})
		for _, sprint := range ordered {
			state := sprint.State
			if state == "" || state == "future" {
				continue
			}
			created := a.sprints[sprint.ID]
			// A sprint a previous run already put where the scenario wants it
			// stays there: Jira does not move a sprint back from closed to
			// active, and its report would lose the dates it closed with.
			if created.State == state {
				continue
			}
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
			if err := a.at(ctx, day, func(ctx context.Context) error {
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
			if err := a.at(ctx, closeDay, func(ctx context.Context) error {
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
			if err := a.at(ctx, day, func(ctx context.Context) error {
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

// readExistingWork indexes the work already on the site by project and
// summary, which is how a re-run recognises the work it raised last time.
func (a *Applier) readExistingWork(ctx context.Context) error {
	rows, err := a.Store.Pool.Query(ctx,
		`SELECT id,project_id,summary FROM issues WHERE workspace_id=$1`, a.workspaceID)
	if err != nil {
		return fmt.Errorf("read the work already on the site: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, projectID, summary string
		if err := rows.Scan(&id, &projectID, &summary); err != nil {
			return err
		}
		a.existing[projectID+"\x00"+summary] = id
	}
	return rows.Err()
}

// workItem raises one piece of work and replays everything that happened to it.
//
// Work the scenario already raised on this site is left exactly as it is: its
// summary within its project is what identifies it. A second run therefore
// adds no second copy of the company and replays no history onto work that
// already has it -- it raises only what the scenario has gained since.
func (a *Applier) workItem(ctx context.Context, item WorkItem) error {
	project := a.projects[item.Project]
	if id := a.existing[project.ID+"\x00"+item.Summary]; id != "" {
		found, err := a.Store.IssueByIDOrKey(ctx, a.workspaceID, id)
		if err != nil {
			return fmt.Errorf("read the work already raised for %q: %w", item.Summary, err)
		}
		a.mutex.Lock()
		a.items[item.ID] = found
		a.mutex.Unlock()
		return nil
	}
	fields := map[string]json.RawMessage{}
	for field, value := range item.Fields {
		encoded, err := a.encodeFieldValue(field, value)
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
	// An estimate is what the time tracking, user workload and version
	// workload reports are made of, and logging work moves the remaining
	// estimate down from it, so the work has to be raised carrying it.
	var original, remaining *int64
	if item.Estimate != "" {
		seconds, err := models.ParseJiraDuration(item.Estimate, models.TimeTrackingConfiguration{})
		if err != nil {
			return fmt.Errorf("estimate: %w", err)
		}
		original, remaining = &seconds, &seconds
	}
	due := ""
	if item.Due != nil {
		due = a.Clock.Day(*item.Due)
	}
	var created *models.Issue
	if err := a.at(ctx, item.CreatedDay, func(ctx context.Context) error {
		issue, _, err := a.Commands.CreateIssue(ctx, commands.CreateIssueInput{
			ActorID: actor, ReporterID: reporter, WorkspaceID: a.workspaceID, ProjectIDOrKey: project.ID,
			Summary: item.Summary, Description: item.Description, IssueTypeID: item.Type,
			AssigneeID: a.people[item.Assignee], PriorityID: item.Priority, Labels: item.Labels,
			ParentIDOrKey: parent, Fields: fields, DueDate: due,
			OriginalEstimate: original, RemainingEstimate: remaining,
		})
		created = issue
		return err
	}); err != nil {
		return err
	}
	a.mutex.Lock()
	a.items[item.ID] = created
	a.mutex.Unlock()
	if item.Sprint != "" {
		sprint := a.sprints[item.Sprint]
		if err := a.at(ctx, item.CreatedDay, func(ctx context.Context) error {
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

// demoConcurrency is how many projects' histories are applied at once. The
// work is database-bound, and every write takes the workspace's action
// sequence, so past a handful of writers the site spends its time waiting on
// itself rather than working.
func demoConcurrency() int {
	if value := os.Getenv("ZZIRA_DEMO_CONCURRENCY"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 4
}

// generatedWork applies each project's generated history, one project at a
// time within the project and every project at once across them.
func (a *Applier) generatedWork(ctx context.Context, order []string, byProject map[string][]WorkItem) error {
	group, groupCtx := errgroup.WithContext(ctx)
	// A site's database has a connection pool behind it, and a demo that
	// opens more work than the pool can carry just waits differently.
	group.SetLimit(demoConcurrency())
	for _, project := range order {
		items := byProject[project]
		group.Go(func() error {
			for _, item := range items {
				if err := a.workItem(groupCtx, item); err != nil {
					return fmt.Errorf("work item %s: %w", item.ID, err)
				}
			}
			return nil
		})
	}
	return group.Wait()
}

// requestEvent replays one thing that happened to a service request. A comment
// on a request is a reply the customer reads or a note only agents read, and
// which of the two it is decides the desk's clocks: a public reply is what
// stops time to first response. Everything else happens to a request as it
// happens to any work item.
func (a *Applier) requestEvent(ctx context.Context, deskID string, issue *models.Issue, event Event) error {
	switch event.Kind {
	case "comment", "approval", "approve", "decline", "asset", "attach":
	default:
		return a.event(ctx, issue, event)
	}
	actor := a.people[event.Actor]
	if actor == "" {
		actor = a.admin
	}
	return a.atMinute(ctx, event.Day, event.Minutes, func(ctx context.Context) error {
		switch event.Kind {
		case "comment":
			_, err := a.Commands.AddServiceRequestComment(ctx, actor, a.workspaceID, issue.ID,
				adf.ParagraphDoc(event.Body), event.Body, !event.Internal)
			return err
		case "attach":
			// A file reaches a request the way the portal sends one: it is
			// uploaded to the desk first, and then a comment carries it.
			content, mime, err := demoFile(event.File)
			if err != nil {
				return err
			}
			temporary, err := a.Commands.CreateServiceTemporaryAttachment(ctx, actor, a.workspaceID, deskID, event.File, mime, bytes.NewReader(content))
			if err != nil {
				return err
			}
			body := event.Body
			if body == "" {
				body = "Attached " + event.File + "."
			}
			_, _, err = a.Commands.CreateServiceAttachmentComment(ctx, actor, a.workspaceID, issue.ID, []string{temporary.ID}, body, !event.Internal)
			return err
		case "asset":
			role := event.Role
			if role == "" {
				role = "affected"
			}
			return a.Store.SetServiceRequestAsset(ctx, a.workspaceID, actor, issue.ID, a.assets[event.Asset], role, true)
		case "approval":
			approvers := make([]string, 0, len(event.Approvers))
			for _, approver := range event.Approvers {
				approvers = append(approvers, a.people[approver])
			}
			_, err := a.Commands.CreateServiceApproval(ctx, actor, a.workspaceID, issue.ID, event.Body, approvers)
			return err
		default:
			// The approval being answered is the one still waiting; a request
			// with none waiting is a scenario that answered twice.
			approvals, err := a.Store.ServiceApprovals(ctx, issue.ID)
			if err != nil {
				return err
			}
			pending := ""
			for _, approval := range approvals {
				if approval.FinalDecision == "pending" {
					pending = approval.ID
					break
				}
			}
			if pending == "" {
				return fmt.Errorf("no approval is waiting to be answered")
			}
			_, err = a.Commands.AnswerServiceApproval(ctx, actor, a.workspaceID, issue.ID, pending, event.Kind)
			return err
		}
	})
}

// event replays one thing that happened to a work item.
func (a *Applier) event(ctx context.Context, issue *models.Issue, event Event) error {
	actor := a.people[event.Actor]
	if actor == "" {
		actor = a.admin
	}
	return a.atMinute(ctx, event.Day, event.Minutes, func(ctx context.Context) error {
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
			a.mutex.Lock()
			target := a.items[event.Target]
			a.mutex.Unlock()
			if target == nil {
				// A scenario links to work by id, and the work it links to
				// has to exist by the time the link is made: saying so beats
				// a nil pointer three frames down.
				return fmt.Errorf("links to %q, which has not been raised yet", event.Target)
			}
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
		case "attach":
			content, mime, err := demoFile(event.File)
			if err != nil {
				return err
			}
			_, _, err = a.Commands.AddAttachment(ctx, actor, a.workspaceID, issue.ID, event.File, mime, bytes.NewReader(content))
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
		// The same as a commit: a deployment is dated by its own day, not by
		// its place in three years of deliveries.
		a.mutex.Lock()
		ordinal := a.ordinals[deployment.Day]
		a.ordinals[deployment.Day] = ordinal + 1
		a.mutex.Unlock()
		at := a.Clock.At(deployment.Day, ordinal)
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

// filtersAndDashboards saves the searches and dashboards people keep. A
// filter and a dashboard the scenario already saved are found by their name
// and owner, so a re-run leaves one of each rather than a growing pile.
func (a *Applier) filtersAndDashboards(ctx context.Context, scenario *Scenario) error {
	saved := map[string]string{}
	for _, filter := range scenario.Filters {
		owner := a.people[filter.Owner]
		if owner == "" {
			owner = a.admin
		}
		existing, err := a.Store.Filters(ctx, a.workspaceID, owner, store.FilterSearch{Name: filter.Name, OwnerID: owner})
		if err != nil {
			return fmt.Errorf("read the saved filters: %w", err)
		}
		if len(existing) > 0 {
			saved[filter.Name] = existing[0].ID
			continue
		}
		created, err := a.Store.CreateFilter(ctx, store.NewID("flt"), a.workspaceID, filter.Name, filter.JQL, filter.Description, owner)
		if err != nil {
			return fmt.Errorf("filter %s: %w", filter.Name, err)
		}
		saved[filter.Name] = created.ID
	}
	for _, dashboard := range scenario.Dashboards {
		if err := a.dashboard(ctx, dashboard, saved); err != nil {
			return fmt.Errorf("dashboard %s: %w", dashboard.Name, err)
		}
	}
	return nil
}

// dashboard saves one dashboard and the gadgets on it. A gadget's type is a
// ZZIRA gadget module; its project or saved filter is the configuration the
// gadget reads when it draws (see docs/DASHBOARDS.md).
func (a *Applier) dashboard(ctx context.Context, declared Dashboard, filters map[string]string) error {
	owner := a.people[declared.Owner]
	if owner == "" {
		owner = a.admin
	}
	existing, err := a.Store.Dashboards(ctx, a.workspaceID, owner)
	if err != nil {
		return err
	}
	id := ""
	for _, found := range existing {
		if found.Name == declared.Name {
			id = found.ID
			break
		}
	}
	if id != "" {
		return nil
	}
	details := store.DashboardDetails{Name: declared.Name}
	if declared.Shared {
		details.SharePermissions = []models.DashboardShare{{Type: "loggedin"}}
	}
	created, err := a.Store.SaveDashboard(ctx, a.workspaceID, owner, "", details)
	if err != nil {
		return err
	}
	for index, gadget := range declared.Gadgets {
		// The default two-column layout is what a new dashboard has, so the
		// gadgets fill it left to right; an unknown type is refused by the
		// store against the gadget catalog.
		update := store.GadgetUpdate{
			ModuleKey: "com.zzira:" + gadget.Type,
			Position:  &models.GadgetPosition{Column: index % 2, Row: index / 2},
		}
		if gadget.Title != "" {
			update.Title = &gadget.Title
		}
		saved, err := a.Store.SaveDashboardGadget(ctx, a.workspaceID, owner, created.ID, 0, update)
		if err != nil {
			return fmt.Errorf("gadget %s: %w", gadget.Type, err)
		}
		config := models.GadgetConfig{
			FilterID: filters[gadget.Filter], JQL: gadget.JQL, GroupBy: gadget.GroupBy, YGroupBy: gadget.YGroupBy,
			Days: gadget.Days, DateField: gadget.DateField, Cumulative: gadget.Cumulative,
			BubbleAxis: gadget.BubbleAxis,
		}
		if gadget.Project != "" {
			config.ProjectKey = a.projects[gadget.Project].Key
		}
		if gadget.Board != "" {
			config.BoardID = a.boards[gadget.Board].ID
		}
		if err := store.NormalizeGadgetConfig(&config); err != nil {
			return fmt.Errorf("gadget %s: %w", gadget.Type, err)
		}
		raw, err := json.Marshal(config)
		if err != nil {
			return err
		}
		if _, err := a.Store.SetDashboardProperty(ctx, a.workspaceID, owner, created.ID, saved.ID, "zzira.config", raw); err != nil {
			return fmt.Errorf("gadget %s configuration: %w", gadget.Type, err)
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
	// A desk is given three queues when its project is made; these are the
	// ones this desk works from beyond those.
	existing, err := a.Store.ServiceQueues(ctx, a.workspaceID, deskID)
	if err != nil {
		return fmt.Errorf("read the queues of %s: %w", declared.Key, err)
	}
	named := map[string]bool{}
	for _, queue := range existing {
		named[strings.ToLower(queue.Name)] = true
	}
	for _, queue := range declared.ServiceDesk.Queues {
		if named[strings.ToLower(queue.Name)] {
			continue
		}
		if _, err := a.Store.CreateServiceQueue(ctx, a.workspaceID, a.admin, deskID, queue.Name, queue.JQL); err != nil {
			return fmt.Errorf("queue %s: %w", queue.Name, err)
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
		// A request type in no group is not shown on the portal, so a
		// declared type joins its group, or the desk's help group.
		group := requestType.Group
		if group == "" {
			group = "help"
		}
		if id, ok := seeded[strings.ToLower(requestType.Name)]; ok {
			a.requestType[requestType.ID] = id
			if err := a.Store.SetServiceRequestTypeGroups(ctx, a.workspaceID, a.admin, deskID, id, []string{group}); err != nil {
				return fmt.Errorf("request type %s: %w", requestType.Name, err)
			}
			continue
		}
		created, err := a.Store.CreateServiceRequestType(ctx, a.workspaceID, a.admin, deskID, requestType.Name, requestType.Description, "", workTypeID, []string{group})
		if err != nil {
			return fmt.Errorf("request type %s: %w", requestType.Name, err)
		}
		a.requestType[requestType.ID] = created.ID
	}
	// The forms are laid out after the desk's inventory is built, because a
	// form can narrow an Assets object field to a schema that does not exist
	// at this point.
	for _, requestType := range declared.ServiceDesk.RequestTypes {
		if len(requestType.Fields) > 0 {
			a.deskForms = append(a.deskForms, deskForm{DeskID: deskID, RequestType: requestType})
		}
	}
	return nil
}

// deskForm is one request type's form, waiting for its desk's inventory.
type deskForm struct {
	DeskID      string
	RequestType RequestType
}

// requestTypeForm lays out what a request type asks a customer: the summary
// and description every desk starts with, then the fields the scenario names,
// with the schema and AQL an Assets object field is narrowed by.
func (a *Applier) requestTypeForm(ctx context.Context, deskID string, declared RequestType) error {
	schemas, err := a.Store.ServiceDeskAssetSchemas(ctx, a.workspaceID, deskID)
	if err != nil {
		return fmt.Errorf("read the desk's Assets schemas: %w", err)
	}
	schemaID := func(key string) string {
		for _, schema := range schemas {
			if strings.EqualFold(schema.Key, key) || strings.EqualFold(schema.Name, key) {
				return schema.ID
			}
		}
		return ""
	}
	requestTypeID := a.requestType[declared.ID]
	fields := []models.ServiceRequestTypeField{
		{ID: "summary", RequestTypeID: requestTypeID, Required: true},
		{ID: "description", RequestTypeID: requestTypeID},
	}
	for _, field := range declared.Fields {
		id := a.fields[field.Field]
		if id == "" {
			return fmt.Errorf("asks for %q, which the scenario does not declare", field.Field)
		}
		asked := models.ServiceRequestTypeField{
			ID: id, RequestTypeID: requestTypeID, Required: field.Required, HelpText: field.HelpText,
			AssetFilter: field.AssetFilter,
		}
		if field.AssetSchema != "" {
			if asked.AssetSchemaID = schemaID(field.AssetSchema); asked.AssetSchemaID == "" {
				return fmt.Errorf("asks for objects of %q, which this desk has no schema for", field.AssetSchema)
			}
		}
		fields = append(fields, asked)
	}
	return a.Store.SetServiceRequestTypeFields(ctx, a.workspaceID, a.admin, deskID, requestTypeID, fields)
}

// service raises the requests customers made, replays what happened to each,
// and records the satisfaction they left.
func (a *Applier) service(ctx context.Context, declared *Service) error {
	enrolled, err := a.Store.ServiceOrganizations(ctx, a.workspaceID, a.admin, "", true)
	if err != nil {
		return fmt.Errorf("read the customer organizations: %w", err)
	}
	for _, organization := range declared.Organizations {
		created := serviceOrganizationNamed(enrolled, organization.Name)
		if created == nil {
			made, err := a.Store.CreateServiceOrganization(ctx, a.workspaceID, organization.Name)
			if err != nil {
				return fmt.Errorf("organization %s: %w", organization.Name, err)
			}
			created = made
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
		// A desk that serves an organization shows each of its members what
		// their colleagues asked for, and is what the Organizations search
		// reads. An organization that names no desk is a customer of them all.
		desks := organization.Desks
		if len(desks) == 0 {
			for project := range a.desks {
				desks = append(desks, project)
			}
			sort.Strings(desks)
		}
		for _, project := range desks {
			deskID := a.desks[project]
			if deskID == "" {
				return fmt.Errorf("organization %s is a customer of %s, which has no service desk", organization.Name, project)
			}
			if err := a.Store.SetServiceDeskOrganization(ctx, a.workspaceID, deskID, created.ID, true); err != nil {
				return fmt.Errorf("organization %s on the %s desk: %w", organization.Name, project, err)
			}
		}
	}
	if err := a.assetInventory(ctx, declared.Assets); err != nil {
		return err
	}
	for _, form := range a.deskForms {
		if err := a.requestTypeForm(ctx, form.DeskID, form.RequestType); err != nil {
			return fmt.Errorf("the %s form: %w", form.RequestType.Name, err)
		}
	}
	// Somebody who raises a request is a customer of the site, whether or not
	// they also have a seat on it: an internal desk is asked for things by the
	// people who work here.
	raisers := map[string]bool{}
	for _, request := range declared.Requests {
		if id := a.people[request.Customer]; id != "" && !raisers[id] {
			raisers[id] = true
			if err := a.Store.EnrollServiceCustomer(ctx, a.workspaceID, id); err != nil {
				return fmt.Errorf("enrol %s as a customer: %w", request.Customer, err)
			}
		}
	}
	for _, request := range declared.Requests {
		deskID := a.desks[request.Project]
		if deskID == "" {
			return fmt.Errorf("request %s names the project %s, which has no service desk", request.ID, request.Project)
		}
		customer := a.people[request.Customer]
		// A request the scenario already raised is found the same way its
		// work is: it is a work item in the desk's project.
		if id := a.existing[a.projects[request.Project].ID+"\x00"+request.Summary]; id != "" {
			found, err := a.Store.IssueByIDOrKey(ctx, a.workspaceID, id)
			if err != nil {
				return fmt.Errorf("read the request already raised for %q: %w", request.Summary, err)
			}
			a.mutex.Lock()
			a.items[request.ID] = found
			a.mutex.Unlock()
			continue
		}
		var raised *models.ServiceRequest
		if err := a.at(ctx, request.CreatedDay, func(ctx context.Context) error {
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
		a.mutex.Lock()
		a.items[request.ID] = issue
		a.mutex.Unlock()
		for _, event := range request.Events {
			if err := a.requestEvent(ctx, deskID, issue, event); err != nil {
				return fmt.Errorf("request %s %s: %w", request.ID, event.Kind, err)
			}
		}
		if request.Satisfaction > 0 {
			day := request.CreatedDay
			if len(request.Events) > 0 {
				day = request.Events[len(request.Events)-1].Day
			}
			if err := a.at(ctx, day, func(ctx context.Context) error {
				_, err := a.Store.PutServiceRequestFeedback(ctx, a.workspaceID, issue.ID, customer, "csat", request.Satisfaction, request.Feedback)
				return err
			}); err != nil {
				return fmt.Errorf("request %s feedback: %w", request.ID, err)
			}
		}
	}
	return nil
}

// assetInventory builds the desk's configuration management database: what the
// company runs, and what each of those needs from the others. It is applied
// before the requests, because a request is about an asset the site already
// holds.
func (a *Applier) assetInventory(ctx context.Context, declared *Assets) error {
	if declared == nil {
		return nil
	}
	deskID := a.desks[declared.Project]
	if deskID == "" {
		return fmt.Errorf("the asset inventory names the project %s, which has no service desk", declared.Project)
	}
	held, err := a.Store.ServiceAssetInventory(ctx, a.workspaceID, a.admin, deskID)
	if err != nil {
		return fmt.Errorf("read the asset inventory: %w", err)
	}
	// A second run keeps the inventory it built: a schema is its key, and an
	// object is its key within that schema.
	schemas := map[string]string{}
	for _, schema := range held.Schemas {
		schemas[schema.Key] = schema.ID
	}
	objects := map[string]string{}
	for _, object := range held.Objects {
		objects[object.SchemaKey+"\x00"+object.Key] = object.ID
	}
	for _, schema := range declared.Schemas {
		schemaID, ok := schemas[schema.Key]
		if !ok {
			attributes := make([]models.ServiceAssetAttribute, 0, len(schema.Attributes))
			for _, attribute := range schema.Attributes {
				attributes = append(attributes, models.ServiceAssetAttribute{
					Key: attribute.Key(), Name: attribute.Name, Type: attribute.Type,
					Required: attribute.Required, Options: attribute.Options,
				})
			}
			created, err := a.Store.CreateServiceAssetSchema(ctx, a.workspaceID, a.admin, deskID, models.ServiceAssetSchema{
				Key: schema.Key, Name: schema.Name, Description: schema.Description, Attributes: attributes,
			})
			if err != nil {
				return fmt.Errorf("asset schema %s: %w", schema.Key, err)
			}
			schemaID = created.ID
		}
		for _, object := range schema.Objects {
			if id, ok := objects[schema.Key+"\x00"+object.Key]; ok {
				a.assets[object.ID] = id
				continue
			}
			values := map[string]string{}
			for name, value := range object.Values {
				values[strings.ToLower(strings.ReplaceAll(name, " ", "_"))] = value
			}
			created, err := a.Store.SaveServiceAssetObject(ctx, a.workspaceID, a.admin, deskID, models.ServiceAssetObject{
				SchemaID: schemaID, Key: object.Key, Label: object.Label, Values: values,
			})
			if err != nil {
				return fmt.Errorf("asset %s: %w", object.Key, err)
			}
			a.assets[object.ID] = created.ID
		}
	}
	linked := map[string]bool{}
	for _, relation := range held.Relationships {
		linked[relation.From.ID+"\x00"+relation.To.ID] = true
	}
	for _, relation := range declared.Relationships {
		from, to := a.assets[relation.From], a.assets[relation.To]
		if linked[from+"\x00"+to] {
			continue
		}
		if _, err := a.Store.CreateServiceAssetRelationship(ctx, a.workspaceID, a.admin, deskID, models.ServiceAssetRelationship{
			Relationship: relation.Type,
			From:         models.ServiceAssetObject{ID: from},
			To:           models.ServiceAssetObject{ID: to},
		}); err != nil {
			return fmt.Errorf("asset relationship %s to %s: %w", relation.From, relation.To, err)
		}
	}
	return nil
}

// serviceOrganizationNamed finds a customer organization by name.
func serviceOrganizationNamed(organizations []models.ServiceOrganization, name string) *models.ServiceOrganization {
	for index, organization := range organizations {
		if organization.Name == name {
			return &organizations[index]
		}
	}
	return nil
}

// wiki builds the knowledge base: spaces, their page trees, blog posts and
// the comments people left.
func (a *Applier) wiki(ctx context.Context, declared *Wiki) error {
	for _, space := range declared.Spaces {
		// A space key is unique, so a space the scenario already made is that
		// space; its pages and posts are then matched by title below.
		created, err := a.Store.WikiSpaceByKey(ctx, a.workspaceID, a.admin, space.Key)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("look for the %s space: %w", space.Key, err)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			created, err = a.Store.CreateWikiSpaceFull(ctx, a.workspaceID, a.admin, store.CreateWikiSpaceInput{
				Key: space.Key, Name: space.Name, Description: space.Description,
			})
			if err != nil {
				return fmt.Errorf("space %s: %w", space.Key, err)
			}
		}
		a.spaces[space.Key] = created.ID
		if err := a.pages(ctx, created.ID, "", space.Pages); err != nil {
			return fmt.Errorf("space %s: %w", space.Key, err)
		}
		if err := a.spaceContent(ctx, created.ID, space); err != nil {
			return fmt.Errorf("space %s: %w", space.Key, err)
		}
		for _, post := range space.BlogPosts {
			author := a.people[post.Author]
			if author == "" {
				author = a.admin
			}
			written, err := a.Store.WikiBlogPosts(ctx, a.workspaceID, a.admin, created.ID, "current", post.Title, "")
			if err != nil {
				return fmt.Errorf("read the blog posts of %s: %w", space.Key, err)
			}
			if len(written) > 0 {
				continue
			}
			if err := a.at(ctx, post.CreatedDay, func(ctx context.Context) error {
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

// translations names the words on a work item in the other languages the
// company reads. A scenario names the thing by the name the site itself gives
// it, which is what somebody writing one knows; the site's ids are found here.
func (a *Applier) translations(ctx context.Context, declared []Translation) error {
	if len(declared) == 0 {
		return nil
	}
	named := map[string]map[string]string{}
	workTypes, err := a.Store.IssueTypesForWorkspace(ctx, a.workspaceID)
	if err != nil {
		return fmt.Errorf("read the work types: %w", err)
	}
	named["issuetype"] = map[string]string{}
	for _, workType := range workTypes {
		named["issuetype"][strings.ToLower(workType.Name)] = workType.ID
	}
	priorities, err := a.Store.PrioritiesForWorkspace(ctx, a.workspaceID)
	if err != nil {
		return fmt.Errorf("read the priorities: %w", err)
	}
	named["priority"] = map[string]string{}
	for _, priority := range priorities {
		named["priority"][strings.ToLower(priority.Name)] = priority.ID
	}
	resolutions, err := a.Store.ResolutionsForWorkspace(ctx, a.workspaceID)
	if err != nil {
		return fmt.Errorf("read the resolutions: %w", err)
	}
	named["resolution"] = map[string]string{}
	for _, resolution := range resolutions {
		named["resolution"][strings.ToLower(resolution.Name)] = resolution.ID
	}
	statuses, err := a.Store.StatusDirectory(ctx, a.workspaceID)
	if err != nil {
		return fmt.Errorf("read the statuses: %w", err)
	}
	named["status"] = map[string]string{}
	for _, status := range statuses {
		named["status"][strings.ToLower(status.Status.Name)] = status.Status.ID
	}
	for _, translation := range declared {
		id := named[translation.Kind][strings.ToLower(translation.Name)]
		if id == "" {
			return fmt.Errorf("%s %q is not on the site, so it cannot be named in %s", translation.Kind, translation.Name, translation.Locale)
		}
		if err := a.Store.SaveIssueMetadataTranslation(ctx, a.workspaceID, a.admin, store.MetadataTranslation{
			EntityType: translation.Kind, EntityID: id, Locale: translation.Locale,
			Name: translation.Translated, Description: translation.Description,
		}); err != nil {
			return fmt.Errorf("name %s %q in %s: %w", translation.Kind, translation.Name, translation.Locale, err)
		}
	}
	return nil
}

// spaceContent builds what a space holds beside its pages: the whiteboards
// people drew on, the databases they keep records in, the folders that hold
// them and the pages they embedded from elsewhere. A space that already has a
// piece of content by that title keeps it, as a page does.
func (a *Applier) spaceContent(ctx context.Context, spaceID string, space Space) error {
	if len(space.Content) == 0 {
		return nil
	}
	// Content is read a kind at a time, which is how the store reads it, and
	// a second run has to recognise what the first one made or it would build
	// the same board twice.
	titles := map[string]bool{}
	for _, kind := range spaceContentTypes {
		held, err := a.Store.WikiContents(ctx, a.workspaceID, a.admin, spaceID, kind)
		if err != nil {
			return fmt.Errorf("read the %ss of %s: %w", kind, space.Key, err)
		}
		for _, content := range held {
			titles[content.Type+"\x00"+content.Title] = true
		}
	}
	for _, declared := range space.Content {
		if titles[declared.Type+"\x00"+declared.Title] {
			continue
		}
		author := a.people[declared.Author]
		if author == "" {
			author = a.admin
		}
		var created *models.WikiContent
		if err := a.at(ctx, declared.CreatedDay, func(ctx context.Context) error {
			made, err := a.Store.CreateWikiContent(ctx, a.workspaceID, author, models.WikiContent{
				Type: declared.Type, Title: declared.Title, SpaceID: spaceID, EmbedURL: declared.EmbedURL,
			})
			created = made
			return err
		}); err != nil {
			return fmt.Errorf("%s %q: %w", declared.Type, declared.Title, err)
		}
		switch declared.Type {
		case "whiteboard":
			if err := a.whiteboard(ctx, author, created.ID, declared); err != nil {
				return fmt.Errorf("whiteboard %q: %w", declared.Title, err)
			}
		case "database":
			if err := a.database(ctx, author, created.ID, declared); err != nil {
				return fmt.Errorf("database %q: %w", declared.Title, err)
			}
		}
	}
	return nil
}

// whiteboard draws a whiteboard's canvas: the objects on it, then the lines
// between them, which name the objects they join by title.
func (a *Applier) whiteboard(ctx context.Context, author, whiteboardID string, declared SpaceContent) error {
	drawn := map[string]string{}
	for _, object := range declared.Objects {
		width, height := object.Width, object.Height
		if width == 0 {
			width = 200
		}
		if height == 0 {
			height = 120
		}
		color := object.Color
		if color == "" {
			color = "yellow"
		}
		kind := object.Type
		if kind == "" {
			kind = "sticky"
		}
		if err := a.at(ctx, declared.CreatedDay, func(ctx context.Context) error {
			made, err := a.Store.SaveWikiWhiteboardObject(ctx, a.workspaceID, author, whiteboardID, models.WikiWhiteboardObject{
				Type: kind, Title: object.Title, Body: object.Body, Color: color,
				X: object.X, Y: object.Y, Width: width, Height: height,
			})
			if err == nil {
				drawn[object.Title] = made.ID
			}
			return err
		}); err != nil {
			return fmt.Errorf("object %q: %w", object.Title, err)
		}
	}
	for _, connector := range declared.Connectors {
		from, to := drawn[connector.From], drawn[connector.To]
		if from == "" || to == "" {
			return fmt.Errorf("a line joins %q to %q, and one of them is not on the board", connector.From, connector.To)
		}
		style := connector.Style
		if style == "" {
			style = "solid"
		}
		if err := a.at(ctx, declared.CreatedDay, func(ctx context.Context) error {
			_, err := a.Store.SaveWikiWhiteboardConnector(ctx, a.workspaceID, author, whiteboardID, models.WikiWhiteboardConnector{
				FromObjectID: from, ToObjectID: to, Label: connector.Label, Style: style,
			})
			return err
		}); err != nil {
			return fmt.Errorf("line from %q to %q: %w", connector.From, connector.To, err)
		}
	}
	return nil
}

// database fills a database: its columns, the records in it, and the views
// people read them through.
func (a *Applier) database(ctx context.Context, author, databaseID string, declared SpaceContent) error {
	keys := map[string]string{}
	for _, column := range declared.Columns {
		key := databaseColumnKey(column.Name)
		keys[column.Name] = key
		kind := column.Type
		if kind == "" {
			kind = "text"
		}
		if err := a.at(ctx, declared.CreatedDay, func(ctx context.Context) error {
			_, err := a.Store.AddWikiDatabaseColumn(ctx, a.workspaceID, author, databaseID, models.WikiDatabaseColumn{
				Key: key, Name: column.Name, Type: kind, Options: column.Options,
			})
			return err
		}); err != nil {
			return fmt.Errorf("column %q: %w", column.Name, err)
		}
	}
	for index, row := range declared.Rows {
		values := map[string]string{}
		for name, value := range row {
			key, ok := keys[name]
			if !ok {
				return fmt.Errorf("row %d sets %q, which is not a column", index+1, name)
			}
			values[key] = value
		}
		if err := a.at(ctx, declared.CreatedDay, func(ctx context.Context) error {
			_, err := a.Store.SaveWikiDatabaseRow(ctx, a.workspaceID, author, databaseID, "", values)
			return err
		}); err != nil {
			return fmt.Errorf("row %d: %w", index+1, err)
		}
	}
	for _, view := range declared.Views {
		direction := view.SortDirection
		if direction == "" {
			direction = "asc"
		}
		if err := a.at(ctx, declared.CreatedDay, func(ctx context.Context) error {
			_, err := a.Store.SaveWikiDatabaseView(ctx, a.workspaceID, author, databaseID, models.WikiDatabaseView{
				Name: view.Name, SortKey: keys[view.SortKey], SortDirection: direction,
				FilterKey: keys[view.FilterKey], FilterValue: view.FilterValue,
			})
			return err
		}); err != nil {
			return fmt.Errorf("view %q: %w", view.Name, err)
		}
	}
	return nil
}

// databaseColumnKey is the key a column is stored under: its name, lower case,
// with anything that is not a letter or a number as an underscore.
func databaseColumnKey(name string) string {
	key := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '_'
	}, name)
	if key == "" {
		return "column"
	}
	return key
}

// pages writes one level of a space's page tree and then its children.
func (a *Applier) pages(ctx context.Context, spaceID, parentID string, pages []Page) error {
	for _, page := range pages {
		author := a.people[page.Author]
		if author == "" {
			author = a.admin
		}
		// A page the scenario already wrote in this space is that page: it
		// keeps the body someone may have edited and is not commented on
		// twice. Its children are still walked, so an edited scenario can add
		// a page under one that is already there.
		written, err := a.Store.WikiPages(ctx, a.workspaceID, a.admin, spaceID, "current", page.Title)
		if err != nil {
			return fmt.Errorf("read the pages of %s: %w", page.Title, err)
		}
		var saved *models.WikiPage
		if len(written) > 0 {
			saved = written[0]
			if err := a.pages(ctx, spaceID, saved.ID, page.Children); err != nil {
				return err
			}
			continue
		}
		if err := a.at(ctx, page.CreatedDay, func(ctx context.Context) error {
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
			if err := a.at(ctx, comment.Day, func(ctx context.Context) error {
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
	// Each row takes the time of the action that made it; the log itself was
	// written with those times, so there is nothing to move.
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

// teamsAndPlans builds the company's delivery teams and the plans that
// schedule them: a site with years of work in it is also a site somebody is
// planning, and a plan with no teams in it plans nothing.
func (a *Applier) teamsAndPlans(ctx context.Context, scenario *Scenario) error {
	teams := map[string]string{}
	if scenario.Generate != nil {
		for _, declared := range scenario.Generate.Teams {
			existing, err := a.Store.AtlassianTeams(ctx, a.workspaceID)
			if err != nil {
				return err
			}
			id := ""
			for _, found := range existing {
				if found.Name == declared.Name {
					id = found.ID
					break
				}
			}
			if id == "" {
				id, err = a.Store.CreateAtlassianTeam(ctx, a.workspaceID, a.admin, declared.Name,
					fmt.Sprintf("%s, who work on %s", declared.Name, strings.Join(declared.Projects, ", ")))
				if err != nil {
					return fmt.Errorf("team %s: %w", declared.Name, err)
				}
			}
			teams[declared.Name] = id
			for _, member := range declared.Members {
				if err := a.Store.SetAtlassianTeamMember(ctx, a.workspaceID, id, a.people[member], true); err != nil {
					return fmt.Errorf("team %s member %s: %w", declared.Name, member, err)
				}
			}
		}
	}
	for _, declared := range scenario.Plans {
		plans, _, err := a.Store.Plans(ctx, a.workspaceID, false, false, 0, 100)
		if err != nil {
			return err
		}
		existing := int64(0)
		for _, found := range plans {
			if found.Name == declared.Name {
				existing = found.ID
				break
			}
		}
		if existing != 0 {
			continue
		}
		plan := store.Plan{
			Name: declared.Name, LeadAccountID: a.people[declared.Lead], Status: "Active",
			Scheduling: store.PlanScheduling{Estimation: "StoryPoints"},
		}
		for _, project := range declared.Projects {
			id, err := strconv.ParseInt(a.projects[project].ID, 10, 64)
			if err != nil {
				return fmt.Errorf("plan %s: project %s: %w", declared.Name, project, err)
			}
			plan.IssueSources = append(plan.IssueSources, store.PlanIssueSource{Type: "Project", Value: id})
		}
		for _, board := range declared.Boards {
			id, err := strconv.ParseInt(a.boards[board].ID, 10, 64)
			if err != nil {
				return fmt.Errorf("plan %s: board %s: %w", declared.Name, board, err)
			}
			plan.IssueSources = append(plan.IssueSources, store.PlanIssueSource{Type: "Board", Value: id})
		}
		for _, release := range declared.Releases {
			grouped := store.PlanRelease{Name: release.Name}
			for _, version := range release.Versions {
				id, err := strconv.ParseInt(a.versions[version].ID, 10, 64)
				if err != nil {
					return fmt.Errorf("plan %s: release %s: version %s: %w", declared.Name, release.Name, version, err)
				}
				grouped.ReleaseIDs = append(grouped.ReleaseIDs, id)
			}
			plan.CrossProjectReleases = append(plan.CrossProjectReleases, grouped)
		}
		store.NormalizePlan(&plan)
		planID, err := a.Store.CreatePlan(ctx, a.workspaceID, a.admin, plan)
		if err != nil {
			return fmt.Errorf("plan %s: %w", declared.Name, err)
		}
		for _, team := range declared.Teams {
			if _, err := a.Store.SavePlanTeam(ctx, a.workspaceID, planID, store.PlanTeam{
				AtlassianTeamID: teams[team], PlanningStyle: "Scrum",
			}, true); err != nil {
				return fmt.Errorf("plan %s: team %s: %w", declared.Name, team, err)
			}
		}
	}
	return nil
}

// commits records what was written before each delivery, so the delivery
// report can say how long a change took to reach production. They are written
// straight to the development store, as a source control integration would
// send them.
func (a *Applier) commits(ctx context.Context, declared []Commit) error {
	if len(declared) == 0 {
		return nil
	}
	repositories := map[string]*models.DevelopmentRepository{}
	order := []string{}
	for index, commit := range declared {
		name := commit.Repository
		if name == "" {
			name = "zzira"
		}
		repository, known := repositories[name]
		if !known {
			payload, err := json.Marshal(map[string]any{
				"id": name, "name": name, "url": "https://git.example.test/" + name, "updateSequenceId": 1,
			})
			if err != nil {
				return err
			}
			repository = &models.DevelopmentRepository{
				ID: name, UpdateSequenceID: 1, Name: name,
				URL: "https://git.example.test/" + name, Payload: payload, Properties: json.RawMessage(`{}`),
			}
			repositories[name] = repository
			order = append(order, name)
		}
		keys := make([]string, 0, len(commit.WorkItems))
		for _, item := range commit.WorkItems {
			if found := a.items[item]; found != nil {
				keys = append(keys, found.Key)
			}
		}
		if len(keys) == 0 {
			continue
		}
		// A commit is written on the day it was written, spread through that
		// day like every other write; numbering them across the whole history
		// would push the last of them months past the day they belong to.
		a.mutex.Lock()
		ordinal := a.ordinals[commit.Day]
		a.ordinals[commit.Day] = ordinal + 1
		a.mutex.Unlock()
		at := a.Clock.At(commit.Day, ordinal)
		message := commit.Message
		if message == "" {
			message = "Change " + commit.ID
		}
		payload, err := json.Marshal(map[string]any{
			"id": commit.ID, "displayId": commit.ID, "message": message, "updateSequenceId": index + 1,
			"url":             "https://git.example.test/" + name + "/commit/" + commit.ID,
			"authorTimestamp": at.Format(time.RFC3339), "issueKeys": keys,
		})
		if err != nil {
			return err
		}
		occurred := at
		repository.Entities = append(repository.Entities, models.DevelopmentEntity{
			RepositoryID: name, Type: "commit", ID: commit.ID, UpdateSequenceID: int64(index + 1),
			IssueKeys: keys, Name: message, URL: "https://git.example.test/" + name + "/commit/" + commit.ID,
			OccurredAt: &occurred, Payload: payload,
		})
	}
	written := make([]models.DevelopmentRepository, 0, len(order))
	for _, name := range order {
		written = append(written, *repositories[name])
	}
	if _, err := a.Store.UpsertDevelopmentRepositories(ctx, a.workspaceID, written); err != nil {
		return fmt.Errorf("record commits: %w", err)
	}
	return nil
}

// automationRules writes the rules the company runs. A site where nothing is
// automated says nothing about what automation does, and these are the rules
// a company like this one would have written: a triage rule, a release rule,
// a rule somebody runs by hand.
func (a *Applier) automationRules(ctx context.Context, declared []AutomationRule) error {
	if len(declared) == 0 {
		return nil
	}
	service := &automation.Service{Store: a.Store, Commands: a.Commands}
	cloudID, err := service.WorkspaceCloudID(ctx, a.workspaceID)
	if err != nil {
		return err
	}
	existing, err := service.Rules(ctx, a.workspaceID, automation.SummaryFilter{Limit: 100})
	if err != nil {
		return err
	}
	written := map[string]bool{}
	for _, rule := range existing.Rules {
		written[rule.Name] = true
	}
	for _, rule := range declared {
		if written[rule.Name] {
			continue
		}
		components := make([]map[string]any, 0, len(rule.Actions))
		for _, action := range rule.Actions {
			components = append(components, automationComponent(action))
		}
		scope := make([]string, 0, len(rule.Projects))
		for _, project := range rule.Projects {
			scope = append(scope, "ari:cloud:jira:"+cloudID+":project/"+a.projects[project].ID)
		}
		state := rule.State
		if state == "" {
			state = "ENABLED"
		}
		trigger := map[string]any{"component": "TRIGGER", "schemaVersion": 1, "type": rule.Trigger, "value": automationTriggerValue(rule)}
		body, err := json.Marshal(map[string]any{"rule": map[string]any{
			"actor": map[string]string{"actor": a.people[rule.Actor], "type": "ACCOUNT_ID"},
			"name":  rule.Name, "description": "", "state": state, "labels": []string{},
			"ruleScopeARIs": scope, "components": components, "trigger": trigger,
			"canOtherRuleTrigger": false, "notifyOnError": "FIRSTERROR", "writeAccessType": "OWNER_ONLY",
		}, "connections": []any{}})
		if err != nil {
			return err
		}
		if _, err := service.CreateRule(ctx, a.workspaceID, a.admin, body); err != nil {
			return fmt.Errorf("automation rule %s: %w", rule.Name, err)
		}
	}
	return nil
}

// automationTriggerValue is what a demo rule's trigger carries: a schedule, a
// query, or nothing.
func automationTriggerValue(rule AutomationRule) map[string]any {
	switch {
	case rule.IntervalMinutes > 0:
		return map[string]any{"intervalMinutes": rule.IntervalMinutes, "timezone": "UTC", "jql": rule.JQL}
	case rule.Trigger == automation.ManualTriggerType:
		return map[string]any{"inputPrompts": []any{}}
	default:
		return map[string]any{"jql": rule.JQL}
	}
}

// automationComponent is one action as a rule's payload carries it.
func automationComponent(action AutomationAction) map[string]any {
	value := map[string]string{}
	switch action.Type {
	case "jira.issue.comment":
		value["comment"] = action.Value
	case "jira.issue.add-label", "jira.issue.remove-label":
		value["label"] = action.Value
	case "jira.issue.assign":
		value["method"] = action.Value
	case "jira.issue.transition":
		value["statusId"] = action.Value
	case "jira.issue.edit":
		value["field"], value["value"] = action.Field, action.Value
	case "jira.create.variable":
		value["variableName"], value["variableValue"] = action.Variable, action.Value
	case "jira.issue.lookup":
		value["jql"] = action.Value
	case "confluence.page.comment":
		value["comment"] = action.Value
	case "confluence.page.label":
		value["label"] = action.Value
	default:
		value["value"] = action.Value
	}
	return map[string]any{"component": "ACTION", "schemaVersion": 1, "type": action.Type, "value": value}
}
