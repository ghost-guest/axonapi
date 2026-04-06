package orchestrator

import (
	"context"
	"slices"
	"strings"

	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type PublicBenefitUpstreamPolicyResolver interface {
	ResolvePublicBenefitUpstreamPolicies(ctx context.Context, apiFormat llm.APIFormat, requestedModel string) []biz.PublicBenefitUpstreamPolicy
}

type PublicBenefitUpstreamSelector struct {
	wrapped       CandidateSelector
	systemService PublicBenefitUpstreamPolicyResolver
	apiFormat     llm.APIFormat
}

func WithPublicBenefitUpstreamSelector(
	wrapped CandidateSelector,
	systemService PublicBenefitUpstreamPolicyResolver,
	apiFormat llm.APIFormat,
) *PublicBenefitUpstreamSelector {
	return &PublicBenefitUpstreamSelector{
		wrapped:       wrapped,
		systemService: systemService,
		apiFormat:     apiFormat,
	}
}

func (s *PublicBenefitUpstreamSelector) Select(ctx context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	candidates, err := s.wrapped.Select(ctx, req)
	if err != nil {
		return nil, err
	}

	policies := s.systemService.ResolvePublicBenefitUpstreamPolicies(ctx, s.apiFormat, req.Model)
	if len(policies) == 0 {
		return candidates, nil
	}

	policyByBaseURL := make(map[string]biz.PublicBenefitUpstreamPolicy, len(policies))
	for _, policy := range policies {
		policyByBaseURL[normalizePublicBenefitBaseURL(policy.BaseURL)] = policy
	}

	preferred := make([]*ChannelModelsCandidate, 0, len(candidates))
	degraded := make([]*ChannelModelsCandidate, 0, len(candidates))
	unmatched := make([]*ChannelModelsCandidate, 0, len(candidates))

	for _, candidate := range candidates {
		if candidate == nil || candidate.Channel == nil {
			continue
		}

		policy, ok := policyByBaseURL[normalizePublicBenefitBaseURL(candidate.Channel.BaseURL)]
		if !ok {
			unmatched = append(unmatched, candidate)
			continue
		}

		if !policy.Enabled {
			continue
		}

		models := filterCandidateModelsByPolicy(candidate.Models, policy)
		if len(models) == 0 {
			continue
		}

		cloned := &ChannelModelsCandidate{
			Channel:  candidate.Channel,
			Priority: candidate.Priority,
			Models:   models,
		}

		if policy.Healthy && policy.SupportsRequestedFamily {
			preferred = append(preferred, cloned)
			continue
		}

		degraded = append(degraded, cloned)
	}

	sortCandidatesByPolicy(preferred, policyByBaseURL)
	sortCandidatesByPolicy(degraded, policyByBaseURL)

	result := append(preferred, degraded...)
	result = append(result, unmatched...)

	if len(result) == 0 {
		if log.DebugEnabled(ctx) {
			log.Debug(ctx, "public benefit upstream selector produced no matched candidates, keeping original candidates",
				log.String("requested_model", req.Model),
				log.String("api_format", string(s.apiFormat)),
			)
		}

		return candidates, nil
	}

	if log.DebugEnabled(ctx) {
		log.Debug(ctx, "public benefit upstream selector applied routing policies",
			log.String("requested_model", req.Model),
			log.String("api_format", string(s.apiFormat)),
			log.Int("preferred_count", len(preferred)),
			log.Int("degraded_count", len(degraded)),
			log.Int("unmatched_count", len(unmatched)),
			log.Bool("health_priority_enabled", true),
		)
	}

	return result, nil
}

func filterCandidateModelsByPolicy(models []biz.ChannelModelEntry, policy biz.PublicBenefitUpstreamPolicy) []biz.ChannelModelEntry {
	if len(models) == 0 {
		return nil
	}

	if len(policy.AvailableModels) == 0 {
		return append([]biz.ChannelModelEntry(nil), models...)
	}

	allowed := make(map[string]struct{}, len(policy.AvailableModels))
	for _, modelID := range policy.AvailableModels {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			continue
		}
		allowed[modelID] = struct{}{}
	}

	result := make([]biz.ChannelModelEntry, 0, len(models))
	for _, entry := range models {
		if _, ok := allowed[entry.ActualModel]; ok {
			result = append(result, entry)
			continue
		}
		if _, ok := allowed[entry.RequestModel]; ok {
			result = append(result, entry)
		}
	}

	return result
}

func sortCandidatesByPolicy(candidates []*ChannelModelsCandidate, policyByBaseURL map[string]biz.PublicBenefitUpstreamPolicy) {
	slices.SortStableFunc(candidates, func(a, b *ChannelModelsCandidate) int {
		ap := policyForCandidate(a, policyByBaseURL)
		bp := policyForCandidate(b, policyByBaseURL)

		switch {
		case ap.RecentProbeRequestCount > 0 && bp.RecentProbeRequestCount == 0:
			return -1
		case ap.RecentProbeRequestCount == 0 && bp.RecentProbeRequestCount > 0:
			return 1
		case ap.RecentProbeSuccessRate > bp.RecentProbeSuccessRate:
			return -1
		case ap.RecentProbeSuccessRate < bp.RecentProbeSuccessRate:
			return 1
		case ap.Weight > bp.Weight:
			return -1
		case ap.Weight < bp.Weight:
			return 1
		default:
			return 0
		}
	})
}

func policyForCandidate(candidate *ChannelModelsCandidate, policyByBaseURL map[string]biz.PublicBenefitUpstreamPolicy) biz.PublicBenefitUpstreamPolicy {
	if candidate == nil || candidate.Channel == nil {
		return biz.PublicBenefitUpstreamPolicy{}
	}
	return policyByBaseURL[normalizePublicBenefitBaseURL(candidate.Channel.BaseURL)]
}

func normalizePublicBenefitBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}
