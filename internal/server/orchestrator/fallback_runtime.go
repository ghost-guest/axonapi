package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/looplj/axonhub/internal/log"
)

type attemptHistoryEntry struct {
	AttemptNo      int
	ChannelID      int
	ChannelName    string
	RequestedModel string
	ActualModel    string
	StartedAt      time.Time
	EndedAt        time.Time
	Result         string
	ErrorClass     string
	HTTPStatus     int
}

func (s *PersistenceState) ensureFallbackRuntimeState() {
	if s == nil {
		return
	}
	if s.TriedCandidateIndices == nil {
		s.TriedCandidateIndices = make(map[int]struct{})
	}
}

func (s *PersistenceState) candidateModel(candidate *ChannelModelsCandidate, modelIndex int) string {
	if candidate == nil || modelIndex < 0 || modelIndex >= len(candidate.Models) {
		return ""
	}
	return candidate.Models[modelIndex].ActualModel
}

func (s *PersistenceState) snapshotCandidatePool() []map[string]any {
	if s == nil || len(s.ChannelModelsCandidates) == 0 {
		return nil
	}
	pool := make([]map[string]any, 0, len(s.ChannelModelsCandidates))
	for idx, candidate := range s.ChannelModelsCandidates {
		if candidate == nil || candidate.Channel == nil {
			continue
		}
		models := make([]string, 0, len(candidate.Models))
		for _, entry := range candidate.Models {
			models = append(models, entry.ActualModel)
		}
		_, tried := s.TriedCandidateIndices[idx]
		pool = append(pool, map[string]any{
			"candidate_index": idx,
			"channel_id":      candidate.Channel.ID,
			"channel_name":    candidate.Channel.Name,
			"priority":        candidate.Priority,
			"models":          models,
			"tried":           tried,
		})
	}
	return pool
}

func classifyFallbackError(err error) string {
	if err == nil {
		return ""
	}
	if execInfo := ExtractErrorInfo(err); execInfo != nil && execInfo.StatusCode != nil {
		switch code := *execInfo.StatusCode; {
		case code == 429:
			return "rate_limited"
		case code == 503:
			return "no_available_providers"
		case code >= 500:
			return "5xx"
		case code == 400:
			return "openai_error"
		}
	}
	return "runtime_error"
}

func (p *PersistentOutboundTransformer) activateCandidate(ctx context.Context, candidateIndex int, candidate *ChannelModelsCandidate, reason string, lastErr error) error {
	if p.state == nil {
		return fmt.Errorf("missing persistence state")
	}
	if candidate == nil || candidate.Channel == nil || len(candidate.Models) == 0 {
		return fmt.Errorf("invalid candidate at index %d", candidateIndex)
	}

	p.state.ensureFallbackRuntimeState()
	p.state.CurrentCandidateIndex = candidateIndex
	p.state.CurrentCandidate = candidate
	p.state.CurrentModelIndex = 0
	p.state.RequestExec = nil
	p.wrapped = resolveCandidateOutboundTransformer(ctx, candidate, p.state.InboundAPIFormat, candidate.Models[0].ActualModel)

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "fallback state transition",
			log.String("stage", reason),
			log.String("requested_model", p.state.OriginalModel),
			log.String("active_model", candidate.Models[0].ActualModel),
			log.Int("active_channel_index", candidateIndex),
			log.Int("retry_budget_total", maxRequestAttempts),
			log.Int("retry_budget_remaining", max(0, maxRequestAttempts-p.state.TotalAttempts)),
			log.Any("candidate_pool", p.state.snapshotCandidatePool()),
			log.String("fallback_reason", classifyFallbackError(lastErr)),
		)
	}

	return nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
