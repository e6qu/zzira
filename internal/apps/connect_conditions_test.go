package apps

import (
	"encoding/json"
	"testing"

	"github.com/e6qu/zzira/internal/models"
)

func TestConnectDashboardItemsKeepConfigurationRefreshAndConditions(t *testing.T) {
	wire, err := translateConnectDashboardItem(connectDashboardItemWire{
		Key: "item", URL: "/item", Name: connectNameWire{Value: "Item"}, Description: connectNameWire{Value: "Item"}, ThumbnailURL: "/item.svg",
		Configurable: true, Refreshable: true,
		Conditions: []json.RawMessage{json.RawMessage(`{"conditions":[{"condition":"user_is_admin"},{"condition":"user_is_logged_in","invert":true}],"type":"OR"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	configurable, refreshable, conditions := DashboardItemOptions(wire.Body)
	if !configurable || !refreshable || len(conditions) == 0 {
		t.Fatalf("options = %v %v %s", configurable, refreshable, conditions)
	}
	if ConnectConditionsMet(conditions, ConnectConditionFacts{LoggedIn: true}) {
		t.Fatal("a signed-in person who is not an administrator met an administrator-or-signed-out condition")
	}
	if !ConnectConditionsMet(conditions, ConnectConditionFacts{LoggedIn: true, SiteAdmin: true}) {
		t.Fatal("an administrator did not meet an administrator-or-signed-out condition")
	}
	if !ConnectConditionsMet(nil, ConnectConditionFacts{}) {
		t.Fatal("a module without conditions was hidden")
	}
	if configurable, refreshable, _ := DashboardItemOptions("Host-rendered gadget text"); configurable || refreshable {
		t.Fatal("a host-rendered gadget was read as configurable")
	}
	for _, raw := range []string{
		`{"condition":"entity_property_equal_to"}`,
		`{"conditions":[{"condition":"user_is_admin"}],"type":"XOR"}`,
		`{"condition":"user_is_admin","conditions":[{"condition":"user_is_admin"}]}`,
		`"user_is_admin"`,
	} {
		if _, err := connectConditions([]json.RawMessage{json.RawMessage(raw)}); err == nil {
			t.Fatalf("accepted condition %s", raw)
		}
	}
}

func TestBridgeRequestsStayWithinTheAppsScopes(t *testing.T) {
	reader := &models.AppInstallation{Status: "active", Scopes: []string{"read:jira-work"}}
	if err := BridgeRequestAllowed(reader, "GET", "/rest/api/3/search/jql"); err != nil {
		t.Fatalf("read request refused: %v", err)
	}
	if err := BridgeRequestAllowed(reader, "PUT", "/rest/api/3/dashboard/10000/items/1/properties/config"); err == nil {
		t.Fatal("a read-only app wrote through the bridge")
	}
	for _, path := range []string{"/admin/users", "/rest/atlassian-connect/1/app/module/dynamic", "/dashboards/1"} {
		if err := BridgeRequestAllowed(reader, "GET", path); err == nil {
			t.Fatalf("bridge allowed %s", path)
		}
	}
	if err := BridgeRequestAllowed(&models.AppInstallation{Status: "suspended", Scopes: []string{"read:jira-work"}}, "GET", "/rest/api/3/search/jql"); err == nil {
		t.Fatal("a suspended app used the bridge")
	}
}
