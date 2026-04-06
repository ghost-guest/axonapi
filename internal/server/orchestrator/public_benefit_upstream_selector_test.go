package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type stubPublicBenefitPolicyResolver struct {
	policies []biz.PublicBenefitUpstreamPolicy
}

func (s *stubPublicBenefitPolicyResolver) ResolvePublicBenefitUpstreamPolicies(_ context.Context, _ llm.APIFormat, _ string) []biz.PublicBenefitUpstreamPolicy {
	return s.policies
}

func TestPublicBenefitUpstreamSelector_Select_PrefersHealthyMatchedUpstreams(t *testing.T) {
	healthyChannel := &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "healthy", BaseURL: "https://a.example"}}
	unhealthyChannel := &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "unhealthy", BaseURL: "https://b.example"}}
	unmatchedChannel := &biz.Channel{Channel: &ent.Channel{ID: 3, Name: "unmatched", BaseURL: "https://c.example"}}

	selector := WithPublicBenefitUpstreamSelector(
		&stubCandidateSelector{
			byModel: map[string][]*ChannelModelsCandidate{
				"claude-opus": {
					{Channel: healthyChannel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "claude-opus", ActualModel: "claude-opus"}}},
					{Channel: unhealthyChannel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "claude-opus", ActualModel: "claude-opus"}}},
					{Channel: unmatchedChannel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "claude-opus", ActualModel: "claude-opus"}}},
				},
			},
		},
		&stubPublicBenefitPolicyResolver{
			policies: []biz.PublicBenefitUpstreamPolicy{
				{BaseURL: "https://a.example", Enabled: true, Healthy: true, SupportsRequestedFamily: true, Weight: 10},
				{BaseURL: "https://b.example", Enabled: true, Healthy: false, SupportsRequestedFamily: true, Weight: 1},
			},
		},
		llm.APIFormatAnthropicMessage,
	)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-opus"})
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, "healthy", result[0].Channel.Name)
	require.Equal(t, "unhealthy", result[1].Channel.Name)
	require.Equal(t, "unmatched", result[2].Channel.Name)
}

func TestPublicBenefitUpstreamSelector_Select_FiltersUnavailableModels(t *testing.T) {
	channel := &biz.Channel{Channel: &ent.Channel{ID: 4, Name: "runtime-filtered", BaseURL: "https://models.example"}}

	selector := WithPublicBenefitUpstreamSelector(
		&stubCandidateSelector{
			byModel: map[string][]*ChannelModelsCandidate{
				"claude-opus": {
					{
						Channel:  channel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "claude-opus", ActualModel: "claude-opus"},
							{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4"},
						},
					},
				},
			},
		},
		&stubPublicBenefitPolicyResolver{
			policies: []biz.PublicBenefitUpstreamPolicy{
				{
					BaseURL:                 "https://models.example",
					Enabled:                 true,
					Healthy:                 true,
					SupportsRequestedFamily: true,
					AvailableModels:         []string{"gpt-5.4"},
				},
			},
		},
		llm.APIFormatAnthropicMessage,
	)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-opus"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0].Models, 1)
	require.Equal(t, "gpt-5.4", result[0].Models[0].ActualModel)
}
