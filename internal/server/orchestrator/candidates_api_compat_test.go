package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
)

func TestAPICompatibilitySelector_Select_PrefersAnthropicCompatibleChannels(t *testing.T) {
	selector := WithAPICompatibilitySelector(&stubCandidateSelector{
		byModel: map[string][]*ChannelModelsCandidate{
			"claude-sonnet-4-5": {
				{
					Channel: &biz.Channel{Channel: &ent.Channel{
						ID:   1,
						Name: "responses",
						Type: channel.TypeOpenaiResponses,
					}},
					Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4-5", ActualModel: "claude-sonnet-4-5"}},
				},
				{
					Channel: &biz.Channel{Channel: &ent.Channel{
						ID:   2,
						Name: "anthropic",
						Type: channel.TypeAnthropic,
					}},
					Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4-5", ActualModel: "MiniMax-M2.7-highspeed"}},
				},
			},
		},
	}, llm.APIFormatAnthropicMessage)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-sonnet-4-5"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "anthropic", result[0].Channel.Name)
}

func TestAPICompatibilitySelector_Select_FallsBackWhenNoCompatibleChannels(t *testing.T) {
	selector := WithAPICompatibilitySelector(&stubCandidateSelector{
		byModel: map[string][]*ChannelModelsCandidate{
			"claude-sonnet-4-5": {
				{
					Channel: &biz.Channel{Channel: &ent.Channel{
						ID:   1,
						Name: "responses",
						Type: channel.TypeOpenaiResponses,
					}},
					Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4-5", ActualModel: "claude-sonnet-4-5"}},
				},
			},
		},
	}, llm.APIFormatAnthropicMessage)

	result, err := selector.Select(context.Background(), &llm.Request{Model: "claude-sonnet-4-5"})
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "responses", result[0].Channel.Name)
}
