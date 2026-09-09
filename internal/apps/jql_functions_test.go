package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
)

func TestInstalledAppJQLFunctionEvaluationAndPrecomputation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := store.Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("connect-jql-function-secret")
	box, err := secretbox.New(bytes.Repeat([]byte{19}, 32))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	functionName := "riskIssues" + strings.ReplaceAll(store.NewID("fn"), "_", "")
	calls := 0
	remote := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls++
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Errorf("read function request: %v", readErr)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if request.URL.Path != "/base/jql/risk" || request.Header.Get("X-Zzira-App-Event") != "jira:jql_function" {
			t.Errorf("function request = %s headers=%v", request.URL.Path, request.Header)
		}
		if err := verifyConnectJWT(connectToken(request), secret, request, body, now); err != nil {
			t.Errorf("function JWT: %v", err)
		}
		var input struct {
			PrecomputationID string `json:"precomputationId"`
			Clause           struct {
				Field, Operator, FunctionName string
				Type                          []string
				Arguments                     []string
			} `json:"clause"`
		}
		if err := json.Unmarshal(body, &input); err != nil || input.PrecomputationID == "" || input.Clause.Field != "key" || input.Clause.Operator != "in" || input.Clause.FunctionName != functionName || !reflect.DeepEqual(input.Clause.Type, []string{"issue"}) {
			t.Errorf("function payload = %+v, %v", input, err)
		}
		response.Header().Set("Content-Type", "application/json")
		if len(input.Clause.Arguments) > 0 && input.Clause.Arguments[0] == "broken" {
			_, _ = response.Write([]byte(`{"error":"Risk data is unavailable","storeErrorAsPrecomputation":true}`))
			return
		}
		_, _ = response.Write([]byte(`{"jql":"project = ops"}`))
	}))
	defer remote.Close()

	appKey := "connect.search-" + strings.ToLower(store.NewID("app"))
	raw := []byte(fmt.Sprintf(`{"key":%q,"name":"Search functions","baseUrl":%q,"authentication":{"type":"jwt"},"scopes":["READ"],"modules":{"jiraJqlFunctions":[{"key":"risk-issues","name":%q,"url":"/jql/risk","arguments":[{"name":"level","required":true},{"name":"team","required":false}],"types":["issue"],"operators":["in","not_in"]}]}}`, appKey, remote.URL+"/base", functionName))
	descriptor, err := ParseDescriptor(raw)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := box.Seal(secret, workspaceID+"/"+appKey)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := st.InstallApp(ctx, workspaceID, adminID, descriptor, raw, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM app_installations WHERE id=$1`, installation.ID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, installation.PrincipalID)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM organization_audit_events WHERE target_type='app' AND target_id=$1`, appKey)
	})

	evaluator := &JQLFunctionEvaluator{Store: st, Secrets: box, Client: remote.Client(), Now: func() time.Time { return now }}
	st.AppJQLExpander = evaluator.Expand
	for attempt := 0; attempt < 2; attempt++ {
		query, err := jql.Parse(fmt.Sprintf(`issue in %s("high", platform)`, functionName))
		if err != nil {
			t.Fatal(err)
		}
		if err := st.ExpandAppJQL(ctx, workspaceID, query); err != nil {
			t.Fatal(err)
		}
		compiled := jql.Compile(query, adminID, jql.DefaultResolver())
		if compiled.Err != nil || !strings.Contains(compiled.Where, "pr.key") || !reflect.DeepEqual(compiled.Args, []any{"OPS"}) {
			t.Fatalf("expanded query SQL=%s args=%#v err=%v", compiled.Where, compiled.Args, compiled.Err)
		}
	}
	if calls != 1 {
		t.Fatalf("function endpoint calls = %d, want one cached call", calls)
	}
	precomputations, total, err := st.JQLFunctionPrecomputations(ctx, installation.ID, []string{appKey + "__risk-issues"}, 0, 10, "")
	if err != nil || total != 1 || precomputations[0].Field != "key" || precomputations[0].Operator != "in" || !reflect.DeepEqual(precomputations[0].Arguments, []string{"high", "platform"}) || precomputations[0].Value == nil || *precomputations[0].Value != "project = ops" {
		t.Fatalf("precomputations = %+v total=%d err=%v", precomputations, total, err)
	}
	firstPrecomputationID := precomputations[0].ID
	now = now.Add(8 * 24 * time.Hour)
	expired, _ := jql.Parse(fmt.Sprintf(`issue in %s("high", platform)`, functionName))
	if err := st.ExpandAppJQL(ctx, workspaceID, expired); err != nil {
		t.Fatal(err)
	}
	precomputations, total, err = st.JQLFunctionPrecomputations(ctx, installation.ID, []string{appKey + "__risk-issues"}, 0, 10, "")
	if err != nil || total != 1 || precomputations[0].ID == firstPrecomputationID || calls != 2 {
		t.Fatalf("expired precomputation = %+v total=%d calls=%d err=%v", precomputations, total, calls, err)
	}

	invalid, _ := jql.Parse(fmt.Sprintf(`issue = %s(high)`, functionName))
	if err := evaluator.Expand(ctx, workspaceID, invalid); err == nil || !strings.Contains(err.Error(), "operator") {
		t.Fatalf("unsupported operator error = %v", err)
	}
	missing, _ := jql.Parse(fmt.Sprintf(`issue in %s()`, functionName))
	if err := evaluator.Expand(ctx, workspaceID, missing); err == nil || !strings.Contains(err.Error(), "expects 1 to 2") {
		t.Fatalf("argument error = %v", err)
	}
	broken, _ := jql.Parse(fmt.Sprintf(`issue in %s(broken)`, functionName))
	if err := evaluator.Expand(ctx, workspaceID, broken); err == nil || err.Error() != "Risk data is unavailable" {
		t.Fatalf("app error = %v", err)
	}
	if err := evaluator.Expand(ctx, workspaceID, broken); err == nil || err.Error() != "Risk data is unavailable" || calls != 3 {
		t.Fatalf("cached app error = %v calls=%d", err, calls)
	}
}
