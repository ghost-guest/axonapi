package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type stubPublicBenefitSessionAffinityResolver struct {
	affinity *biz.PublicBenefitSessionAffinity
	policies []biz.PublicBenefitUpstreamPolicy
}

func (s *stubPublicBenefitSessionAffinityResolver) ResolvePublicBenefitSessionAffinity(_ context.Context, _ llm.APIFormat, _ string) *biz.PublicBenefitSessionAffinity {
	return s.affinity
}

func (s *stubPublicBenefitSessionAffinityResolver) ResolvePublicBenefitUpstreamPolicies(_ context.Context, _ llm.APIFormat, _ string) []biz.PublicBenefitUpstreamPolicy {
	return s.policies
}

func TestPublicBenefitSessionAffinitySelector_Select_PrefersStickyChannel(t *testing.T) {
	stickyChannel := &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "sticky", BaseURL: "https://sticky.example"}}
	otherChannel := &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "other", BaseURL: "https://other.example"}}

	selector := WithPublicBenefitSessionAffinitySelector(
		&stubCandidateSelector{
			byModel: map[string][]*ChannelModelsCandidate{
				"claude-sonnet-4": {
					{Channel: otherChannel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4", ActualModel: "claude-sonnet-4"}}},
					{Channel: stickyChannel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4", ActualModel: "claude-sonnet-4"}}},
				},
			},
		},
		&stubPublicBenefitSessionAffinityResolver{
			affinity: &biz.PublicBenefitSessionAffinity{
				SessionKey: "claude:trace:test-1",
				BaseURL:    "https://sticky.example/",
				ExpiresAt:  time.Now().Add(time.Minute),
			},
			policies: []biz.PublicBenefitUpstreamPolicy{
				{BaseURL: "https://sticky.example", Enabled: true, Healthy: true, SupportsRequestedFamily: true},
			},
		},
		llm.APIFormatAnthropicMessage,
	)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-sonnet-4"})
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, "sticky", result[0].Channel.Name)
	require.Less(t, result[0].Priority, result[1].Priority)
}
