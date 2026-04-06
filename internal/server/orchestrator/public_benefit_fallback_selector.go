package orchestrator

import (
	"context"
	"fmt"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type PublicBenefitModelResolver interface {
	ResolvePublicBenefitModelSequence(ctx context.Context, apiFormat llm.APIFormat, requestedModel string) []string
}

type PublicBenefitFallbackSelector struct {
	wrapped       CandidateSelector
	systemService PublicBenefitModelResolver
	apiFormat     llm.APIFormat
}

const publicBenefitFallbackPriorityStep = 1_000_000

func WithPublicBenefitFallbackSelector(
	wrapped CandidateSelector,
	systemService PublicBenefitModelResolver,
	apiFormat llm.APIFormat,
) *PublicBenefitFallbackSelector {
	return &PublicBenefitFallbackSelector{
		wrapped:       wrapped,
		systemService: systemService,
		apiFormat:     apiFormat,
	}
}

func (s *PublicBenefitFallbackSelector) Select(ctx context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	modelSequence := s.systemService.ResolvePublicBenefitModelSequence(ctx, s.apiFormat, req.Model)
	if len(modelSequence) <= 1 {
		return s.wrapped.Select(ctx, req)
	}

	merged := make([]*ChannelModelsCandidate, 0)
	indexByKey := map[string]int{}
	hasSuccessfulSelection := false

	for sequenceIndex, modelID := range modelSequence {
		cloned := *req
		cloned.Model = modelID

		candidates, err := s.wrapped.Select(ctx, &cloned)
		if err != nil {
			if log.DebugEnabled(ctx) {
				log.Debug(ctx, "public benefit fallback selector skipped model due to selection error",
					log.String("requested_model", req.Model),
					log.String("candidate_model", modelID),
					log.Cause(err),
				)
			}

			continue
		}

		if len(candidates) == 0 {
			continue
		}

		if !hasSuccessfulSelection {
			hasSuccessfulSelection = true
		}

		for _, candidate := range candidates {
			models := modelEntriesForSequence(candidate.Models, sequenceIndex)
			key := candidateKeyForMerge(candidate, sequenceIndex)
			if idx, ok := indexByKey[key]; ok {
				existing := merged[idx]
				existing.Models = appendUniqueModelEntries(existing.Models, models)
				continue
			}

			copied := &ChannelModelsCandidate{
				Channel:  candidate.Channel,
				Priority: priorityForModelSequence(candidate.Priority, sequenceIndex),
				Models:   models,
			}
			indexByKey[key] = len(merged)
			merged = append(merged, copied)
		}
	}

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "public benefit fallback selector resolved model sequence",
			log.String("requested_model", req.Model),
			log.String("api_format", string(s.apiFormat)),
			log.Any("model_sequence", modelSequence),
			log.Int("candidate_count", len(merged)),
		)
	}

	if !hasSuccessfulSelection {
		return nil, nil
	}

	return merged, nil
}

func priorityForModelSequence(basePriority int, sequenceIndex int) int {
	if sequenceIndex <= 0 {
		return basePriority
	}

	return basePriority + sequenceIndex*publicBenefitFallbackPriorityStep
}

func candidateKeyForMerge(candidate *ChannelModelsCandidate, sequenceIndex int) string {
	if candidate == nil || candidate.Channel == nil {
		return ""
	}

	return fmt.Sprintf("%d:%d:%d", sequenceIndex, candidate.Channel.ID, candidate.Priority)
}

func modelEntriesForSequence(models []biz.ChannelModelEntry, sequenceIndex int) []biz.ChannelModelEntry {
	copied := append([]biz.ChannelModelEntry(nil), models...)
	if sequenceIndex <= 0 {
		return copied
	}

	for i := range copied {
		copied[i].Source = "fallback"
	}

	return copied
}

func appendUniqueModelEntries(existing []biz.ChannelModelEntry, incoming []biz.ChannelModelEntry) []biz.ChannelModelEntry {
	seen := make(map[string]struct{}, len(existing))
	for _, entry := range existing {
		seen[entry.ActualModel+"|"+entry.RequestModel] = struct{}{}
	}

	for _, entry := range incoming {
		key := entry.ActualModel + "|" + entry.RequestModel
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, entry)
	}

	return existing
}
