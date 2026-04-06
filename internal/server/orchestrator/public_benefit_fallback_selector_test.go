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

func TestPublicBenefitFallbackSelector_Select_AppendsFallbackCandidatesAfterDirectCandidates(t *testing.T) {
	directChannel := &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "claude-channel"}}
	fallbackChannel := &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "gpt-channel"}}

	selector := WithPublicBenefitFallbackSelector(
		&stubCandidateSelector{
			byModel: map[string][]*ChannelModelsCandidate{
				"claude-sonnet": {
					{
						Channel:  directChannel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "claude-sonnet", ActualModel: "claude-sonnet", Source: "direct"},
						},
					},
				},
				"gpt-5.4": {
					{
						Channel:  fallbackChannel,
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
	require.Len(t, result, 2)
	require.Equal(t, "claude-channel", result[0].Channel.Name)
	require.Len(t, result[0].Models, 1)
	require.Equal(t, "claude-sonnet", result[0].Models[0].ActualModel)
	require.Equal(t, "gpt-channel", result[1].Channel.Name)
	require.Equal(t, publicBenefitFallbackPriorityStep, result[1].Priority)
	require.Len(t, result[1].Models, 1)
	require.Equal(t, "gpt-5.4", result[1].Models[0].ActualModel)
}

func TestPublicBenefitFallbackSelector_Select_AssignsLaterFallbacksLowerPriorityGroups(t *testing.T) {
	firstFallbackChannel := &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "gpt-fallback"}}
	secondFallbackChannel := &biz.Channel{Channel: &ent.Channel{ID: 3, Name: "kimi-fallback"}}

	selector := WithPublicBenefitFallbackSelector(
		&stubCandidateSelector{
			byModel: map[string][]*ChannelModelsCandidate{
				"claude-sonnet": {
					{
						Channel:  &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "claude-channel"}},
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "claude-sonnet", ActualModel: "claude-sonnet", Source: "direct"},
						},
					},
				},
				"gpt-5.4": {
					{
						Channel:  firstFallbackChannel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4", Source: "fallback"},
						},
					},
				},
				"Kimi-K2.5": {
					{
						Channel:  secondFallbackChannel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "Kimi-K2.5", ActualModel: "Kimi-K2.5", Source: "fallback"},
						},
					},
				},
			},
			errs: map[string]error{},
		},
		&stubPublicBenefitResolver{sequence: []string{"claude-sonnet", "gpt-5.4", "Kimi-K2.5"}},
		llm.APIFormatAnthropicMessage,
	)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-sonnet"})
	require.NoError(t, err)
	require.Len(t, result, 3)
	require.Equal(t, 0, result[0].Priority)
	require.Equal(t, publicBenefitFallbackPriorityStep, result[1].Priority)
	require.Equal(t, publicBenefitFallbackPriorityStep*2, result[2].Priority)
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

func TestPublicBenefitFallbackSelector_Select_ReplacesDirectCandidatesOnceFallbackModelSucceeds(t *testing.T) {
	claudeChannel := &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "claude-only"}}
	gptChannel := &biz.Channel{Channel: &ent.Channel{ID: 3, Name: "gpt-fallback"}}

	selector := WithPublicBenefitFallbackSelector(
		&stubCandidateSelector{
			byModel: map[string][]*ChannelModelsCandidate{
				"claude-opus": {
					{
						Channel:  claudeChannel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "claude-opus", ActualModel: "claude-sonnet-4-6", Source: "direct"},
						},
					},
				},
				"gpt-5.4": {
					{
						Channel:  gptChannel,
						Priority: 0,
						Models: []biz.ChannelModelEntry{
							{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4", Source: "fallback"},
						},
					},
				},
			},
			errs: map[string]error{
				"claude-opus": errors.New("provider unavailable"),
			},
		},
		&stubPublicBenefitResolver{sequence: []string{"claude-opus", "gpt-5.4"}},
		llm.APIFormatAnthropicMessage,
	)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-opus"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "gpt-fallback", result[0].Channel.Name)
	require.Len(t, result[0].Models, 1)
	require.Equal(t, "gpt-5.4", result[0].Models[0].ActualModel)
}
