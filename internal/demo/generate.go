package demo

import (
	"fmt"
	"math/rand"
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
	// Service is the desk whose queue fills up over the same years.
	Service *GeneratedService `json:"service,omitempty"`
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
	generatedEstimates      = []string{"1h", "2h", "4h", "1d", "2d", "3d"}
	generatedServiceReplies = []string{
		"Thanks for writing in -- taking a look now.", "I can see the error on our side.",
		"Could you tell me which account this is on?", "This is fixed; please try again.",
		"Passed this to the payments team.", "Sorry about that. It is back.",
	}
	generatedPageKinds = []string{"how it works", "runbook", "what we decided", "meeting notes", "what went wrong", "how to change it"}
	generatedFeedback  = []string{"Quick and clear, thank you.", "Sorted in a day.", "Fine once it was picked up.", "Fast answer."}
	generatedPriority  = []string{"Low", "Medium", "Medium", "High", "Highest"}
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
	if plan.Service != nil {
		if err := s.growService(plan, random); err != nil {
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
				Goal: strings.ToUpper(pick(themes)[:1]) + pick(themes)[1:], StartDay: &start, EndDay: &end, State: state,
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
				ID:   fmt.Sprintf("%s-gv%d", project.ID, versionsMade),
				Name: fmt.Sprintf("%d.%d", series+versionsMade/10, (versionsMade-1)%10),
				Description: fmt.Sprintf("%s, released from %s",
					strings.ToUpper(pick(themes)[:1])+pick(themes)[1:], project.Name),
				StartDay: &start, ReleaseDay: &release, Released: released,
			}
			project.Versions = append(project.Versions, *version)
		}
		for index := range generated.WorkPerSprint {
			item := s.growWorkItem(project, generated, sprintID, version, people, components, themes,
				startDay, endDay, round, index, random)
			s.WorkItems = append(s.WorkItems, item)
		}
	}
	s.growDeliveries(project, generated, plan, people, sprintDays, random)
	return nil
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
		summary = fmt.Sprintf("%s %s", strings.ToUpper(theme[:1])+theme[1:], pick(generatedDefects))
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
		CreatedDay:  startDay + random.Intn(3),
	}
	if len(components) > 0 {
		item.Components = []string{pick(components)}
	}
	if version != nil && version.ReleaseDay != nil && *version.ReleaseDay >= item.CreatedDay {
		item.FixVersions = []string{version.ID}
	}
	// What happened to it, in order: a day cursor walks forward so a comment
	// never lands before the work was picked up, and the applier replays the
	// timeline exactly as it reads.
	day := item.CreatedDay + 1 + random.Intn(3)
	item.Events = append(item.Events, Event{Day: day, Kind: "transition", Status: "In Progress", Actor: assignee})
	if random.Float64() < 0.7 {
		day += random.Intn(2)
		item.Events = append(item.Events, Event{
			Day: day, Kind: "worklog", Actor: assignee, Seconds: 1800 * (1 + random.Intn(12)),
		})
	}
	if random.Float64() < 0.45 {
		day += random.Intn(3)
		item.Events = append(item.Events, Event{Day: day, Kind: "comment", Actor: pick(people), Body: pick(generatedComments)})
	}
	// Work in a sprint that has ended is nearly all done; work in the sprint
	// running now mostly is not, because that is what a backlog looks like.
	finished := random.Float64() < 0.92
	if endDay > 0 {
		finished = random.Float64() < 0.35
	}
	if finished {
		resolution := "Done"
		if bug && random.Float64() < 0.15 {
			resolution = "Won't Do"
		}
		day += 1 + random.Intn(3)
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
	interval := 7.0 / generated.DeploymentsPerWeek
	for day := float64(-plan.Days); day < 0; day += interval {
		at := int(day)
		carried := []string{}
		for attempt := 0; attempt < 3; attempt++ {
			if len(done) == 0 {
				break
			}
			candidate := done[random.Intn(len(done))]
			if candidate.CreatedDay <= at {
				carried = append(carried, candidate.ID)
			}
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
func (s *Scenario) growService(plan *Generation, random *rand.Rand) error {
	declared := plan.Service
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
	for day := float64(-plan.Days); day < 0; day += interval {
		at := int(day)
		raised++
		incident := random.Float64() < declared.IncidentShare
		requestType := pick(declared.RequestTypes)
		subject := pick(declared.Subjects)
		summary := fmt.Sprintf("Help with %s", subject)
		if incident && declared.IncidentType != "" {
			requestType = declared.IncidentType
			summary = fmt.Sprintf("%s is down", strings.ToUpper(subject[:1])+subject[1:])
		}
		agent := pick(declared.Agents)
		request := ServiceRequest{
			ID: fmt.Sprintf("%s-gr%d", declared.Project, raised), Project: declared.Project,
			RequestType: requestType, Customer: pick(declared.Customers), Summary: summary,
			Description: pick(generatedDetail), CreatedDay: at,
		}
		day := at + 1
		request.Events = append(request.Events, Event{Day: day, Kind: "comment", Actor: agent, Body: pick(generatedServiceReplies)})
		// An incident is answered in hours and a question in days, which is
		// what makes the two read differently in the reports.
		if random.Float64() < 0.85 {
			if incident {
				day++
			} else {
				day += 1 + random.Intn(4)
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
				ID:    fmt.Sprintf("%s-gp%d", strings.ToLower(declared.Space), written),
				Title: fmt.Sprintf("%s: %s", strings.ToUpper(subject[:1])+subject[1:], generatedPageKinds[written%len(generatedPageKinds)]),
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
				Title:  fmt.Sprintf("%s, quarter %d", strings.ToUpper(pick(declared.Subjects)[:1])+pick(declared.Subjects)[1:], posted),
				Body:   "<p>What we shipped, what we learned, and what is next.</p>",
				Author: pick(declared.Authors), CreatedDay: int(day),
			})
		}
	}
	return nil
}
