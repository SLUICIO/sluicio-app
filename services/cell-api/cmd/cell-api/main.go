// SPDX-License-Identifier: FSL-1.1-Apache-2.0
//
// Command cell-api is the cell's HTTP API. It exposes read endpoints
// backed by the cell's ClickHouse telemetry store and the cell's
// Postgres metadata (integration definitions, alert rules, etc.).
//
// v0.2 endpoints:
//
//	GET    /api/v1/services                              list of services
//	GET    /api/v1/services/{name}                       per-service stats
//	GET    /api/v1/search?q=...                          keyword search
//	GET    /api/v1/traces/{traceId}                      spans in a trace
//	GET    /api/v1/integrations                          list integrations
//	POST   /api/v1/integrations                          create
//	GET    /api/v1/integrations/{id}                     detail + services
//	PUT    /api/v1/integrations/{id}                     update
//	DELETE /api/v1/integrations/{id}                     delete
//	POST   /api/v1/integrations/{id}/matchers            add matcher
//	DELETE /api/v1/integrations/{id}/matchers/{mId}      remove matcher
//	GET    /api/v1/tags                                  list tags
//	POST   /api/v1/tags                                  create tag
//	GET    /api/v1/tags/{id}                             get tag
//	PATCH  /api/v1/tags/{id}                             update tag
//	DELETE /api/v1/tags/{id}                             delete tag
//	GET    /api/v1/integrations/{id}/tags                tags on integration
//	POST   /api/v1/integrations/{id}/tags/{tagId}        attach tag
//	DELETE /api/v1/integrations/{id}/tags/{tagId}        detach tag
//	GET    /api/v1/services/{name}/tags                  tags on service
//	POST   /api/v1/services/{name}/tags/{tagId}          attach tag
//	DELETE /api/v1/services/{name}/tags/{tagId}          detach tag
//	GET    /healthz                                      liveness
package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/integration-monitor/integration-monitor/ee/audit"
	pkgaudit "github.com/integration-monitor/integration-monitor/pkg/audit"
	imclickhouse "github.com/integration-monitor/integration-monitor/pkg/clickhouse"
	"github.com/integration-monitor/integration-monitor/pkg/env"
	"github.com/integration-monitor/integration-monitor/pkg/httpserver"
	"github.com/integration-monitor/integration-monitor/pkg/license"
	"github.com/integration-monitor/integration-monitor/pkg/log"
	"github.com/integration-monitor/integration-monitor/pkg/mail"
	impostgres "github.com/integration-monitor/integration-monitor/pkg/postgres"
	"github.com/integration-monitor/integration-monitor/pkg/version"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/alerting"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/api/middleware"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/catalog"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/dashboards"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/erroracks"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/errornotify"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/facetmappings"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/facetoverrides"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/identity"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/ingestkeys"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/integrations"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/maps"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/messageviews"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/metadata"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/migrations"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/monitoringtemplates"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/notifyprofiles"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/oauth"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/retention"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/schemas"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/servicefacets"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/servicemeta"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/servicetypes"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/settings"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/store"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/systemtypes"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/tags"
	"github.com/integration-monitor/integration-monitor/services/cell-api/internal/tracecompletion"
)

const serviceName = "cell-api"

func main() {
	logger := log.New(serviceName, log.FormatJSON)
	logger.Info("starting",
		"version", version.Version,
		"commit", version.Commit,
		"build_date", version.BuildDate,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ClickHouse — read-only consumer.
	chCfg := imclickhouse.ConfigFromEnv()
	chConn, err := imclickhouse.Open(ctx, chCfg)
	if err != nil {
		logger.Error("open clickhouse failed", "err", err)
		os.Exit(1)
	}
	defer chConn.Close()
	logger.Info("clickhouse ready", "endpoint", chCfg.Endpoint, "database", chCfg.Database)

	// Postgres — owns integrations and (later) alert rules.
	pg, err := impostgres.PoolFromEnv(ctx)
	if err != nil {
		logger.Error("open postgres failed", "err", err)
		os.Exit(1)
	}
	defer pg.Close()
	if err := impostgres.Migrate(ctx, pg, migrations.FS, migrations.Dir); err != nil {
		logger.Error("postgres migrate failed", "err", err)
		os.Exit(1)
	}
	logger.Info("postgres ready")

	integrationStore := integrations.NewStore(pg)
	resolver := integrations.NewResolver(integrationStore, 5*time.Second)
	facetRegistry := servicetypes.NewRegistry()
	facetMappingsStore := facetmappings.NewStore(pg)
	facetOverridesStore := facetoverrides.NewStore(pg)
	messageViews := messageviews.NewStore(pg)
	dashboardStore := dashboards.NewStore(pg)
	tagStore := tags.NewStore(pg)
	alertStore := alerting.NewStore(pg)
	serviceMetaStore := servicemeta.NewStore(pg)
	metadataStore := metadata.NewStore(pg)
	catalogStore := catalog.NewStore(pg)
	schemasStore := schemas.NewStore(pg)
	mapsStore := maps.NewStore(pg)
	identityStore := identity.NewStore(pg)
	erroracksStore := erroracks.NewStore(pg)
	profilesStore := notifyprofiles.NewStore(pg)
	monitoringTemplateStore := monitoringtemplates.NewStore(pg)
	systemTypeStore := systemtypes.NewStore(pg)
	// Ensure an org-wide default notification profile exists so resolution
	// always has a final fallback.
	if err := profilesStore.EnsureOrgDefault(ctx, integrations.DefaultOrgID); err != nil {
		logger.Warn("ensure default notification profile failed", "err", err)
	}

	// Bootstrap a usable password for the migration-0017-seeded admin
	// user the first time the cell-api boots against a fresh DB. The
	// migration leaves password_hash NULL; we fill it with argon2id(
	// "admin") and set must_reset_password=true so the first login
	// flow can force a rotation. Idempotent — does nothing once the
	// hash is populated.
	if err := identityStore.BootstrapSeedAdminPassword(ctx, "admin@sluicio.local", "admin"); err != nil {
		logger.Warn("seed admin password bootstrap failed", "err", err)
	}

	// Promote the seeded admin to cell operator on a fresh cell — but only
	// if no operator exists yet, so a later demotion (or a hand-picked
	// operator) sticks across restarts. In single-org self-hosted this
	// keeps the admin in full control of orgs + cell-wide settings.
	if promoted, err := identityStore.EnsureBootstrapOperator(ctx, "admin@sluicio.local"); err != nil {
		logger.Warn("bootstrap operator failed", "err", err)
	} else if promoted {
		logger.Info("promoted seeded admin to cell operator", "email", "admin@sluicio.local")
	}

	// Enterprise license manager (ee/license). Verified offline against the
	// embedded public key. A missing/invalid key is not fatal — the core
	// runs fully and Enterprise features stay gated off.
	licenseMgr, err := license.NewManager()
	if err != nil {
		logger.Error("license manager init failed", "err", err)
		os.Exit(1)
	}
	if err := licenseMgr.LoadFromEnv(); err != nil {
		logger.Warn("license key present but invalid; continuing unlicensed", "err", err)
	}
	if st := licenseMgr.Status(); st.Licensed {
		logger.Info("enterprise license loaded", "customer", st.Customer, "plan", st.Plan, "entitlements", st.Entitlements, "warning", st.Warning)
	} else {
		logger.Info("running without an enterprise license; EE features disabled")
	}

	chStore := store.New(chConn)
	// cell-api's own loopback base, for the HTTP MCP endpoint's self-dispatch.
	selfAddr := env.String("CELL_API_ADDR", ":8081")
	selfPort := selfAddr[strings.LastIndex(selfAddr, ":")+1:]
	if selfPort == "" {
		selfPort = "8081"
	}
	selfBaseURL := "http://127.0.0.1:" + selfPort

	// Audit recorder: the Enterprise Postgres store, optionally wrapped
	// with an off-box sink. The sink is deployment-config only (env, not
	// API) so a compromised admin session can't redirect the security log.
	var auditRecorder pkgaudit.Recorder = audit.NewStore(pg)
	if sinkURL := strings.TrimSpace(os.Getenv("SLUICIO_AUDIT_SINK_URL")); sinkURL != "" {
		auditRecorder = pkgaudit.NewForwarding(auditRecorder, sinkURL, os.Getenv("SLUICIO_AUDIT_SINK_SECRET"), logger)
		logger.Info("audit sink enabled", "url", sinkURL,
			"signed", os.Getenv("SLUICIO_AUDIT_SINK_SECRET") != "")
	}

	handlers := &api.Handlers{
		License:             licenseMgr,
		SelfBaseURL:         selfBaseURL,
		Audit:               auditRecorder,
		Store:               chStore,
		ClickHouseConn:      chConn,
		Integrations:        integrationStore,
		Resolver:            resolver,
		ServiceFacets:       facetRegistry,
		ServiceFacetsCustom: servicefacets.NewStore(pg),
		FacetMappings:       facetMappingsStore,
		FacetOverrides:      facetOverridesStore,
		MessageViews:        messageViews,
		Dashboards:          dashboardStore,
		Tags:                tagStore,
		Alerts:              alertStore,
		ServiceMeta:         serviceMetaStore,
		Metadata:            metadataStore,
		Catalog:             catalogStore,
		Schemas:             schemasStore,
		Maps:                mapsStore,
		Identity:            identityStore,
		IngestKeys:          ingestkeys.New(pg),
		ErrorAcks:           erroracksStore,
		Profiles:            profilesStore,
		Templates:           monitoringTemplateStore,
		SystemTypes:         systemTypeStore,
		OAuth:               oauth.NewStore(pg),
		AuthMW:              &middleware.Resolver{Identity: identityStore},
		Logger:              logger,
	}

	// Background catalog reconciler: keeps the Postgres services + the
	// integration_services membership in sync with what ClickHouse is
	// seeing. A 30-second cadence is generous; the warm tick on start
	// means first requests already see the latest membership. Handlers
	// also call RunOnce explicitly after integration / matcher edits so
	// users don't have to wait for the tick.
	// bgCtx scopes every background ClickHouse read to the default org
	// (Phase-2 tenant isolation). These loops run single-org today; their
	// reads must still be org-filtered like request reads. Request-path
	// reconciles (handlers call RunOnce) use the request org via the
	// middleware-stamped context instead.
	bgCtx := imclickhouse.WithOrgFilter(ctx, integrations.DefaultOrgID.String())

	catalogReconciler := catalog.NewReconciler(
		catalogStore, integrationStore, chStore, identityStore, integrations.DefaultOrgID,
		logger, 30*time.Second, 90*24*time.Hour,
	)
	handlers.CatalogReconciler = catalogReconciler
	go catalogReconciler.Run(bgCtx)
	logger.Info("catalog reconciler started")

	// Background alerting: evaluate metric rules and deliver firings to
	// channels. Shares the ClickHouse + Postgres stores; stops with ctx.
	alertEngine := alerting.NewEngine(alertStore, metricEvaluatorAdapter{chStore, catalogStore}, logCounterAdapter{chStore, catalogStore}, traceErrorCounterAdapter{chStore, catalogStore, erroracksStore, integrations.DefaultOrgID}, integrations.DefaultOrgID, logger)
	// Route deliveries through global/integration/team channels when a rule
	// has none of its own.
	alertEngine.SetChannelResolver(profilesStore)
	// Trace-latency rules ("response time over X") read the same service set
	// as trace_error, then ask ClickHouse for the windowed quantile latency.
	alertEngine.SetLatencyEvaluator(traceLatencyEvaluatorAdapter{chStore, catalogStore})
	// Trace-volume rules ("fewer than X traces") read the same service set,
	// then count distinct traces — zero counts as below (dead-man's-switch).
	alertEngine.SetVolumeEvaluator(traceVolumeEvaluatorAdapter{chStore, catalogStore})
	go alertEngine.Run(bgCtx)

	// Error notifier: sends one notification when a service's unacknowledged
	// errors open, routed via the same global/integration channels.
	// ERROR_NOTIFY_INTERVAL (a Go duration) overrides the default cadence.
	notifyInterval := time.Duration(0)
	if v := os.Getenv("ERROR_NOTIFY_INTERVAL"); v != "" {
		if d, derr := time.ParseDuration(v); derr == nil {
			notifyInterval = d
		}
	}
	errorNotifier := errornotify.New(chStore, erroracksStore, profilesStore, alertStore, catalogStore, integrations.DefaultOrgID, logger, notifyInterval)
	go errorNotifier.Run(bgCtx)
	logger.Info("alert engine started")

	// Cell-wide settings + retention enforcer. The settings store is the
	// canonical source for telemetry.retention.*; the enforcer pushes
	// those values into ClickHouse TTL on a periodic loop and
	// synchronously on PATCH (the API handler calls ApplyOnce). 1h tick
	// is the safety-net cadence — ALTER TABLE … MODIFY TTL is metadata-
	// only so the steady-state cost is microseconds.
	settingsStore := settings.NewStore(pg)
	handlers.Settings = settingsStore

	// Cell secret-encryption key. Encrypts MFA TOTP secrets and the replayable
	// credentials stored at rest — the SMTP relay password and OIDC client
	// secrets. SLUICIO_MFA_KEY (base64 32 bytes) takes precedence; otherwise a
	// key is generated + persisted so it works out-of-box. A bad/missing key
	// disables MFA enrollment and leaves those credentials as plaintext (the
	// pre-encryption behavior) rather than blocking startup.
	var secretKey []byte
	if b64 := env.String("SLUICIO_MFA_KEY", ""); b64 != "" {
		if key, err := base64.StdEncoding.DecodeString(b64); err == nil && len(key) == 32 {
			secretKey = key
		} else {
			logger.Warn("SLUICIO_MFA_KEY is not valid base64-encoded 32 bytes; MFA and secret-at-rest encryption disabled")
		}
	} else if key, err := settingsStore.GetOrCreateMFAKey(ctx); err != nil {
		logger.Warn("could not obtain secret-encryption key; MFA and secret-at-rest encryption disabled", "err", err)
	} else {
		secretKey = key
	}
	if secretKey != nil {
		identityStore.SetMFAKey(secretKey)
		settingsStore.SetSecretKey(secretKey)
		// One-time migrations: encrypt any secret written as plaintext before
		// encryption-at-rest existed. Idempotent — safe to run every boot.
		if migrated, err := settingsStore.EncryptSMTPSecretAtRest(ctx); err != nil {
			logger.Warn("smtp secret at-rest migration failed", "err", err)
		} else if migrated {
			logger.Info("encrypted stored SMTP password at rest")
		}
		if n, err := identityStore.EncryptProviderSecretsAtRest(ctx); err != nil {
			logger.Warn("sso client-secret at-rest migration failed", "err", err)
		} else if n > 0 {
			logger.Info("encrypted stored SSO client secrets at rest", "count", n)
		}
	}

	// Global transactional-email transport. Config resolves at send time:
	// SLUICIO_SMTP_* env as defaults, overlaid by anything an admin set in
	// Settings → System (so UI edits take effect without a restart).
	resolveSMTP := func(ctx context.Context) (mail.Config, error) {
		cfg := mail.Config{
			Host:     env.String("SLUICIO_SMTP_HOST", ""),
			Port:     env.String("SLUICIO_SMTP_PORT", ""),
			Username: env.String("SLUICIO_SMTP_USERNAME", ""),
			Password: env.String("SLUICIO_SMTP_PASSWORD", ""),
			From:     env.String("SLUICIO_SMTP_FROM", ""),
			FromName: env.String("SLUICIO_SMTP_FROM_NAME", "Sluicio"),
		}
		s, err := settingsStore.GetSMTP(ctx)
		if err != nil {
			return cfg, nil // fall back to env on a settings read error
		}
		if s.Host != "" {
			cfg.Host = s.Host
		}
		if s.Port != "" {
			cfg.Port = s.Port
		}
		if s.Username != "" {
			cfg.Username = s.Username
		}
		if s.Password != "" {
			cfg.Password = s.Password
		}
		if s.From != "" {
			cfg.From = s.From
		}
		if s.FromName != "" {
			cfg.FromName = s.FromName
		}
		return cfg, nil
	}
	handlers.Mail = mail.NewSender(resolveSMTP)
	// Email alert channels reuse this system SMTP transport unless they set
	// their own server: expose it to the alerting delivery worker as the
	// channel-config keys its email notifier reads.
	alerting.SetSystemMailResolver(func(ctx context.Context) map[string]string {
		c, err := resolveSMTP(ctx)
		if err != nil {
			return nil
		}
		m := map[string]string{}
		if c.Host != "" {
			m["smtp_host"] = c.Host
		}
		if c.Port != "" {
			m["smtp_port"] = c.Port
		}
		if c.From != "" {
			m["from"] = c.From
		}
		if c.Username != "" {
			m["username"] = c.Username
		}
		if c.Password != "" {
			m["password"] = c.Password
		}
		return m
	})

	// Every alert/error notification's title + body lead with the cell's
	// environment label and the org/company name ("Sluicio {env} - … -
	// {company}"), so a recipient instantly sees which deployment + company
	// it's from. Resolved live; failures degrade to omitting the segment.
	alerting.SetDeploymentContextResolver(func(ctx context.Context) (string, string) {
		env, err := settingsStore.GetEnvironment(ctx)
		if err != nil {
			env = ""
		}
		company := ""
		if org, oErr := identityStore.GetOrgByID(ctx, integrations.DefaultOrgID); oErr == nil {
			company = org.Name
		}
		return env, company
	})

	// Enrich alert notifications with live service / integration details +
	// their metadata (read fresh at delivery), and resolve the org-default
	// email template — both implemented in the api layer where the stores live.
	alerting.SetAlertContextResolver(handlers.ResolveAlertContext)
	alerting.SetDefaultEmailTemplateResolver(handlers.DefaultEmailTemplate)

	retentionEnforcer := retention.New(settingsStore, chConn, logger, time.Hour)
	retentionEnforcer.Audit = auditRecorder
	handlers.RetentionEnforcer = retentionEnforcer
	go retentionEnforcer.Run(ctx)
	logger.Info("retention enforcer started")

	// Trace-completion rule evaluator. Rules live in alert_rules with
	// signal='trace' (reusing the existing schema); this evaluator
	// runs every 30s, classifies recent traces per integration rule,
	// and pushes sticky-delayed firings through the existing
	// alerting machinery (alert_instances + notification_jobs). The
	// metric alert engine and this one don't collide — the metric
	// one filters to signal='metric' (see EnabledMetricRules).
	traceCompletionStore := tracecompletion.NewStore(pg)
	handlers.TraceCompletion = traceCompletionStore
	traceEvaluator := tracecompletion.New(
		traceCompletionStore,
		traceCompletionAlertAdapter{alertStore},
		catalogStore,
		chConn,
		integrations.DefaultOrgID,
		logger,
		30*time.Second,
	)
	handlers.TraceCompletionEvaluator = traceEvaluator
	go traceEvaluator.Run(bgCtx)
	logger.Info("trace-completion evaluator started")

	mux := http.NewServeMux()
	handlers.Mount(mux)

	// Wrap the whole mux in the auth middleware. Every endpoint
	// requires authentication except the skip list:
	//   /api/v1/auth/login          — the credential-input endpoint
	//   /api/v1/auth/logout         — clears the cookie even if invalid/missing
	//   /api/v1/auth/install-state  — public so the login page can decide
	//                                  whether to surface the default-admin
	//                                  hint; returns only {fresh:bool}
	// Everything else gets a Principal injected into the request
	// context via middleware.PrincipalFromContext / middleware.OrgID(r).
	// MFA-policy enforcement runs inside the auth wrap (it needs the
	// Principal) and in front of every handler; see api/mfa_enforce.go.
	// Two ordered pre-handler gates (both need the Principal from the auth
	// wrap below): password-reset first, then MFA enrollment.
	inner := handlers.EnforcePasswordReset(handlers.EnforceMFAEnrollment(mux))
	authed := handlers.AuthMW.Wrap([]string{
		"/api/v1/auth/login",
		"/api/v1/auth/logout",
		"/api/v1/auth/install-state",
		// First-run setup is necessarily pre-auth; the handler self-seals
		// after the first-ever login (409 from then on).
		"/api/v1/auth/bootstrap-admin",
		// Password reset is necessarily pre-auth (the user can't log in).
		"/api/v1/auth/forgot-password",
		"/api/v1/auth/reset-password",
		// MFA second step: the user has passed password auth but doesn't
		// have a session yet, so it can't sit behind the session gate.
		"/api/v1/auth/mfa-verify",
		// SSO/OIDC login flow is pre-session (providers list, authorize
		// redirect, callback). Gated inside the handlers by FeatureSSO.
		"/api/v1/auth/sso/",
		// Public API docs — the spec + Redoc UI are readable without a session.
		"/api/v1/openapi.json",
		"/api/docs",
		// Public status badges (opt-in per entity) — the SVG must be embeddable
		// in a README with no session. Only opted-in entities render; the
		// toggles that set that live under the authed entity paths.
		"/api/v1/badges/",
		// OAuth 2.1 server for the MCP endpoint: discovery metadata, dynamic
		// client registration, the authorize/consent screen (session-cookie
		// gated inside the handler) and the token endpoint are all pre-auth.
		"/.well-known/",
		"/api/v1/oauth/",
	}, inner)
	root := httpserver.AllowCORSForDev(authed)

	addr := env.String("CELL_API_ADDR", ":8081")
	if err := httpserver.Run(ctx, httpserver.Config{
		Addr:    addr,
		Handler: root,
		Logger:  logger,
	}); err != nil {
		logger.Error("http server stopped with error", "err", err)
		os.Exit(1)
	}
	logger.Info("shutting down")
}

// metricEvaluatorAdapter lets the alerting engine evaluate metric rules
// against the ClickHouse store without the alerting package depending on
// it: it converts alerting's attribute filters to the store's type.
type metricEvaluatorAdapter struct {
	s       *store.Store
	catalog *catalog.Store
}

// metricScope resolves a rule's binding to the store's serviceIn allowlist,
// mirroring the log/trace adapters: a service-bound rule scopes to that one
// service; an integration-bound rule scopes to the integration's member
// services; a global rule (neither) returns nil = all services. ok=false
// means "integration with no resolved members" — evaluate as no-data rather
// than falling through to nil (which would pool the metric across the whole
// org — the bug this fixes).
func (a metricEvaluatorAdapter) metricScope(ctx context.Context, serviceName string, integrationID *uuid.UUID) (serviceIn []string, ok bool, err error) {
	if serviceName != "" {
		return []string{serviceName}, true, nil
	}
	if integrationID != nil {
		svcs, err := a.catalog.IntegrationServices(ctx, *integrationID)
		if err != nil {
			return nil, false, err
		}
		if len(svcs) == 0 {
			return nil, false, nil // no members yet → no data
		}
		return svcs, true, nil
	}
	return nil, true, nil // global — all services
}

func (a metricEvaluatorAdapter) MetricAggregate(ctx context.Context, metricName string, attrs []alerting.AttrFilter, aggregation, serviceName string, integrationID *uuid.UUID, from, to time.Time) (float64, uint64, error) {
	serviceIn, ok, err := a.metricScope(ctx, serviceName, integrationID)
	if err != nil {
		return 0, 0, err
	}
	if !ok {
		return 0, 0, nil
	}
	return a.s.MetricAggregate(ctx, metricName, alertAttrsToStore(attrs), aggregation, from, to, serviceIn)
}

func (a metricEvaluatorAdapter) MetricAggregateGrouped(ctx context.Context, metricName string, attrs []alerting.AttrFilter, aggregation, splitKey, serviceName string, integrationID *uuid.UUID, from, to time.Time) ([]alerting.MetricGroup, error) {
	serviceIn, ok, err := a.metricScope(ctx, serviceName, integrationID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	rows, err := a.s.MetricAggregateGrouped(ctx, metricName, alertAttrsToStore(attrs), aggregation, splitKey, from, to, serviceIn)
	if err != nil {
		return nil, err
	}
	out := make([]alerting.MetricGroup, len(rows))
	for i, r := range rows {
		out[i] = alerting.MetricGroup{Label: r.Label, Value: r.Value, Samples: r.Samples}
	}
	return out, nil
}

// alertAttrsToStore converts alerting attribute filters to the store's
// type (shared by the scalar + grouped metric adapters).
func alertAttrsToStore(attrs []alerting.AttrFilter) []store.LogAttrFilter {
	sf := make([]store.LogAttrFilter, len(attrs))
	for i, f := range attrs {
		sf[i] = store.LogAttrFilter{Key: f.Key, Op: f.Op, Value: f.Value}
	}
	return sf
}

// logCounterAdapter lets the alerting engine count log matches for log
// rules against ClickHouse, resolving an integration binding to its
// service set via the catalog (the same membership tracecompletion
// uses). Keeps the alerting package independent of the store + catalog.
type logCounterAdapter struct {
	s       *store.Store
	catalog *catalog.Store
}

func (a logCounterAdapter) CountLogs(ctx context.Context, q alerting.LogCountQuery) (uint64, error) {
	p := store.LogQueryParams{
		From:         q.From,
		To:           q.To,
		MinSeverity:  q.MinSeverity,
		BodyContains: q.BodyContains,
		Service:      q.ServiceName,
	}
	for _, f := range q.Attrs {
		p.Attrs = append(p.Attrs, store.LogAttrFilter{Key: f.Key, Op: f.Op, Value: f.Value})
	}
	if q.IntegrationID != nil {
		svcs, err := a.catalog.IntegrationServices(ctx, *q.IntegrationID)
		if err != nil {
			return 0, err
		}
		if len(svcs) == 0 {
			// Integration has no resolved services yet → nothing to match.
			return 0, nil
		}
		p.ServiceIn = svcs
	}
	return a.s.CountLogs(ctx, p)
}

// traceErrorCounterAdapter lets the alerting engine count an integration's
// failed traces for trace_error rules: resolve the integration's service
// set via the catalog (same membership the log adapter uses), then count
// distinct error traces over the window in ClickHouse — excluding any errors
// a maintainer has already cleared (the per-service "Clear errors"
// watermark), so a cleared service reads healthy just like the built-in
// open-error signal and the window error count.
type traceErrorCounterAdapter struct {
	s       *store.Store
	catalog *catalog.Store
	acks    *erroracks.Store
	orgID   uuid.UUID
}

func (a traceErrorCounterAdapter) CountErrorTraces(ctx context.Context, q alerting.TraceErrorQuery) (uint64, error) {
	var svcs []string
	switch {
	case q.IntegrationID != nil:
		s, err := a.catalog.IntegrationServices(ctx, *q.IntegrationID)
		if err != nil {
			return 0, err
		}
		svcs = s
	case q.ServiceName != "":
		svcs = []string{q.ServiceName}
	}
	if len(svcs) == 0 {
		// No resolved services yet → no traces to fail.
		return 0, nil
	}
	return a.countRespectingClears(ctx, svcs, q.From, q.To)
}

// countRespectingClears counts failed traces over [from,to] for the service
// set, skipping errors at or before each service's "Clear errors" watermark.
// When no service in the set has been cleared within the window the watermark
// can't change the answer, so it falls back to the cheap single distinct-set
// query — the common path is byte-for-byte unchanged. Only once a clear lands
// in the window does it switch to a per-service sum since each watermark
// (which counts a trace failing on two of the set's services twice — a
// negligible over-count confined to the post-clear path).
func (a traceErrorCounterAdapter) countRespectingClears(ctx context.Context, svcs []string, from, to time.Time) (uint64, error) {
	acks := map[string]erroracks.Ack{}
	if a.acks != nil {
		if m, err := a.acks.GetAll(ctx, a.orgID); err == nil {
			acks = m
		}
	}
	cleared := false
	for _, svc := range svcs {
		if acks[svc].AcknowledgedUntil.After(from) {
			cleared = true
			break
		}
	}
	if !cleared {
		return a.s.CountErrorTracesForServices(ctx, svcs, from, to)
	}
	var total uint64
	for _, svc := range svcs {
		since := from
		if wm := acks[svc].AcknowledgedUntil; wm.After(since) {
			since = wm
		}
		n, err := a.s.ErrorTraceCountSince(ctx, svc, since, to)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

// traceLatencyEvaluatorAdapter lets the alerting engine read an integration's
// windowed quantile latency for trace_latency rules ("response time over X").
// Same service-set resolution as traceErrorCounterAdapter, then a quantile
// aggregation over span durations in ClickHouse.
type traceLatencyEvaluatorAdapter struct {
	s       *store.Store
	catalog *catalog.Store
}

func (a traceLatencyEvaluatorAdapter) TraceLatencyMs(ctx context.Context, q alerting.TraceLatencyQuery) (float64, uint64, error) {
	// Integration scope: resolve the integration's service set, then measure.
	if q.IntegrationID != nil {
		svcs, err := a.catalog.IntegrationServices(ctx, *q.IntegrationID)
		if err != nil {
			return 0, 0, err
		}
		if len(svcs) == 0 {
			// No resolved services yet → no samples to measure.
			return 0, 0, nil
		}
		return a.s.LatencyMsForServices(ctx, svcs, q.Quantile, q.From, q.To)
	}
	// Service scope: measure latency for the single bound service.
	if q.ServiceName != "" {
		return a.s.LatencyMsForServices(ctx, []string{q.ServiceName}, q.Quantile, q.From, q.To)
	}
	return 0, 0, nil
}

// traceVolumeEvaluatorAdapter lets the alerting engine read the distinct
// trace count for a low-traffic ("fewer than X") rule. Same service-set
// resolution as the latency/error adapters, then DistinctTraceCounts —
// dropping the errored half, since a volume floor cares only about total
// throughput. An integration whose service set is empty reports zero, which
// (per the dead-man's-switch semantics) is itself a breach.
type traceVolumeEvaluatorAdapter struct {
	s       *store.Store
	catalog *catalog.Store
}

func (a traceVolumeEvaluatorAdapter) TotalTraces(ctx context.Context, q alerting.TraceVolumeQuery) (uint64, error) {
	if q.IntegrationID != nil {
		svcs, err := a.catalog.IntegrationServices(ctx, *q.IntegrationID)
		if err != nil {
			return 0, err
		}
		if len(svcs) == 0 {
			return 0, nil
		}
		total, _, err := a.s.DistinctTraceCounts(ctx, svcs, q.From, q.To, nil)
		return total, err
	}
	if q.ServiceName != "" {
		total, _, err := a.s.DistinctTraceCounts(ctx, []string{q.ServiceName}, q.From, q.To, nil)
		return total, err
	}
	return 0, nil
}

// traceCompletionAlertAdapter lets the trace-completion evaluator
// reuse the existing alerting.Store machinery (alert_instances +
// notification_jobs + delivery loop) without the tracecompletion
// package importing alerting. The interface methods convert between
// alerting.AlertInstance and tracecompletion.AlertInstance (which
// only carries the ID — that's all the evaluator needs).
type traceCompletionAlertAdapter struct{ s *alerting.Store }

func (a traceCompletionAlertAdapter) OpenInstance(ctx context.Context, ruleID uuid.UUID, fingerprint string, labels map[string]string, summary string) (tracecompletion.AlertInstance, error) {
	inst, err := a.s.OpenInstance(ctx, ruleID, fingerprint, labels, summary)
	if err != nil {
		return tracecompletion.AlertInstance{}, err
	}
	return tracecompletion.AlertInstance{ID: inst.ID}, nil
}

func (a traceCompletionAlertAdapter) TouchInstance(ctx context.Context, id uuid.UUID) error {
	return a.s.TouchInstance(ctx, id)
}

func (a traceCompletionAlertAdapter) EnqueueJobs(ctx context.Context, instanceID uuid.UUID, channelIDs []uuid.UUID) error {
	return a.s.EnqueueJobs(ctx, instanceID, channelIDs)
}

func (a traceCompletionAlertAdapter) ActiveInstanceByFingerprint(ctx context.Context, ruleID uuid.UUID, fingerprint string) (*tracecompletion.AlertInstance, error) {
	inst, err := a.s.ActiveInstanceByFingerprint(ctx, ruleID, fingerprint)
	if err != nil || inst == nil {
		return nil, err
	}
	return &tracecompletion.AlertInstance{ID: inst.ID}, nil
}

func (a traceCompletionAlertAdapter) ResolveInstance(ctx context.Context, id uuid.UUID, summary string) error {
	return a.s.ResolveInstance(ctx, id, summary)
}
