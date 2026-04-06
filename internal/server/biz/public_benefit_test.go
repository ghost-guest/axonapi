package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm"
)

func setupPublicBenefitSystemService(t *testing.T) (*SystemService, context.Context) {
	t.Helper()
	client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=1")
	t.Cleanup(func() {
		client.Close()
	})

	service := &SystemService{
		AbstractService:               &AbstractService{db: client},
		Cache:                         xcache.NewFromConfig[ent.System](xcache.Config{Mode: xcache.ModeMemory}),
		publicBenefitAffinityBindings: make(map[string]publicBenefitAffinityBinding),
	}

	ctx := authz.WithTestBypass(ent.NewContext(context.Background(), client))

	return service, ctx
}

func TestSystemService_PublicBenefitHubConfig_Default(t *testing.T) {
	service, ctx := setupPublicBenefitSystemService(t)

	cfg, err := service.PublicBenefitHubConfig(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Empty(t, cfg.Providers)
	require.Empty(t, cfg.Upstreams)
	require.Equal(t, objects.PublicBenefitRouteModeAdaptive, cfg.Outbound.DefaultRouteMode)
	require.Contains(t, cfg.Outbound.DefaultClaudeFallback, "MiniMax-M2.7-highspeed")
	require.NotEmpty(t, cfg.Outbound.DefaultGenericFallback)
}

func TestSystemService_SetPublicBenefitHubConfig_ValidateDuplicate(t *testing.T) {
	service, ctx := setupPublicBenefitSystemService(t)

	err := service.SetPublicBenefitHubConfig(ctx, objects.PublicBenefitHubConfig{
		Providers: []objects.PublicBenefitProviderAccount{
			{ID: "p1", Name: "provider-1", BaseURL: "https://example.com", Enabled: true},
			{ID: "p1", Name: "provider-2", BaseURL: "https://example.org", Enabled: true},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate provider id")
}

func TestSystemService_PublicBenefitDashboard(t *testing.T) {
	service, ctx := setupPublicBenefitSystemService(t)

	err := service.SetPublicBenefitHubConfig(ctx, objects.PublicBenefitHubConfig{
		Providers: []objects.PublicBenefitProviderAccount{
			{ID: "p1", Name: "Provider 1", BaseURL: "https://provider-1.example", Enabled: true},
			{ID: "p2", Name: "Provider 2", BaseURL: "https://provider-2.example", Enabled: false},
		},
		Upstreams: []objects.PublicBenefitUpstreamSite{
			{ID: "u1", Name: "Upstream 1", BaseURL: "https://upstream-1.example", APIKey: "k1", Enabled: true},
			{ID: "u2", Name: "Upstream 2", BaseURL: "https://upstream-2.example", APIKey: "k2", Enabled: true},
		},
		Outbound: objects.PublicBenefitOutboundConfig{
			Enabled:          true,
			PublicBaseURL:    "https://gateway.example",
			PublicAPIKey:     "public-key",
			DefaultRouteMode: objects.PublicBenefitRouteModeAdaptive,
		},
	})
	require.NoError(t, err)

	err = service.SetPublicBenefitRuntimeState(ctx, objects.PublicBenefitRuntimeState{
		Providers: []objects.PublicBenefitProviderRuntime{
			{ProviderID: "p1", Balance: 12.5, TotalUsage: 3.5},
			{ProviderID: "p2", Balance: 5, TotalUsage: 1.2},
		},
		Upstreams: []objects.PublicBenefitUpstreamRuntime{
			{UpstreamID: "u1", HealthStatus: "healthy", TotalRequests: 8, TotalTokens: 100},
			{UpstreamID: "u2", HealthStatus: "degraded", TotalRequests: 4, TotalTokens: 50},
		},
		DailyUsage: []objects.PublicBenefitUsageSnapshot{
			{Date: "2026-03-29", TotalRequests: 2, TotalTokens: 25},
		},
	})
	require.NoError(t, err)

	dashboard, err := service.PublicBenefitDashboard(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, dashboard.ProviderCount)
	require.Equal(t, 1, dashboard.EnabledProviderCount)
	require.Equal(t, 2, dashboard.UpstreamCount)
	require.Equal(t, 2, dashboard.EnabledUpstreamCount)
	require.Equal(t, 1, dashboard.HealthyUpstreamCount)
	require.Equal(t, 17.5, dashboard.TotalBalance)
	require.Equal(t, 4.7, dashboard.TotalUsage)
	require.EqualValues(t, 12, dashboard.TotalRequests)
	require.EqualValues(t, 150, dashboard.TotalTokens)
}

func TestSystemService_ResolvePublicBenefitModelSequence(t *testing.T) {
	service, ctx := setupPublicBenefitSystemService(t)

	err := service.SetPublicBenefitHubConfig(ctx, objects.PublicBenefitHubConfig{
		Upstreams: []objects.PublicBenefitUpstreamSite{
			{
				ID:             "u1",
				Name:           "Claude Site",
				BaseURL:        "https://claude.example",
				APIKey:         "k1",
				Enabled:        true,
				SupportsClaude: true,
				Weight:         10,
			},
			{
				ID:            "u2",
				Name:          "Codex Site",
				BaseURL:       "https://codex.example",
				APIKey:        "k2",
				Enabled:       true,
				SupportsCodex: true,
				Weight:        8,
			},
		},
		Outbound: objects.PublicBenefitOutboundConfig{
			DefaultRouteMode:      objects.PublicBenefitRouteModeAdaptive,
			DefaultClaudeFallback: []string{"claude-3-7-sonnet", "gpt-5.4"},
			DefaultCodexFallback:  []string{"gpt-5.4-codex", "gpt-5.4"},
			DefaultGenericFallback: []string{
				"gpt-5.4",
			},
		},
	})
	require.NoError(t, err)

	err = service.SetPublicBenefitRuntimeState(ctx, objects.PublicBenefitRuntimeState{
		Upstreams: []objects.PublicBenefitUpstreamRuntime{
			{
				UpstreamID:      "u1",
				HealthStatus:    "healthy",
				AvailableModels: []string{"claude-sonnet-4", "claude-3-7-sonnet"},
			},
			{
				UpstreamID:      "u2",
				HealthStatus:    "healthy",
				AvailableModels: []string{"gpt-5.4-codex"},
			},
		},
	})
	require.NoError(t, err)

	claudeSequence := service.ResolvePublicBenefitModelSequence(ctx, llm.APIFormatAnthropicMessage, "claude-opus-4")
	require.Equal(t, []string{"claude-opus-4", "claude-sonnet-4", "claude-3-7-sonnet", "gpt-5.4"}, claudeSequence)

	codexSequence := service.ResolvePublicBenefitModelSequence(ctx, llm.APIFormatOpenAIResponse, "gpt-5-codex")
	require.Equal(t, []string{"gpt-5-codex", "gpt-5.4-codex", "gpt-5.4"}, codexSequence)
}

func TestSystemService_PublicBenefitSessionAffinity(t *testing.T) {
	service, ctx := setupPublicBenefitSystemService(t)

	err := service.SetPublicBenefitHubConfig(ctx, objects.PublicBenefitHubConfig{
		Outbound: objects.PublicBenefitOutboundConfig{
			Enabled:                   true,
			DefaultRouteMode:          objects.PublicBenefitRouteModeAdaptive,
			SessionAffinityEnabled:    true,
			SessionAffinityTTLSeconds: 60,
		},
	})
	require.NoError(t, err)

	ctx = contexts.WithTraceID(ctx, "claude-trace-1")
	service.BindPublicBenefitSessionAffinity(ctx, llm.APIFormatAnthropicMessage, "claude-sonnet-4", "https://sticky.example/")

	affinity := service.ResolvePublicBenefitSessionAffinity(ctx, llm.APIFormatAnthropicMessage, "claude-opus-4")
	require.NotNil(t, affinity)
	require.Equal(t, "https://sticky.example", affinity.BaseURL)
	require.Equal(t, "claude:trace:claude-trace-1", affinity.SessionKey)
	require.True(t, affinity.ExpiresAt.After(time.Now()))
}

func TestSystemService_RecordPublicBenefitUsage(t *testing.T) {
	service, ctx := setupPublicBenefitSystemService(t)

	err := service.SetPublicBenefitRuntimeState(ctx, objects.PublicBenefitRuntimeState{
		Upstreams: []objects.PublicBenefitUpstreamRuntime{
			{UpstreamID: "u1", HealthStatus: "healthy", TotalRequests: 2, TotalTokens: 80},
		},
	})
	require.NoError(t, err)

	err = service.RecordPublicBenefitUsage(ctx, "public-benefit/u1", 20)
	require.NoError(t, err)

	state, err := service.PublicBenefitRuntimeState(ctx)
	require.NoError(t, err)
	require.Len(t, state.Upstreams, 1)
	require.EqualValues(t, 3, state.Upstreams[0].TotalRequests)
	require.EqualValues(t, 100, state.Upstreams[0].TotalTokens)
	require.NotNil(t, state.Upstreams[0].LastSwitchAt)
	require.Len(t, state.DailyUsage, 1)
	require.EqualValues(t, 1, state.DailyUsage[0].TotalRequests)
	require.EqualValues(t, 20, state.DailyUsage[0].TotalTokens)
	require.Equal(t, []string{"u1"}, state.DailyUsage[0].UsedUpstreams)
}

func TestSystemService_SetPublicBenefitHubConfig_RejectReservedPublicAPIKey(t *testing.T) {
	service, ctx := setupPublicBenefitSystemService(t)

	err := service.SetPublicBenefitHubConfig(ctx, objects.PublicBenefitHubConfig{
		Outbound: objects.PublicBenefitOutboundConfig{
			Enabled:      true,
			PublicAPIKey: NoAuthAPIKeyValue,
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reserved noauth api key value")
}
