package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/contexts"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/shared"
)

const (
	SystemKeyPublicBenefitConfig  = "public_benefit_hub_config"
	SystemKeyPublicBenefitRuntime = "public_benefit_hub_runtime"
)

var defaultPublicBenefitHubConfig = objects.PublicBenefitHubConfig{
	Providers: []objects.PublicBenefitProviderAccount{},
	Upstreams: []objects.PublicBenefitUpstreamSite{},
	Outbound: objects.PublicBenefitOutboundConfig{
		Enabled:                   false,
		DefaultRouteMode:          objects.PublicBenefitRouteModeAdaptive,
		SessionAffinityEnabled:    true,
		SessionAffinityTTLSeconds: 1800,
		DefaultClaudeFallback: []string{
			"claude-sonnet-4-6",
			"claude-sonnet-4-5",
			"MiniMax-M2.7-highspeed",
		},
		DefaultCodexFallback: []string{
			"gpt-5-codex",
			"gpt-5.4-codex",
			"gpt-5.4",
		},
		DefaultOpenCodeFallback: []string{
			"gpt-5.4",
		},
		DefaultGeminiFallback: []string{
			"gemini-2.5-pro",
			"gemini-2.5-flash",
		},
		DefaultGenericFallback: []string{
			"gpt-5.4",
		},
	},
}

var defaultPublicBenefitRuntimeState = objects.PublicBenefitRuntimeState{
	Providers:  []objects.PublicBenefitProviderRuntime{},
	Upstreams:  []objects.PublicBenefitUpstreamRuntime{},
	DailyUsage: []objects.PublicBenefitUsageSnapshot{},
}

type PublicBenefitUpstreamPolicy struct {
	UpstreamID              string
	BaseURL                 string
	Enabled                 bool
	Healthy                 bool
	SupportsRequestedFamily bool
	AvailableModels         []string
	Weight                  int
}

type PublicBenefitSessionAffinity struct {
	SessionKey string
	BaseURL    string
	ExpiresAt  time.Time
}

type publicBenefitAffinityBinding struct {
	BaseURL   string
	ExpiresAt time.Time
	UpdatedAt time.Time
}

func (s *SystemService) PublicBenefitHubConfig(ctx context.Context) (*objects.PublicBenefitHubConfig, error) {
	value, err := s.getSystemValue(ctx, SystemKeyPublicBenefitConfig)
	if err != nil {
		if ent.IsNotFound(err) {
			cfg := defaultPublicBenefitHubConfig
			return &cfg, nil
		}

		return nil, fmt.Errorf("failed to get public benefit hub config: %w", err)
	}

	var cfg objects.PublicBenefitHubConfig
	if err := json.Unmarshal([]byte(value), &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal public benefit hub config: %w", err)
	}

	normalizePublicBenefitHubConfig(&cfg)

	return &cfg, nil
}

func (s *SystemService) SetPublicBenefitHubConfig(ctx context.Context, cfg objects.PublicBenefitHubConfig) error {
	normalizePublicBenefitHubConfig(&cfg)
	if err := validatePublicBenefitHubConfig(cfg); err != nil {
		return err
	}

	jsonBytes, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("failed to marshal public benefit hub config: %w", err)
	}

	return s.setSystemValue(ctx, SystemKeyPublicBenefitConfig, string(jsonBytes))
}

func (s *SystemService) PublicBenefitRuntimeState(ctx context.Context) (*objects.PublicBenefitRuntimeState, error) {
	value, err := s.getSystemValue(ctx, SystemKeyPublicBenefitRuntime)
	if err != nil {
		if ent.IsNotFound(err) {
			state := defaultPublicBenefitRuntimeState
			return &state, nil
		}

		return nil, fmt.Errorf("failed to get public benefit runtime state: %w", err)
	}

	var state objects.PublicBenefitRuntimeState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal public benefit runtime state: %w", err)
	}

	normalizePublicBenefitRuntimeState(&state)

	return &state, nil
}

func (s *SystemService) SetPublicBenefitRuntimeState(ctx context.Context, state objects.PublicBenefitRuntimeState) error {
	normalizePublicBenefitRuntimeState(&state)

	jsonBytes, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal public benefit runtime state: %w", err)
	}

	return s.setSystemValue(ctx, SystemKeyPublicBenefitRuntime, string(jsonBytes))
}

func (s *SystemService) PublicBenefitDashboard(ctx context.Context) (*objects.PublicBenefitDashboard, error) {
	cfg, err := s.PublicBenefitHubConfig(ctx)
	if err != nil {
		return nil, err
	}

	state, err := s.PublicBenefitRuntimeState(ctx)
	if err != nil {
		return nil, err
	}

	dashboard := &objects.PublicBenefitDashboard{
		ProviderCount:        len(cfg.Providers),
		EnabledProviderCount: len(lo.Filter(cfg.Providers, func(item objects.PublicBenefitProviderAccount, _ int) bool { return item.Enabled })),
		UpstreamCount:        len(cfg.Upstreams),
		EnabledUpstreamCount: len(lo.Filter(cfg.Upstreams, func(item objects.PublicBenefitUpstreamSite, _ int) bool { return item.Enabled })),
		Providers:            state.Providers,
		Upstreams:            state.Upstreams,
		DailyUsage:           state.DailyUsage,
		Outbound:             cfg.Outbound,
	}

	for _, provider := range state.Providers {
		dashboard.TotalBalance += provider.Balance
		dashboard.TotalUsage += provider.TotalUsage
	}

	for _, upstream := range state.Upstreams {
		dashboard.TotalRequests += upstream.TotalRequests
		dashboard.TotalTokens += upstream.TotalTokens
		if strings.EqualFold(upstream.HealthStatus, "healthy") || strings.EqualFold(upstream.HealthStatus, "available") {
			dashboard.HealthyUpstreamCount++
		}
	}

	return dashboard, nil
}

func (s *SystemService) RecordPublicBenefitUsage(ctx context.Context, channelName string, totalTokens int64) error {
	if s == nil {
		return nil
	}

	upstreamID, ok := publicBenefitUpstreamIDFromChannelName(channelName)
	if !ok {
		return nil
	}

	state, err := s.PublicBenefitRuntimeState(ctx)
	if err != nil {
		return err
	}

	now := time.Now()
	state.Upstreams = updatePublicBenefitUsageUpstreamRuntime(state.Upstreams, upstreamID, totalTokens, now)
	state.DailyUsage = updatePublicBenefitDailyUsage(state.DailyUsage, upstreamID, totalTokens, now)

	return s.SetPublicBenefitRuntimeState(ctx, *state)
}

func (s *SystemService) ResolvePublicBenefitModelSequence(ctx context.Context, apiFormat llm.APIFormat, requestedModel string) []string {
	cfg, err := s.PublicBenefitHubConfig(ctx)
	if err != nil {
		if log.DebugEnabled(ctx) {
			log.Debug(ctx, "failed to load public benefit config for model sequence", log.Cause(err))
		}

		return compactModelSequence([]string{requestedModel})
	}

	family := resolvePublicBenefitModelFamily(apiFormat, requestedModel)
	policies := s.ResolvePublicBenefitUpstreamPolicies(ctx, apiFormat, requestedModel)

	sequence := []string{requestedModel}
	sequence = append(sequence, resolvePublicBenefitAvailableModels(policies)...)

	switch family {
	case "claude":
		sequence = append(sequence, cfg.Outbound.DefaultClaudeFallback...)
	case "codex":
		sequence = append(sequence, cfg.Outbound.DefaultCodexFallback...)
	case "gemini":
		sequence = append(sequence, cfg.Outbound.DefaultGeminiFallback...)
	case "opencode":
		sequence = append(sequence, cfg.Outbound.DefaultOpenCodeFallback...)
	default:
		sequence = append(sequence, cfg.Outbound.DefaultGenericFallback...)
	}

	if family != "generic" {
		sequence = append(sequence, cfg.Outbound.DefaultGenericFallback...)
	}

	return compactModelSequence(sequence)
}

func (s *SystemService) ResolvePublicBenefitUpstreamPolicies(ctx context.Context, apiFormat llm.APIFormat, requestedModel string) []PublicBenefitUpstreamPolicy {
	cfg, err := s.PublicBenefitHubConfig(ctx)
	if err != nil {
		return nil
	}

	state, err := s.PublicBenefitRuntimeState(ctx)
	if err != nil {
		state = &defaultPublicBenefitRuntimeState
	}

	family := resolvePublicBenefitModelFamily(apiFormat, requestedModel)
	runtimeByID := make(map[string]objects.PublicBenefitUpstreamRuntime, len(state.Upstreams))
	for _, runtime := range state.Upstreams {
		runtimeByID[runtime.UpstreamID] = runtime
	}

	policies := make([]PublicBenefitUpstreamPolicy, 0, len(cfg.Upstreams))
	for _, upstream := range cfg.Upstreams {
		runtime := runtimeByID[upstream.ID]
		policies = append(policies, PublicBenefitUpstreamPolicy{
			UpstreamID:              upstream.ID,
			BaseURL:                 upstream.BaseURL,
			Enabled:                 upstream.Enabled,
			Healthy:                 isPublicBenefitUpstreamHealthy(runtime.HealthStatus),
			SupportsRequestedFamily: upstreamSupportsFamily(upstream, runtime.AvailableModels, family),
			AvailableModels:         compactModelSequence(runtime.AvailableModels),
			Weight:                  upstream.Weight,
		})
	}

	return policies
}

func (s *SystemService) ResolvePublicBenefitSessionAffinity(ctx context.Context, apiFormat llm.APIFormat, requestedModel string) *PublicBenefitSessionAffinity {
	cfg, err := s.PublicBenefitHubConfig(ctx)
	if err != nil || !cfg.Outbound.SessionAffinityEnabled {
		return nil
	}

	sessionKey := publicBenefitSessionKeyFromContext(ctx, apiFormat, requestedModel)
	if sessionKey == "" {
		return nil
	}

	now := time.Now()

	s.mu.RLock()
	binding, ok := s.getPublicBenefitAffinityBindings()[sessionKey]
	s.mu.RUnlock()
	if !ok || binding.BaseURL == "" || (!binding.ExpiresAt.IsZero() && !binding.ExpiresAt.After(now)) {
		return nil
	}

	return &PublicBenefitSessionAffinity{
		SessionKey: sessionKey,
		BaseURL:    binding.BaseURL,
		ExpiresAt:  binding.ExpiresAt,
	}
}

func (s *SystemService) BindPublicBenefitSessionAffinity(ctx context.Context, apiFormat llm.APIFormat, requestedModel, baseURL string) {
	baseURL = normalizePublicBenefitBaseURLValue(baseURL)
	if baseURL == "" {
		return
	}

	cfg, err := s.PublicBenefitHubConfig(ctx)
	if err != nil || !cfg.Outbound.SessionAffinityEnabled {
		return
	}

	sessionKey := publicBenefitSessionKeyFromContext(ctx, apiFormat, requestedModel)
	if sessionKey == "" {
		return
	}

	ttl := time.Duration(cfg.Outbound.SessionAffinityTTLSeconds) * time.Second
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}

	now := time.Now()
	expiresAt := now.Add(ttl)

	s.mu.Lock()
	defer s.mu.Unlock()

	bindings := s.getPublicBenefitAffinityBindings()
	for key, binding := range bindings {
		if binding.ExpiresAt.Before(now) {
			delete(bindings, key)
		}
	}

	bindings[sessionKey] = publicBenefitAffinityBinding{
		BaseURL:   baseURL,
		ExpiresAt: expiresAt,
		UpdatedAt: now,
	}
}

func normalizePublicBenefitHubConfig(cfg *objects.PublicBenefitHubConfig) {
	if cfg == nil {
		return
	}

	if cfg.Providers == nil {
		cfg.Providers = []objects.PublicBenefitProviderAccount{}
	}
	if cfg.Upstreams == nil {
		cfg.Upstreams = []objects.PublicBenefitUpstreamSite{}
	}
	if cfg.Outbound.DefaultRouteMode == "" {
		cfg.Outbound.DefaultRouteMode = defaultPublicBenefitHubConfig.Outbound.DefaultRouteMode
	}
	if cfg.Outbound.SessionAffinityTTLSeconds <= 0 {
		cfg.Outbound.SessionAffinityTTLSeconds = defaultPublicBenefitHubConfig.Outbound.SessionAffinityTTLSeconds
	}
	if len(cfg.Outbound.DefaultClaudeFallback) == 0 {
		cfg.Outbound.DefaultClaudeFallback = append([]string(nil), defaultPublicBenefitHubConfig.Outbound.DefaultClaudeFallback...)
	}
	if len(cfg.Outbound.DefaultCodexFallback) == 0 {
		cfg.Outbound.DefaultCodexFallback = append([]string(nil), defaultPublicBenefitHubConfig.Outbound.DefaultCodexFallback...)
	}
	if len(cfg.Outbound.DefaultOpenCodeFallback) == 0 {
		cfg.Outbound.DefaultOpenCodeFallback = append([]string(nil), defaultPublicBenefitHubConfig.Outbound.DefaultOpenCodeFallback...)
	}
	if len(cfg.Outbound.DefaultGeminiFallback) == 0 {
		cfg.Outbound.DefaultGeminiFallback = append([]string(nil), defaultPublicBenefitHubConfig.Outbound.DefaultGeminiFallback...)
	}
	if len(cfg.Outbound.DefaultGenericFallback) == 0 {
		cfg.Outbound.DefaultGenericFallback = append([]string(nil), defaultPublicBenefitHubConfig.Outbound.DefaultGenericFallback...)
	}

	for i := range cfg.Providers {
		cfg.Providers[i].ID = strings.TrimSpace(cfg.Providers[i].ID)
		cfg.Providers[i].Name = strings.TrimSpace(cfg.Providers[i].Name)
		cfg.Providers[i].BaseURL = strings.TrimRight(strings.TrimSpace(cfg.Providers[i].BaseURL), "/")
		cfg.Providers[i].Username = strings.TrimSpace(cfg.Providers[i].Username)
		cfg.Providers[i].BalancePath = strings.TrimSpace(cfg.Providers[i].BalancePath)
		cfg.Providers[i].CheckInPath = strings.TrimSpace(cfg.Providers[i].CheckInPath)
		cfg.Providers[i].Remark = strings.TrimSpace(cfg.Providers[i].Remark)
	}

	for i := range cfg.Upstreams {
		cfg.Upstreams[i].ID = strings.TrimSpace(cfg.Upstreams[i].ID)
		cfg.Upstreams[i].Name = strings.TrimSpace(cfg.Upstreams[i].Name)
		cfg.Upstreams[i].BaseURL = strings.TrimRight(strings.TrimSpace(cfg.Upstreams[i].BaseURL), "/")
		cfg.Upstreams[i].HealthCheckPath = strings.TrimSpace(cfg.Upstreams[i].HealthCheckPath)
		cfg.Upstreams[i].HealthCheckModel = strings.TrimSpace(cfg.Upstreams[i].HealthCheckModel)
		cfg.Upstreams[i].PreferredModelFamily = strings.TrimSpace(cfg.Upstreams[i].PreferredModelFamily)
		cfg.Upstreams[i].Remark = strings.TrimSpace(cfg.Upstreams[i].Remark)
		if cfg.Upstreams[i].RouteMode == "" {
			cfg.Upstreams[i].RouteMode = cfg.Outbound.DefaultRouteMode
		}
		if cfg.Upstreams[i].Weight <= 0 {
			cfg.Upstreams[i].Weight = 1
		}
		if cfg.Upstreams[i].FailureThreshold <= 0 {
			cfg.Upstreams[i].FailureThreshold = 3
		}
		if cfg.Upstreams[i].RecoverThreshold <= 0 {
			cfg.Upstreams[i].RecoverThreshold = 1
		}
		if cfg.Upstreams[i].ModelAllowlist == nil {
			cfg.Upstreams[i].ModelAllowlist = []string{}
		}
		if cfg.Upstreams[i].ModelBlocklist == nil {
			cfg.Upstreams[i].ModelBlocklist = []string{}
		}
	}
}

func normalizePublicBenefitRuntimeState(state *objects.PublicBenefitRuntimeState) {
	if state == nil {
		return
	}
	if state.Providers == nil {
		state.Providers = []objects.PublicBenefitProviderRuntime{}
	}
	if state.Upstreams == nil {
		state.Upstreams = []objects.PublicBenefitUpstreamRuntime{}
	}
	if state.DailyUsage == nil {
		state.DailyUsage = []objects.PublicBenefitUsageSnapshot{}
	}
}

func publicBenefitUpstreamIDFromChannelName(channelName string) (string, bool) {
	channelName = strings.TrimSpace(channelName)
	if !strings.HasPrefix(channelName, "public-benefit/") {
		return "", false
	}

	upstreamID := strings.TrimSpace(strings.TrimPrefix(channelName, "public-benefit/"))
	if upstreamID == "" {
		return "", false
	}

	return upstreamID, true
}

func updatePublicBenefitUsageUpstreamRuntime(
	items []objects.PublicBenefitUpstreamRuntime,
	upstreamID string,
	totalTokens int64,
	now time.Time,
) []objects.PublicBenefitUpstreamRuntime {
	for i := range items {
		if items[i].UpstreamID != upstreamID {
			continue
		}

		items[i].TotalRequests++
		items[i].TotalTokens += totalTokens
		items[i].LastSwitchAt = lo.ToPtr(now)
		if strings.TrimSpace(items[i].HealthStatus) == "" {
			items[i].HealthStatus = "healthy"
		}

		return items
	}

	items = append(items, objects.PublicBenefitUpstreamRuntime{
		UpstreamID:    upstreamID,
		HealthStatus:  "healthy",
		TotalRequests: 1,
		TotalTokens:   totalTokens,
		LastSwitchAt:  lo.ToPtr(now),
	})

	return items
}

func updatePublicBenefitDailyUsage(
	items []objects.PublicBenefitUsageSnapshot,
	upstreamID string,
	totalTokens int64,
	now time.Time,
) []objects.PublicBenefitUsageSnapshot {
	date := now.Format("2006-01-02")
	for i := range items {
		if items[i].Date != date {
			continue
		}

		items[i].TotalRequests++
		items[i].TotalTokens += totalTokens
		if upstreamID != "" && !slices.Contains(items[i].UsedUpstreams, upstreamID) {
			items[i].UsedUpstreams = append(items[i].UsedUpstreams, upstreamID)
		}

		return items
	}

	snapshot := objects.PublicBenefitUsageSnapshot{
		Date:          date,
		TotalRequests: 1,
		TotalTokens:   totalTokens,
	}
	if upstreamID != "" {
		snapshot.UsedUpstreams = []string{upstreamID}
	}

	return append([]objects.PublicBenefitUsageSnapshot{snapshot}, items...)
}

func publicBenefitSessionKeyFromContext(ctx context.Context, apiFormat llm.APIFormat, requestedModel string) string {
	family := resolvePublicBenefitModelFamily(apiFormat, requestedModel)
	if family != "claude" && family != "codex" {
		return ""
	}

	if trace, ok := contexts.GetTrace(ctx); ok && trace != nil && strings.TrimSpace(trace.TraceID) != "" {
		return family + ":trace:" + strings.TrimSpace(trace.TraceID)
	}
	if traceID, ok := contexts.GetTraceID(ctx); ok && strings.TrimSpace(traceID) != "" {
		return family + ":trace:" + strings.TrimSpace(traceID)
	}
	if thread, ok := contexts.GetThread(ctx); ok && thread != nil && strings.TrimSpace(thread.ThreadID) != "" {
		return family + ":thread:" + strings.TrimSpace(thread.ThreadID)
	}
	if sessionID, ok := shared.GetSessionID(ctx); ok && strings.TrimSpace(sessionID) != "" {
		return family + ":session:" + strings.TrimSpace(sessionID)
	}

	return ""
}

func (s *SystemService) getPublicBenefitAffinityBindings() map[string]publicBenefitAffinityBinding {
	if s == nil {
		return map[string]publicBenefitAffinityBinding{}
	}
	if s.publicBenefitAffinityBindings == nil {
		s.publicBenefitAffinityBindings = make(map[string]publicBenefitAffinityBinding)
	}

	return s.publicBenefitAffinityBindings
}

func validatePublicBenefitHubConfig(cfg objects.PublicBenefitHubConfig) error {
	providerIDs := map[string]struct{}{}
	for _, provider := range cfg.Providers {
		if provider.ID == "" {
			return fmt.Errorf("provider id cannot be empty")
		}
		if provider.Name == "" {
			return fmt.Errorf("provider %s name cannot be empty", provider.ID)
		}
		if provider.BaseURL == "" {
			return fmt.Errorf("provider %s base_url cannot be empty", provider.ID)
		}
		if _, exists := providerIDs[provider.ID]; exists {
			return fmt.Errorf("duplicate provider id: %s", provider.ID)
		}
		providerIDs[provider.ID] = struct{}{}
	}

	upstreamIDs := map[string]struct{}{}
	for _, upstream := range cfg.Upstreams {
		if upstream.ID == "" {
			return fmt.Errorf("upstream id cannot be empty")
		}
		if upstream.Name == "" {
			return fmt.Errorf("upstream %s name cannot be empty", upstream.ID)
		}
		if upstream.BaseURL == "" {
			return fmt.Errorf("upstream %s base_url cannot be empty", upstream.ID)
		}
		if upstream.APIKey == "" {
			return fmt.Errorf("upstream %s api_key cannot be empty", upstream.ID)
		}
		if _, exists := upstreamIDs[upstream.ID]; exists {
			return fmt.Errorf("duplicate upstream id: %s", upstream.ID)
		}
		upstreamIDs[upstream.ID] = struct{}{}
		if !slices.Contains([]objects.PublicBenefitRouteMode{
			objects.PublicBenefitRouteModePriority,
			objects.PublicBenefitRouteModeRoundRobin,
			objects.PublicBenefitRouteModeAdaptive,
			objects.PublicBenefitRouteModeFailover,
		}, upstream.RouteMode) {
			return fmt.Errorf("upstream %s has unsupported route mode: %s", upstream.ID, upstream.RouteMode)
		}
	}

	if strings.TrimSpace(cfg.Outbound.PublicAPIKey) == NoAuthAPIKeyValue {
		return fmt.Errorf("public benefit outbound public_api_key cannot use reserved noauth api key value")
	}

	return nil
}

func resolvePublicBenefitModelFamily(apiFormat llm.APIFormat, requestedModel string) string {
	modelLower := strings.ToLower(strings.TrimSpace(requestedModel))

	switch {
	case apiFormat == llm.APIFormatAnthropicMessage:
		return "claude"
	case apiFormat == llm.APIFormatGeminiContents:
		return "gemini"
	case apiFormat == llm.APIFormatOpenAIResponse && strings.Contains(modelLower, "codex"):
		return "codex"
	case apiFormat == llm.APIFormatOpenAIResponse:
		return "codex"
	case strings.Contains(modelLower, "claude"):
		return "claude"
	case strings.Contains(modelLower, "codex"):
		return "codex"
	case strings.Contains(modelLower, "gemini"):
		return "gemini"
	case strings.Contains(modelLower, "opencode"):
		return "opencode"
	default:
		return "generic"
	}
}

func compactModelSequence(models []string) []string {
	result := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))

	for _, modelID := range models {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		if _, ok := seen[modelID]; ok {
			continue
		}
		seen[modelID] = struct{}{}
		result = append(result, modelID)
	}

	return result
}

func resolvePublicBenefitAvailableModels(policies []PublicBenefitUpstreamPolicy) []string {
	result := make([]string, 0)
	for _, policy := range policies {
		if !policy.Enabled || !policy.Healthy || !policy.SupportsRequestedFamily {
			continue
		}
		result = append(result, policy.AvailableModels...)
	}

	return compactModelSequence(result)
}

func upstreamSupportsFamily(
	upstream objects.PublicBenefitUpstreamSite,
	availableModels []string,
	family string,
) bool {
	switch family {
	case "claude":
		return upstream.SupportsClaude || hasModelFamily(availableModels, "claude")
	case "codex":
		return upstream.SupportsCodex || hasModelFamily(availableModels, "codex")
	case "gemini":
		return upstream.SupportsGemini || hasModelFamily(availableModels, "gemini")
	case "opencode":
		return upstream.SupportsOpenCode || hasModelFamily(availableModels, "opencode")
	default:
		return true
	}
}

func hasModelFamily(models []string, keyword string) bool {
	for _, modelID := range models {
		if strings.Contains(strings.ToLower(modelID), keyword) {
			return true
		}
	}

	return false
}

func normalizePublicBenefitBaseURLValue(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func isPublicBenefitUpstreamHealthy(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "unknown":
		return true
	case "healthy", "available", "ok", "degraded":
		return true
	default:
		return false
	}
}
