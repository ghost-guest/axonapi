package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

type stubPublicBenefitResolver struct {
	sequence []string
}

func (s *stubPublicBenefitResolver) ResolvePublicBenefitModelSequence(_ context.Context, _ llm.APIFormat, _ string) []string {
	return s.sequence
}

type stubCandidateSelector struct {
	byModel map[string][]*ChannelModelsCandidate
	errs    map[string]error
}

func (s *stubCandidateSelector) Select(_ context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	if err := s.errs[req.Model]; err != nil {
		return nil, err
	}

	return s.byModel[req.Model], nil
}

func TestPublicBenefitFallbackSelector_Select_MergesModelsByChannel(t *testing.T) {
	channel := &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "test-channel"}}

	selector := WithPublicBenefitFallbackSelector(
		&stubCandidateSelector{
			byModel: map[string][]*ChannelModelsCandidate{
				"claude-sonnet": {
					{
						Channel:  channel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "claude-sonnet", ActualModel: "claude-sonnet", Source: "direct"},
						},
					},
				},
				"gpt-5.4": {
					{
						Channel:  channel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4", Source: "fallback"},
						},
					},
				},
			},
			errs: map[string]error{},
		},
		&stubPublicBenefitResolver{sequence: []string{"claude-sonnet", "gpt-5.4"}},
		llm.APIFormatAnthropicMessage,
	)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-sonnet"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Len(t, result[0].Models, 2)
	require.Equal(t, "claude-sonnet", result[0].Models[0].ActualModel)
	require.Equal(t, "gpt-5.4", result[0].Models[1].ActualModel)
}

func TestPublicBenefitFallbackSelector_Select_SkipsInvalidFallbackModel(t *testing.T) {
	channel := &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "fallback-channel"}}

	selector := WithPublicBenefitFallbackSelector(
		&stubCandidateSelector{
			byModel: map[string][]*ChannelModelsCandidate{
				"gpt-5.4": {
					{
						Channel:  channel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4", Source: "fallback"},
						},
					},
				},
			},
			errs: map[string]error{
				"claude-opus": errors.New("model not found"),
			},
		},
		&stubPublicBenefitResolver{sequence: []string{"claude-opus", "gpt-5.4"}},
		llm.APIFormatAnthropicMessage,
	)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-opus"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "gpt-5.4", result[0].Models[0].ActualModel)
}
