package demo

import (
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// A company that has been running for years is not something anybody writes
// out by hand: three years of a single team's sprints is seventy-eight
// sprints, several hundred pieces of work, and every worklog, comment and
// deployment that went with them. A scenario therefore declares the shape of
// that history -- how long the company has been going, how much work a team
// gets through in a sprint, how often it ships -- and the generator writes it
// out. The result is the same every time, because a demo that changes under
// you is a demo nobody can write a test against.

// Generation is the history a scenario grows for itself.
type Generation struct {
	// Seed makes the generated company the same on every run.
	Seed int64 `json:"seed"`
	// Days is how far back the history reaches.
	Days int `json:"days"`
	// SprintDays is the length of a sprint; 14 when it is not said.
	SprintDays int `json:"sprintDays,omitempty"`
	// Teams are the delivery teams the generated work is shared between.
	Teams []GeneratedTeam `json:"teams,omitempty"`
	// Projects are the projects that grow a history.
	Projects []GeneratedProject `json:"projects,omitempty"`
	// Service is a desk whose queue fills up over the same years, and
	// Services are the rest of them: a company with an external desk and an
	// internal one has two queues, not one twice the size.
	Service  *GeneratedService  `json:"service,omitempty"`
	Services []GeneratedService `json:"services,omitempty"`
	// Knowledge is what the company wrote down as it went.
	Knowledge *GeneratedKnowledge `json:"knowledge,omitempty"`
}

// GeneratedKnowledge is the pages and posts a company accumulates: meeting
// notes, runbooks, decisions and the occasional announcement.
type GeneratedKnowledge struct {
	// Space is the key of the space they are written in.
	Space string `json:"space"`
	// PagesPerMonth and PostsPerQuarter are how much gets written.
	PagesPerMonth   float64 `json:"pagesPerMonth"`
	PostsPerQuarter float64 `json:"postsPerQuarter,omitempty"`
	// Authors are the scenario person ids who write them, and Subjects what
	// they write about.
	Authors  []string `json:"authors,omitempty"`
	Subjects []string `json:"subjects,omitempty"`
}

// GeneratedTeam is a group of people who work on the same projects, so the
// generated work looks like a company rather than a lottery.
type GeneratedTeam struct {
	Name string `json:"name"`
	// Members are scenario person ids; the first is the team's lead.
	Members []string `json:"members"`
	// Projects are the scenario project ids this team works on.
	Projects []string `json:"projects"`
}

// GeneratedService is the stream of requests a service desk answers.
type GeneratedService struct {
	// Project is the scenario id of the service project.
	Project string `json:"project"`
	// RequestsPerWeek is how many customers ask for something.
	RequestsPerWeek float64 `json:"requestsPerWeek"`
	// IncidentShare is how many of those are incidents, which is what the
	// delivery report reads to say how long recovery takes.
	IncidentShare float64 `json:"incidentShare,omitempty"`
	// Customers are the scenario person ids who raise them, and Agents the
	// people who answer.
	Customers []string `json:"customers,omitempty"`
	Agents    []string `json:"agents,omitempty"`
	// RequestTypes are the ids a request can be raised under, and
	// IncidentType the one incidents use.
	RequestTypes []string `json:"requestTypes,omitempty"`
	IncidentType string   `json:"incidentType,omitempty"`
	// ApprovalType is the request type somebody has to approve before it is
	// done -- asking for access, in most companies -- and Approvers are the
	// people who answer. Without approvers the agents answer.
	ApprovalType string   `json:"approvalType,omitempty"`
	Approvers    []string `json:"approvers,omitempty"`
	// Assets are the scenario ids of the objects an incident is about, so
	// the inventory is connected to the incidents that hit it rather than
	// sitting on its own.
	Assets []string `json:"assets,omitempty"`
	// Subjects are what customers write in about.
	Subjects []string `json:"subjects,omitempty"`
}

// GeneratedProject is how much history one project has.
type GeneratedProject struct {
	// Project and Board are scenario ids; a project with no board runs
	// without sprints, as a kanban team does.
	Project string `json:"project"`
	Board   string `json:"board,omitempty"`
	// WorkPerSprint is how much work the team starts in a sprint. A project
	// with no board takes this much work every SprintDays instead.
	WorkPerSprint int `json:"workPerSprint"`
	// ReleaseEvery is how many sprints go into a release; 0 ships nothing.
	ReleaseEvery int `json:"releaseEvery,omitempty"`
	// DeploymentsPerWeek and FailureRate are what the project delivers, and
	// how much of it goes wrong. A project that deploys nothing has no
	// delivery metrics, which is the point of saying so.
	DeploymentsPerWeek float64 `json:"deploymentsPerWeek,omitempty"`
	FailureRate        float64 `json:"failureRate,omitempty"`
	// Themes are what this project's work is about: each piece of work takes
	// one, so a reader can tell a payments backlog from a marketing one.
	Themes []string `json:"themes,omitempty"`
	// BugShare is how much of the work is a defect rather than a feature.
	BugShare float64 `json:"bugShare,omitempty"`
	// Components names the project's components the work is spread over;
	// empty spreads it over every component the project declares.
	Components []string `json:"components,omitempty"`
	// Points is the scenario id of the story points field this project
	// estimates in. Without one its board has nothing to draw a velocity or
	// a burndown from, which is most of what a board is for.
	Points string `json:"points,omitempty"`
	// Impact is the scenario id of a select field the work carries, so the
	// site has custom field values to group, filter and report on.
	Impact string `json:"impact,omitempty"`
	// LinkShare is how much of the work is linked to other work in the same
	// sprint, and WatchShare how much somebody is watching.
	LinkShare  float64 `json:"linkShare,omitempty"`
	WatchShare float64 `json:"watchShare,omitempty"`
}

// generatedVerbs and generatedObjects build summaries that read like work.
var (
	generatedVerbs   = []string{"Add", "Fix", "Speed up", "Simplify", "Document", "Harden", "Retire", "Measure", "Rework", "Report on"}
	generatedDefects = []string{"fails", "times out", "returns the wrong total", "loses its state", "double counts", "stops at the second page"}
	generatedDetail  = []string{
		"Raised after a customer report.", "Found while reading the logs.", "Agreed at the planning session.",
		"Carried over from the last release.", "Asked for by the operations team.", "Follows the design review.",
	}
	generatedComments = []string{
		"Picked this up.", "Reproduced it on staging.", "Waiting on a review.", "Merged and on its way out.",
		"Split the rest into a follow-up.", "Checked with the customer; this is what they meant.",
		"The numbers look right now.", "Rolled the change forward after the fix.",
	}
	generatedEstimates = []string{"1h", "2h", "4h", "1d", "2d", "3d"}
	// generatedServiceNotes are what agents write to each other on a request:
	// a note the customer never sees, which does not answer them.
	generatedServiceNotes = []string{
		"Checked the logs -- this looks like the retry bug.",
		"Waiting on the payments team before I reply.",
		"Same as the ticket from last week; reusing that answer.",
		"Escalating: this account is on the enterprise plan.",
		"Reproduced on staging.",
	}
	generatedServiceReplies = []string{
		"Thanks for writing in -- taking a look now.", "I can see the error on our side.",
		"Could you tell me which account this is on?", "This is fixed; please try again.",
		"Passed this to the payments team.", "Sorry about that. It is back.",
	}
	generatedPageKinds = []string{"how it works", "runbook", "what we decided", "meeting notes", "what went wrong", "how to change it"}
	generatedFeedback  = []string{"Quick and clear, thank you.", "Sorted in a day.", "Fine once it was picked up.", "Fast answer."}
	generatedPriority  = []string{"Low", "Medium", "Medium", "High", "Highest"}
	// generatedPoints is what a team estimates in: a Fibonacci-ish scale,
	// weighted towards the small end as a real backlog is.
	generatedPoints = []string{"1", "2", "2", "3", "3", "5", "5", "8", "13"}
	generatedLinks  = []string{"blocks", "relates to"}
	// generatedImpacts are the options the shipped company's customer impact
	// field offers; a scenario with another field says its own.
	generatedImpacts = []string{"One customer", "Several customers", "Everyone"}
)

// Expand writes the generated history into the scenario: sprints and versions
// on the projects that grow them, the work that went through them, and the
// deliveries that carried it. It is called before the scenario is validated,
// so what it writes is checked like anything a person wrote by hand.
func (s *Scenario) Expand() error {
	if s.Generate == nil {
		return nil
	}
	plan := s.Generate
	if plan.Days <= 0 {
		return fmt.Errorf("a generated history needs the days it reaches back")
	}
	sprintDays := plan.SprintDays
	if sprintDays <= 0 {
		sprintDays = 14
	}
	random := rand.New(rand.NewSource(plan.Seed)) // #nosec G404 -- a demo is repeatable, not secret.
	projects := map[string]*Project{}
	for index := range s.Projects {
		projects[s.Projects[index].ID] = &s.Projects[index]
	}
	// Who works where: a team's members take the work on the team's projects,
	// and a project no team claims falls to whoever leads it.
	workers := map[string][]string{}
	for _, team := range plan.Teams {
		for _, project := range team.Projects {
			workers[project] = append(workers[project], team.Members...)
		}
	}
	for _, generated := range plan.Projects {
		project, known := projects[generated.Project]
		if !known {
			return fmt.Errorf("the generated history names the unknown project %q", generated.Project)
		}
		people := workers[generated.Project]
		if len(people) == 0 {
			people = []string{project.Lead}
		}
		if err := s.growProject(project, generated, plan, people, sprintDays, random); err != nil {
			return err
		}
	}
	desks := plan.Services
	if plan.Service != nil {
		desks = append([]GeneratedService{*plan.Service}, desks...)
	}
	for index := range desks {
		if err := s.growService(&desks[index], plan.Days, random); err != nil {
			return err
		}
	}
	if plan.Knowledge != nil {
		if err := s.growKnowledge(plan, random); err != nil {
			return err
		}
	}
	// Expanding consumes the plan: what it described is now declared, so a
	// scenario written back out is the whole company and reading it again
	// does not grow a second copy of the same three years.
	s.Generate = &Generation{Seed: plan.Seed, Days: plan.Days, SprintDays: plan.SprintDays, Teams: plan.Teams}
	return nil
}

// growProject writes one project's sprints, releases, work and deliveries.
func (s *Scenario) growProject(project *Project, generated GeneratedProject, plan *Generation, people []string, sprintDays int, random *rand.Rand) error {
	pick := func(from []string) string {
		if len(from) == 0 {
			return ""
		}
		return from[random.Intn(len(from))]
	}
	components := generated.Components
	if len(components) == 0 {
		for _, component := range project.Components {
			components = append(components, component.Name)
		}
	}
	themes := generated.Themes
	if len(themes) == 0 {
		themes = []string{strings.ToLower(project.Name)}
	}
	// Generated history fills the years *before* whatever the scenario wrote
	// by hand: a curated sprint or release is the recent, readable part of
	// the company, and the generator must not run a second sprint alongside
	// it -- a site with parallel sprints off allows exactly one.
	cutoff := 0
	for _, version := range project.Versions {
		if version.StartDay != nil && *version.StartDay < cutoff {
			cutoff = *version.StartDay
		}
	}
	if project.Board != nil {
		for _, sprint := range project.Board.Sprints {
			if sprint.StartDay != nil && *sprint.StartDay < cutoff {
				cutoff = *sprint.StartDay
			}
		}
	}
	// Sprints run back to back from the start of the history up to the
	// curated part; the last generated one is active only when nothing
	// curated follows it.
	rounds := (plan.Days + cutoff) / sprintDays
	board := project.Board
	if generated.Board != "" {
		if board == nil || board.ID != generated.Board {
			return fmt.Errorf("the generated history names the unknown board %q", generated.Board)
		}
	}
	version := (*Version)(nil)
	versionsMade := 0
	series := releaseSeries(project)
	for round := range rounds {
		startDay := -plan.Days + round*sprintDays
		endDay := startDay + sprintDays
		sprintID := ""
		if board != nil {
			sprintID = fmt.Sprintf("%s-gs%d", project.ID, round+1)
			state := "closed"
			if endDay > 0 && cutoff == 0 {
				state = "active"
			}
			start, end := startDay, endDay
			board.Sprints = append(board.Sprints, Sprint{
				ID: sprintID, Name: fmt.Sprintf("%s sprint %d", project.Name, round+1),
				Goal: capitalise(pick(themes)), StartDay: &start, EndDay: &end, State: state,
			})
		}
		// A release gathers the sprints that went into it.
		if generated.ReleaseEvery > 0 && round%generated.ReleaseEvery == 0 {
			versionsMade++
			start := startDay
			release := startDay + generated.ReleaseEvery*sprintDays
			released := release <= 0
			// Named for when it ships, so a generated release never collides
			// with one the scenario declared by hand and a reader can tell
			// at a glance when it went out.
			version = &Version{
				ID:          fmt.Sprintf("%s-gv%d", project.ID, versionsMade),
				Name:        fmt.Sprintf("%d.%d", series+versionsMade/10, (versionsMade-1)%10),
				Description: fmt.Sprintf("%s, released from %s", capitalise(pick(themes)), project.Name),
				StartDay:    &start, ReleaseDay: &release, Released: released,
			}
			project.Versions = append(project.Versions, *version)
		}
		// The sprint's work, then what people did around it: work blocks or
		// relates to work beside it, and somebody is watching some of it.
		sprintWork := make([]WorkItem, 0, generated.WorkPerSprint)
		for index := range generated.WorkPerSprint {
			sprintWork = append(sprintWork, s.growWorkItem(project, generated, sprintID, version, people, components, themes,
				startDay, endDay, round, index, random))
		}
		linkSprintWork(sprintWork, generated, people, random)
		s.WorkItems = append(s.WorkItems, sprintWork...)
	}
	s.assureRecentIncident(project, generated, people, random)
	s.growDeliveries(project, generated, plan, people, sprintDays, random)
	return nil
}

// assureRecentIncident makes sure the last month holds one incident that was
// recovered from. A team of this size has about one a month, so a freshly
// built site lands on a month with none often enough -- and a delivery report
// missing its time to restore reads as a report that does not work rather than
// as a quiet month.
func (s *Scenario) assureRecentIncident(project *Project, generated GeneratedProject, people []string, random *rand.Rand) {
	for _, item := range s.WorkItems {
		if item.Project == project.ID && item.CreatedDay >= -deliveryWindowDays && slices.Contains(item.Labels, "incident") {
			return
		}
	}
	themes := generated.Themes
	if len(themes) == 0 {
		themes = []string{strings.ToLower(project.Name)}
	}
	theme := themes[random.Intn(len(themes))]
	assignee := ""
	if len(people) > 0 {
		assignee = people[random.Intn(len(people))]
	}
	day := until(-3 - random.Intn(10))
	item := WorkItem{
		Generated: true, ID: fmt.Sprintf("%s-gi1", project.ID), Project: project.ID, Type: "Bug",
		Summary:     fmt.Sprintf("%s stopped working for everyone", capitalise(theme)),
		Description: "Production was down until somebody put it back.",
		Reporter:    assignee, Assignee: assignee, Priority: "Highest",
		Labels:   []string{"incident", strings.ReplaceAll(theme, " ", "-")},
		Estimate: "2h", CreatedDay: day,
		Events: []Event{
			{Day: day, Kind: "transition", Status: "In Progress", Actor: assignee},
			{Day: day, Kind: "worklog", Actor: assignee, Seconds: 1800 * (1 + random.Intn(4))},
			{Day: day, Kind: "transition", Status: "Done", Resolution: "Done", Actor: assignee},
		},
	}
	if generated.Points != "" {
		item.Fields = map[string]string{generated.Points: "2"}
	}
	s.WorkItems = append(s.WorkItems, item)
}

// releaseSeries is where a project's generated releases start numbering, so
// they never collide with the versions the scenario declared by hand: one
// major above the highest declared one.
func releaseSeries(project *Project) int {
	highest := 0
	for _, version := range project.Versions {
		major, _, _ := strings.Cut(version.Name, ".")
		if number, err := strconv.Atoi(strings.TrimSpace(major)); err == nil && number > highest {
			highest = number
		}
	}
	return highest + 1
}

// capitalise writes a phrase as a title starts: the whole phrase, with its
// first letter upper case. Splicing one phrase's first letter onto another
// phrase's tail is how the shipped company came to ship "Craud checks".
func capitalise(phrase string) string {
	if phrase == "" {
		return phrase
	}
	return strings.ToUpper(phrase[:1]) + phrase[1:]
}

// deliveryWindowDays is how far back a deployment reaches for the work it
// carries: about a month, which is a sprint or two of finished work.
const deliveryWindowDays = 30

// until holds a generated day at today: day zero is the day the site is built,
// and a scenario that runs past it dates work in the future.
func until(day int) int {
	if day > 0 {
		return 0
	}
	return day
}

// growWorkItem writes one piece of work and the life it had: picked up, worked
// on, talked about, and usually finished before the sprint ended. Some of it
// is still open, because a backlog with nothing left in it is not a backlog.
func (s *Scenario) growWorkItem(project *Project, generated GeneratedProject, sprintID string, version *Version,
	people, components, themes []string, startDay, endDay, round, index int, random *rand.Rand) WorkItem {
	pick := func(from []string) string {
		if len(from) == 0 {
			return ""
		}
		return from[random.Intn(len(from))]
	}
	theme := pick(themes)
	bug := random.Float64() < generated.BugShare
	workType, summary := "Task", fmt.Sprintf("%s %s", pick(generatedVerbs), theme)
	if bug {
		workType = "Bug"
		summary = fmt.Sprintf("%s %s", capitalise(theme), pick(generatedDefects))
	}
	if project.Type != "software" {
		workType = "Task"
	}
	assignee := pick(people)
	item := WorkItem{
		Generated:   true,
		ID:          fmt.Sprintf("%s-g%d-%d", project.ID, round+1, index+1),
		Project:     project.ID,
		Type:        workType,
		Summary:     summary,
		Description: pick(generatedDetail),
		Reporter:    pick(people),
		Assignee:    assignee,
		Priority:    pick(generatedPriority),
		Labels:      []string{strings.ReplaceAll(theme, " ", "-")},
		Sprint:      sprintID,
		Estimate:    pick(generatedEstimates),
		Fields:      map[string]string{},
		CreatedDay:  startDay + random.Intn(3),
	}
	if len(components) > 0 {
		item.Components = []string{pick(components)}
	}
	// A board with no estimates has no velocity, no burndown and no sprint
	// health: the numbers those draw are the points on the work.
	if generated.Points != "" {
		item.Fields[generated.Points] = pick(generatedPoints)
	}
	if generated.Impact != "" && random.Float64() < 0.6 {
		item.Fields[generated.Impact] = pick(generatedImpacts)
	}
	if len(item.Fields) == 0 {
		item.Fields = nil
	}
	if version != nil && version.ReleaseDay != nil && *version.ReleaseDay >= item.CreatedDay {
		item.FixVersions = []string{version.ID}
	}
	// A few of the bugs are what took production down. A delivery team that
	// never marks one has no time to restore, which is a quarter of its
	// delivery metrics missing.
	incident := bug && random.Float64() < 0.18
	if incident {
		item.Labels = append(item.Labels, "incident")
		item.Priority = "Highest"
	}
	// Some work is promised for a day. The calendar gadget and the due-date
	// columns and queries have nothing to show without it, and a site where
	// every piece of work is due is not one either.
	if random.Float64() < 0.35 {
		due := item.CreatedDay + 3 + random.Intn(12)
		item.Due = &due
	}
	// What happened to it, in order: a day cursor walks forward so a comment
	// never lands before the work was picked up, and the applier replays the
	// timeline exactly as it reads. Nothing happens after today: work in the
	// sprint running now was picked up and talked about up to this morning,
	// not next week.
	day := until(item.CreatedDay + 1 + random.Intn(3))
	item.Events = append(item.Events, Event{Day: day, Kind: "transition", Status: "In Progress", Actor: assignee})
	if random.Float64() < 0.7 {
		day = until(day + random.Intn(2))
		item.Events = append(item.Events, Event{
			Day: day, Kind: "worklog", Actor: assignee, Seconds: 1800 * (1 + random.Intn(12)),
		})
	}
	if random.Float64() < 0.45 {
		day = until(day + random.Intn(3))
		item.Events = append(item.Events, Event{Day: day, Kind: "comment", Actor: pick(people), Body: pick(generatedComments)})
	}
	// Work in a sprint that has ended is nearly all done; work in the sprint
	// running now mostly is not, because that is what a backlog looks like.
	finished := random.Float64() < 0.92
	if endDay > 0 {
		finished = random.Float64() < 0.35
	}
	// An outage is over: somebody stayed until it was.
	if incident {
		finished = true
	}
	if finished {
		resolution := "Done"
		if bug && random.Float64() < 0.15 {
			resolution = "Won't Do"
		}
		day = until(day + 1 + random.Intn(3))
		if endDay <= 0 && day > endDay {
			// A closed sprint's work finished inside it, or the sprint report
			// would show work completed after the sprint ended.
			day = endDay
		}
		item.Events = append(item.Events, Event{
			Day: day, Kind: "transition", Status: "Done", Resolution: resolution, Actor: assignee,
		})
	}
	return item
}

// linkSprintWork joins work in a sprint to the work beside it and puts
// watchers on some of it. Both are read all over the site -- the related work
// on a work item, a branch over linked work, the watched work gadget -- and
// none of it means anything on a site where nothing is linked or watched.
func linkSprintWork(work []WorkItem, generated GeneratedProject, people []string, random *rand.Rand) {
	if len(work) < 2 {
		return
	}
	for index := range work {
		// A link points backwards, at work the applier has already raised:
		// the timeline is replayed in order, so a link to work still to come
		// would be a link to nothing.
		if generated.LinkShare > 0 && index > 0 && random.Float64() < generated.LinkShare {
			other := random.Intn(index)
			if other != index {
				// A link is recorded on the work item that acts: the one that
				// blocks, as the applier reads it.
				work[index].Events = append(work[index].Events, Event{
					Day: until(work[index].CreatedDay + 1), Kind: "link",
					LinkType: generatedLinks[random.Intn(len(generatedLinks))],
					Target:   work[other].ID, Actor: work[index].Reporter,
				})
			}
		}
		if generated.WatchShare > 0 && random.Float64() < generated.WatchShare && len(people) > 0 {
			work[index].Events = append(work[index].Events, Event{
				Day: until(work[index].CreatedDay + 1), Kind: "watch", Actor: people[random.Intn(len(people))],
			})
		}
		// Some of it people ask for: a vote is what the voted work gadget and
		// the votes column read, and what a bubble chart sizes by.
		if generated.WatchShare > 0 && random.Float64() < generated.WatchShare/2 && len(people) > 0 {
			work[index].Events = append(work[index].Events, Event{
				Day: until(work[index].CreatedDay + 2), Kind: "vote", Actor: people[random.Intn(len(people))],
			})
		}
	}
	for index := range work {
		sortEventsByDay(work[index].Events)
	}
}

// sortEventsByDay keeps a work item's timeline in the order it happened,
// which is what the applier replays and what the scenario is checked against.
func sortEventsByDay(events []Event) {
	sort.SliceStable(events, func(first, second int) bool { return events[first].Day < events[second].Day })
}

// growDeliveries writes what the project shipped: a steady stream of
// production deployments carrying the work that was done by then, some of
// which failed, because a change failure rate of nought is not a measurement.
func (s *Scenario) growDeliveries(project *Project, generated GeneratedProject, plan *Generation, people []string, sprintDays int, random *rand.Rand) {
	if generated.DeploymentsPerWeek <= 0 {
		return
	}
	// The work this project has, newest last, so a deployment can carry what
	// existed when it ran.
	done := []WorkItem{}
	for _, item := range s.WorkItems {
		if item.Project == project.ID {
			done = append(done, item)
		}
	}
	// Work is in creation order, so a window over it is a window over the
	// weeks before a deployment.
	sort.SliceStable(done, func(first, second int) bool { return done[first].CreatedDay < done[second].CreatedDay })
	interval := 7.0 / generated.DeploymentsPerWeek
	first := 0
	for day := float64(-plan.Days); day < 0; day += interval {
		at := int(day)
		// A deployment carries what was finished recently, not whatever the
		// project has ever done. Redeploying a three-year-old work item makes
		// the lead time of every commit on it read as three years, because a
		// commit is measured against the first delivery that carried it.
		for first < len(done) && done[first].CreatedDay < at-deliveryWindowDays {
			first++
		}
		last := first
		for last < len(done) && done[last].CreatedDay <= at {
			last++
		}
		carried := []string{}
		for attempt := 0; attempt < 3; attempt++ {
			if last <= first {
				break
			}
			carried = append(carried, done[first+random.Intn(last-first)].ID)
		}
		if len(carried) == 0 {
			continue
		}
		state := "successful"
		if random.Float64() < generated.FailureRate {
			state = "failed"
		}
		s.Deployments = append(s.Deployments, Deployment{
			Pipeline: project.ID + "-release", Environment: "production", Type: "production",
			State: state, Day: at, WorkItems: carried,
		})
		// What went out was written first: a commit a few days before the
		// deployment that carried it, which is the pair the delivery report
		// measures a lead time from.
		for _, item := range carried {
			s.Commits = append(s.Commits, Commit{
				ID:         fmt.Sprintf("%s-c%d-%s", project.ID, len(s.Commits)+1, item),
				Repository: project.ID,
				Message:    fmt.Sprintf("Work on %s", item),
				Day:        at - 1 - random.Intn(5),
				WorkItems:  []string{item},
			})
		}
	}
}

// growService fills the desk's queue: years of people asking for things, some
// of it broken and answered in a hurry, most of it answered and rated. An
// incident that was resolved is what the delivery report reads to say how
// long this company takes to recover.
func (s *Scenario) growService(declared *GeneratedService, days int, random *rand.Rand) error {
	if declared.RequestsPerWeek <= 0 {
		return nil
	}
	if s.Service == nil {
		s.Service = &Service{}
	}
	pick := func(from []string) string {
		if len(from) == 0 {
			return ""
		}
		return from[random.Intn(len(from))]
	}
	interval := 7.0 / declared.RequestsPerWeek
	raised := 0
	for day := float64(-days); day < 0; day += interval {
		at := int(day)
		raised++
		incident := random.Float64() < declared.IncidentShare
		requestType := pick(declared.RequestTypes)
		subject := pick(declared.Subjects)
		summary := fmt.Sprintf("Help with %s", subject)
		if incident && declared.IncidentType != "" {
			requestType = declared.IncidentType
			summary = fmt.Sprintf("%s is down", capitalise(subject))
		}
		agent := pick(declared.Agents)
		request := ServiceRequest{
			ID: fmt.Sprintf("%s-gr%d", declared.Project, raised), Project: declared.Project,
			RequestType: requestType, Customer: pick(declared.Customers), Summary: summary,
			Description: pick(generatedDetail), CreatedDay: at,
		}
		// Most requests are answered the day they arrive, inside the desk's
		// four working hours for a first response; the rest are answered the
		// next day and breach it. A desk whose SLAs are all met, or all
		// breached, shows nothing about what an SLA is for.
		day := at
		if at < 0 && random.Float64() < 0.3 {
			day = at + 1
		}
		// Agents talk among themselves before they answer, and a note is not
		// a reply: it leaves the first response clock running.
		if random.Float64() < 0.25 {
			request.Events = append(request.Events, Event{
				Day: at, Kind: "comment", Actor: agent, Internal: true, Body: pick(generatedServiceNotes),
			})
		}
		request.Events = append(request.Events, Event{Day: day, Kind: "comment", Actor: agent, Body: pick(generatedServiceReplies)})
		// An incident is about something the company runs, and connecting it
		// is what makes the topology worth having: the request shows what
		// else depends on what broke.
		if incident && len(declared.Assets) > 0 {
			request.Events = append(request.Events, Event{
				Day: day, Kind: "asset", Actor: agent, Asset: pick(declared.Assets),
			})
		}
		// Asking for access is answered by a person, not by the agent who
		// took the request: most are approved, a few are refused, and a few
		// are still waiting, which is what an approval queue looks like.
		approved, refused := true, false
		if declared.ApprovalType != "" && requestType == declared.ApprovalType {
			approvers := declared.Approvers
			if len(approvers) == 0 {
				approvers = declared.Agents
			}
			approver := pick(approvers)
			request.Events = append(request.Events, Event{
				Day: day, Kind: "approval", Actor: agent, Body: "Manager approval", Approvers: []string{approver},
			})
			switch answer := random.Float64(); {
			case answer < 0.75:
				request.Events = append(request.Events, Event{Day: day, Kind: "approve", Actor: approver})
			case answer < 0.9:
				approved, refused = false, true
				request.Events = append(request.Events, Event{Day: day, Kind: "decline", Actor: approver})
			default:
				// Still waiting, so the request waits with it.
				approved = false
			}
		}
		// A refused request is closed as soon as it is refused: nobody is
		// getting the access, and the request does not sit in the queue.
		if refused {
			if day < 0 {
				day++
			}
			request.Events = append(request.Events, Event{
				Day: day, Kind: "transition", Status: "Done", Resolution: "Won't Do", Actor: agent,
			})
		}
		// An incident is answered in hours and a question in days, which is
		// what makes the two read differently in the reports. Most are
		// resolved the day they were answered, so the desk's time to
		// resolution is met more often than it is missed.
		//
		// What is still open is what came in recently. A desk does not carry
		// a request from two years ago that nobody ever answered, and one
		// that did would read as a resolution clock breached by months.
		answered := 0.98
		if at > -30 {
			answered = 0.6
		}
		if approved && random.Float64() < answered {
			switch {
			case incident || random.Float64() < 0.65:
			case random.Float64() < 0.5:
				day++
			default:
				day += 1 + random.Intn(4)
			}
			if day > 0 {
				day = 0
			}
			request.Events = append(request.Events, Event{
				Day: day, Kind: "transition", Status: "Done", Resolution: "Done", Actor: agent,
			})
			if random.Float64() < 0.5 {
				request.Satisfaction = 3 + random.Intn(3)
				request.Feedback = pick(generatedFeedback)
			}
		}
		s.Service.Requests = append(s.Service.Requests, request)
	}
	return nil
}

// growKnowledge writes what the company wrote down as it went: a page every
// few weeks and an announcement every quarter, which is what a space looks
// like after three years rather than after a demo.
func (s *Scenario) growKnowledge(plan *Generation, random *rand.Rand) error {
	declared := plan.Knowledge
	if declared.PagesPerMonth <= 0 && declared.PostsPerQuarter <= 0 {
		return nil
	}
	if s.Wiki == nil {
		s.Wiki = &Wiki{}
	}
	space := (*Space)(nil)
	for index := range s.Wiki.Spaces {
		if s.Wiki.Spaces[index].Key == declared.Space {
			space = &s.Wiki.Spaces[index]
		}
	}
	if space == nil {
		return fmt.Errorf("the written history names the unknown space %q", declared.Space)
	}
	pick := func(from []string) string {
		if len(from) == 0 {
			return ""
		}
		return from[random.Intn(len(from))]
	}
	written := 0
	if declared.PagesPerMonth > 0 {
		interval := 30.0 / declared.PagesPerMonth
		for day := float64(-plan.Days); day < 0; day += interval {
			written++
			subject := pick(declared.Subjects)
			space.Pages = append(space.Pages, Page{
				ID: fmt.Sprintf("%s-gp%d", strings.ToLower(declared.Space), written),
				// Numbered, because a page is matched by its title when a
				// scenario is applied again: two pages called "Payments:
				// runbook" are one page, and three years of writing makes
				// that collision many times over.
				Title: fmt.Sprintf("%s: %s (%d)", capitalise(subject),
					generatedPageKinds[written%len(generatedPageKinds)], written),
				Body: fmt.Sprintf("<p>%s</p><p>%s</p>", pick(generatedDetail),
					"Written as this went out, and kept because the next one will ask the same questions."),
				Author: pick(declared.Authors), CreatedDay: int(day),
			})
		}
	}
	posted := 0
	if declared.PostsPerQuarter > 0 {
		interval := 90.0 / declared.PostsPerQuarter
		for day := float64(-plan.Days); day < 0; day += interval {
			posted++
			space.BlogPosts = append(space.BlogPosts, BlogPost{
				Title:  fmt.Sprintf("%s, quarter %d", capitalise(pick(declared.Subjects)), posted),
				Body:   "<p>What we shipped, what we learned, and what is next.</p>",
				Author: pick(declared.Authors), CreatedDay: int(day),
			})
		}
	}
	return nil
}
