package commands

import (
	"strconv"
	"strings"

	"github.com/e6qu/zzira/internal/models"
)

type whiteboardTemplate struct {
	Title string
	Cards []string
	Flow  bool
}

// These are native, editable starter layouts for the pinned template keys.
// A card names the question it helps the team answer, followed by its prompt.
var wikiWhiteboardTemplates = map[string]whiteboardTemplate{
	"2x2-prioritization":        {Title: "Impact and effort", Cards: []string{"Quick wins|High impact, low effort", "Major projects|High impact, high effort", "Fill-ins|Low impact, low effort", "Reconsider|Low impact, high effort"}},
	"4ls-retro":                 {Title: "4Ls retrospective", Cards: []string{"Liked|What went well?", "Learned|What did we learn?", "Lacked|What was missing?", "Longed for|What would make next time better?"}},
	"annual-calendar":           {Title: "Annual calendar", Cards: calendarMonths},
	"annual-calendar-2025-2026": {Title: "Annual calendar", Cards: calendarMonths},
	"brainwriting":              {Title: "Brainwriting", Cards: []string{"Ideas|Write independently before discussing", "Build on an idea|Add a different perspective", "Combine|Join promising ideas", "Next steps|Choose an experiment"}},
	"concept-map":               {Title: "Concept map", Cards: []string{"Core concept|Name the idea", "Related concept|Describe a connection", "Example|Make it concrete", "Open question|What needs investigation?"}, Flow: true},
	"crazy-8s":                  {Title: "Crazy 8s", Cards: []string{"Idea 1", "Idea 2", "Idea 3", "Idea 4", "Idea 5", "Idea 6", "Idea 7", "Idea 8"}},
	"daily-sync":                {Title: "Daily sync", Cards: []string{"Progress|What changed since yesterday?", "Today|What will move forward?", "Blockers|Where do we need help?"}},
	"disruptive-brainstorm":     {Title: "Disruptive brainstorm", Cards: []string{"Assumptions|What do we take for granted?", "Reverse it|What if the opposite were true?", "Possibilities|What new options appear?", "Experiment|Test the strongest possibility"}},
	"dot-voting":                {Title: "Dot voting", Cards: []string{"Options|List the choices", "Criteria|Agree what matters", "Votes|Record each participant's choices", "Decision|Capture the result and next step"}},
	"elevator-pitch":            {Title: "Elevator pitch", Cards: []string{"Audience|Who is this for?", "Problem|What need are we addressing?", "Solution|What do we offer?", "Value|Why is it different?"}},
	"flow-chart":                {Title: "Flow chart", Cards: []string{"Start|What begins the process?", "Activity|What happens next?", "Decision|Which path should we take?", "End|What outcome completes it?"}, Flow: true},
	"gap-analysis":              {Title: "Gap analysis", Cards: []string{"Current state|Where are we now?", "Desired state|Where do we want to be?", "Gap|What prevents the change?", "Actions|How will we close the gap?"}},
	"ice-breakers":              {Title: "Ice breakers", Cards: []string{"Introduce yourself|Name and role", "A highlight|Share something that went well", "A surprise|What might others not know?", "One hope|What would make today useful?"}},
	"incident-postmortem":       {Title: "Incident postmortem", Cards: []string{"Timeline|Record events and decisions", "Impact|Who was affected and how?", "Contributing factors|What conditions made this possible?", "What worked|What helped detection and recovery?", "What to improve|Where did response slow down?", "Follow-up actions|Name an owner and a due date"}},
	"journey-mapping-kit":       {Title: "Customer journey", Cards: []string{"Discover|What creates awareness?", "Evaluate|What helps a decision?", "Start|What is the first experience?", "Use|What helps the customer succeed?", "Pain points|Where does the journey break down?", "Opportunities|What should improve?"}},
	"kanban-board":              {Title: "Kanban board", Cards: []string{"Backlog|Work to consider", "Ready|Work ready to start", "In progress|Work currently underway", "Done|Completed work"}},
	"lean-coffee":               {Title: "Lean coffee", Cards: []string{"To discuss|Add topics", "Discussing|Move the current topic here", "Discussed|Capture decisions and follow-ups"}},
	"network-of-teams":          {Title: "Network of teams", Cards: []string{"Team A|Purpose and responsibilities", "Team B|Purpose and responsibilities", "Team C|Purpose and responsibilities", "Shared outcome|What do these teams deliver together?"}, Flow: true},
	"org-chart":                 {Title: "Organization chart", Cards: []string{"Leadership|Accountability and direction", "Team A|People and responsibilities", "Team B|People and responsibilities", "Team C|People and responsibilities"}, Flow: true},
	"pi-planning":               {Title: "Program increment planning", Cards: []string{"Objectives|What outcomes matter?", "Team plans|What will each team deliver?", "Dependencies|Who needs what from whom?", "Risks|What could prevent delivery?", "Capacity|What can we realistically take on?", "Commitments|Confirm owners and dates"}},
	"prioritization":            {Title: "Prioritization", Cards: []string{"Must have|Essential to the outcome", "Should have|Important but negotiable", "Could have|Useful if capacity allows", "Later|Outside the current commitment"}},
	"prioritization-experiment": {Title: "Prioritization experiment", Cards: []string{"Hypothesis|What might be valuable?", "Evidence|What supports the hypothesis?", "Experiment|What is the smallest useful test?", "Result|What did we learn?"}, Flow: true},
	"product-roadmap":           {Title: "Product roadmap", Cards: []string{"Now|Current outcomes and work", "Next|What follows the current work?", "Later|Longer-term opportunities"}},
	"product-vision-board":      {Title: "Product vision board", Cards: []string{"Vision|What change do we want to create?", "Audience|Who benefits?", "Needs|What problems matter?", "Product|What will we offer?", "Business goals|How do we know it succeeds?"}},
	"rice":                      {Title: "RICE prioritization", Cards: []string{"Reach|How many people benefit?", "Impact|How much does it help?", "Confidence|How strong is the evidence?", "Effort|How much work does it take?"}},
	"sailboat-retro":            {Title: "Sailboat retrospective", Cards: []string{"Destination|What are we trying to achieve?", "Wind|What moves us forward?", "Anchors|What slows us down?", "Rocks|What risks lie ahead?"}},
	"service-blueprint":         {Title: "Service blueprint", Cards: []string{"Customer actions|What does the customer do?", "Visible service|What does the customer see?", "Behind the scenes|What enables the experience?", "Support processes|Which systems and partners help?"}},
	"simple-retrospective":      {Title: "Retrospective", Cards: []string{"Went well|What should we keep?", "To improve|What should change?", "Actions|What will we try next?"}},
	"sprint-planning":           {Title: "Sprint planning", Cards: []string{"Sprint goal|What outcome will we deliver?", "Candidate work|What supports that outcome?", "Capacity|Who is available and how much can we do?", "Dependencies|What must happen first?", "Commitment|What work are we taking on?", "Risks|What needs early attention?"}},
	"sticky-note-pack":          {Title: "Sticky notes", Cards: []string{"Idea|Write one idea", "Question|Ask one question", "Observation|Record one observation", "Action|Choose one next step"}},
	"swimlanes":                 {Title: "Swimlanes", Cards: []string{"Lane A|Work for the first participant", "Lane B|Work for the second participant", "Lane C|Work for the third participant"}},
	"team-formation-guide":      {Title: "Team formation", Cards: []string{"Purpose|Why does this team exist?", "People|Who brings which strengths?", "Working agreements|How will we work together?", "Success|How will we know we are effective?"}},
	"timeline":                  {Title: "Timeline", Cards: []string{"Past|Events that brought us here", "Present|What is happening now?", "Future|What comes next?"}, Flow: true},
	"timeline-workflow":         {Title: "Timeline workflow", Cards: []string{"Intake|What starts the work?", "Plan|What needs to be decided?", "Deliver|How does the work happen?", "Review|What did we learn?"}, Flow: true},
	"user-story-map":            {Title: "User story map", Cards: []string{"User goal|What does the person want to achieve?", "Activities|What steps do they take?", "Stories|What capabilities support each step?", "First release|What is the smallest useful journey?", "Later releases|What builds on the first release?"}},
	"workflow":                  {Title: "Workflow", Cards: []string{"To do|Work that is ready", "In progress|Work underway", "Review|Work awaiting feedback", "Done|Work that meets its completion criteria"}, Flow: true},
	"vision-board":              {Title: "Vision board", Cards: []string{"Purpose|Why does this matter?", "Future|What do we want to see?", "Principles|What will guide our choices?", "First steps|How do we begin?"}},
	"venn-diagram":              {Title: "Overlapping ideas", Cards: []string{"Set A|What belongs in the first set?", "Shared|What do the sets have in common?", "Set B|What belongs in the second set?"}},
	"storyboard":                {Title: "Storyboard", Cards: []string{"Scene 1|Introduce the person and setting", "Scene 2|Show the need", "Scene 3|Describe the obstacle", "Scene 4|Show the response", "Scene 5|Describe the outcome", "Scene 6|What happens next?"}},
	"action-plan":               {Title: "Action plan", Cards: []string{"Outcome|What needs to change?", "Actions|What will we do?", "Owners|Who is responsible?", "Review|When will we check progress?"}, Flow: true},
	"root-cause-analysis":       {Title: "Root cause analysis", Cards: []string{"Problem|Describe what happened", "Evidence|What do we know?", "Causes|Which conditions contributed?", "Prevention|What would reduce recurrence?"}},
	"executive-summary":         {Title: "Executive summary", Cards: []string{"Context|What is the situation?", "Findings|What did we learn?", "Recommendation|What should we do?", "Decision|What approval or action is needed?"}},
	"stakeholder-mapping":       {Title: "Stakeholder mapping", Cards: []string{"Manage closely|High influence, high interest", "Keep satisfied|High influence, low interest", "Keep informed|Low influence, high interest", "Monitor|Low influence, low interest"}},
	"health-monitor":            {Title: "Team health monitor", Cards: []string{"Purpose|Are we aligned on our goal?", "Ownership|Are responsibilities clear?", "Delivery|Can we ship useful outcomes?", "Learning|Do we act on feedback?", "Support|Do we have what we need?", "Actions|What should improve first?"}},
	"okr-planning":              {Title: "OKR planning", Cards: []string{"Objective|What meaningful change do we want?", "Key result 1|How will we measure progress?", "Key result 2|How will we measure progress?", "Initiatives|What work supports the results?"}},
	"swot-analysis":             {Title: "SWOT analysis", Cards: []string{"Strengths|What advantages do we have?", "Weaknesses|What limits us?", "Opportunities|What could help us?", "Threats|What could harm us?"}},
	"poker-planning":            {Title: "Planning poker", Cards: []string{"Work item|Describe the outcome", "Questions|Clarify scope and assumptions", "Estimates|Record each person's estimate", "Agreement|Capture the estimate and reasoning"}},
	"fishbone-diagram":          {Title: "Cause and effect", Cards: []string{"Effect|Describe the observed problem", "People|Contributing human factors", "Process|Contributing workflow factors", "Technology|Contributing system factors", "Environment|Contributing external factors"}},
	"risk-assessment":           {Title: "Risk assessment", Cards: []string{"Risk|What could happen?", "Likelihood|How probable is it?", "Impact|What would the consequences be?", "Response|How will we reduce or handle it?"}},
	"bounded-context":           {Title: "Bounded contexts", Cards: []string{"Context A|Responsibility and language", "Context B|Responsibility and language", "Context C|Responsibility and language", "Integration|How do the contexts exchange information?"}, Flow: true},
	"hopes-and-fears":           {Title: "Hopes and fears", Cards: []string{"Hopes|What outcome do we hope for?", "Fears|What worries us?", "Actions|How can we support the hopes and address the fears?"}},
	"swimlane-vertical":         {Title: "Vertical swimlanes", Cards: []string{"Lane A|Work for the first participant", "Lane B|Work for the second participant", "Lane C|Work for the third participant"}},
}

var calendarMonths = []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"}

func wikiWhiteboardCanvas(key string) models.WikiWhiteboardData {
	spec, ok := wikiWhiteboardTemplates[key]
	if !ok {
		return models.WikiWhiteboardData{}
	}
	canvas := models.WikiWhiteboardData{Objects: []models.WikiWhiteboardObject{{ID: "heading", Type: "text", Title: spec.Title, X: 40, Y: 20, Width: 1200, Height: 80, Color: "gray"}}}
	colors := []string{"yellow", "blue", "green", "pink"}
	for index, card := range spec.Cards {
		title, body, _ := strings.Cut(card, "|")
		object := models.WikiWhiteboardObject{ID: strconv.Itoa(index), Type: "sticky", Title: title, Body: body, X: 40 + (index%4)*330, Y: 140 + (index/4)*210, Width: 280, Height: 180, Color: colors[index%len(colors)]}
		if spec.Flow {
			object.Type = "shape"
		}
		canvas.Objects = append(canvas.Objects, object)
		if spec.Flow && index > 0 {
			canvas.Connectors = append(canvas.Connectors, models.WikiWhiteboardConnector{FromObjectID: strconv.Itoa(index - 1), ToObjectID: object.ID, Style: "solid"})
		}
	}
	return canvas
}
