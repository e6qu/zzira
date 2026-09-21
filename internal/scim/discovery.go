package scim

import (
	"net/http"
	"strings"
)

// An identity provider reads these three before it provisions anything: what
// the service supports, which resources it has, and what those resources look
// like. RFC 7644 §4 gives them their shape.

// ServiceProviderConfig says which parts of SCIM this service implements.
func (h *Handler) ServiceProviderConfig(w http.ResponseWriter, r *http.Request) {
	if _, _, _, ok := h.authorize(w, r); !ok {
		return
	}
	writeSCIM(w, http.StatusOK, map[string]any{
		"schemas":          []string{"urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig"},
		"documentationUri": strings.TrimRight(h.BaseURL, "/") + "/",
		"patch":            map[string]any{"supported": true},
		"bulk":             map[string]any{"supported": false, "maxOperations": 0, "maxPayloadSize": 0},
		"filter":           map[string]any{"supported": true, "maxResults": maximumPage},
		"changePassword":   map[string]any{"supported": false},
		"sort":             map[string]any{"supported": false},
		"etag":             map[string]any{"supported": false},
		"authenticationSchemes": []map[string]any{{
			"type":        "oauthbearertoken",
			"name":        "OAuth Bearer Token",
			"description": "Authentication with a bearer token of an organization administrator.",
			"primary":     true,
		}},
		"meta": map[string]any{"resourceType": "ServiceProviderConfig", "location": strings.TrimRight(h.BaseURL, "/") + r.URL.Path},
	})
}

// ResourceTypes lists the resources a provider can provision.
func (h *Handler) ResourceTypes(w http.ResponseWriter, r *http.Request) {
	_, _, directoryID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	base := strings.TrimRight(h.BaseURL, "/") + "/scim/directory/" + directoryID
	resources := []map[string]any{
		{
			"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"},
			"id":      "User", "name": "User", "endpoint": "/Users", "description": "A person in this directory.",
			"schema": userSchema,
			"meta":   map[string]any{"resourceType": "ResourceType", "location": base + "/ResourceTypes/User"},
		},
		{
			"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:ResourceType"},
			"id":      "Group", "name": "Group", "endpoint": "/Groups", "description": "A group in this directory.",
			"schema": groupSchema,
			"meta":   map[string]any{"resourceType": "ResourceType", "location": base + "/ResourceTypes/Group"},
		},
	}
	writeSCIM(w, http.StatusOK, listResponse(resources, 1, maximumPage))
}

// Schemas lists the attributes each resource carries. Only the attributes this
// directory can hold are advertised, so a provider does not send what would be
// dropped.
func (h *Handler) Schemas(w http.ResponseWriter, r *http.Request) {
	_, _, directoryID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	base := strings.TrimRight(h.BaseURL, "/") + "/scim/directory/" + directoryID
	attribute := func(name, kind string, multi bool) map[string]any {
		return map[string]any{
			"name": name, "type": kind, "multiValued": multi, "required": false,
			"caseExact": false, "mutability": "readWrite", "returned": "default", "uniqueness": "none",
		}
	}
	resources := []map[string]any{
		{
			"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:Schema"},
			"id":      userSchema, "name": "User", "description": "A person in this directory.",
			"attributes": []map[string]any{
				attribute("userName", "string", false), attribute("displayName", "string", false),
				attribute("name", "complex", false), attribute("emails", "complex", true),
				attribute("active", "boolean", false), attribute("externalId", "string", false),
				attribute("groups", "complex", true),
			},
			"meta": map[string]any{"resourceType": "Schema", "location": base + "/Schemas/" + userSchema},
		},
		{
			"schemas": []string{"urn:ietf:params:scim:schemas:core:2.0:Schema"},
			"id":      groupSchema, "name": "Group", "description": "A group in this directory.",
			"attributes": []map[string]any{
				attribute("displayName", "string", false), attribute("members", "complex", true),
				attribute("externalId", "string", false),
			},
			"meta": map[string]any{"resourceType": "Schema", "location": base + "/Schemas/" + groupSchema},
		},
	}
	writeSCIM(w, http.StatusOK, listResponse(resources, 1, maximumPage))
}
