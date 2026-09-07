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
	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/automation"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/confluence"
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
	mode := flag.String("mode", "run", "run|migrate|seed")
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
	if *mode == "migrate" {
		fmt.Println("migrations applied")
		return
	}
	if *mode == "seed" {
		if err := seedUsers(ctx, st); err != nil {
			log.Fatalf("seed: %v", err)
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
	blobs, err := attachments.NewFS(envOr("DATA_DIR", "data/attachments"))
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
	api := &api3.Handler{Store: st, Commands: cmdSvc, Blobs: blobs, BaseURL: baseURL, WorkspaceSlug: workspaceSlug}
	agileAPI := &agile.Handler{Store: st, Commands: cmdSvc, IssueBean: api.IssueBean, BaseURL: envOr("BASE_URL", "http://localhost:"+port), WorkspaceSlug: workspaceSlug}
	automationAPI := &automation.Handler{Service: automationSvc, WorkspaceSlug: workspaceSlug}
	adminAPI := &admin.Handler{
		Store: st, BaseURL: api.BaseURL, WorkspaceSlug: workspaceSlug,
		InvitationNotificationsConfigured: smtpSender != nil,
	}
	bus := notifybus.New()
	sse := &syncapi.SSEHandler{Store: st, Bus: bus, WorkspaceSlug: workspaceSlug}
	sync := &syncapi.Handler{Store: st, WorkspaceSlug: workspaceSlug}
	dispatcher := &webhooks.Dispatcher{
		Store:  st,
		Client: &http.Client{Timeout: 10 * time.Second},
		Checker: &webhooks.JQLChecker{Search: func(ctx context.Context, wsID, jqlText string) (bool, error) {
			// webhooks are admin-configured: evaluate JQL as a workspace admin
			adminID, err := st.FirstAdminID(ctx, wsID)
			if err != nil {
				return false, err
			}
			q, err := jql.Parse(jqlText)
			if err != nil {
				return false, err
			}
			compiled := jql.CompileAt(q, adminID, jql.DefaultResolver(), 1)
			if compiled.Err != nil {
				return false, compiled.Err
			}
			issues, _, err := st.Search(ctx, wsID, adminID, compiled, 1, 0)
			return err == nil && len(issues) > 0, nil
		}},
	}
	go dispatcher.Run(ctx, workspaceID)
	go (&automation.Runner{Service: automationSvc}).Run(ctx, workspaceID)
	go (&store.APITaskRunner{Store: st}).Run(ctx, workspaceID)
	go (&store.ServiceSLARunner{Store: st}).Run(ctx, workspaceID)
	go (&commands.ServiceTemporaryAttachmentRunner{Service: cmdSvc}).Run(ctx)
	if smtpSender != nil {
		go (&mailer.Runner{Store: st, Sender: smtpSender}).Run(ctx)
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
	mux.HandleFunc("POST /service/requests/{key}/notification", webHandler.ServiceRequestNotification)
	mux.HandleFunc("POST /service/requests/{key}/feedback", webHandler.ServiceRequestFeedback)
	mux.HandleFunc("POST /service/requests/{key}/transition", webHandler.ServiceRequestTransition)
	mux.HandleFunc("POST /service/requests/{key}/participants", webHandler.ServiceRequestParticipant)
	mux.HandleFunc("POST /service/requests/{key}/links", webHandler.ServiceRequestLink)
	mux.HandleFunc("POST /service/requests/{key}/links/{link}/delete", webHandler.ServiceRequestLinkDelete)
	mux.HandleFunc("GET /service/agent", webHandler.ServiceAgent)
	mux.HandleFunc("GET /service/agent/{desk}", webHandler.ServiceAgent)
	mux.HandleFunc("GET /service/agent/{desk}/reports", webHandler.ServiceReports)
	mux.HandleFunc("POST /service/agent/{desk}/agents", webHandler.ServiceAgentSettings)
	mux.HandleFunc("POST /service/agent/{desk}/queues", webHandler.ServiceQueueSettings)
	mux.HandleFunc("POST /service/agent/{desk}/request-types/{requestType}/fields", webHandler.ServiceRequestTypeFieldSettings)
	mux.HandleFunc("POST /service/agent/{desk}/customers", webHandler.ServiceCustomerSettings)
	mux.HandleFunc("POST /service/agent/{desk}/organizations", webHandler.ServiceOrganizationSettings)
	mux.HandleFunc("POST /service/agent/{desk}/knowledge", webHandler.ServiceKnowledgeSettings)
	mux.HandleFunc("POST /service/agent/{desk}/calendar", webHandler.ServiceCalendarSettings)
	mux.HandleFunc("POST /service/agent/{desk}/calendar/holidays", webHandler.ServiceCalendarHolidaySettings)
	mux.HandleFunc("POST /service/agent/{desk}/sla/{metric}", webHandler.ServiceSLASettings)
	mux.HandleFunc("POST /service/agent/{desk}/sla/{metric}/goals", webHandler.ServiceSLAGoalSettings)
	mux.HandleFunc("POST /service/agent/{desk}/requests/{key}/assign", webHandler.ServiceAgentAssign)
	mux.HandleFunc("GET /admin", webHandler.AdminPage)
	mux.HandleFunc("POST /admin/identity-providers/{provider}", webHandler.UpdateAdminIdentityProvider)
	mux.HandleFunc("POST /admin/identity-providers", webHandler.CreateAdminIdentityProvider)
	mux.HandleFunc("POST /admin/groups", webHandler.CreateAdminGroup)
	mux.HandleFunc("POST /admin/groups/{groupId}/delete", webHandler.DeleteAdminGroup)
	mux.HandleFunc("POST /admin/groups/{groupId}/members", webHandler.UpdateAdminGroupMember)
	mux.HandleFunc("POST /admin/groups/{groupId}/roles", webHandler.UpdateAdminGroupRole)
	mux.HandleFunc("POST /admin/users/invite", webHandler.InviteAdminUser)
	mux.HandleFunc("POST /admin/users/{accountId}", webHandler.UpdateAdminUserStatus)
	mux.HandleFunc("POST /admin/users/{accountId}/profile", webHandler.UpdateAdminUserProfile)
	mux.HandleFunc("POST /admin/domains", webHandler.CreateAdminDomain)
	mux.HandleFunc("POST /admin/domains/{domainId}", webHandler.UpdateAdminDomain)
	mux.HandleFunc("POST /admin/policies", webHandler.CreateAdminPolicy)
	mux.HandleFunc("POST /admin/policies/{policyId}", webHandler.UpdateAdminPolicy)
	mux.HandleFunc("GET /wiki", webHandler.WikiHome)
	mux.HandleFunc("POST /wiki/spaces", webHandler.WikiHome)
	mux.HandleFunc("GET /wiki/spaces/{space}", webHandler.WikiSpacePage)
	mux.HandleFunc("GET /wiki/spaces/{space}/pages/new", webHandler.WikiEdit)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/new", webHandler.WikiEdit)
	mux.HandleFunc("GET /wiki/spaces/{space}/pages/{page}", webHandler.WikiPage)
	mux.HandleFunc("GET /wiki/spaces/{space}/pages/{page}/edit", webHandler.WikiEdit)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/edit", webHandler.WikiEdit)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/trash", webHandler.WikiTrash)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/labels", webHandler.WikiPageLabels)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/comments", webHandler.WikiCommentCreate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/comments/{comment}", webHandler.WikiCommentUpdate)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/comments/{comment}/delete", webHandler.WikiCommentDelete)
	mux.HandleFunc("POST /wiki/spaces/{space}/pages/{page}/comments/{comment}/like", webHandler.WikiCommentLike)
	confluenceHandler := &confluence.Handler{Store: st, Commands: api.Commands, WorkspaceSlug: workspaceSlug, BaseURL: api.BaseURL}
	mux.Handle("/wiki/api/v2/", confluenceHandler)
	mux.Handle("/wiki/rest/api/", &confluence.V1Handler{Handler: confluenceHandler})
	mux.HandleFunc("GET /projects/{key}/releases", webHandler.Releases)
	mux.HandleFunc("POST /projects/{key}/releases", webHandler.Releases)
	mux.HandleFunc("GET /projects/{key}/releases/{version}", webHandler.Release)
	mux.HandleFunc("POST /projects/{key}/releases/{version}", webHandler.Release)
	mux.HandleFunc("GET /projects/{key}/reports/dora", webHandler.DORAReport)
	mux.HandleFunc("GET /projects/new", webHandler.NewProject)
	mux.HandleFunc("POST /projects/new", webHandler.NewProject)
	mux.HandleFunc("GET /projects/{key}/settings", webHandler.ProjectSettings)
	mux.HandleFunc("POST /projects/{key}/settings", webHandler.ProjectSettings)
	mux.HandleFunc("GET /projects/{key}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.ProjectOverview(w, r, r.PathValue("key"))
	})
	mux.HandleFunc("GET /people", webHandler.PeoplePage)
	mux.HandleFunc("GET /profile", webHandler.SelfProfile)
	mux.HandleFunc("POST /profile/identities/{provider}/unlink", webHandler.UnlinkIdentityProvider)
	mux.HandleFunc("GET /people/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.ProfilePage(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /settings/workflows", webHandler.WorkflowsPage)
	mux.HandleFunc("GET /settings/statuses", webHandler.StatusesPage)
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
	mux.HandleFunc("GET /settings/automation/{uuid}", webHandler.AutomationRule)
	mux.HandleFunc("POST /settings/automation/{uuid}", webHandler.AutomationUpdate)
	mux.HandleFunc("GET /issues/new", webHandler.CreateDialog)
	mux.HandleFunc("POST /issues", webHandler.CreateIssue)
	mux.HandleFunc("GET /issues/{key}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.ProjectIssues(w, r, r.PathValue("key"))
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
	mux.HandleFunc("POST /issues/{key}/watch", func(w http.ResponseWriter, r *http.Request) {
		webHandler.SetWatching(w, r, r.PathValue("key"))
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
	mux.Handle("/rest/api/3/", api)
	mux.Handle("/rest/servicedeskapi/", api)
	mux.Handle("/jira/forms/cloud/", api)
	mux.Handle("/rest/devinfo/0.10/", api)
	mux.Handle("/jira/devinfo/0.1/cloud/", api)
	mux.Handle("/rest/builds/0.1/", api)
	mux.Handle("/jira/builds/0.1/cloud/", api)
	mux.Handle("/rest/deployments/0.1/", api)
	mux.Handle("/jira/deployments/0.1/cloud/", api)
	mux.HandleFunc("GET /_edge/tenant_info", automationAPI.TenantInfo)
	mux.Handle("/gateway/api/automation/public/jira/", automationAPI)
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
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir(static))))
	mux.HandleFunc("GET /sw.js", func(w http.ResponseWriter, r *http.Request) {
		// Root scope is required for the service worker to control page navigations.
		w.Header().Set("Service-Worker-Allowed", "/")
		http.ServeFile(w, r, filepath.Join(static, "sw.js"))
	})

	srv := &http.Server{
		Addr:              address,
		Handler:           http.MaxBytesHandler(authn.SecurityHeadersDynamic(authn.ProtectCookieMutations(mux), identityProviders.FormActionOrigins), 34<<20),
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
