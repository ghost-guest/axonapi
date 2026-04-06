package orchestrator

import (
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// PersistenceState holds shared state with channel management and retry capabilities.
type PersistenceState struct {
	APIKey *ent.APIKey

	RequestService      *biz.RequestService
	UsageLogService     *biz.UsageLogService
	ChannelService      *biz.ChannelService
	PromptProvider      PromptProvider
	PromptProtecter     PromptProtecter
	RetryPolicyProvider RetryPolicyProvider
	CandidateSelector   CandidateSelector
	LoadBalancer        *LoadBalancer

	// Request state
	ModelMapper *ModelMapper
	// Proxy config, will be used to override channel's default proxy config.
	Proxy *httpclient.ProxyConfig

	// OriginalModel is the model after API key profile mapping, used for channel selection
	ClientRequestModel string
	OriginalModel      string
	RawRequest         *httpclient.Request
	LlmRequest         *llm.Request
	InboundAPIFormat   llm.APIFormat

	// Persistence state
	Request     *ent.Request
	RequestExec *ent.RequestExecution

	// Retry tracking for a single incoming request.
	// TotalAttempts counts every outbound execution attempt, including the first one.
	// TriedCandidateIndices records exhausted/bad channels to avoid hitting the same broken
	// channel again within the same request, while still keeping candidate ordering compatible
	// with existing fallback / session-affinity / public-benefit selection.
	TotalAttempts         int
	TriedCandidateIndices map[int]struct{}
	AttemptHistory        []attemptHistoryEntry

	// ChannelModelsCandidates is the primary state for channel selection
	ChannelModelsCandidates []*ChannelModelsCandidate
	// Candidate state - current candidate index of ChannelModelsCandidates
	CurrentCandidateIndex int
	// CurrentCandidate is the currently selected candidate of ChannelModelsCandidates
	CurrentCandidate *ChannelModelsCandidate
	// CurrentModelIndex is the current model index in CurrentCandidate.Models
	CurrentModelIndex int

	// Perf is the performance record for the current request.
	Perf *biz.PerformanceRecord

	// StreamCompleted tracks whether the stream has response successfully completed.
	// This is used to distinguish between a stream that was canceled mid-way
	// versus a stream that completed successfully but the client disconnected
	// immediately after receiving the last chunk.
	StreamCompleted bool
}
