package orchestrator

import (
	"context"

	"github.com/samber/lo"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/llm"
)

// APICompatibilitySelector prefers channels that natively match the inbound API format.
// This avoids sending Anthropic-format requests to OpenAI Responses channels when
// native Anthropic-compatible candidates are already available.
type APICompatibilitySelector struct {
	wrapped   CandidateSelector
	apiFormat llm.APIFormat
}

func WithAPICompatibilitySelector(wrapped CandidateSelector, apiFormat llm.APIFormat) *APICompatibilitySelector {
	return &APICompatibilitySelector{
		wrapped:   wrapped,
		apiFormat: apiFormat,
	}
}

func (s *APICompatibilitySelector) Select(ctx context.Context, req *llm.Request) ([]*ChannelModelsCandidate, error) {
	candidates, err := s.wrapped.Select(ctx, req)
	if err != nil {
		return nil, err
	}

	if len(candidates) <= 1 {
		return candidates, nil
	}

	compatible := lo.Filter(candidates, func(candidate *ChannelModelsCandidate, _ int) bool {
		if candidate == nil || candidate.Channel == nil {
			return false
		}

		return channelSupportsAPIFormat(candidate.Channel.Type, s.apiFormat)
	})

	if len(compatible) > 0 {
		return compatible, nil
	}

	return candidates, nil
}

func channelSupportsAPIFormat(ty channel.Type, apiFormat llm.APIFormat) bool {
	switch apiFormat {
	case llm.APIFormatAnthropicMessage:
		return ty.IsAnthropic() || ty.IsAnthropicLike() || ty == channel.TypeClaudecode
	case llm.APIFormatGeminiContents:
		return ty == channel.TypeGemini || ty == channel.TypeGeminiVertex || ty == channel.TypeAntigravity
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseCompact:
		return ty == channel.TypeOpenaiResponses || ty == channel.TypeCodex
	case llm.APIFormatOpenAIChatCompletion:
		return ty.IsOpenAI() || ty == channel.TypeGeminiOpenai
	default:
		return true
	}
}
