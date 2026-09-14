package wikimarkup

import (
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
)

var taskMetadata = map[string]bool{"task-id": true, "task-uuid": true, "task-status": true}

// Task is one item of a Confluence task list in storage format:
//
//	<ac:task-list><ac:task>
//	  <ac:task-id>1</ac:task-id><ac:task-status>incomplete</ac:task-status>
//	  <ac:task-body>Ship it <ac:link><ri:user ri:account-id="…" /></ac:link> <time datetime="2026-10-01" /></ac:task-body>
//	</ac:task></ac:task-list>
//
// The first person a task's body mentions is its assignee, and the first date
// in it is when it is due.
type Task struct {
	ID       string
	Status   string
	Body     string
	Assignee string
	Due      string

	// Byte offsets in the storage body: the status text, and the end of the
	// id, after which a missing status is written.
	statusStart, statusEnd, idEnd int
	hasStatus                     bool
}

// Tasks lists the tasks a storage body holds, in document order. Each task
// needs an id unique within the body and a complete or incomplete status,
// which defaults to incomplete.
func Tasks(storage string) ([]Task, error) {
	const root = "<root>"
	wrapped := root + storage + "</root>"
	decoder := xml.NewDecoder(strings.NewReader(wrapped))
	type openField struct {
		kind  string
		task  int
		start int
	}
	var tasks []Task
	var taskStack []int
	var fields []openField
	seen := map[string]bool{}
	start := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("invalid storage markup: %w", err)
		}
		end := int(decoder.InputOffset())
		switch t := token.(type) {
		case xml.StartElement:
			if len(fields) > 0 && fields[len(fields)-1].kind == "task-body" {
				task := &tasks[fields[len(fields)-1].task]
				for _, a := range t.Attr {
					if t.Name.Space == "ri" && t.Name.Local == "user" && a.Name.Local == "account-id" && task.Assignee == "" {
						task.Assignee = a.Value
					}
					if t.Name.Space == "" && t.Name.Local == "time" && a.Name.Local == "datetime" && task.Due == "" {
						task.Due = a.Value
					}
				}
			}
			if t.Name.Space != "ac" {
				break
			}
			switch t.Name.Local {
			case "task":
				tasks = append(tasks, Task{Status: "incomplete"})
				taskStack = append(taskStack, len(tasks)-1)
			case "task-id", "task-status", "task-body":
				if len(taskStack) == 0 {
					return nil, fmt.Errorf("ac:%s belongs inside ac:task", t.Name.Local)
				}
				fields = append(fields, openField{kind: t.Name.Local, task: taskStack[len(taskStack)-1], start: end})
			}
		case xml.EndElement:
			if t.Name.Space != "ac" {
				break
			}
			switch t.Name.Local {
			case "task":
				task := tasks[taskStack[len(taskStack)-1]]
				taskStack = taskStack[:len(taskStack)-1]
				if task.ID == "" || len(task.ID) > 255 {
					return nil, fmt.Errorf("every task needs an ac:task-id of at most 255 characters")
				}
				if seen[task.ID] {
					return nil, fmt.Errorf("task id %s is used more than once", task.ID)
				}
				seen[task.ID] = true
			case "task-id", "task-status", "task-body":
				field := fields[len(fields)-1]
				fields = fields[:len(fields)-1]
				task := &tasks[field.task]
				inner := wrapped[field.start:start]
				switch field.kind {
				case "task-id":
					task.ID, task.idEnd = strings.TrimSpace(html.UnescapeString(inner)), end-len(root)
				case "task-status":
					status := strings.TrimSpace(inner)
					if status != "complete" && status != "incomplete" {
						return nil, fmt.Errorf("a task's status is complete or incomplete")
					}
					task.Status, task.hasStatus = status, true
					task.statusStart, task.statusEnd = field.start-len(root), start-len(root)
				case "task-body":
					task.Body = strings.TrimSpace(inner)
				}
			}
		}
		start = end
	}
	return tasks, nil
}

// SetTaskStatus rewrites one task's status in a storage body.
func SetTaskStatus(storage, id, status string) (string, error) {
	if status != "complete" && status != "incomplete" {
		return "", fmt.Errorf("a task's status is complete or incomplete")
	}
	tasks, err := Tasks(storage)
	if err != nil {
		return "", err
	}
	for _, task := range tasks {
		if task.ID != id {
			continue
		}
		if task.hasStatus {
			return storage[:task.statusStart] + status + storage[task.statusEnd:], nil
		}
		return storage[:task.idEnd] + "<ac:task-status>" + status + "</ac:task-status>" + storage[task.idEnd:], nil
	}
	return "", fmt.Errorf("task %s is not in the body", id)
}

// NextTaskID is an id no task in the body uses yet.
func NextTaskID(tasks []Task) string {
	next := 1
	for _, task := range tasks {
		if n, err := strconv.Atoi(task.ID); err == nil && n >= next {
			next = n + 1
		}
	}
	return strconv.Itoa(next)
}

// TaskList is the storage markup of a task list holding one new task.
func TaskList(id, body string) string {
	return "<ac:task-list><ac:task><ac:task-id>" + html.EscapeString(id) + "</ac:task-id><ac:task-status>incomplete</ac:task-status><ac:task-body>" + body + "</ac:task-body></ac:task></ac:task-list>"
}
