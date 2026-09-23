package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// A site's SAML providers: what an administrator configures, what a sign-in
// remembers, and what makes an answer worth reading only once.
func TestSAMLProvidersAndOneUseSignIns(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	st, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := Migrate(ctx, st.Pool); err != nil {
		t.Fatal(err)
	}
	workspaceID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := st.FirstAdminID(ctx, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	key := "idp" + strings.ToLower(NewID("p")[len(NewID("p"))-6:])
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(ctx, `DELETE FROM saml_identity_providers WHERE provider_key LIKE 'idp%'`)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM saml_sign_in_requests WHERE provider_key LIKE 'idp%'`)
		_, _ = st.Pool.Exec(ctx, `DELETE FROM saml_seen_assertions WHERE assertion_id LIKE 'test-assertion-%'`)
	})

	provider := SAMLProvider{
		ProviderKey: key, DisplayName: "Company SAML", EntityID: "https://idp.example.test/" + key,
		SSOURL: "https://idp.example.test/sso", Certificates: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----",
		Enabled: true,
	}
	if err := st.SaveSAMLProvider(ctx, workspaceID, actorID, provider); err != nil {
		t.Fatalf("save the provider: %v", err)
	}
	read, err := st.SAMLProvider(ctx, workspaceID, key)
	if err != nil || read.EntityID != provider.EntityID || !read.Enabled {
		t.Fatalf("provider = %+v, %v", read, err)
	}
	// Two providers cannot call themselves the same thing, because an
	// assertion says which provider it is from by that name.
	twin := provider
	twin.ProviderKey = key + "b"
	if err := st.SaveSAMLProvider(ctx, workspaceID, actorID, twin); err == nil {
		t.Fatal("two providers share an entity ID")
	}
	// Saving again changes what is there rather than adding another.
	provider.DisplayName, provider.Enabled = "Company SSO", false
	if err := st.SaveSAMLProvider(ctx, workspaceID, actorID, provider); err != nil {
		t.Fatal(err)
	}
	if read, err = st.SAMLProvider(ctx, workspaceID, key); err != nil || read.DisplayName != "Company SSO" || read.Enabled {
		t.Fatalf("provider after saving again = %+v, %v", read, err)
	}

	// A sign-in is remembered once and read once: an answer to a sign-in
	// already answered belongs to nothing.
	if err := st.StartSAMLSignIn(ctx, workspaceID, key, "request-1", "", ""); err != nil {
		t.Fatalf("start a sign-in: %v", err)
	}
	signIn, err := st.ConsumeSAMLSignIn(ctx, workspaceID, "request-1")
	if err != nil || signIn.ProviderKey != key {
		t.Fatalf("sign-in = %+v, %v", signIn, err)
	}
	if _, err := st.ConsumeSAMLSignIn(ctx, workspaceID, "request-1"); err == nil {
		t.Fatal("one sign-in was read twice")
	}
	if _, err := st.ConsumeSAMLSignIn(ctx, workspaceID, "request-nobody-started"); err == nil {
		t.Fatal("a sign-in nobody started was read")
	}

	// An assertion is read once, whatever else it says.
	expires := time.Now().Add(time.Hour)
	if err := st.RememberSAMLAssertion(ctx, "test-assertion-1", expires); err != nil {
		t.Fatalf("remember an assertion: %v", err)
	}
	if err := st.RememberSAMLAssertion(ctx, "test-assertion-1", expires); err == nil {
		t.Fatal("one assertion was used twice")
	}
	if err := st.RememberSAMLAssertion(ctx, "  ", expires); err == nil {
		t.Fatal("an assertion with no id was remembered")
	}

	// Taking the provider away takes its sign-ins with it.
	if err := st.StartSAMLSignIn(ctx, workspaceID, key, "request-2", "", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSAMLProvider(ctx, workspaceID, actorID, key); err != nil {
		t.Fatalf("delete the provider: %v", err)
	}
	if _, err := st.SAMLProvider(ctx, workspaceID, key); err == nil {
		t.Fatal("a deleted provider is still configured")
	}
	if err := st.DeleteSAMLProvider(ctx, workspaceID, actorID, key); err == nil {
		t.Fatal("a provider that is not there was deleted")
	}
}
