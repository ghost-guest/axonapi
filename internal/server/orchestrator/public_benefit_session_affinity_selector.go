package orchestrator

import (
	"context"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type PublicBenefitSessionAffinityResolver interface {
	ResolvePublicBenefitSessionAffinity(ctx context.Context, apiFormat llm.APIFormat, requestedModel string) *biz.PublicBenefitSessionAffinity
	ResolvePublicBenefitUpstreamPolicies(ctx context.Context, apiFormat llm.APIFormat, requestedModel string) []biz.PublicBenefitUpstreamPolicy
}

type PublicBenefitSessionAffinitySelector struct {
	wrapped       CandidateSelector
	systemService PublicBenefitSessionAffinityResolver
	apiFormat     llm.APIFormat
}

func WithPublicBenefitSessionAffinitySelector(
	wrapped CandidateSelector,
	systemService PublicBenefitSessionAffinityResolver,
	apiFormat llm.APIFormat,
) *PublicBenefitSessionAffinitySelector {
	return &PublicBenefitSessionAffinitySelector{
		wrapped:       wrapped,
		systemService: systemService,
		apiFormat:     apiFormat,
	}
}

func (s *PublicBenefitSessionAffinitySelector) Select(ctx context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	candidates, err := s.wrapped.Select(ctx, req)
	if err != nil {
		return nil, err
	}

	affinity := s.systemService.ResolvePublicBenefitSessionAffinity(ctx, s.apiFormat, req.Model)
	if affinity == nil || affinity.BaseURL == "" || len(candidates) <= 1 {
		return candidates, nil
	}

	policyByBaseURL := map[string]biz.PublicBenefitUpstreamPolicy{}
	for _, policy := range s.systemService.ResolvePublicBenefitUpstreamPolicies(ctx, s.apiFormat, req.Model) {
		policyByBaseURL[normalizePublicBenefitBaseURL(policy.BaseURL)] = policy
	}
	if policy, ok := policyByBaseURL[normalizePublicBenefitBaseURL(affinity.BaseURL)]; ok {
		if !policy.Enabled || !policy.Healthy || !policy.SupportsRequestedFamily {
			return candidates, nil
		}
	}

	sticky := make([]*ChannelModelsCandidate, 0, len(candidates))
	others := make([]*ChannelModelsCandidate, 0, len(candidates))
	minPriority := 0
	hasPriority := false

	for _, candidate := range candidates {
		if candidate == nil || candidate.Channel == nil {
			continue
		}
		if !hasPriority || candidate.Priority < minPriority {
			minPriority = candidate.Priority
			hasPriority = true
		}
		if normalizePublicBenefitBaseURL(candidate.Channel.BaseURL) == normalizePublicBenefitBaseURL(affinity.BaseURL) {
			sticky = append(sticky, candidate)
			continue
		}
		others = append(others, candidate)
	}

	if len(sticky) == 0 {
		return candidates, nil
	}

	stickyPriority := minPriority - 1
	result := make([]*ChannelModelsCandidate, 0, len(candidates))
	for _, candidate := range sticky {
		cloned := &ChannelModelsCandidate{
			Channel:  candidate.Channel,
			Priority: stickyPriority,
			Models:   append([]biz.ChannelModelEntry(nil), candidate.Models...),
		}
		result = append(result, cloned)
	}
	result = append(result, others...)

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "public benefit session affinity selector preferred sticky upstream",
			log.String("requested_model", req.Model),
			log.String("api_format", string(s.apiFormat)),
			log.String("sticky_base_url", affinity.BaseURL),
			log.Int("sticky_count", len(sticky)),
		)
	}

	return result, nil
}
