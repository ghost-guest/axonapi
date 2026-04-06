package objects

import "time"

type PublicBenefitProviderKind string

const (
	PublicBenefitProviderKindNewAPI    PublicBenefitProviderKind = "new_api"
	PublicBenefitProviderKindOneAPI    PublicBenefitProviderKind = "one_api"
	PublicBenefitProviderKindOneHub    PublicBenefitProviderKind = "one_hub"
	PublicBenefitProviderKindDoneHub   PublicBenefitProviderKind = "done_hub"
	PublicBenefitProviderKindAnyRouter PublicBenefitProviderKind = "anyrouter"
	PublicBenefitProviderKindCubence   PublicBenefitProviderKind = "cubence"
	PublicBenefitProviderKindNekoCode  PublicBenefitProviderKind = "nekocode"
	PublicBenefitProviderKindSub2API   PublicBenefitProviderKind = "sub2api"
	PublicBenefitProviderKindYesCode   PublicBenefitProviderKind = "yescode"
	PublicBenefitProviderKindGeneric   PublicBenefitProviderKind = "generic"
)

type PublicBenefitAuthType string

const (
	PublicBenefitAuthTypeAPIKey   PublicBenefitAuthType = "api_key"
	PublicBenefitAuthTypeCookie   PublicBenefitAuthType = "cookie"
	PublicBenefitAuthTypeToken    PublicBenefitAuthType = "token"
	PublicBenefitAuthTypePassword PublicBenefitAuthType = "password"
	PublicBenefitAuthTypeMixed    PublicBenefitAuthType = "mixed"
)

type PublicBenefitRouteMode string

const (
	PublicBenefitRouteModePriority   PublicBenefitRouteMode = "priority"
	PublicBenefitRouteModeRoundRobin PublicBenefitRouteMode = "round_robin"
	PublicBenefitRouteModeAdaptive   PublicBenefitRouteMode = "adaptive"
	PublicBenefitRouteModeFailover   PublicBenefitRouteMode = "failover"
)

type PublicBenefitProviderAccount struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Kind        PublicBenefitProviderKind `json:"kind"`
	BaseURL     string                    `json:"base_url"`
	AuthType    PublicBenefitAuthType     `json:"auth_type"`
	Username    string                    `json:"username,omitempty"`
	Password    string                    `json:"password,omitempty"`
	Cookie      string                    `json:"cookie,omitempty"`
	Token       string                    `json:"token,omitempty"`
	APIKey      string                    `json:"api_key,omitempty"`
	Enabled     bool                      `json:"enabled"`
	AutoCheckIn bool                      `json:"auto_check_in"`
	CheckInCron string                    `json:"check_in_cron,omitempty"`
	BalancePath string                    `json:"balance_path,omitempty"`
	CheckInPath string                    `json:"check_in_path,omitempty"`
	Remark      string                    `json:"remark,omitempty"`
	Extra       map[string]any            `json:"extra,omitempty"`
}

type PublicBenefitUpstreamSite struct {
	ID                   string                 `json:"id"`
	Name                 string                 `json:"name"`
	BaseURL              string                 `json:"base_url"`
	APIKey               string                 `json:"api_key"`
	Enabled              bool                   `json:"enabled"`
	AutoDiscoverModels   bool                   `json:"auto_discover_models"`
	DiscoverModelsCron   string                 `json:"discover_models_cron,omitempty"`
	PreferredModelFamily string                 `json:"preferred_model_family,omitempty"`
	RouteMode            PublicBenefitRouteMode `json:"route_mode"`
	Weight               int                    `json:"weight"`
	HealthCheckPath      string                 `json:"health_check_path,omitempty"`
	HealthCheckModel     string                 `json:"health_check_model,omitempty"`
	FailureThreshold     int                    `json:"failure_threshold"`
	RecoverThreshold     int                    `json:"recover_threshold"`
	SupportsClaude       bool                   `json:"supports_claude"`
	SupportsCodex        bool                   `json:"supports_codex"`
	SupportsOpenCode     bool                   `json:"supports_opencode"`
	SupportsGemini       bool                   `json:"supports_gemini"`
	ModelAllowlist       []string               `json:"model_allowlist,omitempty"`
	ModelBlocklist       []string               `json:"model_blocklist,omitempty"`
	Remark               string                 `json:"remark,omitempty"`
	Extra                map[string]any         `json:"extra,omitempty"`
}

type PublicBenefitOutboundConfig struct {
	Enabled                   bool                   `json:"enabled"`
	PublicBaseURL             string                 `json:"public_base_url"`
	PublicAPIKey              string                 `json:"public_api_key"`
	DefaultRouteMode          PublicBenefitRouteMode `json:"default_route_mode"`
	SessionAffinityEnabled    bool                   `json:"session_affinity_enabled"`
	SessionAffinityTTLSeconds int                    `json:"session_affinity_ttl_seconds"`
	DefaultClaudeFallback     []string               `json:"default_claude_fallback,omitempty"`
	DefaultCodexFallback      []string               `json:"default_codex_fallback,omitempty"`
	DefaultOpenCodeFallback   []string               `json:"default_opencode_fallback,omitempty"`
	DefaultGeminiFallback     []string               `json:"default_gemini_fallback,omitempty"`
	DefaultGenericFallback    []string               `json:"default_generic_fallback,omitempty"`
}

type PublicBenefitHubConfig struct {
	Providers []PublicBenefitProviderAccount `json:"providers"`
	Upstreams []PublicBenefitUpstreamSite    `json:"upstreams"`
	Outbound  PublicBenefitOutboundConfig    `json:"outbound"`
}

type PublicBenefitProviderRuntime struct {
	ProviderID        string     `json:"provider_id"`
	LastBalanceAt     *time.Time `json:"last_balance_at,omitempty"`
	LastCheckInAt     *time.Time `json:"last_check_in_at,omitempty"`
	LastCheckInStatus string     `json:"last_check_in_status,omitempty"`
	Balance           float64    `json:"balance"`
	Currency          string     `json:"currency,omitempty"`
	TotalUsage        float64    `json:"total_usage"`
	AccountName       string     `json:"account_name,omitempty"`
	LastError         string     `json:"last_error,omitempty"`
}

type PublicBenefitUpstreamRuntime struct {
	UpstreamID          string     `json:"upstream_id"`
	LastModelSyncAt     *time.Time `json:"last_model_sync_at,omitempty"`
	LastHealthCheckAt   *time.Time `json:"last_health_check_at,omitempty"`
	LastSwitchAt        *time.Time `json:"last_switch_at,omitempty"`
	HealthStatus        string     `json:"health_status,omitempty"`
	AvailableModels     []string   `json:"available_models,omitempty"`
	TotalRequests       int64      `json:"total_requests"`
	TotalTokens         int64      `json:"total_tokens"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	ChannelID           int        `json:"channel_id,omitempty"`
	ChannelName         string     `json:"channel_name,omitempty"`
	LastError           string     `json:"last_error,omitempty"`
}

type PublicBenefitUsageSnapshot struct {
	Date          string   `json:"date"`
	TotalTokens   int64    `json:"total_tokens"`
	TotalRequests int64    `json:"total_requests"`
	UsedUpstreams []string `json:"used_upstreams,omitempty"`
}

type PublicBenefitRuntimeState struct {
	Providers  []PublicBenefitProviderRuntime `json:"providers"`
	Upstreams  []PublicBenefitUpstreamRuntime `json:"upstreams"`
	DailyUsage []PublicBenefitUsageSnapshot   `json:"daily_usage"`
}

type PublicBenefitDashboard struct {
	ProviderCount        int                            `json:"provider_count"`
	EnabledProviderCount int                            `json:"enabled_provider_count"`
	UpstreamCount        int                            `json:"upstream_count"`
	EnabledUpstreamCount int                            `json:"enabled_upstream_count"`
	HealthyUpstreamCount int                            `json:"healthy_upstream_count"`
	TotalBalance         float64                        `json:"total_balance"`
	TotalUsage           float64                        `json:"total_usage"`
	TotalRequests        int64                          `json:"total_requests"`
	TotalTokens          int64                          `json:"total_tokens"`
	Providers            []PublicBenefitProviderRuntime `json:"providers"`
	Upstreams            []PublicBenefitUpstreamRuntime `json:"upstreams"`
	DailyUsage           []PublicBenefitUsageSnapshot   `json:"daily_usage"`
	Outbound             PublicBenefitOutboundConfig    `json:"outbound"`
}
