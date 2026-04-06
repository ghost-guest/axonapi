package biz

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xcache"
	"github.com/looplj/axonhub/llm/httpclient"
)

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
			ID:           "p1",
			Name:         "provider-1",
			BaseURL:      "https://provider.example",
			Enabled:      true,
			AutoCheckIn:  true,
			CheckInPath:  "/api/user/checkin",
			BalancePath:  "",
			Kind:         objects.PublicBenefitProviderKindGeneric,
			AuthType:     objects.PublicBenefitAuthTypeCookie,
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
