package api3

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/e6qu/zzira/internal/store"
)

// Jira's structured user query:
//
//	is assignee of PROJ
//	is watcher of (PROJ-1, PROJ-2)
//	[propertyKey].entity.property.path is "value"
//
// joined with AND and OR, AND binding tighter.

var errUserQuery = errors.New("invalid user query")

type userQueryToken struct {
	kind  string // word, string, property, lparen, rparen, comma
	value string
}

func lexUserQuery(query string) ([]userQueryToken, error) {
	tokens := []userQueryToken{}
	runes := []rune(query)
	for i := 0; i < len(runes); {
		c := runes[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '(':
			tokens = append(tokens, userQueryToken{kind: "lparen"})
			i++
		case c == ')':
			tokens = append(tokens, userQueryToken{kind: "rparen"})
			i++
		case c == ',':
			tokens = append(tokens, userQueryToken{kind: "comma"})
			i++
		case c == '"' || c == '\'':
			end := i + 1
			var b strings.Builder
			for ; end < len(runes) && runes[end] != c; end++ {
				if runes[end] == '\\' && end+1 < len(runes) {
					end++
				}
				b.WriteRune(runes[end])
			}
			if end >= len(runes) {
				return nil, fmt.Errorf("%w: unterminated string", errUserQuery)
			}
			tokens = append(tokens, userQueryToken{kind: "string", value: b.String()})
			i = end + 1
		case c == '[':
			end := i + 1
			for end < len(runes) && runes[end] != ']' {
				end++
			}
			if end >= len(runes) || end == i+1 {
				return nil, fmt.Errorf("%w: property key is not closed", errUserQuery)
			}
			key := string(runes[i+1 : end])
			i = end + 1
			path := ""
			if i < len(runes) && runes[i] == '.' {
				start := i + 1
				for i = start; i < len(runes) && !unicode.IsSpace(runes[i]); i++ {
				}
				path = string(runes[start:i])
			}
			tokens = append(tokens, userQueryToken{kind: "property", value: key + "\x00" + path})
		default:
			start := i
			for i < len(runes) && !unicode.IsSpace(runes[i]) && !strings.ContainsRune(`(),"'[`, runes[i]) {
				i++
			}
			tokens = append(tokens, userQueryToken{kind: "word", value: string(runes[start:i])})
		}
	}
	return tokens, nil
}

type userQueryParser struct {
	h           *Handler
	r           *http.Request
	workspaceID string
	tokens      []userQueryToken
	pos         int
}

func (p *userQueryParser) peekWord(word string) bool {
	return p.pos < len(p.tokens) && p.tokens[p.pos].kind == "word" && strings.EqualFold(p.tokens[p.pos].value, word)
}

func (p *userQueryParser) expectWord(word string) error {
	if !p.peekWord(word) {
		return fmt.Errorf("%w: expected %q", errUserQuery, word)
	}
	p.pos++
	return nil
}

func (h *Handler) evaluateUserQuery(r *http.Request, workspaceID, query string) ([]string, error) {
	tokens, err := lexUserQuery(query)
	if err != nil {
		return nil, err
	}
	p := &userQueryParser{h: h, r: r, workspaceID: workspaceID, tokens: tokens}
	set, err := p.or()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.tokens) {
		return nil, fmt.Errorf("%w: unexpected %q", errUserQuery, p.tokens[p.pos].value)
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	return ids, nil
}

func (p *userQueryParser) or() (map[string]bool, error) {
	left, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.peekWord("OR") {
		p.pos++
		right, err := p.and()
		if err != nil {
			return nil, err
		}
		for id := range right {
			left[id] = true
		}
	}
	return left, nil
}

func (p *userQueryParser) and() (map[string]bool, error) {
	left, err := p.statement()
	if err != nil {
		return nil, err
	}
	for p.peekWord("AND") {
		p.pos++
		right, err := p.statement()
		if err != nil {
			return nil, err
		}
		for id := range left {
			if !right[id] {
				delete(left, id)
			}
		}
	}
	return left, nil
}

func (p *userQueryParser) statement() (map[string]bool, error) {
	if p.pos >= len(p.tokens) {
		return nil, fmt.Errorf("%w: a statement is missing", errUserQuery)
	}
	token := p.tokens[p.pos]
	if token.kind == "property" {
		p.pos++
		if err := p.expectWord("is"); err != nil {
			return nil, err
		}
		if p.pos >= len(p.tokens) || (p.tokens[p.pos].kind != "string" && p.tokens[p.pos].kind != "word") {
			return nil, fmt.Errorf("%w: a property value is missing", errUserQuery)
		}
		value := p.tokens[p.pos].value
		p.pos++
		key, path, _ := strings.Cut(token.value, "\x00")
		matched, err := p.h.usersWithPropertyValue(p.r, p.workspaceID, key, path, value)
		if err != nil {
			return nil, err
		}
		for id, ok := range matched {
			if !ok {
				delete(matched, id)
			}
		}
		return matched, nil
	}
	if err := p.expectWord("is"); err != nil {
		return nil, err
	}
	if p.pos >= len(p.tokens) || p.tokens[p.pos].kind != "word" {
		return nil, fmt.Errorf("%w: a relation is missing", errUserQuery)
	}
	relation := strings.ToLower(p.tokens[p.pos].value)
	if _, ok := store.UserIssueRelations[relation]; !ok {
		return nil, fmt.Errorf("%w: %q is not a relation", errUserQuery, p.tokens[p.pos].value)
	}
	p.pos++
	if err := p.expectWord("of"); err != nil {
		return nil, err
	}
	var projectIDs, issueIDs []string
	switch {
	case p.pos < len(p.tokens) && p.tokens[p.pos].kind == "word":
		ref := p.tokens[p.pos].value
		p.pos++
		project, err := p.h.Store.ProjectByIDOrKey(p.r.Context(), p.workspaceID, ref)
		if err != nil {
			return nil, fmt.Errorf("%w: project %q does not exist", errUserQuery, ref)
		}
		projectIDs = []string{project.ID}
	case p.pos < len(p.tokens) && p.tokens[p.pos].kind == "lparen":
		p.pos++
		for {
			if p.pos >= len(p.tokens) || p.tokens[p.pos].kind != "word" {
				return nil, fmt.Errorf("%w: an issue key is missing", errUserQuery)
			}
			ref := p.tokens[p.pos].value
			p.pos++
			issue, err := p.h.Store.IssueByIDOrKey(p.r.Context(), p.workspaceID, ref)
			if err != nil {
				return nil, fmt.Errorf("%w: issue %q does not exist", errUserQuery, ref)
			}
			issueIDs = append(issueIDs, issue.ID)
			if p.pos < len(p.tokens) && p.tokens[p.pos].kind == "comma" {
				p.pos++
				continue
			}
			if p.pos < len(p.tokens) && p.tokens[p.pos].kind == "rparen" {
				p.pos++
				break
			}
			return nil, fmt.Errorf("%w: the issue list is not closed", errUserQuery)
		}
	default:
		return nil, fmt.Errorf("%w: a project or issue list is missing", errUserQuery)
	}
	ids, err := p.h.Store.UsersRelatedToIssues(p.r.Context(), p.workspaceID, relation, projectIDs, issueIDs)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, id := range ids {
		set[id] = true
	}
	return set, nil
}
