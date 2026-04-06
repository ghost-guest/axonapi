package biz

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aptible/supercronic/cronexpr"
	"github.com/samber/lo"
	"github.com/tidwall/gjson"
	"github.com/zhenzou/executors"
	"go.uber.org/fx"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm/httpclient"
)

type PublicBenefitRuntimeServiceParams struct {
	fx.In

	Ent            *ent.Client
	SystemService  *SystemService
	ChannelService *ChannelService
	HttpClient     *httpclient.HttpClient
}

type PublicBenefitRuntimeService struct {
	*AbstractService

	SystemService  *SystemService
	ChannelService *ChannelService
	httpClient     *httpclient.HttpClient
	executor       executors.ScheduledExecutor
}

func NewPublicBenefitRuntimeService(params PublicBenefitRuntimeServiceParams) *PublicBenefitRuntimeService {
	return &PublicBenefitRuntimeService{
		AbstractService: &AbstractService{db: params.Ent},
		SystemService:   params.SystemService,
		ChannelService:  params.ChannelService,
		httpClient:      params.HttpClient,
		executor:        executors.NewPoolScheduleExecutor(executors.WithMaxConcurrent(1)),
	}
}

func (svc *PublicBenefitRuntimeService) Start(ctx context.Context) error {
	_, err := svc.executor.ScheduleFuncAtCronRate(func(_ context.Context) {
		runCtx := authz.WithSystemBypass(ent.NewContext(context.Background(), svc.db), "public-benefit-runtime-sync")
		if err := svc.RunSync(runCtx); err != nil {
			log.Warn(runCtx, "public benefit runtime sync failed", log.Cause(err))
		}
	}, executors.CRONRule{Expr: "*/10 * * * *"})

	return err
}

func (svc *PublicBenefitRuntimeService) Stop(ctx context.Context) error {
	return svc.executor.Shutdown(ctx)
}

func (svc *PublicBenefitRuntimeService) RunSync(ctx context.Context) error {
	cfg, err := svc.SystemService.PublicBenefitHubConfig(ctx)
	if err != nil {
		return err
	}

	current, err := svc.SystemService.PublicBenefitRuntimeState(ctx)
	if err != nil {
		return err
	}

	next := &objects.PublicBenefitRuntimeState{
		Providers:  make([]objects.PublicBenefitProviderRuntime, 0, len(cfg.Providers)),
		Upstreams:  make([]objects.PublicBenefitUpstreamRuntime, 0, len(cfg.Upstreams)),
		DailyUsage: current.DailyUsage,
	}

	now := time.Now()
	currentProviderRuntimeByID := make(map[string]objects.PublicBenefitProviderRuntime, len(current.Providers))
	for _, item := range current.Providers {
		currentProviderRuntimeByID[item.ProviderID] = item
	}

	for _, provider := range cfg.Providers {
		next.Providers = append(next.Providers, svc.syncProviderRuntime(ctx, provider, currentProviderRuntimeByID[provider.ID], now, false))
	}

	for _, upstream := range cfg.Upstreams {
		upstreamRuntime := svc.syncUpstreamRuntime(ctx, upstream, now)
		next.Upstreams = append(next.Upstreams, upstreamRuntime)
		if upstream.Enabled {
			if err := svc.upsertChannelForUpstream(ctx, upstream, upstreamRuntime); err != nil {
				upstreamRuntime.LastError = err.Error()
			}
		}
	}

	return svc.SystemService.SetPublicBenefitRuntimeState(ctx, *next)
}

func (svc *PublicBenefitRuntimeService) syncProviderRuntime(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
	previous objects.PublicBenefitProviderRuntime,
	now time.Time,
	forceCheckIn bool,
) objects.PublicBenefitProviderRuntime {
	runtime := objects.PublicBenefitProviderRuntime{
		ProviderID:        provider.ID,
		AccountName:       provider.Name,
		LastCheckInAt:     previous.LastCheckInAt,
		LastCheckInStatus: previous.LastCheckInStatus,
	}

	if !provider.Enabled {
		runtime.LastError = "provider disabled"
		return runtime
	}

	balance, usage, currency, err := svc.fetchProviderBalance(ctx, provider)
	if err != nil {
		runtime.LastError = err.Error()
	} else {
		runtime.Balance = balance
		runtime.TotalUsage = usage
		runtime.Currency = currency
		runtime.LastBalanceAt = lo.ToPtr(now)
	}

	if forceCheckIn || shouldPerformProviderCheckIn(provider, previous, now) {
		checkinErr := svc.tryProviderCheckIn(ctx, provider)
		runtime.LastCheckInAt = lo.ToPtr(now)
		if checkinErr != nil {
			runtime.LastCheckInStatus = "failed"
			if runtime.LastError == "" {
				runtime.LastError = checkinErr.Error()
			}
		} else {
			runtime.LastCheckInStatus = "ok"
		}
	}

	return runtime
}

func (svc *PublicBenefitRuntimeService) fetchProviderBalance(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) (float64, float64, string, error) {
	switch provider.Kind {
	case objects.PublicBenefitProviderKindNewAPI, objects.PublicBenefitProviderKindOneAPI, objects.PublicBenefitProviderKindOneHub, objects.PublicBenefitProviderKindDoneHub:
		return svc.fetchNewAPIBalance(ctx, provider)
	case objects.PublicBenefitProviderKindAnyRouter:
		return svc.fetchAnyRouterBalance(ctx, provider)
	case objects.PublicBenefitProviderKindCubence:
		return svc.fetchCubenceBalance(ctx, provider)
	case objects.PublicBenefitProviderKindNekoCode:
		return svc.fetchNekoCodeBalance(ctx, provider)
	case objects.PublicBenefitProviderKindSub2API:
		return svc.fetchSub2APIBalance(ctx, provider)
	case objects.PublicBenefitProviderKindYesCode:
		return svc.fetchYesCodeBalance(ctx, provider)
	default:
		return svc.fetchGenericProviderBalance(ctx, provider)
	}
}

func (svc *PublicBenefitRuntimeService) tryProviderCheckIn(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) error {
	req, err := buildPublicBenefitCheckInRequest(provider)
	if err != nil {
		return err
	}
	if req == nil {
		return nil
	}

	resp, err := svc.httpClient.Do(ctx, req)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("provider checkin failed with status %d", resp.StatusCode)
	}
	if !publicBenefitCheckInSucceeded(provider, resp.Body) {
		return fmt.Errorf("provider checkin response indicates failure")
	}

	return nil
}

func (svc *PublicBenefitRuntimeService) syncUpstreamRuntime(
	ctx context.Context,
	upstream objects.PublicBenefitUpstreamSite,
	now time.Time,
) objects.PublicBenefitUpstreamRuntime {
	runtime := objects.PublicBenefitUpstreamRuntime{
		UpstreamID: upstream.ID,
	}

	if !upstream.Enabled {
		runtime.HealthStatus = "disabled"
		return runtime
	}

	modelResult, err := NewModelFetcher(svc.httpClient, svc.ChannelService).FetchModels(ctx, FetchModelsInput{
		ChannelType: channel.TypeOpenaiResponses.String(),
		BaseURL:     upstream.BaseURL,
		APIKey:      lo.ToPtr(upstream.APIKey),
	})
	if err != nil {
		runtime.HealthStatus = "error"
		runtime.LastError = err.Error()
		return runtime
	}

	if modelResult.Error != nil {
		runtime.HealthStatus = "degraded"
		runtime.LastError = *modelResult.Error
	} else {
		runtime.HealthStatus = "healthy"
	}

	runtime.AvailableModels = lo.Map(modelResult.Models, func(item ModelIdentify, _ int) string { return item.ID })
	runtime.LastModelSyncAt = lo.ToPtr(now)
	runtime.LastHealthCheckAt = lo.ToPtr(now)

	return runtime
}

func (svc *PublicBenefitRuntimeService) upsertChannelForUpstream(
	ctx context.Context,
	upstream objects.PublicBenefitUpstreamSite,
	runtime objects.PublicBenefitUpstreamRuntime,
) error {
	channelName := "public-benefit/" + upstream.ID
	supportedModels := runtime.AvailableModels
	if len(supportedModels) == 0 {
		supportedModels = []string{"gpt-5.4"}
	}

	settings := &objects.ChannelSettings{
		ModelMappings: []objects.ModelMapping{},
	}

	existing, err := svc.entFromContext(ctx).Channel.Query().
		Where(channel.Name(channelName)).
		First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}

	status := channel.StatusEnabled
	if runtime.HealthStatus != "healthy" && runtime.HealthStatus != "available" && runtime.HealthStatus != "degraded" {
		status = channel.StatusDisabled
	}

	if existing == nil {
		created, err := svc.ChannelService.CreateChannel(ctx, ent.CreateChannelInput{
			Type:             channel.TypeOpenaiResponses,
			Name:             channelName,
			BaseURL:          lo.ToPtr(upstream.BaseURL),
			Credentials:      objects.ChannelCredentials{APIKey: upstream.APIKey},
			SupportedModels:  supportedModels,
			ManualModels:     []string{},
			DefaultTestModel: supportedModels[0],
			Settings:         settings,
		})
		if err != nil {
			return err
		}
		if _, err := svc.ChannelService.UpdateChannelStatus(ctx, created.ID, status); err != nil {
			return err
		}

		return nil
	}

	_, err = svc.ChannelService.UpdateChannel(ctx, existing.ID, &ent.UpdateChannelInput{
		BaseURL:          lo.ToPtr(upstream.BaseURL),
		Credentials:      lo.ToPtr(objects.ChannelCredentials{APIKey: upstream.APIKey}),
		SupportedModels:  supportedModels,
		DefaultTestModel: lo.ToPtr(supportedModels[0]),
		Settings:         settings,
		Remark:           lo.ToPtr("managed by public benefit runtime"),
	})
	if err != nil {
		return err
	}

	_, err = svc.ChannelService.UpdateChannelStatus(ctx, existing.ID, status)
	return err
}

func ApplyPublicBenefitAuth(headers http.Header, provider objects.PublicBenefitProviderAccount) {
	if provider.APIKey != "" {
		headers.Set("Authorization", "Bearer "+provider.APIKey)
		headers.Set("X-API-Key", provider.APIKey)
		headers.Set("Api-Key", provider.APIKey)
	}
	if provider.Token != "" {
		headers.Set("Authorization", "Bearer "+provider.Token)
		headers.Set("X-Access-Token", provider.Token)
	}
	if provider.Cookie != "" {
		headers.Set("Cookie", provider.Cookie)
	}
	if provider.Username != "" {
		headers.Set("X-User-Name", provider.Username)
		headers.Set("X-User-ID", provider.Username)
		headers.Set("New-Api-User", provider.Username)
	}
}

func ParseNewAPIUserIDFromCookie(cookie string) (string, bool) {
	sessionValue := strings.TrimSpace(cookie)
	if sessionValue == "" {
		return "", false
	}

	if strings.Contains(sessionValue, "session=") {
		if index := strings.Index(sessionValue, "session="); index >= 0 {
			sessionValue = sessionValue[index+len("session="):]
			if end := strings.Index(sessionValue, ";"); end >= 0 {
				sessionValue = sessionValue[:end]
			}
		}
	}

	sessionValue = strings.Join(strings.Fields(sessionValue), "")
	if sessionValue == "" {
		return "", false
	}

	decodedOuter, err := decodePublicBenefitURLSafeBase64(sessionValue)
	if err != nil {
		return "", false
	}

	parts := strings.Split(string(decodedOuter), "|")
	if len(parts) < 2 {
		return "", false
	}

	gobData, err := decodePublicBenefitURLSafeBase64(parts[1])
	if err != nil {
		return "", false
	}

	idPattern := []byte{0x02, 'i', 'd', 0x03, 'i', 'n', 't'}
	idIndex := slices.Index(gobData, idPattern[0])
	for idIndex >= 0 && idIndex+len(idPattern) <= len(gobData) {
		if slices.Equal(gobData[idIndex:idIndex+len(idPattern)], idPattern) {
			break
		}
		next := slices.Index(gobData[idIndex+1:], idPattern[0])
		if next < 0 {
			idIndex = -1
			break
		}
		idIndex += 1 + next
	}
	if idIndex < 0 {
		return "", false
	}

	valueStart := idIndex + len(idPattern) + 2
	if valueStart+1 >= len(gobData) || gobData[valueStart] != 0 {
		return "", false
	}

	marker := int(gobData[valueStart+1])
	if marker < 0x80 {
		return "", false
	}

	length := 256 - marker
	if valueStart+2+length > len(gobData) {
		return "", false
	}

	value := 0
	for _, b := range gobData[valueStart+2 : valueStart+2+length] {
		value = (value << 8) | int(b)
	}

	return strconv.Itoa(value >> 1), true
}

func decodePublicBenefitURLSafeBase64(value string) ([]byte, error) {
	cleaned := strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "").Replace(strings.TrimSpace(value))
	if cleaned == "" {
		return nil, fmt.Errorf("empty base64 payload")
	}
	if mod := len(cleaned) % 4; mod != 0 {
		cleaned += strings.Repeat("=", 4-mod)
	}
	cleaned = strings.ReplaceAll(cleaned, "-", "+")
	cleaned = strings.ReplaceAll(cleaned, "_", "/")
	return base64.StdEncoding.DecodeString(cleaned)
}

func (svc *PublicBenefitRuntimeService) fetchGenericProviderBalance(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) (float64, float64, string, error) {
	endpoint := strings.TrimSpace(provider.BalancePath)
	if endpoint == "" {
		if extraPath, ok := publicBenefitExtraString(provider.Extra, "balance_path"); ok {
			endpoint = extraPath
		} else {
			endpoint = "/api/user/self"
		}
	}

	req := buildPublicBenefitRequest(provider, endpoint, publicBenefitExtraStringOrDefault(provider.Extra, "balance_method", http.MethodGet), publicBenefitExtraStringOrDefault(provider.Extra, "balance_body", ""), publicBenefitExtraMap(provider.Extra, "balance_headers"))

	resp, err := svc.httpClient.Do(ctx, req)
	if err != nil {
		return 0, 0, "", err
	}
	if resp.StatusCode >= 400 {
		return 0, 0, "", fmt.Errorf("provider balance request failed with status %d", resp.StatusCode)
	}

	balance := extractFloatFromJSONPaths(resp.Body,
		publicBenefitExtraStringSlice(provider.Extra, "balance_paths"),
		[]string{"data.quota", "data.balance", "quota", "balance", "data.remain_quota"},
	)
	usage := extractFloatFromJSONPaths(resp.Body,
		publicBenefitExtraStringSlice(provider.Extra, "usage_paths"),
		[]string{"data.used_quota", "data.usedQuota", "used_quota", "usedQuota", "data.usage", "usage"},
	)
	currency := extractStringFromJSONPaths(resp.Body,
		publicBenefitExtraStringSlice(provider.Extra, "currency_paths"),
		[]string{"data.currency", "currency"},
	)
	if currency == "" {
		currency = "USD"
	}

	return normalizeQuotaBalance(balance), normalizeQuotaBalance(usage), currency, nil
}

func (svc *PublicBenefitRuntimeService) fetchNewAPIBalance(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) (float64, float64, string, error) {
	endpoint := strings.TrimSpace(provider.BalancePath)
	if endpoint == "" {
		endpoint = "/api/user/self"
	}

	req := &httpclient.Request{
		Method:  http.MethodGet,
		URL:     provider.BaseURL + endpoint,
		Headers: make(http.Header),
	}
	ApplyPublicBenefitAuth(req.Headers, provider)

	resp, err := svc.httpClient.Do(ctx, req)
	if err != nil {
		return 0, 0, "", err
	}
	if resp.StatusCode >= 400 {
		return 0, 0, "", fmt.Errorf("new api balance request failed with status %d", resp.StatusCode)
	}

	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			Quota     float64 `json:"quota"`
			UsedQuota float64 `json:"used_quota"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return 0, 0, "", err
	}

	return normalizeQuotaBalance(payload.Data.Quota), normalizeQuotaBalance(payload.Data.UsedQuota), "USD", nil
}

func (svc *PublicBenefitRuntimeService) fetchAnyRouterBalance(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) (float64, float64, string, error) {
	return svc.fetchNewAPIBalance(ctx, provider)
}

func (svc *PublicBenefitRuntimeService) fetchCubenceBalance(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) (float64, float64, string, error) {
	return svc.fetchGenericProviderBalance(ctx, provider)
}

func (svc *PublicBenefitRuntimeService) fetchNekoCodeBalance(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) (float64, float64, string, error) {
	return svc.fetchGenericProviderBalance(ctx, provider)
}

func (svc *PublicBenefitRuntimeService) fetchSub2APIBalance(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) (float64, float64, string, error) {
	reqMe := &httpclient.Request{
		Method:  http.MethodGet,
		URL:     provider.BaseURL + "/api/v1/auth/me?timezone=Asia/Shanghai",
		Headers: make(http.Header),
	}
	ApplyPublicBenefitAuth(reqMe.Headers, provider)

	resp, err := svc.httpClient.Do(ctx, reqMe)
	if err != nil {
		return 0, 0, "", err
	}
	if resp.StatusCode >= 400 {
		return 0, 0, "", fmt.Errorf("sub2api auth/me failed with status %d", resp.StatusCode)
	}

	var payload struct {
		Code int `json:"code"`
		Data struct {
			Balance float64 `json:"balance"`
			Points  float64 `json:"points"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &payload); err != nil {
		return 0, 0, "", err
	}

	return payload.Data.Balance + payload.Data.Points, 0, "USD", nil
}

func (svc *PublicBenefitRuntimeService) fetchYesCodeBalance(
	ctx context.Context,
	provider objects.PublicBenefitProviderAccount,
) (float64, float64, string, error) {
	endpoints := []string{
		"/api/v1/user/balance",
		"/api/v1/auth/profile",
	}

	var totalBalance float64
	var weeklySpent float64
	for _, endpoint := range endpoints {
		req := &httpclient.Request{
			Method:  http.MethodGet,
			URL:     provider.BaseURL + endpoint,
			Headers: make(http.Header),
		}
		ApplyPublicBenefitAuth(req.Headers, provider)

		resp, err := svc.httpClient.Do(ctx, req)
		if err != nil {
			if endpoint == endpoints[len(endpoints)-1] {
				return 0, 0, "", err
			}
			continue
		}
		if resp.StatusCode >= 400 {
			if endpoint == endpoints[len(endpoints)-1] {
				return 0, 0, "", fmt.Errorf("yescode request failed with status %d", resp.StatusCode)
			}
			continue
		}

		totalBalance += extractFloatFromJSONPaths(resp.Body, nil, []string{
			"total_balance",
			"data.total_balance",
			"pay_as_you_go_balance",
			"subscription_balance",
		})
		weeklySpent = extractFloatFromJSONPaths(resp.Body, nil, []string{
			"weekly_spent_balance",
			"current_week_spend",
			"data.weekly_spent_balance",
		})
	}

	return totalBalance, weeklySpent, "USD", nil
}

func (svc *PublicBenefitRuntimeService) RefreshProvider(ctx context.Context, providerID string) error {
	cfg, err := svc.SystemService.PublicBenefitHubConfig(ctx)
	if err != nil {
		return err
	}
	provider, ok := findPublicBenefitProvider(cfg.Providers, providerID)
	if !ok {
		return fmt.Errorf("provider %s not found", providerID)
	}

	state, err := svc.SystemService.PublicBenefitRuntimeState(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	runtime := svc.syncProviderRuntime(ctx, provider, findPublicBenefitProviderRuntime(state.Providers, providerID), now, false)
	state.Providers = upsertPublicBenefitProviderRuntime(state.Providers, runtime)

	return svc.SystemService.SetPublicBenefitRuntimeState(ctx, *state)
}

func (svc *PublicBenefitRuntimeService) CheckInProvider(ctx context.Context, providerID string) error {
	cfg, err := svc.SystemService.PublicBenefitHubConfig(ctx)
	if err != nil {
		return err
	}
	provider, ok := findPublicBenefitProvider(cfg.Providers, providerID)
	if !ok {
		return fmt.Errorf("provider %s not found", providerID)
	}

	state, err := svc.SystemService.PublicBenefitRuntimeState(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	runtime := svc.syncProviderRuntime(ctx, provider, findPublicBenefitProviderRuntime(state.Providers, providerID), now, true)
	state.Providers = upsertPublicBenefitProviderRuntime(state.Providers, runtime)

	return svc.SystemService.SetPublicBenefitRuntimeState(ctx, *state)
}

func (svc *PublicBenefitRuntimeService) RefreshUpstream(ctx context.Context, upstreamID string) error {
	cfg, err := svc.SystemService.PublicBenefitHubConfig(ctx)
	if err != nil {
		return err
	}
	upstream, ok := findPublicBenefitUpstream(cfg.Upstreams, upstreamID)
	if !ok {
		return fmt.Errorf("upstream %s not found", upstreamID)
	}

	state, err := svc.SystemService.PublicBenefitRuntimeState(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	runtime := svc.syncUpstreamRuntime(ctx, upstream, now)
	state.Upstreams = upsertPublicBenefitUpstreamRuntime(state.Upstreams, runtime)
	if upstream.Enabled {
		if err := svc.upsertChannelForUpstream(ctx, upstream, runtime); err != nil {
			runtime.LastError = err.Error()
			state.Upstreams = upsertPublicBenefitUpstreamRuntime(state.Upstreams, runtime)
		}
	}

	return svc.SystemService.SetPublicBenefitRuntimeState(ctx, *state)
}

func shouldPerformProviderCheckIn(
	provider objects.PublicBenefitProviderAccount,
	previous objects.PublicBenefitProviderRuntime,
	now time.Time,
) bool {
	if !provider.AutoCheckIn {
		return false
	}
	if previous.LastCheckInAt == nil {
		return true
	}

	expr := strings.TrimSpace(provider.CheckInCron)
	if expr == "" {
		return now.Sub(*previous.LastCheckInAt) >= 24*time.Hour
	}

	parsed, err := cronexpr.ParseStrict(expr)
	if err != nil {
		return now.Sub(*previous.LastCheckInAt) >= 24*time.Hour
	}

	nextAt := parsed.Next(previous.LastCheckInAt.Add(time.Second))
	return !nextAt.After(now)
}

func buildPublicBenefitCheckInRequest(provider objects.PublicBenefitProviderAccount) (*httpclient.Request, error) {
	paths := publicBenefitCheckInPaths(provider)
	if len(paths) == 0 {
		return nil, nil
	}

	method := publicBenefitExtraStringOrDefault(provider.Extra, "check_in_method", http.MethodPost)
	body := publicBenefitExtraStringOrDefault(provider.Extra, "check_in_body", "")
	headers := publicBenefitExtraMap(provider.Extra, "check_in_headers")
	for _, endpoint := range paths {
		return buildPublicBenefitRequest(provider, endpoint, method, body, headers), nil
	}

	return nil, nil
}

func buildPublicBenefitRequest(
	provider objects.PublicBenefitProviderAccount,
	endpoint string,
	method string,
	body string,
	extraHeaders map[string]string,
) *httpclient.Request {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}

	req := &httpclient.Request{
		Method:  method,
		URL:     provider.BaseURL + strings.TrimSpace(endpoint),
		Headers: make(http.Header),
	}
	ApplyPublicBenefitAuth(req.Headers, provider)
	for key, value := range extraHeaders {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		req.Headers.Set(key, renderPublicBenefitTemplate(value, provider))
	}

	renderedBody := strings.TrimSpace(renderPublicBenefitTemplate(body, provider))
	if renderedBody != "" && method != http.MethodGet {
		req.Body = []byte(renderedBody)
		if req.Headers.Get("Content-Type") == "" {
			req.Headers.Set("Content-Type", "application/json")
		}
	}

	return req
}

func publicBenefitCheckInPaths(provider objects.PublicBenefitProviderAccount) []string {
	if path := strings.TrimSpace(provider.CheckInPath); path != "" {
		return []string{path}
	}
	if paths := publicBenefitExtraStringSlice(provider.Extra, "check_in_paths"); len(paths) > 0 {
		return paths
	}

	switch provider.Kind {
	case objects.PublicBenefitProviderKindNewAPI, objects.PublicBenefitProviderKindOneAPI, objects.PublicBenefitProviderKindOneHub, objects.PublicBenefitProviderKindDoneHub, objects.PublicBenefitProviderKindAnyRouter:
		return []string{"/api/user/checkin"}
	case objects.PublicBenefitProviderKindSub2API:
		return []string{"/api/user/checkin", "/api/v1/user/checkin"}
	default:
		return nil
	}
}

func publicBenefitCheckInSucceeded(provider objects.PublicBenefitProviderAccount, body []byte) bool {
	successPaths := publicBenefitExtraStringSlice(provider.Extra, "check_in_success_paths")
	if len(successPaths) > 0 {
		for _, path := range successPaths {
			result := gjson.GetBytes(body, path)
			if result.Exists() && isTruthyJSONResult(result) {
				return true
			}
		}
		return false
	}

	if len(body) == 0 {
		return true
	}

	for _, path := range []string{"success", "data.success", "code", "status"} {
		result := gjson.GetBytes(body, path)
		if !result.Exists() {
			continue
		}
		if isTruthyJSONResult(result) {
			return true
		}
	}

	message := strings.ToLower(strings.TrimSpace(extractStringFromJSONPaths(body, nil, []string{"message", "msg"})))
	if strings.Contains(message, "success") || strings.Contains(message, "ok") || strings.Contains(message, "成功") || strings.Contains(message, "已签到") {
		return true
	}

	return !gjson.GetBytes(body, "error").Exists()
}

func isTruthyJSONResult(result gjson.Result) bool {
	switch result.Type {
	case gjson.True:
		return true
	case gjson.False:
		return false
	case gjson.Number:
		value := result.Float()
		return value == 0 || value == 1 || value == 200
	case gjson.String:
		value := strings.ToLower(strings.TrimSpace(result.String()))
		return value == "ok" || value == "success" || value == "true" || value == "0" || value == "200"
	default:
		return false
	}
}

func extractFloatFromJSONPaths(body []byte, preferred []string, defaults []string) float64 {
	paths := append([]string{}, preferred...)
	paths = append(paths, defaults...)
	for _, path := range paths {
		result := gjson.GetBytes(body, strings.TrimSpace(path))
		if !result.Exists() {
			continue
		}
		switch result.Type {
		case gjson.Number:
			return result.Float()
		case gjson.String:
			if value := strings.TrimSpace(result.String()); value != "" {
				if parsed := gjson.Parse(value); parsed.Type == gjson.Number {
					return parsed.Float()
				}
			}
		}
	}

	return 0
}

func extractStringFromJSONPaths(body []byte, preferred []string, defaults []string) string {
	paths := append([]string{}, preferred...)
	paths = append(paths, defaults...)
	for _, path := range paths {
		result := gjson.GetBytes(body, strings.TrimSpace(path))
		if result.Exists() && strings.TrimSpace(result.String()) != "" {
			return strings.TrimSpace(result.String())
		}
	}

	return ""
}

func normalizeQuotaBalance(value float64) float64 {
	if value > 100000 {
		return value / 500000.0
	}
	return value
}

func publicBenefitExtraString(extra map[string]any, key string) (string, bool) {
	if len(extra) == 0 {
		return "", false
	}
	value, ok := extra[key]
	if !ok {
		return "", false
	}
	strValue, ok := value.(string)
	if !ok || strings.TrimSpace(strValue) == "" {
		return "", false
	}

	return strings.TrimSpace(strValue), true
}

func publicBenefitExtraStringOrDefault(extra map[string]any, key string, defaultValue string) string {
	if value, ok := publicBenefitExtraString(extra, key); ok {
		return value
	}
	return defaultValue
}

func publicBenefitExtraStringSlice(extra map[string]any, key string) []string {
	if len(extra) == 0 {
		return nil
	}
	raw, ok := extra[key]
	if !ok {
		return nil
	}

	switch typed := raw.(type) {
	case []string:
		return compactStringSlice(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			strValue, ok := item.(string)
			if ok {
				values = append(values, strValue)
			}
		}
		return compactStringSlice(values)
	default:
		return nil
	}
}

func publicBenefitExtraMap(extra map[string]any, key string) map[string]string {
	if len(extra) == 0 {
		return nil
	}
	raw, ok := extra[key]
	if !ok {
		return nil
	}
	typed, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	result := make(map[string]string, len(typed))
	for mapKey, value := range typed {
		strValue, ok := value.(string)
		if ok && strings.TrimSpace(strValue) != "" {
			result[mapKey] = strValue
		}
	}
	return result
}

func renderPublicBenefitTemplate(raw string, provider objects.PublicBenefitProviderAccount) string {
	replacer := strings.NewReplacer(
		"{{username}}", provider.Username,
		"{{password}}", provider.Password,
		"{{cookie}}", provider.Cookie,
		"{{token}}", provider.Token,
		"{{api_key}}", provider.APIKey,
		"{{name}}", provider.Name,
	)
	return replacer.Replace(raw)
}

func compactStringSlice(values []string) []string {
	result := make([]string, 0, len(values))
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" || slices.Contains(result, item) {
			continue
		}
		result = append(result, item)
	}
	return result
}

func findPublicBenefitProvider(items []objects.PublicBenefitProviderAccount, providerID string) (objects.PublicBenefitProviderAccount, bool) {
	for _, item := range items {
		if item.ID == providerID {
			return item, true
		}
	}

	return objects.PublicBenefitProviderAccount{}, false
}

func findPublicBenefitProviderRuntime(items []objects.PublicBenefitProviderRuntime, providerID string) objects.PublicBenefitProviderRuntime {
	for _, item := range items {
		if item.ProviderID == providerID {
			return item
		}
	}

	return objects.PublicBenefitProviderRuntime{ProviderID: providerID}
}

func upsertPublicBenefitProviderRuntime(
	items []objects.PublicBenefitProviderRuntime,
	runtime objects.PublicBenefitProviderRuntime,
) []objects.PublicBenefitProviderRuntime {
	for i := range items {
		if items[i].ProviderID == runtime.ProviderID {
			items[i] = runtime
			return items
		}
	}

	return append(items, runtime)
}

func findPublicBenefitUpstream(items []objects.PublicBenefitUpstreamSite, upstreamID string) (objects.PublicBenefitUpstreamSite, bool) {
	for _, item := range items {
		if item.ID == upstreamID {
			return item, true
		}
	}

	return objects.PublicBenefitUpstreamSite{}, false
}

func upsertPublicBenefitUpstreamRuntime(
	items []objects.PublicBenefitUpstreamRuntime,
	runtime objects.PublicBenefitUpstreamRuntime,
) []objects.PublicBenefitUpstreamRuntime {
	for i := range items {
		if items[i].UpstreamID == runtime.UpstreamID {
			items[i] = runtime
			return items
		}
	}

	return append(items, runtime)
}
