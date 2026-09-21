package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/admin"
	"github.com/e6qu/zzira/internal/agile"
	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/apps"
	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/automation"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/confluence"
	"github.com/e6qu/zzira/internal/demo"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/mailer"
	"github.com/e6qu/zzira/internal/notifybus"
	"github.com/e6qu/zzira/internal/secretbox"
	"github.com/e6qu/zzira/internal/store"
	"github.com/e6qu/zzira/internal/syncapi"
	"github.com/e6qu/zzira/internal/web"
	"github.com/e6qu/zzira/internal/webhooks"
)

func main() {
	mode := flag.String("mode", "run", "run|migrate|seed|demo")
	scenario := flag.String("scenario", "demo/company.json", "the demo scenario -mode=demo applies")
	workspace := flag.String("workspace", "", "the workspace -mode=demo applies the scenario to (default $WORKSPACE_SLUG, then the scenario's own slug)")
	healthcheck := flag.Bool("healthcheck", false, "verify the process can serve and exit")
	addr := flag.String("addr", "", "listen address (default :$SERVER_PORT or :8080)")
	staticDir := flag.String("static", "", "static dir (default web/static)")
	flag.Parse()

	if *healthcheck {
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get("http://localhost:" + envOr("SERVER_PORT", "8080") + "/rest/api/3/serverInfo")
		if err != nil {
			os.Exit(1)
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Printf("healthcheck body close: %v", closeErr)
		}
		if resp.StatusCode != 200 {
			os.Exit(1)
		}
		fmt.Println("ok")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, store.DSNFromEnv())
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	if err := store.Migrate(ctx, st.Pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	// The one-shot modes leave tables the planner has never looked at, so
	// each one hands over a database that can be planned for.
	if *mode == "migrate" {
		if err := st.AnalyzeForPlanner(ctx); err != nil {
			log.Fatalf("analyze: %v", err)
		}
		fmt.Println("migrations applied")
		return
	}
	if *mode == "demo" {
		if err := applyDemoScenario(ctx, st, *scenario, *workspace, os.Getenv); err != nil {
			log.Fatalf("demo: %v", err)
		}
		if err := st.AnalyzeForPlanner(ctx); err != nil {
			log.Fatalf("analyze: %v", err)
		}
		return
	}
	if *mode == "seed" {
		if err := seedUsers(ctx, st); err != nil {
			log.Fatalf("seed: %v", err)
		}
		if err := st.AnalyzeForPlanner(ctx); err != nil {
			log.Fatalf("analyze: %v", err)
		}
		return
	}
	if email := os.Getenv("ZZIRA_BOOTSTRAP_ADMIN_EMAIL"); email != "" {
		if err := ensureBootstrapAdmin(ctx, st, email); err != nil {
			log.Fatalf("bootstrap admin: %v", err)
		}
	}
	workspaceSlug, err := servingWorkspaceSlug(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	workspaceID, err := st.WorkspaceBySlug(ctx, workspaceSlug)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			log.Fatal("configured workspace does not exist")
		}
		log.Fatalf("load configured workspace: %v", err)
	}

	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}
	address := *addr
	if address == "" {
		address = ":" + port
	}
	static := *staticDir
	if static == "" {
		static = envOr("STATIC_DIR", "web/static")
	}
	blobs, err := attachments.NewFS(blobDir(os.Getenv))
	if err != nil {
		log.Fatalf("blob storage: %v", err)
	}
	cmdSvc := &commands.Service{Store: st, Blobs: blobs}
	automationSvc := &automation.Service{Store: st, Commands: cmdSvc}
	smtpSender, err := mailer.SMTPFromEnv()
	if err != nil {
		log.Fatalf("configure invitation email: %v", err)
	}

	identityProviders, err := web.NewIdentityProviders(ctx)
	if err != nil {
		log.Fatalf("configure identity providers: %v", err)
	}
	identityExternalURL := strings.TrimRight(os.Getenv("ZZIRA_EXTERNAL_URL"), "/")
	providerSecrets, err := secretbox.FromEnv("ZZIRA_IDENTITY_ENCRYPTION_KEY")
	if err != nil {
		log.Fatalf("configure identity provider credential encryption: %v", err)
	}
	registrations, err := st.IdentityProviderRegistrationsByWorkspace(ctx, workspaceID)
	if err != nil {
		log.Fatalf("load identity provider registrations: %v", err)
	}
	if err := identityProviders.LoadStored(ctx, registrations, providerSecrets, workspaceID, identityExternalURL); err != nil {
		log.Fatalf("configure stored identity providers: %v", err)
	}
	providerSettings, err := st.IdentityProviderSettingsByWorkspace(ctx, workspaceID)
	if err != nil {
		log.Fatalf("load identity provider settings: %v", err)
	}
	identityProviders.ApplyEnabled(providerSettings)
	baseURL := envOr("BASE_URL", "http://localhost:"+port)
	webHandler := &web.Handler{
		Store: st, Commands: cmdSvc, Automation: automationSvc, OIDC: identityProviders.Provider("shauth"), IdentityProviders: identityProviders, ProviderSecrets: providerSecrets, IdentityExternalURL: identityExternalURL,
		WorkspaceSlug: workspaceSlug, BaseURL: baseURL, InvitationNotificationsConfigured: smtpSender != nil,
	}
	// ZZIRA_ANONYMOUS_ACCESS=off closes the instance to callers without
	// credentials, downloads included, for an installation that must be
	// reachable only after signing in. The REST API is unchanged: this is the
	// instance's configuration, the way a Jira site's administrator decides
	// whether the anonymous user exists at all, and the default keeps it.
	anonymousAccess := envOr("ZZIRA_ANONYMOUS_ACCESS", "on") != "off"
	if !anonymousAccess {
		log.Printf("anonymous access is off: a caller without credentials is refused, downloads included")
	}
	// ZZIRA_LOCAL_CREDENTIALS=off refuses the credentials this installation
	// issued itself -- passwords and API tokens -- so the only way in is a
	// session an identity provider established. An installation published
	// behind single sign-on sets it, because -mode=demo mints a password and
	// an API token for every person in the scenario and those would otherwise
	// be working logins past the provider. Like anonymous access it is the
	// instance's configuration, not a site setting the REST API exposes, and
	// the default keeps password sign-in and API tokens.
	localCredentials := envOr("ZZIRA_LOCAL_CREDENTIALS", "on") != "off"
	if !localCredentials {
		if len(identityProviders.LoginProviders()) == 0 {
			log.Fatal("ZZIRA_LOCAL_CREDENTIALS=off needs an identity provider: nothing else could sign anyone in")
		}
		log.Printf("local credentials are off: only a session from an identity provider is accepted")
	}
	api := &api3.Handler{Store: st, Commands: cmdSvc, Blobs: blobs, BaseURL: baseURL, WorkspaceSlug: workspaceSlug, StaticDir: static, AnonymousAccess: anonymousAccess}
	st.IssueExpressionEvaluator = api.EvaluateIssueExpression
	if providerSecrets != nil {
		appJQL := &apps.JQLFunctionEvaluator{Store: st, Secrets: providerSecrets, Client: &http.Client{Timeout: 10 * time.Second}}
		st.AppJQLExpander = appJQL.Expand
	}
	agileAPI := &agile.Handler{Store: st, Commands: cmdSvc, IssueBean: api.IssueBean, BaseURL: envOr("BASE_URL", "http://localhost:"+port), WorkspaceSlug: workspaceSlug}
	automationAPI := &automation.Handler{Service: automationSvc, WorkspaceSlug: workspaceSlug}
	appAPI := &apps.Handler{Store: st, Secrets: providerSecrets, WorkspaceSlug: workspaceSlug}
	adminAPI := &admin.Handler{
		Store: st, BaseURL: api.BaseURL, WorkspaceSlug: workspaceSlug,
		InvitationNotificationsConfigured: smtpSender != nil,
	}
	bus := notifybus.New()
	sse := &syncapi.SSEHandler{Store: st, Bus: bus, WorkspaceSlug: workspaceSlug}
	sync := &syncapi.Handler{Store: st, WorkspaceSlug: workspaceSlug}
	webhookSearch := func(ctx context.Context, wsID, jqlText string) (bool, error) {
		// Webhooks are admin-configured, so their filters use a workspace
		// administrator's complete issue view.
		adminID, err := st.FirstAdminID(ctx, wsID)
		if err != nil {
			return false, err
		}
		q, err := jql.Parse(jqlText)
		if err != nil {
			return false, err
		}
		if err := st.ExpandAppJQL(ctx, wsID, q); err != nil {
			return false, err
		}
		resolver, err := st.JQLResolver(ctx, wsID)
		if err != nil {
			return false, err
		}
		compiled := jql.CompileAt(q, adminID, resolver, 1)
		if compiled.Err != nil {
			return false, compiled.Err
		}
		issues, _, err := st.Search(ctx, wsID, adminID, compiled, 1, 0)
		return err == nil && len(issues) > 0, nil
	}
	dispatcher := &webhooks.Dispatcher{
		Store:   st,
		Client:  &http.Client{Timeout: 10 * time.Second},
		Checker: &webhooks.JQLChecker{Search: webhookSearch},
	}
	go dispatcher.Run(ctx, workspaceID)
	if providerSecrets != nil {
		go (&apps.OutboundRunner{
			Store: st, Secrets: providerSecrets,
			Client: &http.Client{Timeout: 10 * time.Second}, Search: webhookSearch,
		}).Run(ctx, workspaceID)
	}
	go (&automation.Runner{Service: automationSvc}).Run(ctx, workspaceID)
	go (&store.FilterSubscriptionRunner{Store: st, BaseURL: baseURL}).Run(ctx, workspaceID)
	go (&store.DashboardSubscriptionRunner{Store: st, BaseURL: baseURL}).Run(ctx, workspaceID)
	go (&store.WikiNotificationEmailRunner{Store: st, BaseURL: baseURL}).Run(ctx)
	go (&store.APITaskRunner{Store: st, BulkIssueExecutor: cmdSvc, Blobs: blobs}).Run(ctx, workspaceID)
	go (&store.ProjectTrashRunner{Store: st}).Run(ctx, workspaceID)
	go (&store.ServiceSLARunner{Store: st}).Run(ctx, workspaceID)
	go (&store.ServiceIncidentEscalationRunner{Store: st}).Run(ctx, workspaceID)
	go (&commands.AttachmentBlobDeletionRunner{Service: cmdSvc}).Run(ctx)
	go (&commands.ServiceTemporaryAttachmentRunner{Service: cmdSvc}).Run(ctx)
	if smtpSender != nil {
		go (&mailer.Runner{Store: st, Sender: smtpSender, BaseURL: baseURL}).Run(ctx)
	}
	go func() {
		for {
			if err := bus.Listen(ctx, st.Pool); err != nil && ctx.Err() == nil {
				log.Printf("notifybus listen: %v (retrying in 2s)", err)
			}
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", webHandler.Home)
	mux.HandleFunc("GET /login", webHandler.LoginForm)
	mux.HandleFunc("GET /auth/{provider}", webHandler.OIDCLogin)
	mux.HandleFunc("GET /auth/{provider}/link", webHandler.IdentityProviderLink)
	mux.HandleFunc("GET /auth/{provider}/callback", webHandler.OIDCCallback)
	mux.HandleFunc("GET /auth/{provider}/logout/complete", webHandler.OIDCLogoutComplete)
	mux.HandleFunc("GET /auth/validation", webHandler.Validation)
	mux.HandleFunc("GET /monitoring/observation", webHandler.Monitoring)
	mux.HandleFunc("POST /auth/{provider}/backchannel-logout", webHandler.BackChannelLogout)
	mux.HandleFunc("POST /login", webHandler.LoginSubmit)
	mux.HandleFunc("POST /logout", webHandler.Logout)
	mux.HandleFunc("GET /signed-out", webHandler.SignedOut)
	mux.HandleFunc("GET /projects", webHandler.ProjectsPage)
	mux.HandleFunc("GET /service", webHandler.ServiceHome)
	mux.HandleFunc("GET /service/portals/{desk}", webHandler.ServicePortal)
	mux.HandleFunc("GET /service/knowledge/{page}", webHandler.ServiceKnowledgePage)
	mux.HandleFunc("GET /service/portals/{desk}/request/{requestType}", webHandler.ServiceRequestForm)
	mux.HandleFunc("POST /service/portals/{desk}/request/{requestType}", webHandler.ServiceRequestForm)
	mux.HandleFunc("GET /service/requests/{key}", webHandler.ServiceRequestPage)
	mux.HandleFunc("POST /service/requests/{key}/comments", webHandler.ServiceRequestComment)
	mux.HandleFunc("POST /service/requests/{key}/approvals", webHandler.ServiceRequestApproval)
	mux.HandleFunc("POST /service/requests/{key}/approvals/{approval}", webHandler.ServiceRequestApprovalDecision)
	mux.HandleFunc("POST /service/requests/{key}/operations", webHandler.ServiceRequestOperations)
	mux.HandleFunc("POST /service/requests/{key}/incident-updates", webHandler.ServiceIncidentUpdate)
	mux.HandleFunc("POST /service/requests/{key}/incident-roles", webHandler.ServiceIncidentRole)
	mux.HandleFunc("POST /service/requests/{key}/incident-stakeholders", webHandler.ServiceIncidentStakeholder)
	mux.HandleFunc("POST /service/requests/{key}/notification", webHandler.ServiceRequestNotification)
	mux.HandleFunc("POST /service/requests/{key}/feedback", webHandler.ServiceRequestFeedback)
	mux.HandleFunc("POST /service/requests/{key}/transition", webHandler.ServiceRequestTransition)
	mux.HandleFunc("POST /service/requests/{key}/participants", webHandler.ServiceRequestParticipant)
	mux.HandleFunc("POST /service/requests/{key}/links", webHandler.ServiceRequestLink)
	mux.HandleFunc("POST /service/requests/{key}/links/{link}/delete", webHandler.ServiceRequestLinkDelete)
	mux.HandleFunc("POST /service/requests/{key}/assets", webHandler.ServiceRequestAssetSettings)
	mux.HandleFunc("GET /service/agent", webHandler.ServiceAgent)
	mux.HandleFunc("GET /service/agent/{desk}", webHandler.ServiceAgent)
	mux.HandleFunc("GET /service/agent/{desk}/reports", webHandler.ServiceReports)
	mux.HandleFunc("GET /service/agent/{desk}/assets", webHandler.ServiceAssetsPage)
	mux.HandleFunc("POST /service/agent/{desk}/assets/schemas", webHandler.ServiceAssetSchemaSettings)
	mux.HandleFunc("POST /service/agent/{desk}/assets/objects", webHandler.ServiceAssetObjectSettings)
	mux.HandleFunc("POST /service/agent/{desk}/assets/relationships", webHandler.ServiceAssetRelationshipSettings)
	mux.HandleFunc("POST /service/agent/{desk}/agents", webHandler.ServiceAgentSettings)
	mux.HandleFunc("POST /service/agent/{desk}/operations", webHandler.ServiceOperationsSettings)
	mux.HandleFunc("POST /service/agent/{desk}/on-call", webHandler.ServiceOnCallSettings)
	mux.HandleFunc("POST /service/agent/{desk}/escalations", webHandler.ServiceEscalationSettings)
	mux.HandleFunc("POST /service/agent/{desk}/queues", webHandler.ServiceQueueSettings)
	mux.HandleFunc("POST /service/agent/{desk}/request-types/{requestType}/fields", webHandler.ServiceRequestTypeFieldSettings)
	mux.HandleFunc("POST /service/agent/{desk}/request-types", webHandler.ServiceRequestTypeSettings)
	mux.HandleFunc("POST /service/agent/{desk}/request-type-groups", webHandler.ServiceRequestTypeGroupSettings)
	mux.HandleFunc("POST /service/agent/{desk}/customers", webHandler.ServiceCustomerSettings)
	mux.HandleFunc("POST /service/agent/{desk}/portal", webHandler.ServicePortalSettings)
	mux.HandleFunc("POST /service/agent/{desk}/announcement", webHandler.ServicePortalAnnouncement)
	mux.HandleFunc("POST /service/help-center", webHandler.ServiceHelpCenterSettings)
	mux.HandleFunc("POST /service/agent/{desk}/deployment-gating", webHandler.ServiceDeploymentGateSettings)
	mux.HandleFunc("POST /service/agent/{desk}/organizations", webHandler.ServiceOrganizationSettings)
	mux.HandleFunc("POST /service/agent/{desk}/knowledge", webHandler.ServiceKnowledgeSettings)
	mux.HandleFunc("POST /service/agent/{desk}/calendar", webHandler.ServiceCalendarSettings)
	mux.HandleFunc("POST /service/agent/{desk}/calendars", webHandler.ServiceCalendars)
	mux.HandleFunc("POST /service/agent/{desk}/calendar/holidays", webHandler.ServiceCalendarHolidaySettings)
	mux.HandleFunc("POST /service/agent/{desk}/sla/{metric}", webHandler.ServiceSLASettings)
	mux.HandleFunc("POST /service/agent/{desk}/sla/{metric}/goals", webHandler.ServiceSLAGoalSettings)
	mux.HandleFunc("POST /service/agent/{desk}/slas", webHandler.ServiceSLACreate)
	mux.HandleFunc("POST /service/agent/{desk}/sla/{metric}/delete", webHandler.ServiceSLADelete)
	mux.HandleFunc("POST /service/agent/{desk}/requests/{key}/assign", webHandler.ServiceAgentAssign)
	mux.HandleFunc("POST /service/agent/{desk}/bulk", webHandler.ServiceQueueBulk)
	mux.HandleFunc("GET /admin", webHandler.AdminPage)
	mux.HandleFunc("GET /admin/apps/modules/{module}", webHandler.AdminAppModulePage)
	mux.HandleFunc("POST /admin/filter-subscriptions/{subscription}/delete", webHandler.DeleteAdminFilterSubscription)
	mux.HandleFunc("GET /admin/notification-helper", webHandler.NotificationHelperPage)
	mux.HandleFunc("GET /admin/permission-helper", webHandler.PermissionHelperPage)
	mux.HandleFunc("POST /admin/apps", webHandler.CreateAdminApp)
	mux.HandleFunc("POST /admin/apps/{appKey}", webHandler.UpdateAdminApp)
	mux.HandleFunc("POST /admin/apps/{appKey}/transfers", webHandler.CreateAdminAppTransfer)
	mux.HandleFunc("POST /admin/identity-providers/{provider}", webHandler.UpdateAdminIdentityProvider)
	mux.HandleFunc("POST /admin/identity-providers", webHandler.CreateAdminIdentityProvider)
	mux.HandleFunc("POST /admin/groups", webHandler.CreateAdminGroup)
	mux.HandleFunc("POST /admin/groups/{groupId}/delete", webHandler.DeleteAdminGroup)
	mux.HandleFunc("POST /admin/groups/{groupId}/members", webHandler.UpdateAdminGroupMember)
	mux.HandleFunc("POST /admin/groups/{groupId}/roles", webHandler.UpdateAdminGroupRole)
	mux.HandleFunc("POST /admin/users/invite", webHandler.InviteAdminUser)
	mux.HandleFunc("POST /admin/users/{accountId}", webHandler.UpdateAdminUserStatus)
	mux.HandleFunc("POST /admin/users/{accountId}/profile", webHandler.UpdateAdminUserProfile)
	mux.HandleFunc("POST /admin/products/{productId}/plan", webHandler.UpdateAdminProductPlan)
	mux.HandleFunc("POST /admin/domains", webHandler.CreateAdminDomain)
	mux.HandleFunc("POST /admin/domains/{domainId}", webHandler.UpdateAdminDomain)
	mux.HandleFunc("POST /admin/policies", webHandler.CreateAdminPolicy)
	mux.HandleFunc("POST /admin/policies/{policyId}", webHandler.UpdateAdminPolicy)
	mux.HandleFunc("POST /admin/jira-configuration/{section}", webHandler.UpdateAdminJiraConfiguration)
	mux.HandleFunc("POST /admin/jira-application-properties/{property}", webHandler.UpdateAdminJiraApplicationProperty)
	mux.HandleFunc("POST /admin/global-permissions", webHandler.CreateAdminGlobalPermissionGrant)
	mux.HandleFunc("POST /admin/global-permissions/{grantId}/delete", webHandler.DeleteAdminGlobalPermissionGrant)
	mux.HandleFunc("POST /admin/issue-events", webHandler.CreateAdminIssueEvent)
	mux.HandleFunc("POST /admin/issue-events/{eventId}", webHandler.UpdateAdminIssueEvent)
	mux.HandleFunc("POST /admin/project-categories", webHandler.CreateAdminProjectCategory)
	mux.HandleFunc("POST /admin/project-categories/{categoryId}", webHandler.UpdateAdminProjectCategory)
	mux.HandleFunc("POST /admin/classification-levels", webHandler.CreateAdminClassificationLevel)
	mux.HandleFunc("POST /admin/classification-levels/{levelId}", webHandler.UpdateAdminClassificationLevel)
	mux.HandleFunc("GET /wiki", webHandler.WikiHome)
	mux.HandleFunc("GET /wiki/pages/{page}", webHandler.WikiPageRedirect)
	mux.HandleFunc("GET /wiki/blogposts/{blogpost}", webHandler.WikiBlogPostRedirect)
	mux.HandleFunc("POST /wiki/spaces", webHandler.WikiHome)
	mux.HandleFunc("GET /wiki/spaces/{space}", webHandler.WikiSpacePage)
	mux.HandleFunc("POST /wiki/spaces/{space}/classification", webHandler.WikiSpaceClassification)
	mux.HandleFunc("POST /wiki/spaces/{space}/properties", webHandler.WikiSpaceProperty)
	mux.HandleFunc("POST /wiki/spaces/{space}/roles", webHandler.WikiSpaceRoleSettings)
	mux.HandleFunc("POST /wiki/spaces/{space}/details", webHandler.WikiSpaceDetails)
	mux.HandleFunc("POST /wiki/spaces/{space}/permissions", webHandler.WikiSpacePermissionGrants)
	mux.HandleFunc("POST /wiki/spaces/{space}/role-assignments", webHandler.WikiSpaceRoleAssignments)
	mux.HandleFunc("GET /wiki/spaces/{space}/blogposts/new", webHandler.WikiBlogPostNew)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/new", webHandler.WikiBlogPostNew)
	mux.HandleFunc("GET /wiki/spaces/{space}/blogposts/{blogpost}", webHandler.WikiBlogPostPage)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}", webHandler.WikiBlogPostPage)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/lifecycle", webHandler.WikiBlogPostLifecycle)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/metadata", webHandler.WikiBlogPostMetadata)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/attachments", webHandler.WikiBlogAttachmentCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/attachments/{attachment}/delete", webHandler.WikiBlogAttachmentDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/comments", webHandler.WikiBlogCommentCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/inline-comments", webHandler.WikiBlogInlineCommentCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/inline-comments/{comment}", webHandler.WikiBlogInlineCommentUpdate)
	mux.HandleFunc("POST /wiki/spaces/{space}/folders", webHandler.WikiFolderCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/folders/{folder}/delete", webHandler.WikiFolderDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/embeds", webHandler.WikiSmartLinkCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/embeds/{embed}/delete", webHandler.WikiSmartLinkDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases", webHandler.WikiDatabaseCreate)
	mux.HandleFunc("GET /wiki/spaces/{space}/databases/{database}", webHandler.WikiDatabasePage)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/columns", webHandler.WikiDatabaseColumnCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/columns/{column}/delete", webHandler.WikiDatabaseColumnDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/rows", webHandler.WikiDatabaseRowSave)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/rows/{row}", webHandler.WikiDatabaseRowSave)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/rows/{row}/delete", webHandler.WikiDatabaseRowDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/views", webHandler.WikiDatabaseViewSave)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/views/{view}/delete", webHandler.WikiDatabaseViewDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/classification", webHandler.WikiDatabaseClassification)
	mux.HandleFunc("POST /wiki/spaces/{space}/databases/{database}/delete", webHandler.WikiDatabaseDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/whiteboards", webHandler.WikiWhiteboardCreate)
	mux.HandleFunc("GET /wiki/spaces/{space}/whiteboards/{whiteboard}", webHandler.WikiWhiteboardPage)
	mux.HandleFunc("POST /wiki/spaces/{space}/whiteboards/{whiteboard}/objects", webHandler.WikiWhiteboardObjectSave)
	mux.HandleFunc("POST /wiki/spaces/{space}/whiteboards/{whiteboard}/objects/{object}", webHandler.WikiWhiteboardObjectSave)
	mux.HandleFunc("POST /wiki/spaces/{space}/whiteboards/{whiteboard}/objects/{object}/delete", webHandler.WikiWhiteboardObjectDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/whiteboards/{whiteboard}/connectors", webHandler.WikiWhiteboardConnectorSave)
	mux.HandleFunc("POST /wiki/spaces/{space}/whiteboards/{whiteboard}/connectors/{connector}/delete", webHandler.WikiWhiteboardConnectorDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/whiteboards/{whiteboard}/classification", webHandler.WikiWhiteboardClassification)
	mux.HandleFunc("POST /wiki/spaces/{space}/whiteboards/{whiteboard}/delete", webHandler.WikiWhiteboardDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/watch", webHandler.WikiSpaceWatch)
	mux.HandleFunc("POST /wiki/spaces/{space}/content-state-settings", webHandler.WikiSpaceContentStateSettings)
	mux.HandleFunc("GET /wiki/spaces/{space}/templates", webHandler.WikiSpaceTemplates)
	mux.HandleFunc("POST /wiki/spaces/{space}/templates", webHandler.WikiSpaceTemplates)
	mux.HandleFunc("GET /wiki/spaces/{space}/analytics", webHandler.WikiSpaceAnalytics)
	mux.HandleFunc("POST /wiki/spaces/{space}/status", webHandler.WikiSpaceStatus)
	mux.HandleFunc("POST /wiki/spaces/{space}/trash", webHandler.WikiSpaceTrash)
	mux.HandleFunc("POST /wiki/spaces/{space}/restore", webHandler.WikiSpaceRestore)
	mux.HandleFunc("POST /wiki/spaces/{space}/purge", webHandler.WikiSpacePurge)
	mux.HandleFunc("POST /wiki/spaces/{space}/exports", webHandler.WikiSpaceExportCreate)
	mux.HandleFunc("GET /wiki/spaces/{space}/exports/{file}", webHandler.WikiSpaceExportFile)
	mux.HandleFunc("GET /wiki/spaces/{space}/pages/new", webHandler.WikiEdit)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/new", webHandler.WikiEdit)
	mux.HandleFunc("GET /wiki/spaces/{space}/pages/{page}", webHandler.WikiPage)
	mux.HandleFunc("GET /wiki/spaces/{space}/pages/{page}/edit", webHandler.WikiEdit)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/edit", webHandler.WikiEdit)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/trash", webHandler.WikiTrash)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/archive", webHandler.WikiPageArchive)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/restore", webHandler.WikiPageRestore)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/move", webHandler.WikiPageMove)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/favourite", webHandler.WikiPageFavourite)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/draft/discard", webHandler.WikiPageDraftDiscard)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/purge", webHandler.WikiPagePurge)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/owner", webHandler.WikiPageOwner)
	mux.HandleFunc("GET /wiki/spaces/{space}/embeds/{embed}", webHandler.WikiSmartLinkPage)
	mux.HandleFunc("POST /wiki/spaces/{space}/content/{node}/move", webHandler.WikiContentMove)
	mux.HandleFunc("POST /wiki/spaces/{space}/content/{node}/archive", webHandler.WikiContentArchive)
	mux.HandleFunc("POST /wiki/spaces/{space}/content/{node}/restore", webHandler.WikiContentRestore)
	mux.HandleFunc("POST /wiki/spaces/{space}/content/{node}/rename", webHandler.WikiContentRename)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/labels", webHandler.WikiPageLabels)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/metadata", webHandler.WikiPageMetadata)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/watch", webHandler.WikiPageWatch)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/presence", webHandler.WikiPagePresence)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/live", webHandler.WikiPageLive)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/presence", webHandler.WikiBlogPostPresence)
	mux.HandleFunc("POST /wiki/spaces/{space}/blogposts/{blogpost}/live", webHandler.WikiBlogPostLive)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/labels/{label}/watch", webHandler.WikiLabelWatch)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/restrictions", webHandler.WikiPageRestrictions)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/attachments", webHandler.WikiAttachmentCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/attachments/{attachment}/metadata", webHandler.WikiAttachmentMetadata)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/attachments/{attachment}/delete", webHandler.WikiAttachmentDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/comments", webHandler.WikiCommentCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/inline-comments", webHandler.WikiInlineCommentCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/inline-comments/{comment}", webHandler.WikiInlineCommentUpdate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/inline-comments/{comment}/delete", webHandler.WikiInlineCommentDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/tasks", webHandler.WikiTaskCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/tasks/{task}", webHandler.WikiTaskUpdate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/comments/{comment}", webHandler.WikiCommentUpdate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/comments/{comment}/delete", webHandler.WikiCommentDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/comments/{comment}/like", webHandler.WikiCommentLike)
	confluenceHandler := &confluence.Handler{Store: st, Commands: api.Commands, Blobs: blobs, WorkspaceSlug: workspaceSlug, BaseURL: api.BaseURL}
	mux.Handle("/wiki/api/v2/", confluenceHandler)
	mux.Handle("/wiki/rest/api/", &confluence.V1Handler{Handler: confluenceHandler})
	mux.Handle("/wiki/download/attachments/", &confluence.DownloadHandler{Handler: confluenceHandler})
	mux.Handle("/wiki/download/thumbnails/", &confluence.ThumbnailHandler{Handler: confluenceHandler})
	mux.HandleFunc("GET /projects/{key}/releases", webHandler.Releases)
	mux.HandleFunc("POST /projects/{key}/releases", webHandler.Releases)
	mux.HandleFunc("GET /projects/{key}/releases/{version}", webHandler.Release)
	mux.HandleFunc("POST /projects/{key}/releases/{version}", webHandler.Release)
	mux.HandleFunc("POST /projects/{key}/boards", webHandler.ProjectBoards)
	mux.HandleFunc("GET /projects/{key}/reports", webHandler.ProjectReports)
	mux.HandleFunc("GET /projects/{key}/reports/apps/{module}", webHandler.ProjectAppReport)
	mux.HandleFunc("GET /projects/{key}/reports/dora", webHandler.DORAReport)
	mux.HandleFunc("GET /projects/{key}/reports/sprint", webHandler.SprintReport)
	mux.HandleFunc("GET /projects/{key}/timeline", webHandler.ProjectTimeline)
	mux.HandleFunc("POST /projects/{key}/timeline", webHandler.ScheduleTimelineItem)
	mux.HandleFunc("GET /projects/{key}/reports/velocity", webHandler.VelocityReport)
	mux.HandleFunc("GET /projects/{key}/reports/cumulative-flow", webHandler.CumulativeFlowReport)
	mux.HandleFunc("GET /projects/{key}/reports/control-chart", webHandler.ControlChartReport)
	mux.HandleFunc("GET /projects/{key}/reports/epic", webHandler.EpicReport)
	mux.HandleFunc("GET /projects/{key}/reports/version", webHandler.VersionReport)
	mux.HandleFunc("GET /projects/{key}/reports/created-vs-resolved", webHandler.CreatedVsResolvedReport)
	mux.HandleFunc("GET /projects/{key}/reports/resolution-time", webHandler.ResolutionTimeReport)
	mux.HandleFunc("GET /projects/{key}/reports/epic-burndown", webHandler.EpicBurndownReport)
	mux.HandleFunc("GET /projects/{key}/reports/release-burndown", webHandler.ReleaseBurndownReport)
	mux.HandleFunc("GET /projects/{key}/reports/user-workload", webHandler.UserWorkloadReport)
	mux.HandleFunc("GET /projects/{key}/reports/version-workload", webHandler.VersionWorkloadReport)
	mux.HandleFunc("GET /projects/{key}/reports/time-tracking", webHandler.TimeTrackingReport)
	mux.HandleFunc("GET /projects/{key}/reports/group-by", webHandler.SingleLevelGroupByReport)
	mux.HandleFunc("POST /reports/email", webHandler.ReportEmail)
	mux.HandleFunc("GET /projects/new", webHandler.NewProject)
	mux.HandleFunc("POST /projects/new", webHandler.NewProject)
	mux.HandleFunc("GET /projects/{key}/settings", webHandler.ProjectSettings)
	mux.HandleFunc("POST /projects/{key}/settings", webHandler.ProjectSettings)
	mux.HandleFunc("POST /projects/{key}/settings/governance", webHandler.ProjectGovernanceSettings)
	mux.HandleFunc("POST /projects/{key}/settings/templates", webHandler.ProjectTemplateSettings)
	mux.HandleFunc("POST /projects/{key}/lifecycle", webHandler.ProjectLifecycleSettings)
	mux.HandleFunc("POST /projects/{key}/components", webHandler.ProjectComponentSettings)
	mux.HandleFunc("POST /projects/{key}/components/{id}", webHandler.ProjectComponentSettings)
	mux.HandleFunc("GET /projects/{key}/settings/roles", webHandler.ProjectRoleAssignmentsPage)
	mux.HandleFunc("POST /projects/{key}/settings/roles/{id}", webHandler.ProjectRoleAssignmentMutation)
	mux.HandleFunc("GET /projects/{key}/settings/permissions", webHandler.ProjectPermissionsPage)
	mux.HandleFunc("GET /projects/{key}/settings/notifications", webHandler.ProjectNotificationsPage)
	mux.HandleFunc("GET /projects/{key}/settings/issue-security", webHandler.ProjectIssueSecurityPage)
	mux.HandleFunc("GET /projects/{key}/settings/apps/{module}", webHandler.ProjectAdminAppModulePage)
	mux.HandleFunc("GET /projects/{key}/apps/{module}", webHandler.ProjectAppModulePage)
	mux.HandleFunc("GET /projects/{key}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.ProjectOverview(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("GET /people", webHandler.PeoplePage)
	mux.HandleFunc("GET /plans", webHandler.PlansPage)
	mux.HandleFunc("GET /plans/{id}", webHandler.PlanPage)
	mux.HandleFunc("POST /plans/{id}/work", webHandler.PlanWorkChange)
	mux.HandleFunc("POST /plans/{id}/scenarios", webHandler.PlanScenarioChange)
	mux.HandleFunc("POST /plans/{id}/capacity", webHandler.PlanCapacityChange)
	mux.HandleFunc("POST /plans/{id}/settings", webHandler.PlanSettingsChange)
	mux.HandleFunc("POST /plans/{id}/teams", webHandler.PlanTeamSettings)
	mux.HandleFunc("GET /plans/{id}/review", webHandler.PlanReview)
	mux.HandleFunc("POST /plans/{id}/review", webHandler.PlanReview)
	mux.HandleFunc("GET /teams", webHandler.TeamsPage)
	mux.HandleFunc("POST /teams", webHandler.TeamsPage)
	mux.HandleFunc("GET /teams/{id}", webHandler.TeamPage)
	mux.HandleFunc("POST /teams/{id}", webHandler.TeamPage)
	mux.HandleFunc("GET /service-registry", webHandler.ServiceRegistryPage)
	mux.HandleFunc("POST /service-registry", webHandler.ServiceRegistryPage)
	mux.HandleFunc("POST /service-registry/{id}", webHandler.ServiceRegistryPage)
	mux.HandleFunc("GET /profile", webHandler.SelfProfile)
	mux.HandleFunc("POST /profile/identities/{provider}/unlink", webHandler.UnlinkIdentityProvider)
	mux.HandleFunc("POST /profile/notifications", webHandler.UpdateNotificationPreferences)
	mux.HandleFunc("POST /profile/tokens", webHandler.CreateAPIToken)
	mux.HandleFunc("POST /profile/tokens/{token}/revoke", webHandler.RevokeAPIToken)
	mux.HandleFunc("GET /people/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.ProfilePage(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /settings/workflows", webHandler.WorkflowsPage)
	mux.HandleFunc("GET /settings/project-roles", webHandler.ProjectRolesAdminPage)
	mux.HandleFunc("POST /settings/project-roles", webHandler.ProjectRolesAdminPage)
	mux.HandleFunc("POST /settings/project-roles/{id}", webHandler.ProjectRoleAdminMutation)
	mux.HandleFunc("GET /settings/permission-schemes", webHandler.PermissionSchemesPage)
	mux.HandleFunc("POST /settings/permission-schemes", webHandler.PermissionSchemesPage)
	mux.HandleFunc("POST /settings/permission-schemes/{id}", webHandler.PermissionSchemeMutation)
	mux.HandleFunc("GET /settings/notification-schemes", webHandler.NotificationSchemesPage)
	mux.HandleFunc("POST /settings/notification-schemes", webHandler.NotificationSchemesPage)
	mux.HandleFunc("POST /settings/notification-schemes/{id}", webHandler.NotificationSchemeMutation)
	mux.HandleFunc("GET /settings/custom-fields", webHandler.CustomFieldsPage)
	mux.HandleFunc("POST /settings/custom-fields", webHandler.CustomFieldMutation)
	mux.HandleFunc("POST /settings/custom-fields/{id}", webHandler.CustomFieldContextMutation)
	mux.HandleFunc("GET /settings/field-configurations", webHandler.FieldConfigurationsPage)
	mux.HandleFunc("POST /settings/field-configurations", webHandler.FieldConfigurationsPage)
	mux.HandleFunc("POST /settings/field-configurations/{id}", webHandler.FieldConfigurationMutation)
	mux.HandleFunc("GET /settings/screen-schemes", webHandler.ScreenSchemesPage)
	mux.HandleFunc("POST /settings/screen-schemes", webHandler.ScreenSchemesPage)
	mux.HandleFunc("POST /settings/screen-schemes/{id}", webHandler.ScreenSchemeMutation)
	mux.HandleFunc("GET /settings/screens", webHandler.ScreensPage)
	mux.HandleFunc("POST /settings/screens", webHandler.ScreensPage)
	mux.HandleFunc("POST /settings/screens/{id}", webHandler.ScreenMutation)
	mux.HandleFunc("GET /settings/issue-security-schemes", webHandler.IssueSecuritySchemesPage)
	mux.HandleFunc("POST /settings/issue-security-schemes", webHandler.IssueSecuritySchemesPage)
	mux.HandleFunc("POST /settings/issue-security-schemes/{id}", webHandler.IssueSecuritySchemeMutation)
	mux.HandleFunc("GET /settings/statuses", webHandler.StatusesPage)
	mux.HandleFunc("GET /settings/hierarchy", webHandler.HierarchyPage)
	mux.HandleFunc("GET /settings/work-types", webHandler.WorkTypesPage)
	mux.HandleFunc("POST /settings/work-types", webHandler.WorkTypesMutation)
	mux.HandleFunc("GET /settings/priorities", webHandler.PrioritiesPage)
	mux.HandleFunc("POST /settings/priorities", webHandler.PrioritiesMutation)
	mux.HandleFunc("GET /settings/resolutions", webHandler.ResolutionsPage)
	mux.HandleFunc("POST /settings/resolutions", webHandler.ResolutionsMutation)
	mux.HandleFunc("POST /settings/hierarchy", webHandler.HierarchyMutation)
	mux.HandleFunc("GET /settings/workflow-schemes", webHandler.WorkflowSchemesPage)
	mux.HandleFunc("POST /settings/workflow-schemes", webHandler.CreateWorkflowScheme)
	mux.HandleFunc("GET /settings/workflow-schemes/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.WorkflowSchemePage(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflow-schemes/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SaveWorkflowSchemeDraft(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflow-schemes/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		webHandler.FinishWorkflowSchemeDraft(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflow-schemes/{id}/copy", func(w http.ResponseWriter, r *http.Request) {
		webHandler.CopyWorkflowScheme(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflow-schemes/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteWorkflowScheme(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflow-schemes/{id}/projects", func(w http.ResponseWriter, r *http.Request) {
		webHandler.AssignWorkflowScheme(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/statuses", webHandler.CreateStatus)
	mux.HandleFunc("POST /settings/statuses/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.UpdateStatus(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/statuses/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteStatus(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflows", webHandler.CreateWorkflow)
	mux.HandleFunc("GET /settings/workflows/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.WorkflowPage(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflows/{id}/transitions", func(w http.ResponseWriter, r *http.Request) {
		webHandler.AddWorkflowTransition(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflows/{id}/transitions/{transition}/delete", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteWorkflowTransition(w, r, r.PathValue("id"), r.PathValue("transition"))
	})
	mux.HandleFunc("POST /settings/workflows/{id}/statuses/{status}/approval", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SaveWorkflowStatusApproval(w, r, r.PathValue("id"), r.PathValue("status"))
	})
	mux.HandleFunc("POST /settings/workflows/{id}/statuses/{status}/editable", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SaveWorkflowStatusEditable(w, r, r.PathValue("id"), r.PathValue("status"))
	})
	mux.HandleFunc("POST /settings/workflows/{id}/layout", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SaveWorkflowLayout(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflows/{id}/projects", func(w http.ResponseWriter, r *http.Request) {
		webHandler.AssignProjectWorkflow(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /settings/workflows/{id}/draft", func(w http.ResponseWriter, r *http.Request) {
		webHandler.FinishWorkflowDraft(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /settings/automation", webHandler.AutomationRules)
	mux.HandleFunc("POST /settings/automation", webHandler.AutomationCreate)
	mux.HandleFunc("GET /settings/automation/new", webHandler.AutomationNew)
	mux.HandleFunc("GET /settings/automation/templates", webHandler.AutomationTemplates)
	mux.HandleFunc("POST /settings/automation/templates", webHandler.AutomationCreateFromTemplate)
	mux.HandleFunc("GET /settings/automation/{uuid}", webHandler.AutomationRule)
	mux.HandleFunc("POST /settings/automation/{uuid}", webHandler.AutomationUpdate)
	mux.HandleFunc("GET /issues/new", webHandler.CreateDialog)
	mux.HandleFunc("POST /issues", webHandler.CreateIssue)
	mux.HandleFunc("GET /filters", webHandler.SavedFilters)
	mux.HandleFunc("POST /filters/default-scope", webHandler.SavedFilterDefaultScope)
	mux.HandleFunc("POST /filters/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.UpdateSavedFilter(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /issues/{key}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.ProjectIssues(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/bulk/delete", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SubmitBulkIssueDelete(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/bulk/move", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SubmitBulkIssueMove(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/bulk/edit", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SubmitBulkIssueEdit(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/bulk/watch", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SubmitBulkIssueWatch(w, r, r.PathValue("key"), true)
	})
	mux.HandleFunc("POST /issues/{key}/bulk/unwatch", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SubmitBulkIssueWatch(w, r, r.PathValue("key"), false)
	})
	mux.HandleFunc("POST /issues/{key}/bulk/transition", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SubmitBulkIssueTransition(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("GET /issues/{key}/bulk/{task}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.BulkIssueTask(w, r, r.PathValue("key"), r.PathValue("task"))
	})
	mux.HandleFunc("POST /issues/{key}/filters", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SaveNavigatorFilter(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("GET /browse/{key}/preview", func(w http.ResponseWriter, r *http.Request) {
		webHandler.IssuePreview(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("GET /browse/{key}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.BrowseIssue(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/transition", func(w http.ResponseWriter, r *http.Request) {
		webHandler.TransitionIssue(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/comments", func(w http.ResponseWriter, r *http.Request) {
		webHandler.AddComment(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/attachments", func(w http.ResponseWriter, r *http.Request) {
		webHandler.UploadAttachment(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/forms", func(w http.ResponseWriter, r *http.Request) {
		webHandler.AttachIssueForm(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/forms/{form}/action/{action}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.UpdateIssueForm(w, r, r.PathValue("key"), r.PathValue("form"), r.PathValue("action"))
	})
	mux.HandleFunc("POST /issues/{key}/timetracking", func(w http.ResponseWriter, r *http.Request) {
		webHandler.EditTimeTracking(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/worklogs", func(w http.ResponseWriter, r *http.Request) {
		webHandler.AddWorklog(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("DELETE /issues/{key}/worklogs/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteWorklog(w, r, r.PathValue("key"), r.PathValue("id"))
	})
	mux.HandleFunc("POST /issues/{key}/worklogs/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteWorklog(w, r, r.PathValue("key"), r.PathValue("id"))
	})
	mux.HandleFunc("DELETE /issues/{key}/attachments/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteAttachment(w, r, r.PathValue("key"), r.PathValue("id"))
	})
	mux.HandleFunc("POST /issues/{key}/attachments/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteAttachment(w, r, r.PathValue("key"), r.PathValue("id"))
	})
	mux.HandleFunc("POST /issues/{key}/fields", func(w http.ResponseWriter, r *http.Request) {
		webHandler.UpdateIssueField(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/app-content/{module}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SetIssueAppContent(w, r, r.PathValue("key"), r.PathValue("module"))
	})
	mux.HandleFunc("POST /issues/{key}/watch", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SetWatching(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/vote", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SetVoting(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/links", func(w http.ResponseWriter, r *http.Request) {
		webHandler.LinkIssue(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("DELETE /issues/{key}/links/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteIssueLink(w, r, r.PathValue("key"), r.PathValue("id"))
	})
	mux.HandleFunc("POST /issues/{key}/links/{id}/delete", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteIssueLink(w, r, r.PathValue("key"), r.PathValue("id"))
	})
	mux.HandleFunc("GET /issues/{key}/edit", func(w http.ResponseWriter, r *http.Request) {
		webHandler.EditDialog(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("POST /issues/{key}/edit", func(w http.ResponseWriter, r *http.Request) {
		webHandler.EditIssue(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("DELETE /issues/{key}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.DeleteIssue(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("GET /dashboards", webHandler.CustomDashboards)
	mux.HandleFunc("POST /dashboards", webHandler.CustomDashboards)
	mux.HandleFunc("GET /dashboards/{id}", webHandler.CustomDashboard)
	mux.HandleFunc("POST /dashboards/{id}", webHandler.CustomDashboard)
	mux.HandleFunc("GET /dashboards/{id}/content", webHandler.CustomDashboard)
	mux.HandleFunc("GET /dashboards/{id}/wallboard", webHandler.DashboardWallboard)
	mux.HandleFunc("GET /dashboards/slideshow", webHandler.DashboardWallboardSlideshow)
	mux.HandleFunc("GET /dashboard", webHandler.DashboardPage)
	mux.HandleFunc("GET /notifications", webHandler.NotificationsPage)
	mux.HandleFunc("POST /notifications/read-all", webHandler.MarkAllNotificationsReadPage)
	mux.HandleFunc("POST /notifications/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SetNotificationReadPage(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /notifications/{id}/open", func(w http.ResponseWriter, r *http.Request) {
		webHandler.OpenNotification(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /board/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.BoardPage(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /board/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		webHandler.BoardSettingsPage(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /board/{id}/settings", func(w http.ResponseWriter, r *http.Request) {
		webHandler.UpdateBoardSettings(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /board/{id}/settings/admins", func(w http.ResponseWriter, r *http.Request) {
		webHandler.AddBoardAdministrator(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /board/{id}/settings/admins/{admin}/remove", func(w http.ResponseWriter, r *http.Request) {
		webHandler.RemoveBoardAdministrator(w, r, r.PathValue("id"), r.PathValue("admin"))
	})
	mux.HandleFunc("GET /board/{id}/backlog", func(w http.ResponseWriter, r *http.Request) {
		webHandler.BacklogPage(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /board/{id}/backlog/sprints", func(w http.ResponseWriter, r *http.Request) {
		webHandler.CreateBacklogSprint(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /board/{id}/backlog/sprints/{sprint}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.UpdateBacklogSprint(w, r, r.PathValue("id"), r.PathValue("sprint"))
	})
	mux.HandleFunc("POST /board/{id}/backlog/move", func(w http.ResponseWriter, r *http.Request) {
		webHandler.MoveBacklogIssue(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /board/{id}/fragment", func(w http.ResponseWriter, r *http.Request) {
		webHandler.BoardFragment(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /board/{id}/rank", func(w http.ResponseWriter, r *http.Request) {
		webHandler.RankIssue(w, r, r.PathValue("id"))
	})
	mux.Handle("GET /sync", sync)
	mux.HandleFunc("GET /bootstrap", api.BootstrapHandler)
	mux.Handle("GET /sse", sse)
	mux.HandleFunc("GET /rest/zzira/1/notifications", api.NotificationsHandler)
	mux.HandleFunc("PUT /rest/zzira/1/notifications/{id}", func(w http.ResponseWriter, r *http.Request) {
		api.NotificationHandler(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("POST /rest/zzira/1/notifications/read-all", api.MarkAllNotificationsReadHandler)
	mux.HandleFunc("POST /rest/zzira/1/product-activity", webHandler.RecordProductActivity)
	mux.Handle("/rest/agile/1.0/", agileAPI)
	mux.Handle("/rest/software/1.0/", agileAPI)
	mux.Handle("/rest/api/3/", api)
	// Attachment content and thumbnail downloads that the attachment
	// operations redirect to.
	mux.Handle("/secure/attachment/", api)
	mux.Handle("/secure/thumbnail/", api)
	for _, prefix := range []string{"/rest/webhooks/1.0/", "/rest/atlassian-connect/1/addons/", "/rest/atlassian-connect/1/migration/", "/rest/atlassian-connect/1/service-registry", "/rest/forge/1/app/properties", "/rest/forge/1/app/properties/", "/rest/internal/api/latest/worklog/bulk"} {
		mux.Handle(prefix, api)
	}
	mux.Handle("/rest/servicedeskapi/", api)
	mux.Handle("/jira/forms/cloud/", api)
	mux.Handle("/rest/devinfo/0.10/", api)
	mux.Handle("/jira/devinfo/0.1/cloud/", api)
	mux.Handle("/rest/builds/0.1/", api)
	mux.Handle("/jira/builds/0.1/cloud/", api)
	mux.Handle("/rest/deployments/0.1/", api)
	for _, prefix := range []string{"/rest/operations/1.0/", "/rest/security/1.0/", "/rest/devopscomponents/1.0/", "/rest/featureflags/0.1/", "/rest/remotelinks/1.0/"} {
		mux.Handle(prefix, api)
	}
	mux.Handle("/jira/deployments/0.1/cloud/", api)
	mux.HandleFunc("GET /_edge/tenant_info", automationAPI.TenantInfo)
	mux.HandleFunc("POST /pro/hooks/{token}", func(w http.ResponseWriter, r *http.Request) {
		automationAPI.IncomingWebhook(w, r, r.PathValue("token"))
	})
	mux.Handle("/gateway/api/automation/public/jira/", automationAPI)
	mux.Handle("/automation/public/jira/", automationAPI)
	mux.HandleFunc("GET /admin/v1/orgs", adminAPI.Organizations)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}", adminAPI.Organization)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories", adminAPI.Directories)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/groups", adminAPI.Groups)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups", adminAPI.Groups)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/count", adminAPI.GroupCount)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/search", adminAPI.SearchGroups)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/stats", adminAPI.GroupStats)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}", adminAPI.GroupDetails)
	mux.HandleFunc("DELETE /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}", adminAPI.GroupDetails)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/memberships", adminAPI.GroupMembership)
	mux.HandleFunc("DELETE /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/memberships/{accountId}", adminAPI.DeleteGroupMembership)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/workspaces", adminAPI.Workspaces)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/users/{userId}/role-assignments/assign", adminAPI.UserRoleMutation)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/users/{userId}/role-assignments/revoke", adminAPI.UserRoleMutation)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/users/{userId}/roles/assign", adminAPI.UserRoleMutation)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/users/{userId}/roles/revoke", adminAPI.UserRoleMutation)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/role-assignments", adminAPI.RoleAssignments)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/role-assignments/assign", adminAPI.GroupRoleMutation)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/groups/{groupId}/role-assignments/revoke", adminAPI.GroupRoleMutation)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}/role-assignments", adminAPI.RoleAssignments)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/users", adminAPI.ManagedUsers)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/directory/users/{accountId}/last-active-dates", adminAPI.UserLastActiveDates)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/events", adminAPI.Events)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/events-stream", adminAPI.Events)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/events/{eventId}", adminAPI.EventDetails)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/event-actions", adminAPI.EventActions)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/domains", adminAPI.Domains)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/domains/{domainId}", adminAPI.DomainDetails)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/policies", adminAPI.Policies)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/policies", adminAPI.Policies)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/policies/{policyId}", adminAPI.PolicyDetails)
	mux.HandleFunc("PUT /admin/v1/orgs/{orgId}/policies/{policyId}", adminAPI.PolicyDetails)
	mux.HandleFunc("DELETE /admin/v1/orgs/{orgId}/policies/{policyId}", adminAPI.PolicyDetails)
	mux.HandleFunc("POST /admin/v1/orgs/{orgId}/policies/{policyId}/resources", adminAPI.PolicyResources)
	mux.HandleFunc("PUT /admin/v1/orgs/{orgId}/policies/{policyId}/resources/{resourceId}", adminAPI.PolicyResourceDetails)
	mux.HandleFunc("DELETE /admin/v1/orgs/{orgId}/policies/{policyId}/resources/{resourceId}", adminAPI.PolicyResourceDetails)
	mux.HandleFunc("GET /admin/v1/orgs/{orgId}/policies/{policyId}/validate", adminAPI.ValidatePolicy)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users", adminAPI.DirectoryUsers)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users/count", adminAPI.DirectoryUserCount)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/users/search", adminAPI.SearchUsers)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users/stats", adminAPI.UserStats)
	mux.HandleFunc("GET /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{userId}", adminAPI.DirectoryUserDetails)
	mux.HandleFunc("DELETE /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}", adminAPI.DirectoryUserLifecycle)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}/suspend", adminAPI.DirectoryUserLifecycle)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/directories/{directoryId}/users/{accountId}/restore", adminAPI.DirectoryUserLifecycle)
	mux.HandleFunc("POST /admin/v2/orgs/{orgId}/users/invite", adminAPI.InviteUsers)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /apps/modules/{module}", webHandler.AppModulePage)
	mux.HandleFunc("GET /app-modules/{module}/frame", webHandler.AppModuleFrame)
	mux.HandleFunc("POST /app-modules/{module}/request", webHandler.AppModuleRequest)
	mux.HandleFunc("GET /app-modules/{module}/thumbnail", webHandler.AppModuleThumbnail)
	mux.HandleFunc("GET /app-modules/{module}/icon", webHandler.AppModuleIcon)
	mux.HandleFunc("GET /app-modules/{module}/status-icon", webHandler.AppModuleStatusIcon)
	mux.HandleFunc("POST /apps/{appKey}/lifecycle/{event}", appAPI.Lifecycle)
	mux.HandleFunc("GET /apps/{appKey}/storage/{key}", appAPI.Storage)
	mux.HandleFunc("PUT /apps/{appKey}/storage/{key}", appAPI.Storage)
	mux.HandleFunc("DELETE /apps/{appKey}/storage/{key}", appAPI.Storage)
	mux.HandleFunc("GET /rest/atlassian-connect/1/app/module/dynamic", appAPI.DynamicModules)
	mux.HandleFunc("POST /rest/atlassian-connect/1/app/module/dynamic", appAPI.DynamicModules)
	mux.HandleFunc("DELETE /rest/atlassian-connect/1/app/module/dynamic", appAPI.DynamicModules)
	mux.HandleFunc("GET /wiki/rest/atlassian-connect/1/app/module/dynamic", appAPI.DynamicModules)
	mux.HandleFunc("POST /wiki/rest/atlassian-connect/1/app/module/dynamic", appAPI.DynamicModules)
	mux.HandleFunc("DELETE /wiki/rest/atlassian-connect/1/app/module/dynamic", appAPI.DynamicModules)
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(static))))
	// Jira names priority icons by /images/icons/priorities/<name>.png or
	// .svg; both extensions resolve to the same icon, served as SVG.
	mux.HandleFunc("GET /images/icons/priorities/{file}", func(w http.ResponseWriter, r *http.Request) {
		// Only Jira's fixed icon names resolve, so no part of the request
		// reaches the file path.
		var icon string
		switch strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(r.PathValue("file"), ".png"), ".svg"), "_new") {
		case "highest":
			icon = "highest.svg"
		case "high":
			icon = "high.svg"
		case "medium":
			icon = "medium.svg"
		case "low":
			icon = "low.svg"
		case "lowest":
			icon = "lowest.svg"
		case "blocker":
			icon = "blocker.svg"
		case "critical":
			icon = "critical.svg"
		case "major":
			icon = "major.svg"
		case "minor":
			icon = "minor.svg"
		case "trivial":
			icon = "trivial.svg"
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, filepath.Join(static, "img", "priorities", icon))
	})
	mux.HandleFunc("GET /secure/archived-issues-export/{file}", api.ArchivedIssuesExportFile)
	mux.HandleFunc("GET /atlassian-connect/all.js", func(w http.ResponseWriter, r *http.Request) {
		// Connect apps load the JavaScript API from the product they run in.
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=300")
		http.ServeFile(w, r, filepath.Join(static, "atlassian-connect", "all.js"))
	})
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		// Root scope is required for the service worker to control page navigations.
		w.Header().Set("Service-Worker-Allowed", "/")
		http.ServeFile(w, r, filepath.Join(static, "sw.js"))
	})

	// Scheduled report emails draw each report through these routes, as its
	// recipient, so an email always matches the report's CSV download; app
	// frames' AP.request calls are served through them too.
	webHandler.Routes = mux
	go (&store.ReportSubscriptionRunner{Store: st, BaseURL: baseURL, Render: webHandler.RenderReport}).Run(ctx, workspaceID)

	handler := appAPI.APIPrincipal(store.RequestMetadataHandler(st.IPAllowlistHandler(workspaceSlug, mux)))
	if !localCredentials {
		// Outside every route, so no entry point can authenticate a password
		// or an API token: the browser's sign-in form, the REST APIs, the
		// sync and event streams and the organization administration API all
		// resolve their caller below this.
		handler = authn.RefuseLocalCredentials(handler)
	}
	srv := &http.Server{
		Addr:              address,
		Handler:           http.MaxBytesHandler(authn.SecurityHeadersDynamic(authn.ProtectCookieMutations(handler), identityProviders.FormActionOrigins), 34<<20),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	fmt.Printf("%s %s listening on %s\n", build.Product, build.Version, address)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("server shutdown: %v", err)
		}
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("serve: %v", err)
	}
}

// ensureBootstrapAdmin grants ZZIRA_BOOTSTRAP_ADMIN_EMAIL admin membership,
// matching the pattern deployed apps in this ecosystem already use for their
// identity provider's break-glass account. A first OIDC sign-in provisions
// only an ordinary member (ResolveOIDCUser), so this is still how the
// break-glass account -- or any account that needs admin -- gets the admin
// role. Runs on every boot; idempotent past the first.
func ensureBootstrapAdmin(ctx context.Context, st *store.Store, email string) error {
	hash, err := authn.UnusablePasswordHash()
	if err != nil {
		return err
	}
	return st.EnsureBootstrapAdmin(ctx, email, "Bootstrap Admin", hash, "admin")
}

// seedUsers creates the demo users (idempotent per user) and prints a fresh
// API token for each missing token holder.
func seedUsers(ctx context.Context, st *store.Store) error {
	wsID, _, err := st.DefaultWorkspace(ctx)
	if err != nil {
		return err
	}
	tokens := map[string]string{}
	for _, su := range []struct {
		email, password, displayName, role string
	}{
		{"demo@zzira.dev", "demo1234", "Demo User", "admin"},
		{"ana@zzira.dev", "ana12345", "Ana Soursop", "member"},
	} {
		userID, err := ensureUser(ctx, st, wsID, su.email, su.password, su.displayName, su.role)
		if err != nil {
			return err
		}
		plain, apiHash, err := authn.NewAPIToken()
		if err != nil {
			return err
		}
		if err := st.CreateAPIToken(ctx, store.NewID("tok"), userID, apiHash, "seed"); err != nil {
			return err
		}
		// Credentials go to the gitignored data/ file only — never to stdout.
		tokens[su.email] = plain
		tokens[su.email+".password"] = su.password
		fmt.Printf("seeded %s\n", su.email)
	}
	// Dev convenience: e2e tests authenticate with these. Local artifact only.
	if len(tokens) > 0 {
		seedDir := envOr("DATA_DIR", "data")
		if err := os.MkdirAll(seedDir, 0o700); err != nil {
			return err
		}
		f, err := json.MarshalIndent(tokens, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(seedDir, "seed-tokens.json"), f, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func ensureUser(ctx context.Context, st *store.Store, wsID, email, password, displayName, role string) (string, error) {
	if id, _, _, err := st.UserByEmail(ctx, email); err == nil {
		if err := st.AddMember(ctx, wsID, id, role); err != nil {
			return "", err
		}
		return id, nil
	}
	hash, err := authn.HashPassword(password)
	if err != nil {
		return "", err
	}
	u, err := st.CreateUser(ctx, store.NewID("usr"), email, hash, displayName)
	if err != nil {
		return "", err
	}
	if err := st.AddMember(ctx, wsID, u.ID, role); err != nil {
		return "", err
	}
	return u.ID, nil
}

// blobDir is the one place that decides where attachment bytes live. The
// server and the demo seeder resolve it through this function so they cannot
// disagree: the seeder used to read a BLOB_DIR that nothing else set, and so
// on a deployment that sets only DATA_DIR it wrote the scenario's attachments
// to a relative data/blobs the server never read -- and on the scratch image,
// where the working directory is not writable, could not create at all.
func blobDir(getenv func(string) string) string {
	if dir := getenv("DATA_DIR"); dir != "" {
		return dir
	}
	return "data/attachments"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// servingWorkspaceSlug reads the one workspace an application instance may
// serve. Control characters are rejected before the value reaches a log sink
// or is used to select tenant data.
func servingWorkspaceSlug(getenv func(string) string) (string, error) {
	slug := getenv("WORKSPACE_SLUG")
	if slug == "" {
		return "", errors.New("WORKSPACE_SLUG must name the workspace served by this instance")
	}
	for _, r := range slug {
		if unicode.IsControl(r) {
			return "", errors.New("WORKSPACE_SLUG contains control characters")
		}
	}
	return slug, nil
}

// demoWorkspaceSlug names the workspace -mode=demo applies a scenario to.
//
// An instance serves exactly one workspace, the one WORKSPACE_SLUG names, so
// seeding a deployment with the scenario's own slug built a company the server
// would never show. The -workspace flag wins, then WORKSPACE_SLUG, then the
// scenario's slug: the same order -static and -addr use, where the flag is the
// operator's one-off override of the deployment's environment. Only the slug
// is taken; the site keeps the scenario's display name.
func demoWorkspaceSlug(flagValue string, getenv func(string) string, scenario *demo.Scenario) string {
	if flagValue != "" {
		return flagValue
	}
	if slug := getenv("WORKSPACE_SLUG"); slug != "" {
		return slug
	}
	return scenario.Site.Slug
}

// applyDemoScenario builds a demo site from a declarative scenario and writes
// the credentials it created where the local tooling looks for them.
func applyDemoScenario(ctx context.Context, st *store.Store, path, workspaceFlag string, getenv func(string) string) error {
	// #nosec G304 -- the scenario is the operator's own -scenario flag on a
	// local command, like -static and -mode; it is read, never written, and no
	// request can reach it.
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scenario, err := demo.Read(file)
	if err != nil {
		return err
	}
	blobs, err := attachments.NewFS(blobDir(getenv))
	if err != nil {
		return err
	}
	slug := demoWorkspaceSlug(workspaceFlag, getenv, scenario)
	result, err := demo.Apply(ctx, st, &commands.Service{Store: st, Blobs: blobs}, scenario, demo.NewClock(time.Now().UTC()), slug)
	if err != nil {
		return err
	}
	credentials := map[string]any{"site": result.Slug, "passwords": result.Passwords, "tokens": result.Tokens}
	encoded, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return err
	}
	directory := envOr("DATA_DIR", "data")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "demo-credentials.json"), encoded, 0o600); err != nil {
		return err
	}
	fmt.Printf("built the %s demo site in the %s workspace: %d people, %d projects; credentials in %s\n",
		scenario.Name, result.Slug, len(scenario.People), len(scenario.Projects), filepath.Join(directory, "demo-credentials.json"))
	return nil
}
