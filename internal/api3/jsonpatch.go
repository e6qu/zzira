package api3

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// jsonPatchOperation is one RFC 6902 operation.
type jsonPatchOperation struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	From  string          `json:"from"`
	Value json.RawMessage `json:"value"`
}

// applyJSONPatch applies RFC 6902 operations to a JSON document and returns
// the patched document. Operations apply in order and all of them must
// succeed.
func applyJSONPatch(document []byte, patch []byte) ([]byte, error) {
	var operations []jsonPatchOperation
	if err := json.Unmarshal(patch, &operations); err != nil {
		return nil, fmt.Errorf("the request body must be a JSON Patch array")
	}
	var root any
	if err := json.Unmarshal(document, &root); err != nil {
		return nil, err
	}
	for _, operation := range operations {
		var value any
		if operation.Op == "add" || operation.Op == "replace" || operation.Op == "test" {
			if len(operation.Value) == 0 {
				return nil, fmt.Errorf("the %s operation at %s needs a value", operation.Op, operation.Path)
			}
			if err := json.Unmarshal(operation.Value, &value); err != nil {
				return nil, fmt.Errorf("the value at %s is not valid JSON", operation.Path)
			}
		}
		var err error
		switch operation.Op {
		case "add":
			root, err = patchAdd(root, operation.Path, value)
		case "remove":
			root, _, err = patchRemove(root, operation.Path)
		case "replace":
			if _, err = patchGet(root, operation.Path); err == nil {
				root, _, err = patchRemove(root, operation.Path)
				if err == nil {
					root, err = patchAdd(root, operation.Path, value)
				}
			}
		case "move":
			var moved any
			if strings.HasPrefix(operation.Path, operation.From+"/") {
				return nil, fmt.Errorf("cannot move %s into itself", operation.From)
			}
			root, moved, err = patchRemove(root, operation.From)
			if err == nil {
				root, err = patchAdd(root, operation.Path, moved)
			}
		case "copy":
			var copied any
			if copied, err = patchGet(root, operation.From); err == nil {
				root, err = patchAdd(root, operation.Path, deepCopyJSON(copied))
			}
		case "test":
			var current any
			if current, err = patchGet(root, operation.Path); err == nil && !reflect.DeepEqual(current, value) {
				err = fmt.Errorf("the test at %s failed", operation.Path)
			}
		default:
			err = fmt.Errorf("the operation %q is not supported", operation.Op)
		}
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(root)
}

func patchTokens(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("the path %q must start with /", path)
	}
	tokens := strings.Split(path[1:], "/")
	for index, token := range tokens {
		tokens[index] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	return tokens, nil
}

func deepCopyJSON(value any) any {
	raw, _ := json.Marshal(value)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

func patchIndex(token string, length int, allowEnd bool) (int, error) {
	if token == "-" && allowEnd {
		return length, nil
	}
	index, err := strconv.Atoi(token)
	if err != nil || index < 0 || (token != "0" && strings.HasPrefix(token, "0")) || index > length || (!allowEnd && index == length) {
		return 0, fmt.Errorf("the array index %q is out of range", token)
	}
	return index, nil
}

func patchGet(root any, path string) (any, error) {
	tokens, err := patchTokens(path)
	if err != nil {
		return nil, err
	}
	current := root
	for _, token := range tokens {
		switch node := current.(type) {
		case map[string]any:
			value, ok := node[token]
			if !ok {
				return nil, fmt.Errorf("the path %s does not exist", path)
			}
			current = value
		case []any:
			index, err := patchIndex(token, len(node), false)
			if err != nil {
				return nil, err
			}
			current = node[index]
		default:
			return nil, fmt.Errorf("the path %s does not exist", path)
		}
	}
	return current, nil
}

// patchAdd adds value at path, returning the new root.
func patchAdd(root any, path string, value any) (any, error) {
	tokens, err := patchTokens(path)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return value, nil
	}
	return patchAddAt(root, tokens, value, path)
}

func patchAddAt(node any, tokens []string, value any, path string) (any, error) {
	token := tokens[0]
	last := len(tokens) == 1
	switch current := node.(type) {
	case map[string]any:
		if last {
			current[token] = value
			return current, nil
		}
		child, ok := current[token]
		if !ok {
			return nil, fmt.Errorf("the path %s does not exist", path)
		}
		updated, err := patchAddAt(child, tokens[1:], value, path)
		if err != nil {
			return nil, err
		}
		current[token] = updated
		return current, nil
	case []any:
		index, err := patchIndex(token, len(current), last)
		if err != nil {
			return nil, err
		}
		if last {
			current = append(current, nil)
			copy(current[index+1:], current[index:])
			current[index] = value
			return current, nil
		}
		updated, err := patchAddAt(current[index], tokens[1:], value, path)
		if err != nil {
			return nil, err
		}
		current[index] = updated
		return current, nil
	}
	return nil, fmt.Errorf("the path %s does not exist", path)
}

// patchRemove removes the value at path, returning the new root and the value.
func patchRemove(root any, path string) (any, any, error) {
	tokens, err := patchTokens(path)
	if err != nil {
		return nil, nil, err
	}
	if len(tokens) == 0 {
		return nil, root, nil
	}
	return patchRemoveAt(root, tokens, path)
}

func patchRemoveAt(node any, tokens []string, path string) (any, any, error) {
	token := tokens[0]
	last := len(tokens) == 1
	switch current := node.(type) {
	case map[string]any:
		child, ok := current[token]
		if !ok {
			return nil, nil, fmt.Errorf("the path %s does not exist", path)
		}
		if last {
			delete(current, token)
			return current, child, nil
		}
		updated, removed, err := patchRemoveAt(child, tokens[1:], path)
		if err != nil {
			return nil, nil, err
		}
		current[token] = updated
		return current, removed, nil
	case []any:
		index, err := patchIndex(token, len(current), false)
		if err != nil {
			return nil, nil, err
		}
		if last {
			removed := current[index]
			return append(current[:index:index], current[index+1:]...), removed, nil
		}
		updated, removed, err := patchRemoveAt(current[index], tokens[1:], path)
		if err != nil {
			return nil, nil, err
		}
		current[index] = updated
		return current, removed, nil
	}
	return nil, nil, fmt.Errorf("the path %s does not exist", path)
}
