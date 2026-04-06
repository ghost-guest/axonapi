package biz

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zhenzou/executors"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm/httpclient"
)

func TestPublicBenefitRuntimeService_Start_SchedulesMinuteCron(t *testing.T) {
	svc := &PublicBenefitRuntimeService{}
	expr := "*/10 * * * *"
	svc.executor = &publicBenefitRuntimeTestExecutor{
		scheduleFuncAtCronRate: func(_ func(context.Context), rule executors.CRONRule) (executors.CancelFunc, error) {
			expr = rule.Expr
			return nil, nil
		},
	}

	require.NoError(t, svc.Start(context.Background()))
	require.Equal(t, "* * * * *", expr)
}

type publicBenefitRuntimeTestExecutor struct {
	scheduleFuncAtCronRate func(fn func(context.Context), rule executors.CRONRule) (executors.CancelFunc, error)
}

func (e *publicBenefitRuntimeTestExecutor) Execute(executors.Runnable) error {
	panic("unexpected call")
}

func (e *publicBenefitRuntimeTestExecutor) ExecuteFunc(func(context.Context)) error {
	panic("unexpected call")
}

func (e *publicBenefitRuntimeTestExecutor) Schedule(executors.Runnable, time.Duration) (executors.CancelFunc, error) {
	panic("unexpected call")
}

func (e *publicBenefitRuntimeTestExecutor) ScheduleFunc(func(context.Context), time.Duration) (executors.CancelFunc, error) {
	panic("unexpected call")
}

func (e *publicBenefitRuntimeTestExecutor) ScheduleAtFixRate(executors.Runnable, time.Duration) (executors.CancelFunc, error) {
	panic("unexpected call")
}

func (e *publicBenefitRuntimeTestExecutor) ScheduleFuncAtFixRate(func(context.Context), time.Duration) (executors.CancelFunc, error) {
	panic("unexpected call")
}

func (e *publicBenefitRuntimeTestExecutor) ScheduleAtCronRate(executors.Runnable, executors.CRONRule) (executors.CancelFunc, error) {
	panic("unexpected call")
}

func (e *publicBenefitRuntimeTestExecutor) ScheduleFuncAtCronRate(fn func(context.Context), rule executors.CRONRule) (executors.CancelFunc, error) {
	if e.scheduleFuncAtCronRate == nil {
		panic("unexpected call")
	}

	return e.scheduleFuncAtCronRate(fn, rule)
}

func (e *publicBenefitRuntimeTestExecutor) Shutdown(ctx context.Context) error {
	return nil
}


func TestPublicBenefitRuntimeService_RunSync_DisabledProviderAndUpstream(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))

	systemService := &SystemService{
		AbstractService: &AbstractService{db: client},
		Cache:           xcache.NewFromConfig[ent.System](xcache.Config{Mode: xcache.ModeMemory}),
	}

	channelService := NewChannelServiceForTest(client)
	runtimeService := &PublicBenefitRuntimeService{
		AbstractService: &AbstractService{db: client},
		SystemService:   systemService,
		ChannelService:  channelService,
		httpClient:      httpclient.NewHttpClient(),
	}

	err := systemService.SetPublicBenefitHubConfig(ctx, objects.PublicBenefitHubConfig{
		Providers: []objects.PublicBenefitProviderAccount{
			{ID: "p1", Name: "disabled-provider", BaseURL: "https://provider.example", Enabled: false},
		},
		Upstreams: []objects.PublicBenefitUpstreamSite{
			{ID: "u1", Name: "disabled-upstream", BaseURL: "https://upstream.example", APIKey: "k", Enabled: false},
		},
	})
	require.NoError(t, err)

	err = runtimeService.RunSync(ctx)
	require.NoError(t, err)

	state, err := systemService.PublicBenefitRuntimeState(ctx)
	require.NoError(t, err)
	require.Len(t, state.Providers, 1)
	require.Equal(t, "provider disabled", state.Providers[0].LastError)
	require.Len(t, state.Upstreams, 1)
	require.Equal(t, "disabled", state.Upstreams[0].HealthStatus)
}

func TestShouldPerformProviderCheckIn_DefaultDailyWindow(t *testing.T) {
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	last := now.Add(-2 * time.Hour)

	require.False(t, shouldPerformProviderCheckIn(
		objects.PublicBenefitProviderAccount{AutoCheckIn: true},
		objects.PublicBenefitProviderRuntime{LastCheckInAt: &last},
		now,
	))

	oldLast := now.Add(-25 * time.Hour)
	require.True(t, shouldPerformProviderCheckIn(
		objects.PublicBenefitProviderAccount{AutoCheckIn: true},
		objects.PublicBenefitProviderRuntime{LastCheckInAt: &oldLast},
		now,
	))
}

func TestApplyPublicBenefitAuth_NewAPICompatibleHeaders(t *testing.T) {
	headers := make(http.Header)

	ApplyPublicBenefitAuth(headers, objects.PublicBenefitProviderAccount{
		APIKey:   "pb-api-key",
		Token:    "pb-token",
		Cookie:   "session=abc",
		Username: "259",
	})

	require.Equal(t, "Bearer pb-token", headers.Get("Authorization"))
	require.Equal(t, "pb-api-key", headers.Get("X-API-Key"))
	require.Equal(t, "pb-api-key", headers.Get("Api-Key"))
	require.Equal(t, "pb-token", headers.Get("X-Access-Token"))
	require.Equal(t, "session=abc", headers.Get("Cookie"))
	require.Equal(t, "259", headers.Get("X-User-ID"))
	require.Equal(t, "259", headers.Get("New-Api-User"))
}

func TestApplyPublicBenefitAuth_CookieOnlyTemplates(t *testing.T) {
	headers := make(http.Header)

	ApplyPublicBenefitAuth(headers, objects.PublicBenefitProviderAccount{
		Cookie:   "token=abc; yescode_auth=def",
		Username: "",
	})

	require.Equal(t, "token=abc; yescode_auth=def", headers.Get("Cookie"))
	require.Empty(t, headers.Get("X-User-ID"))
	require.Empty(t, headers.Get("New-Api-User"))
}

func TestSyncProviderRuntime_PreservesPreviousCheckInStateWhenNotDue(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))

	systemService := &SystemService{
		AbstractService: &AbstractService{db: client},
		Cache:           xcache.NewFromConfig[ent.System](xcache.Config{Mode: xcache.ModeMemory}),
	}

	runtimeService := &PublicBenefitRuntimeService{
		AbstractService: &AbstractService{db: client},
		SystemService:   systemService,
		ChannelService:  NewChannelServiceForTest(client),
		httpClient:      httpclient.NewHttpClient(),
	}

	lastCheckInAt := time.Now().Add(-2 * time.Hour)
	runtime := runtimeService.syncProviderRuntime(ctx,
		objects.PublicBenefitProviderAccount{
			ID:          "p1",
			Name:        "provider-1",
			BaseURL:     "https://provider.example",
			Enabled:     true,
			AutoCheckIn: true,
			CheckInPath: "/api/user/checkin",
			BalancePath: "",
			Kind:        objects.PublicBenefitProviderKindGeneric,
			AuthType:    objects.PublicBenefitAuthTypeCookie,
		},
		objects.PublicBenefitProviderRuntime{
			ProviderID:        "p1",
			LastCheckInAt:     &lastCheckInAt,
			LastCheckInStatus: "ok",
		},
		time.Now(),
		false,
	)

	require.Equal(t, "ok", runtime.LastCheckInStatus)
	require.NotNil(t, runtime.LastCheckInAt)
	require.WithinDuration(t, lastCheckInAt, *runtime.LastCheckInAt, time.Second)
}

func TestBuildPublicBenefitProbeRequest_PrefersClaudeProtocol(t *testing.T) {
	probeType, modelID, req := buildPublicBenefitProbeRequest(objects.PublicBenefitUpstreamSite{
		BaseURL:        "https://probe.example/",
		APIKey:         "secret",
		SupportsClaude: true,
	}, []string{"claude-sonnet-4-5", "gpt-5.4"})
	require.NotNil(t, req)
	require.Equal(t, "anthropic_messages", probeType)
	require.Equal(t, "claude-sonnet-4-5", modelID)
	require.Equal(t, "https://probe.example/v1/messages", req.URL)
	require.Equal(t, "Bearer secret", req.Headers.Get("Authorization"))

	var body map[string]any
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.Equal(t, "claude-sonnet-4-5", body["model"])
	require.Equal(t, float64(5), body["max_tokens"])
}

func TestBuildPublicBenefitProbeRequest_UsesConfiguredChatPath(t *testing.T) {
	probeType, modelID, req := buildPublicBenefitProbeRequest(objects.PublicBenefitUpstreamSite{
		BaseURL:          "https://probe.example/base",
		APIKey:           "secret",
		SupportsOpenCode: true,
		HealthCheckPath:  "/v1/chat/completions?via=custom",
		HealthCheckModel: "gpt-5.4",
	}, []string{"claude-sonnet-4-5"})
	require.NotNil(t, req)
	require.Equal(t, "openai_chat", probeType)
	require.Equal(t, "gpt-5.4", modelID)
	require.Equal(t, "https://probe.example/base/v1/chat/completions?via=custom", req.URL)

	var body map[string]any
	require.NoError(t, json.Unmarshal(req.Body, &body))
	require.Equal(t, "gpt-5.4", body["model"])
	require.Equal(t, float64(5), body["max_tokens"])
}

func TestPublicBenefitRuntimeService_SyncUpstreamRuntime_RealProbeUpdatesHealth(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))

	var probePath string
	var probeAuth string
	var probeBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"claude-sonnet-4-5"}]}`)
		case "/v1/messages":
			probePath = r.URL.Path
			probeAuth = r.Header.Get("Authorization")
			defer r.Body.Close()
			require.NoError(t, json.NewDecoder(r.Body).Decode(&probeBody))
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"msg_123","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	systemService := &SystemService{
		AbstractService:               &AbstractService{db: client},
		Cache:                         xcache.NewFromConfig[ent.System](xcache.Config{Mode: xcache.ModeMemory}),
		publicBenefitAffinityBindings: make(map[string]publicBenefitAffinityBinding),
	}
	channelService := NewChannelServiceForTest(client)
	svc := &PublicBenefitRuntimeService{
		AbstractService: &AbstractService{db: client},
		SystemService:   systemService,
		ChannelService:  channelService,
		httpClient:      httpclient.NewHttpClientWithClient(server.Client()),
	}

	runtime := svc.syncUpstreamRuntime(ctx, objects.PublicBenefitUpstreamSite{
		ID:             "u1",
		Name:           "Claude upstream",
		BaseURL:        server.URL,
		APIKey:         "secret",
		Enabled:        true,
		SupportsClaude: true,
	}, time.Now())

	require.Equal(t, "healthy", runtime.HealthStatus)
	require.Equal(t, []string{"claude-sonnet-4-5"}, runtime.AvailableModels)
	require.NotNil(t, runtime.LastHealthCheckAt)
	require.Empty(t, runtime.LastError)
	require.Equal(t, "/v1/messages", probePath)
	require.Equal(t, "Bearer secret", probeAuth)
	require.Equal(t, "claude-sonnet-4-5", probeBody["model"])
	require.Equal(t, float64(5), probeBody["max_tokens"])
}

func TestPublicBenefitRuntimeService_SyncUpstreamRuntime_ProbeFailureMarksError(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"gpt-5.4-codex"}]}`)
		case "/responses":
			http.Error(w, `{"error":"probe failed"}`, http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	systemService := &SystemService{
		AbstractService:               &AbstractService{db: client},
		Cache:                         xcache.NewFromConfig[ent.System](xcache.Config{Mode: xcache.ModeMemory}),
		publicBenefitAffinityBindings: make(map[string]publicBenefitAffinityBinding),
	}
	channelService := NewChannelServiceForTest(client)
	svc := &PublicBenefitRuntimeService{
		AbstractService: &AbstractService{db: client},
		SystemService:   systemService,
		ChannelService:  channelService,
		httpClient:      httpclient.NewHttpClientWithClient(server.Client()),
	}

	runtime := svc.syncUpstreamRuntime(ctx, objects.PublicBenefitUpstreamSite{
		ID:            "u1",
		Name:          "Codex upstream",
		BaseURL:       server.URL,
		APIKey:        "secret",
		Enabled:       true,
		SupportsCodex: true,
	}, time.Now())

	require.Equal(t, "error", runtime.HealthStatus)
	require.NotNil(t, runtime.LastHealthCheckAt)
	require.Contains(t, runtime.LastError, "openai_responses probe failed")
}
