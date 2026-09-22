// Package demo builds a working site from a declarative scenario: a company's
// people, projects, work, releases, deliveries, service requests and knowledge,
// described once and applied through the ordinary command layer.
//
// Every moment in a scenario is a day offset from the day it is applied, so a
// scenario always produces a site whose history ends today: sprints that closed
// last month, a release that shipped three weeks ago, incidents recent enough
// for the delivery metrics to mean something.
package demo

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/models"
	"github.com/e6qu/zzira/internal/store"
)

// Scenario is a whole company, declared.
type Scenario struct {
	// Name and Description say what the scenario is for; they are not applied.
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Site is the workspace the scenario builds.
	Site Site `json:"site"`
	// Groups and People are the accounts the rest of the scenario refers to.
	Groups []Group  `json:"groups,omitempty"`
	People []Person `json:"people"`
	// Hierarchy adds levels above Epic, with the work types that sit on them.
	Hierarchy []HierarchyLevel `json:"hierarchy,omitempty"`
	// CustomFields are declared before the work that uses them.
	CustomFields []CustomField `json:"customFields,omitempty"`
	Projects     []Project     `json:"projects"`
	WorkItems    []WorkItem    `json:"workItems,omitempty"`
	// Deployments and Commits feed the delivery (DORA) report.
	Deployments []Deployment `json:"deployments,omitempty"`
	Commits     []Commit     `json:"commits,omitempty"`
	// Service is the service desk's customers and their requests.
	Service *Service `json:"service,omitempty"`
	// Wiki is the knowledge base: spaces, pages and blog posts.
	Wiki *Wiki `json:"wiki,omitempty"`
	// Filters and Dashboards are what people saved for themselves.
	Filters    []Filter    `json:"filters,omitempty"`
	Dashboards []Dashboard `json:"dashboards,omitempty"`
	// Automation are the rules the company runs: a site where nothing is
	// automated says nothing about what automation does.
	Automation []AutomationRule `json:"automation,omitempty"`
	// Plans are the cross-project plans people run the company by.
	Plans []Plan `json:"plans,omitempty"`
	// Translations are the words on a work item in the other languages the
	// company reads: a site that speaks one language says nothing about what
	// translating it does.
	Translations []Translation `json:"translations,omitempty"`
	// Generate is the history the scenario grows for itself: years of
	// sprints, releases, work and deliveries that nobody would write out by
	// hand. It is expanded before the scenario is checked.
	Generate *Generation `json:"generate,omitempty"`
}

// Site is the workspace a scenario builds.
type Site struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// Group is a directory group people belong to.
type Group struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Person is an account. Role is "admin" or "member"; an admin administers the
// site.
type Person struct {
	ID          string   `json:"id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"displayName"`
	Password    string   `json:"password,omitempty"`
	Role        string   `json:"role,omitempty"`
	Groups      []string `json:"groups,omitempty"`
	// Customer marks a service desk customer, who has no seat on the site.
	Customer bool `json:"customer,omitempty"`
}

// HierarchyLevel is one level above Epic and the work types on it. Levels are
// numbered from Epic upwards: Epic is 1, so the level above it is 2.
type HierarchyLevel struct {
	Level     int      `json:"level"`
	Name      string   `json:"name"`
	WorkTypes []string `json:"workTypes"`
}

// CustomField is a field the work items use. Type is one of the field types
// the forms render, such as "number" or "select".
type CustomField struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
}

// Project is one project, with what it needs to look lived-in: components,
// versions and, for software, a board with its sprints.
type Project struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Type        string `json:"type"`
	// Template is the Jira project template key; empty takes the default for
	// the type, which a service project does not have.
	Template   string      `json:"template,omitempty"`
	Lead       string      `json:"lead"`
	Components []Component `json:"components,omitempty"`
	Versions   []Version   `json:"versions,omitempty"`
	Board      *Board      `json:"board,omitempty"`
	// ServiceDesk names this project's desk when the project is a service
	// project.
	ServiceDesk *ServiceDesk `json:"serviceDesk,omitempty"`
	// IncidentJQL is what this project counts as an incident for its delivery
	// metrics. A project that does not say counts its service desk's incident
	// requests, which a project without a desk never has -- so a delivery team
	// that says nothing can never show a time to restore.
	IncidentJQL string `json:"incidentJql,omitempty"`
}

// Component is a part of a project, optionally with its own lead.
type Component struct {
	Name string `json:"name"`
	Lead string `json:"lead,omitempty"`
}

// Version is a release of a project. StartDay and ReleaseDay are day offsets;
// Released marks a version that has shipped.
type Version struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	StartDay    *int   `json:"startDay,omitempty"`
	ReleaseDay  *int   `json:"releaseDay,omitempty"`
	Released    bool   `json:"released,omitempty"`
}

// Board is a project's board and the sprints it has run.
type Board struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Sprints []Sprint `json:"sprints,omitempty"`
}

// Sprint is one sprint. State is "closed", "active" or "future"; a closed
// sprint needs both StartDay and EndDay in the past.
type Sprint struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Goal     string `json:"goal,omitempty"`
	StartDay *int   `json:"startDay,omitempty"`
	EndDay   *int   `json:"endDay,omitempty"`
	State    string `json:"state,omitempty"`
}

// ServiceDesk is a service project's portal.
type ServiceDesk struct {
	PortalName string   `json:"portalName"`
	Agents     []string `json:"agents,omitempty"`
	// Queues are the desk's own queues, beyond the three every desk is given.
	// A queue is a name and the JQL an agent works from.
	Queues       []ServiceQueue `json:"queues,omitempty"`
	RequestTypes []RequestType  `json:"requestTypes,omitempty"`
}

// ServiceQueue is a queue an agent works from.
type ServiceQueue struct {
	Name string `json:"name"`
	JQL  string `json:"jql"`
}

// RequestType is one thing a customer can ask for.
type RequestType struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Group is "incidents", "problems", "changes" or empty for ordinary help.
	Group string `json:"group,omitempty"`
}

// WorkItem is one piece of work and everything that happened to it.
type WorkItem struct {
	ID          string   `json:"id"`
	Project     string   `json:"project"`
	Type        string   `json:"type"`
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Reporter    string   `json:"reporter,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Labels      []string `json:"labels,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	Components  []string `json:"components,omitempty"`
	FixVersions []string `json:"fixVersions,omitempty"`
	Sprint      string   `json:"sprint,omitempty"`
	// Estimate is the original estimate as Jira writes one -- "2d", "4h",
	// "1w 2d" -- and logging work moves the remaining estimate down from it.
	Estimate string `json:"estimate,omitempty"`
	// Due is the day offset the work is due on, as a version has a release
	// day. Nil leaves the work with no due date.
	Due        *int              `json:"due,omitempty"`
	Fields     map[string]string `json:"fields,omitempty"`
	CreatedDay int               `json:"createdDay"`
	Events     []Event           `json:"events,omitempty"`
	// Generated marks work the generator wrote rather than a person. Such
	// work refers to nothing outside its own project, so a project's history
	// can be applied while another project's is being applied.
	Generated bool `json:"-"`
}

// Event is something that happened to a work item on a given day. Kind is
// "transition", "comment", "worklog", "assign", "label", "link", "watch" or
// "vote".
type Event struct {
	Day    int    `json:"day"`
	Kind   string `json:"kind"`
	Actor  string `json:"actor,omitempty"`
	Status string `json:"status,omitempty"`
	// Resolution is set with a transition that finishes the work.
	Resolution string `json:"resolution,omitempty"`
	Body       string `json:"body,omitempty"`
	// Internal marks a comment on a service request as an agent's internal
	// note rather than a reply the customer sees. A public reply is what
	// stops the desk's first response clock; a note does not.
	Internal bool   `json:"internal,omitempty"`
	Seconds  int    `json:"seconds,omitempty"`
	Assignee string `json:"assignee,omitempty"`
	LinkType string `json:"linkType,omitempty"`
	Target   string `json:"target,omitempty"`
	// Approvers are the people an "approval" event asks to approve a service
	// request. An "approve" or "decline" event afterwards is one of them
	// answering, and the actor is which one.
	Approvers []string `json:"approvers,omitempty"`
	// Asset is the object a service request is about, by scenario id, on an
	// "asset" event. Role is "affected" or "depends_on"; empty is affected.
	Asset string `json:"asset,omitempty"`
	Role  string `json:"role,omitempty"`
}

// Deployment is one delivery to an environment, and what it carried.
type Deployment struct {
	Pipeline    string `json:"pipeline"`
	Environment string `json:"environment"`
	// Type is the environment type the delivery report counts, such as
	// "production" or "staging".
	Type      string   `json:"type"`
	State     string   `json:"state"`
	Day       int      `json:"day"`
	WorkItems []string `json:"workItems,omitempty"`
}

// Commit is one change somebody pushed, and the work it was for. The delivery
// report reads commits to say how long a change took to reach production, so
// a company with no commits has no lead time.
type Commit struct {
	ID string `json:"id"`
	// Repository is what it was pushed to; a project's own by default.
	Repository string   `json:"repository"`
	Message    string   `json:"message,omitempty"`
	Day        int      `json:"day"`
	WorkItems  []string `json:"workItems,omitempty"`
}

// Service is the service desk's customers and the requests they raised.
type Service struct {
	Organizations []Organization   `json:"organizations,omitempty"`
	Requests      []ServiceRequest `json:"requests,omitempty"`
	// Assets is the inventory the desk runs on: what the company owns, what
	// depends on what, and which of it a request is about.
	Assets *Assets `json:"assets,omitempty"`
}

// Assets is a service desk's configuration management database: the schemas
// its objects are described by, the objects themselves, and how they depend
// on one another.
type Assets struct {
	// Project is the scenario id of the service project whose desk owns it.
	Project string        `json:"project"`
	Schemas []AssetSchema `json:"schemas,omitempty"`
	// Relationships say what an object depends on, by object id.
	Relationships []AssetRelationship `json:"relationships,omitempty"`
}

// AssetSchema is one kind of thing the company keeps track of.
type AssetSchema struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Attributes  []string `json:"attributes,omitempty"`
	Objects     []Asset  `json:"objects,omitempty"`
}

// Asset is one thing the company owns or runs.
type Asset struct {
	ID     string            `json:"id"`
	Key    string            `json:"key"`
	Label  string            `json:"label"`
	Values map[string]string `json:"values,omitempty"`
}

// AssetRelationship is what one object needs from another.
type AssetRelationship struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// Organization groups customers, as Jira Service Management does.
type Organization struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members,omitempty"`
}

// ServiceRequest is one request a customer raised through a portal.
type ServiceRequest struct {
	ID          string  `json:"id"`
	Project     string  `json:"project"`
	RequestType string  `json:"requestType"`
	Customer    string  `json:"customer"`
	Summary     string  `json:"summary"`
	Description string  `json:"description,omitempty"`
	CreatedDay  int     `json:"createdDay"`
	Events      []Event `json:"events,omitempty"`
	// Satisfaction is the customer's rating, 1 to 5, left after resolution.
	Satisfaction int    `json:"satisfaction,omitempty"`
	Feedback     string `json:"feedback,omitempty"`
}

// Wiki is the knowledge base.
type Wiki struct {
	Spaces []Space `json:"spaces,omitempty"`
}

// Space is one Confluence space with its content.
type Space struct {
	Key         string     `json:"key"`
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	Pages       []Page     `json:"pages,omitempty"`
	BlogPosts   []BlogPost `json:"blogPosts,omitempty"`
	// Content is what a space holds beside its pages: whiteboards, databases,
	// folders and embedded pages from elsewhere.
	Content []SpaceContent `json:"content,omitempty"`
}

// SpaceContent is one whiteboard, database, folder or embed in a space.
type SpaceContent struct {
	// Type is "whiteboard", "database", "folder" or "embed".
	Type  string `json:"type"`
	Title string `json:"title"`
	// Author is who made it; the site administrator when it is not said.
	Author string `json:"author,omitempty"`
	// CreatedDay is the day offset it was made on.
	CreatedDay int `json:"createdDay,omitempty"`
	// EmbedURL is what an embed shows.
	EmbedURL string `json:"embedUrl,omitempty"`
	// Objects and Connectors are a whiteboard's canvas; a connector names the
	// objects it joins by their titles.
	Objects    []WhiteboardObject    `json:"objects,omitempty"`
	Connectors []WhiteboardConnector `json:"connectors,omitempty"`
	// Columns, Rows and Views are a database's shape, its records and the
	// ways people read them. A row's keys are column names.
	Columns []DatabaseColumn    `json:"columns,omitempty"`
	Rows    []map[string]string `json:"rows,omitempty"`
	Views   []DatabaseView      `json:"views,omitempty"`
}

// WhiteboardObject is one thing on a whiteboard: a sticky, a piece of text or
// a shape.
type WhiteboardObject struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Color  string `json:"color,omitempty"`
	X      int    `json:"x"`
	Y      int    `json:"y"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// WhiteboardConnector joins two objects by their titles.
type WhiteboardConnector struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Style string `json:"style,omitempty"`
}

// DatabaseColumn is one field of a database.
type DatabaseColumn struct {
	Name string `json:"name"`
	// Type is "text", "number", "date", "checkbox" or "select".
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
}

// DatabaseView is a saved way of reading a database.
type DatabaseView struct {
	Name          string `json:"name"`
	SortKey       string `json:"sortKey,omitempty"`
	SortDirection string `json:"sortDirection,omitempty"`
	FilterKey     string `json:"filterKey,omitempty"`
	FilterValue   string `json:"filterValue,omitempty"`
}

// Page is one page, with its children beneath it.
type Page struct {
	ID         string        `json:"id"`
	Title      string        `json:"title"`
	Body       string        `json:"body"`
	Author     string        `json:"author,omitempty"`
	CreatedDay int           `json:"createdDay"`
	Comments   []PageComment `json:"comments,omitempty"`
	Children   []Page        `json:"children,omitempty"`
}

// BlogPost is one blog post in a space.
type BlogPost struct {
	Title      string `json:"title"`
	Body       string `json:"body"`
	Author     string `json:"author,omitempty"`
	CreatedDay int    `json:"createdDay"`
}

// PageComment is a comment on a page.
type PageComment struct {
	Author string `json:"author,omitempty"`
	Body   string `json:"body"`
	Day    int    `json:"day"`
}

// Filter is a saved search.
type Filter struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	JQL         string `json:"jql"`
	Owner       string `json:"owner,omitempty"`
	Shared      bool   `json:"shared,omitempty"`
}

// Dashboard is a saved dashboard and the gadgets on it.
type Dashboard struct {
	Name    string   `json:"name"`
	Owner   string   `json:"owner,omitempty"`
	Shared  bool     `json:"shared,omitempty"`
	Gadgets []Gadget `json:"gadgets,omitempty"`
}

// Gadget is one gadget on a dashboard. Type is the catalog key without its
// "com.zzira:" prefix; the rest is what that kind of gadget reads.
type Gadget struct {
	Type    string `json:"type"`
	Title   string `json:"title,omitempty"`
	Project string `json:"project,omitempty"`
	Filter  string `json:"filter,omitempty"`
	// Board is the scrum board a sprint gadget follows, by scenario id.
	Board string `json:"board,omitempty"`
	// JQL is what a query gadget counts or lists when it does not use a
	// saved filter.
	JQL string `json:"jql,omitempty"`
	// GroupBy and YGroupBy are what a chart gadget counts by.
	GroupBy  string `json:"groupBy,omitempty"`
	YGroupBy string `json:"yGroupBy,omitempty"`
	// Days is a report gadget's window: 7, 30 or 90.
	Days int `json:"days,omitempty"`
	// DateField is what the time since chart counts, and Cumulative whether
	// created vs resolved shows running totals.
	DateField  string `json:"dateField,omitempty"`
	Cumulative bool   `json:"cumulative,omitempty"`
	// BubbleAxis is what a bubble chart sizes its bubbles by: participants
	// or votes.
	BubbleAxis string `json:"bubbleAxis,omitempty"`
}

// AutomationRule is one rule the company runs. Trigger is the trigger type,
// and the actions are the catalog's, named as the rule payload names them.
type AutomationRule struct {
	Name string `json:"name"`
	// Actor is who the rule runs as; Author who wrote it.
	Actor string `json:"actor"`
	// State is "ENABLED" or "DISABLED".
	State string `json:"state,omitempty"`
	// Trigger is a trigger type such as jira.issue.event.trigger:created, a
	// scheduled trigger, or the manual trigger.
	Trigger string `json:"trigger"`
	// JQL narrows an event trigger, or is what a scheduled rule runs for.
	JQL string `json:"jql,omitempty"`
	// IntervalMinutes is how often a scheduled rule runs.
	IntervalMinutes int `json:"intervalMinutes,omitempty"`
	// Projects are the scenario project ids the rule is scoped to; empty is
	// the whole site.
	Projects []string `json:"projects,omitempty"`
	// Actions are what it does.
	Actions []AutomationAction `json:"actions"`
}

// AutomationAction is one thing a demo rule does.
type AutomationAction struct {
	// Type is the action type, such as jira.issue.comment.
	Type string `json:"type"`
	// Value is what that action takes: the comment, the label, the query.
	Value string `json:"value,omitempty"`
	// Field and Variable are what an edit sets and what a lookup or a
	// variable is called.
	Field    string `json:"field,omitempty"`
	Variable string `json:"variable,omitempty"`
}

// Plan is one cross-project plan: what it draws from, and the teams that do
// the work in it. Teams come from the generated history, which is where the
// company's teams are declared.
type Plan struct {
	Name string `json:"name"`
	Lead string `json:"lead"`
	// Projects and Boards are the scenario ids the plan schedules.
	Projects []string `json:"projects,omitempty"`
	Boards   []string `json:"boards,omitempty"`
	// Teams are the names of the teams that work in this plan.
	Teams []string `json:"teams,omitempty"`
	// Releases are the cross-project releases the plan groups: the versions
	// from several projects that ship as one thing.
	Releases []PlanRelease `json:"releases,omitempty"`
}

// PlanRelease groups versions from several projects that ship together.
type PlanRelease struct {
	Name string `json:"name"`
	// Versions are the scenario ids of the versions in it.
	Versions []string `json:"versions,omitempty"`
}

// Read parses a scenario and checks that it hangs together.
func Read(r io.Reader) (*Scenario, error) {
	var scenario Scenario
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&scenario); err != nil {
		return nil, fmt.Errorf("read scenario: %w", err)
	}
	// The declared history grows before anything is checked, so generated
	// work is held to the same rules as work somebody wrote by hand.
	if err := scenario.Expand(); err != nil {
		return nil, err
	}
	if err := scenario.Validate(); err != nil {
		return nil, err
	}
	return &scenario, nil
}

// Write writes a scenario as the JSON a person edits: indented, with a
// trailing newline.
func Write(w io.Writer, scenario *Scenario) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(scenario)
}

var (
	projectKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
	spaceKeyPattern   = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
	eventKinds        = []string{"transition", "comment", "worklog", "assign", "link", "watch", "vote", "approval", "approve", "decline", "asset"}
	// What a space holds beside its pages, and what a board and a database
	// are made of.
	spaceContentTypes     = []string{"whiteboard", "database", "folder", "embed"}
	whiteboardObjectTypes = []string{"sticky", "text", "shape"}
	databaseColumnTypes   = []string{"text", "number", "date", "checkbox", "select"}
	projectTypes          = []string{"software", "business", "service_desk"}
	sprintStates          = []string{"future", "active", "closed"}
)

// Validate reports the first thing in a scenario that could not be applied:
// an unknown reference, a duplicate id, or a time that contradicts itself.
// Applying an invalid scenario is refused rather than half-built.
func (s *Scenario) Validate() error {
	if strings.TrimSpace(s.Site.Slug) == "" || strings.TrimSpace(s.Site.Name) == "" {
		return fmt.Errorf("the site needs a slug and a name")
	}
	people := map[string]bool{}
	for _, person := range s.People {
		if person.ID == "" || person.Email == "" || person.DisplayName == "" {
			return fmt.Errorf("person %q needs an id, an email and a display name", person.ID)
		}
		if people[person.ID] {
			return fmt.Errorf("two people share the id %q", person.ID)
		}
		people[person.ID] = true
	}
	groups := map[string]bool{}
	for _, group := range s.Groups {
		if groups[group.ID] {
			return fmt.Errorf("two groups share the id %q", group.ID)
		}
		groups[group.ID] = true
	}
	for _, person := range s.People {
		for _, group := range person.Groups {
			if !groups[group] {
				return fmt.Errorf("person %q is in the unknown group %q", person.ID, group)
			}
		}
	}
	knownPerson := func(where, id string) error {
		if id != "" && !people[id] {
			return fmt.Errorf("%s names the unknown person %q", where, id)
		}
		return nil
	}
	projects, versions, sprints, components := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	requestTypes := map[string]bool{}
	deskAgents := map[string]bool{}
	for _, project := range s.Projects {
		if projects[project.ID] {
			return fmt.Errorf("two projects share the id %q", project.ID)
		}
		projects[project.ID] = true
		if !projectKeyPattern.MatchString(project.Key) {
			return fmt.Errorf("project %q needs a key of 2 to 10 capitals and digits", project.ID)
		}
		if !slices.Contains(projectTypes, project.Type) {
			return fmt.Errorf("project %q has the unknown type %q", project.ID, project.Type)
		}
		if err := knownPerson("project "+project.ID, project.Lead); err != nil {
			return err
		}
		for _, component := range project.Components {
			components[project.ID+"/"+component.Name] = true
			if err := knownPerson("component "+component.Name, component.Lead); err != nil {
				return err
			}
		}
		for _, version := range project.Versions {
			if versions[version.ID] {
				return fmt.Errorf("two versions share the id %q", version.ID)
			}
			versions[version.ID] = true
			if version.Released && version.ReleaseDay == nil {
				return fmt.Errorf("released version %q needs a releaseDay", version.ID)
			}
		}
		if project.Board != nil {
			for _, sprint := range project.Board.Sprints {
				if sprints[sprint.ID] {
					return fmt.Errorf("two sprints share the id %q", sprint.ID)
				}
				sprints[sprint.ID] = true
				if sprint.State != "" && !slices.Contains(sprintStates, sprint.State) {
					return fmt.Errorf("sprint %q has the unknown state %q", sprint.ID, sprint.State)
				}
				if sprint.StartDay != nil && sprint.EndDay != nil && *sprint.EndDay < *sprint.StartDay {
					return fmt.Errorf("sprint %q ends before it starts", sprint.ID)
				}
			}
		}
		if query := strings.TrimSpace(project.IncidentJQL); query != "" {
			if _, err := jql.Parse(query); err != nil {
				return fmt.Errorf("project %q counts incidents with %q: %w", project.ID, query, err)
			}
		}
		if project.ServiceDesk != nil {
			for _, agent := range project.ServiceDesk.Agents {
				if err := knownPerson("service desk of "+project.ID, agent); err != nil {
					return err
				}
				deskAgents[project.ID+"\x00"+agent] = true
			}
			// A queue whose query does not compile is a queue that breaks the
			// agent view it is on, and the store takes the JQL as given.
			for _, queue := range project.ServiceDesk.Queues {
				if queue.Name == "" || queue.JQL == "" {
					return fmt.Errorf("a queue of %s needs a name and a query", project.ID)
				}
				if _, err := jql.Parse(queue.JQL); err != nil {
					return fmt.Errorf("queue %q of %s: %w", queue.Name, project.ID, err)
				}
			}
			for _, requestType := range project.ServiceDesk.RequestTypes {
				if requestTypes[requestType.ID] {
					return fmt.Errorf("two request types share the id %q", requestType.ID)
				}
				requestTypes[requestType.ID] = true
			}
		}
	}
	for index, level := range s.Hierarchy {
		if level.Level != index+2 {
			return fmt.Errorf("hierarchy levels run upwards from 2; level %q is %d", level.Name, level.Level)
		}
		if level.Name == "" || len(level.WorkTypes) == 0 {
			return fmt.Errorf("hierarchy level %d needs a name and at least one work type", level.Level)
		}
	}
	fields := map[string]bool{}
	for _, field := range s.CustomFields {
		if fields[field.ID] {
			return fmt.Errorf("two custom fields share the id %q", field.ID)
		}
		fields[field.ID] = true
	}
	items := map[string]bool{}
	for _, item := range s.WorkItems {
		if items[item.ID] {
			return fmt.Errorf("two work items share the id %q", item.ID)
		}
		items[item.ID] = true
	}
	for _, item := range s.WorkItems {
		if !projects[item.Project] {
			return fmt.Errorf("work item %q is in the unknown project %q", item.ID, item.Project)
		}
		if item.Summary == "" || item.Type == "" {
			return fmt.Errorf("work item %q needs a type and a summary", item.ID)
		}
		for _, who := range []string{item.Assignee, item.Reporter} {
			if err := knownPerson("work item "+item.ID, who); err != nil {
				return err
			}
		}
		if item.Parent != "" && !items[item.Parent] {
			return fmt.Errorf("work item %q names the unknown parent %q", item.ID, item.Parent)
		}
		if item.Sprint != "" && !sprints[item.Sprint] {
			return fmt.Errorf("work item %q names the unknown sprint %q", item.ID, item.Sprint)
		}
		for _, version := range item.FixVersions {
			if !versions[version] {
				return fmt.Errorf("work item %q names the unknown version %q", item.ID, version)
			}
		}
		for _, component := range item.Components {
			if !components[item.Project+"/"+component] {
				return fmt.Errorf("work item %q names the unknown component %q", item.ID, component)
			}
		}
		for field := range item.Fields {
			if !fields[field] {
				return fmt.Errorf("work item %q sets the unknown custom field %q", item.ID, field)
			}
		}
		// An estimate the applier cannot read would be dropped silently, and
		// the reports made of estimates would be empty without saying why.
		if item.Estimate != "" {
			if _, err := models.ParseJiraDuration(item.Estimate, models.TimeTrackingConfiguration{}); err != nil {
				return fmt.Errorf("work item %q: estimate: %w", item.ID, err)
			}
		}
		if item.Due != nil && *item.Due < item.CreatedDay {
			return fmt.Errorf("work item %q is due on day %d, before it was raised on day %d", item.ID, *item.Due, item.CreatedDay)
		}
		if err := validateEvents("work item "+item.ID, item.CreatedDay, item.Events, people, items, false); err != nil {
			return err
		}
	}
	for _, deployment := range s.Deployments {
		if deployment.Pipeline == "" || deployment.Environment == "" || deployment.Type == "" || deployment.State == "" {
			return fmt.Errorf("a deployment needs a pipeline, an environment, a type and a state")
		}
		for _, item := range deployment.WorkItems {
			if !items[item] {
				return fmt.Errorf("deployment %q carries the unknown work item %q", deployment.Pipeline, item)
			}
		}
	}
	if s.Service != nil {
		customers := map[string]bool{}
		for _, person := range s.People {
			customers[person.ID] = true
		}
		for _, organization := range s.Service.Organizations {
			for _, member := range organization.Members {
				if !customers[member] {
					return fmt.Errorf("organization %q names the unknown person %q", organization.Name, member)
				}
			}
		}
		assets := map[string]bool{}
		if inventory := s.Service.Assets; inventory != nil {
			if !projects[inventory.Project] {
				return fmt.Errorf("the asset inventory is on the unknown project %q", inventory.Project)
			}
			schemaKeys := map[string]bool{}
			for _, schema := range inventory.Schemas {
				if schema.ID == "" || schema.Key == "" || schema.Name == "" {
					return fmt.Errorf("an asset schema needs an id, a key and a name")
				}
				if schemaKeys[schema.Key] {
					return fmt.Errorf("two asset schemas share the key %q", schema.Key)
				}
				schemaKeys[schema.Key] = true
				attributes := map[string]bool{}
				for _, attribute := range schema.Attributes {
					attributes[attribute] = true
				}
				for _, object := range schema.Objects {
					if object.ID == "" || object.Key == "" || object.Label == "" {
						return fmt.Errorf("an object of %q needs an id, a key and a label", schema.Name)
					}
					if assets[object.ID] {
						return fmt.Errorf("two assets share the id %q", object.ID)
					}
					assets[object.ID] = true
					for name := range object.Values {
						if !attributes[name] {
							return fmt.Errorf("asset %q sets %q, which %q does not describe", object.ID, name, schema.Name)
						}
					}
				}
			}
			for _, relation := range inventory.Relationships {
				if !assets[relation.From] || !assets[relation.To] {
					return fmt.Errorf("a relationship names an unknown asset (%q to %q)", relation.From, relation.To)
				}
				if relation.Type == "" {
					return fmt.Errorf("the relationship from %q to %q has no type", relation.From, relation.To)
				}
			}
		}
		for _, request := range s.Service.Requests {
			if !projects[request.Project] {
				return fmt.Errorf("request %q is in the unknown project %q", request.ID, request.Project)
			}
			if !requestTypes[request.RequestType] {
				return fmt.Errorf("request %q names the unknown request type %q", request.ID, request.RequestType)
			}
			if err := knownPerson("request "+request.ID, request.Customer); err != nil {
				return err
			}
			if request.Satisfaction < 0 || request.Satisfaction > 5 {
				return fmt.Errorf("request %q rates satisfaction %d, which is not 1 to 5", request.ID, request.Satisfaction)
			}
			if err := validateEvents("request "+request.ID, request.CreatedDay, request.Events, people, items, true); err != nil {
				return err
			}
			// Answering a request is something an agent of that desk does. A
			// person who is not one cannot see the request at all, so a
			// scenario that has them reply builds a site that refuses it.
			for _, event := range request.Events {
				if event.Kind == "asset" && !assets[event.Asset] {
					return fmt.Errorf("request %q is about the unknown asset %q", request.ID, event.Asset)
				}
				switch event.Kind {
				case "comment", "approval", "asset":
				default:
					continue
				}
				if event.Actor == "" || event.Actor == request.Customer {
					continue
				}
				if !deskAgents[request.Project+"\x00"+event.Actor] {
					return fmt.Errorf("request %q is answered by %q, who is not an agent of the %s desk",
						request.ID, event.Actor, request.Project)
				}
			}
		}
	}
	if s.Wiki != nil {
		spaces := map[string]bool{}
		for _, space := range s.Wiki.Spaces {
			if !spaceKeyPattern.MatchString(space.Key) {
				return fmt.Errorf("space %q needs a key of 2 to 10 capitals and digits", space.Key)
			}
			if spaces[space.Key] {
				return fmt.Errorf("two spaces share the key %q", space.Key)
			}
			spaces[space.Key] = true
			if err := validatePages(space.Key, space.Pages, people); err != nil {
				return err
			}
			for _, post := range space.BlogPosts {
				if err := knownPerson("blog post "+post.Title, post.Author); err != nil {
					return err
				}
			}
			if err := validateSpaceContent(space, people); err != nil {
				return err
			}
		}
	}
	for _, filter := range s.Filters {
		if filter.Name == "" || filter.JQL == "" {
			return fmt.Errorf("a filter needs a name and a JQL query")
		}
		if err := knownPerson("filter "+filter.Name, filter.Owner); err != nil {
			return err
		}
	}
	// A dashboard names the filters and boards the rest of the scenario
	// declared, so a gadget cannot quietly show nothing.
	filters := map[string]bool{}
	for _, filter := range s.Filters {
		filters[filter.Name] = true
	}
	boards := map[string]bool{}
	for _, project := range s.Projects {
		if project.Board != nil {
			boards[project.Board.ID] = true
		}
	}
	teams := map[string]bool{}
	if s.Generate != nil {
		for _, team := range s.Generate.Teams {
			teams[team.Name] = true
		}
	}
	for _, rule := range s.Automation {
		if strings.TrimSpace(rule.Name) == "" || strings.TrimSpace(rule.Trigger) == "" {
			return fmt.Errorf("an automation rule needs a name and a trigger")
		}
		if err := knownPerson("automation rule "+rule.Name, rule.Actor); err != nil {
			return err
		}
		if len(rule.Actions) == 0 {
			return fmt.Errorf("automation rule %q does nothing", rule.Name)
		}
		for _, project := range rule.Projects {
			if !projects[project] {
				return fmt.Errorf("automation rule %q is scoped to the unknown project %q", rule.Name, project)
			}
		}
	}
	for _, translation := range s.Translations {
		if !slices.Contains(store.MetadataTranslationKinds, translation.Kind) {
			return fmt.Errorf("a translation is of the unknown kind %q", translation.Kind)
		}
		if translation.Name == "" || translation.Translated == "" || translation.Locale == "" {
			return fmt.Errorf("a %s translation needs a name, a language and the word in it", translation.Kind)
		}
		if store.NormalizeLocale(translation.Locale) == "" {
			return fmt.Errorf("%q is not a language tag such as es or pt-br", translation.Locale)
		}
	}
	for _, plan := range s.Plans {
		if strings.TrimSpace(plan.Name) == "" {
			return fmt.Errorf("a plan needs a name")
		}
		if err := knownPerson("plan "+plan.Name, plan.Lead); err != nil {
			return err
		}
		for _, project := range plan.Projects {
			if !projects[project] {
				return fmt.Errorf("plan %q draws from the unknown project %q", plan.Name, project)
			}
		}
		for _, board := range plan.Boards {
			if !boards[board] {
				return fmt.Errorf("plan %q draws from the unknown board %q", plan.Name, board)
			}
		}
		for _, team := range plan.Teams {
			if !teams[team] {
				return fmt.Errorf("plan %q names the unknown team %q", plan.Name, team)
			}
		}
		for _, release := range plan.Releases {
			if release.Name == "" {
				return fmt.Errorf("a cross-project release of plan %q has no name", plan.Name)
			}
			if len(release.Versions) < 2 {
				return fmt.Errorf("cross-project release %q groups fewer than two versions, which is not across projects", release.Name)
			}
			for _, version := range release.Versions {
				if !versions[version] {
					return fmt.Errorf("cross-project release %q names the unknown version %q", release.Name, version)
				}
			}
		}
	}
	for _, dashboard := range s.Dashboards {
		if err := knownPerson("dashboard "+dashboard.Name, dashboard.Owner); err != nil {
			return err
		}
		for _, gadget := range dashboard.Gadgets {
			if gadget.Project != "" && !projects[gadget.Project] {
				return fmt.Errorf("dashboard %q shows the unknown project %q", dashboard.Name, gadget.Project)
			}
			if gadget.Board != "" && !boards[gadget.Board] {
				return fmt.Errorf("dashboard %q follows the unknown board %q", dashboard.Name, gadget.Board)
			}
			if gadget.Filter != "" && !filters[gadget.Filter] {
				return fmt.Errorf("dashboard %q shows the unknown filter %q", dashboard.Name, gadget.Filter)
			}
			if gadget.Days != 0 && gadget.Days != 7 && gadget.Days != 30 && gadget.Days != 90 {
				return fmt.Errorf("dashboard %q asks gadget %q for a window of %d days, which is not 7, 30 or 90", dashboard.Name, gadget.Type, gadget.Days)
			}
		}
	}
	return nil
}

// validateEvents checks one timeline: known kinds, known people, and days that
// do not run backwards or start before the work existed.
// validateEvents checks a timeline is one the applier can replay. request says
// whether it belongs to a service request, because asking for an approval is
// something only a request has.
func validateEvents(where string, createdDay int, events []Event, people, items map[string]bool, request bool) error {
	previous, asked := createdDay, false
	for _, event := range events {
		if !slices.Contains(eventKinds, event.Kind) {
			return fmt.Errorf("%s has the unknown event kind %q", where, event.Kind)
		}
		if event.Day < createdDay {
			return fmt.Errorf("%s has a %s before it existed", where, event.Kind)
		}
		// Day zero is the day the site is built, and nothing has happened
		// after it: a history that runs into tomorrow dates work, comments
		// and clocks in the future.
		if event.Day > 0 {
			return fmt.Errorf("%s has a %s on day %d, which is after the day the site is built", where, event.Kind, event.Day)
		}
		if event.Day < previous {
			return fmt.Errorf("%s has events out of order", where)
		}
		previous = event.Day
		if event.Actor != "" && !people[event.Actor] {
			return fmt.Errorf("%s has a %s by the unknown person %q", where, event.Kind, event.Actor)
		}
		switch event.Kind {
		case "transition":
			if event.Status == "" {
				return fmt.Errorf("%s has a transition with no status", where)
			}
		case "comment":
			if event.Body == "" {
				return fmt.Errorf("%s has an empty comment", where)
			}
		case "worklog":
			if event.Seconds <= 0 {
				return fmt.Errorf("%s logs work of %d seconds", where, event.Seconds)
			}
		case "assign":
			if event.Assignee != "" && !people[event.Assignee] {
				return fmt.Errorf("%s assigns to the unknown person %q", where, event.Assignee)
			}
		case "link":
			if event.LinkType == "" || event.Target == "" {
				return fmt.Errorf("%s has a link with no type or target", where)
			}
			if !items[event.Target] {
				return fmt.Errorf("%s links to the unknown work item %q", where, event.Target)
			}
		case "approval":
			if !request {
				return fmt.Errorf("%s asks for an approval, which only a service request has", where)
			}
			if len(event.Approvers) == 0 {
				return fmt.Errorf("%s asks for an approval from nobody", where)
			}
			for _, approver := range event.Approvers {
				if !people[approver] {
					return fmt.Errorf("%s asks the unknown person %q to approve", where, approver)
				}
			}
			if event.Body == "" {
				return fmt.Errorf("%s asks for an approval with no name", where)
			}
			asked = true
		case "approve", "decline":
			if !asked {
				return fmt.Errorf("%s answers an approval nobody asked for", where)
			}
			if event.Actor == "" {
				return fmt.Errorf("%s has an approval answered by nobody", where)
			}
		case "asset":
			if !request {
				return fmt.Errorf("%s connects an asset, which only a service request has", where)
			}
			if event.Asset == "" {
				return fmt.Errorf("%s connects an asset with no id", where)
			}
			if event.Role != "" && event.Role != "affected" && event.Role != "depends_on" {
				return fmt.Errorf("%s connects an asset as %q, which is neither affected nor depends_on", where, event.Role)
			}
		}
	}
	return nil
}

// validatePages checks a space's page tree.
func validatePages(space string, pages []Page, people map[string]bool) error {
	for _, page := range pages {
		if page.Title == "" || page.Body == "" {
			return fmt.Errorf("page %q in space %s needs a title and a body", page.ID, space)
		}
		if page.Author != "" && !people[page.Author] {
			return fmt.Errorf("page %q is by the unknown person %q", page.Title, page.Author)
		}
		for _, comment := range page.Comments {
			if comment.Author != "" && !people[comment.Author] {
				return fmt.Errorf("a comment on %q is by the unknown person %q", page.Title, comment.Author)
			}
		}
		if err := validatePages(space, page.Children, people); err != nil {
			return err
		}
	}
	return nil
}

// validateSpaceContent checks a space's whiteboards, databases, folders and
// embeds are ones the site can build: types it knows, a line between objects
// that are on the board, and a record whose fields are columns.
func validateSpaceContent(space Space, people map[string]bool) error {
	for _, content := range space.Content {
		where := fmt.Sprintf("%s %q of space %s", content.Type, content.Title, space.Key)
		if content.Title == "" {
			return fmt.Errorf("a piece of content in space %s has no title", space.Key)
		}
		if !slices.Contains(spaceContentTypes, content.Type) {
			return fmt.Errorf("%s is of the unknown kind %q", where, content.Type)
		}
		if content.Author != "" && !people[content.Author] {
			return fmt.Errorf("%s was written by the unknown person %q", where, content.Author)
		}
		if content.CreatedDay > 0 {
			return fmt.Errorf("%s was written on day %d, which is after the day the site is built", where, content.CreatedDay)
		}
		if content.Type == "embed" && content.EmbedURL == "" {
			return fmt.Errorf("%s embeds nothing", where)
		}
		objects := map[string]bool{}
		for _, object := range content.Objects {
			if object.Title == "" {
				return fmt.Errorf("%s has an object with no title", where)
			}
			if objects[object.Title] {
				return fmt.Errorf("%s has two objects titled %q, so a line between them is ambiguous", where, object.Title)
			}
			objects[object.Title] = true
			if object.Type != "" && !slices.Contains(whiteboardObjectTypes, object.Type) {
				return fmt.Errorf("%s has a %q, which is not something a board holds", where, object.Type)
			}
		}
		for _, connector := range content.Connectors {
			if !objects[connector.From] || !objects[connector.To] {
				return fmt.Errorf("%s draws a line from %q to %q, and one of them is not on the board", where, connector.From, connector.To)
			}
		}
		columns := map[string]bool{}
		for _, column := range content.Columns {
			if column.Name == "" {
				return fmt.Errorf("%s has a column with no name", where)
			}
			columns[column.Name] = true
			if column.Type != "" && !slices.Contains(databaseColumnTypes, column.Type) {
				return fmt.Errorf("%s has a %q column, which is not a kind of field", where, column.Type)
			}
		}
		for index, row := range content.Rows {
			for name := range row {
				if !columns[name] {
					return fmt.Errorf("%s has a record %d that sets %q, which is not one of its columns", where, index+1, name)
				}
			}
		}
		for _, view := range content.Views {
			if view.Name == "" {
				return fmt.Errorf("%s has a view with no name", where)
			}
			for _, key := range []string{view.SortKey, view.FilterKey} {
				if key != "" && !columns[key] {
					return fmt.Errorf("%s has a view reading %q, which is not one of its columns", where, key)
				}
			}
		}
	}
	return nil
}

// Translation is what the site calls one work type, priority, resolution or
// status in another language. The thing is named by the name the site itself
// gives it, because that is what a person writing a scenario knows.
type Translation struct {
	// Kind is "issuetype", "priority", "resolution" or "status".
	Kind string `json:"kind"`
	// Name is the site's own name for it, and Locale the language tag.
	Name   string `json:"name"`
	Locale string `json:"locale"`
	// Translated is what that language calls it.
	Translated  string `json:"translated"`
	Description string `json:"description,omitempty"`
}

// Clock turns a scenario's day offsets into timestamps. Day 0 is the morning
// of the day the scenario is applied, so every offset is in the past.
type Clock struct {
	origin time.Time
}

// NewClock anchors a scenario to a day.
func NewClock(today time.Time) Clock {
	year, month, day := today.UTC().Date()
	return Clock{origin: time.Date(year, month, day, 9, 0, 0, 0, time.UTC)}
}

// WritesPerDay is how many writes one day of a scenario spreads through its
// working hours before they start sharing a moment. A day holds far fewer than
// this in practice; the cap is what keeps a busy day inside itself.
const WritesPerDay = 4096

// At is the timestamp of a day offset, spread through the working day so that
// events on one day keep their order. The spread stays inside the day: a
// history whose writes ran into the days after it would put work, comments and
// SLA clocks in the future, which is where they were until this was capped.
func (c Clock) At(day, ordinal int) time.Time {
	if ordinal >= WritesPerDay {
		ordinal = WritesPerDay - 1
	}
	return c.origin.AddDate(0, 0, day).Add(time.Duration(ordinal) * 7 * time.Second)
}

// Day is the date of a day offset, for fields that hold a day rather than a
// moment.
func (c Clock) Day(day int) string {
	return c.origin.AddDate(0, 0, day).Format("2006-01-02")
}
