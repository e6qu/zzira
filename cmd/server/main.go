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
	"syscall"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/e6qu/zzira/internal/agile"
	"github.com/e6qu/zzira/internal/api3"
	"github.com/e6qu/zzira/internal/attachments"
	"github.com/e6qu/zzira/internal/authn"
	"github.com/e6qu/zzira/internal/build"
	"github.com/e6qu/zzira/internal/commands"
	"github.com/e6qu/zzira/internal/jql"
	"github.com/e6qu/zzira/internal/notifybus"
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

	oidcSSO, err := web.NewOIDC(ctx)
	if err != nil {
		log.Fatalf("configure OIDC SSO: %v", err)
	}
	webHandler := &web.Handler{Store: st, Commands: cmdSvc, OIDC: oidcSSO, WorkspaceSlug: workspaceSlug}
	api := &api3.Handler{Store: st, Commands: cmdSvc, Blobs: blobs, BaseURL: envOr("BASE_URL", "http://localhost:"+port), WorkspaceSlug: workspaceSlug}
	agileAPI := &agile.Handler{Store: st, IssueBean: api.IssueBean, BaseURL: envOr("BASE_URL", "http://localhost:"+port), WorkspaceSlug: workspaceSlug}
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
	mux.HandleFunc("GET /auth/shauth", webHandler.OIDCLogin)
	mux.HandleFunc("GET /auth/shauth/callback", webHandler.OIDCCallback)
	mux.HandleFunc("GET /auth/shauth/logout/complete", webHandler.OIDCLogoutComplete)
	mux.HandleFunc("GET /auth/validation", webHandler.Validation)
	mux.HandleFunc("GET /monitoring/observation", webHandler.Monitoring)
	mux.HandleFunc("POST /auth/shauth/backchannel-logout", webHandler.BackChannelLogout)
	mux.HandleFunc("POST /login", webHandler.LoginSubmit)
	mux.HandleFunc("POST /logout", webHandler.Logout)
	mux.HandleFunc("GET /signed-out", webHandler.SignedOut)
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
	mux.HandleFunc("POST /issues/{key}/worklogs", func(w http.ResponseWriter, r *http.Request) {
		webHandler.AddWorklog(w, r, r.PathValue("key"))
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
	mux.HandleFunc("GET /dashboard", webHandler.DashboardPage)
	mux.HandleFunc("GET /board/{id}", func(w http.ResponseWriter, r *http.Request) {
		webHandler.BoardPage(w, r, r.PathValue("id"))
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
	mux.Handle("/rest/agile/1.0/", agileAPI)
	mux.Handle("/rest/api/3/", api)
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
		Handler:           http.MaxBytesHandler(authn.SecurityHeaders(authn.ProtectCookieMutations(mux)), 34<<20),
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
		if err := os.MkdirAll("data", 0o700); err != nil {
			return err
		}
		f, err := json.MarshalIndent(tokens, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile("data/seed-tokens.json", f, 0o600); err != nil {
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
