package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
)

func TestShouldUseAnthropicMessages(t *testing.T) {
	tests := []struct {
		name    string
		channel *biz.Channel
		modelID string
		want    bool
	}{
		{
			name:    "claude model without channel uses anthropic",
			channel: nil,
			modelID: "claude-3-5-sonnet-20241022",
			want:    true,
		},
		{
			name:    "openai model without channel stays openai",
			channel: nil,
			modelID: "gpt-4o-mini",
			want:    false,
		},
		{
			name:    "anthropic-like channel forces anthropic messages",
			channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeDoubaoAnthropic}},
			modelID: "kimi-k2",
			want:    true,
		},
		{
			name:    "claudecode channel forces anthropic messages",
			channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeClaudecode}},
			modelID: "claude-sonnet-4-20250514",
			want:    true,
		},
		{
			name:    "openai channel with claude model uses anthropic messages",
			channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeOpenai}},
			modelID: "claude-3-7-sonnet-latest",
			want:    true,
		},
		{
			name:    "openai channel with gpt model stays openai",
			channel: &biz.Channel{Channel: &ent.Channel{Type: channel.TypeOpenai}},
			modelID: "gpt-4o",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldUseAnthropicMessages(tt.channel, tt.modelID))
		})
	}
}

func TestTestChannel_SelectsAnthropicInboundForClaudeModel(t *testing.T) {
	channelModel := &biz.Channel{Channel: &ent.Channel{Type: channel.TypeOpenaiResponses}}
	inbound := selectTestChannelInboundTransformer(channelModel, "claude-sonnet-4-5")
	require.IsType(t, anthropic.NewInboundTransformer(), inbound)
}

func TestBuildTestChannelRequest_AddsMaxTokensForAnthropic(t *testing.T) {
	req := buildTestChannelRequest("claude-sonnet-4-5", false, llm.APIFormatAnthropicMessage)
	require.NotNil(t, req.MaxTokens)
	require.EqualValues(t, 256, *req.MaxTokens)
	require.NotNil(t, req.MaxCompletionTokens)
	require.EqualValues(t, 256, *req.MaxCompletionTokens)
}

func TestBuildTestChannelRequest_DoesNotForceMaxTokensForOpenAI(t *testing.T) {
	req := buildTestChannelRequest("gpt-5.4", false, llm.APIFormatOpenAIChatCompletion)
	require.Nil(t, req.MaxTokens)
	require.NotNil(t, req.MaxCompletionTokens)
	require.EqualValues(t, 256, *req.MaxCompletionTokens)
}

func TestParseTestChannelResponseBody_Anthropic(t *testing.T) {
	body, err := json.Marshal(anthropic.Message{
		ID:    "msg_123",
		Type:  "message",
		Role:  "assistant",
		Model: "MiniMax-M2.7-highspeed",
		Content: []anthropic.MessageContentBlock{
			{Type: "text", Text: lo.ToPtr("hello")},
			{Type: "text", Text: lo.ToPtr(" world")},
		},
	})
	require.NoError(t, err)

	text, err := parseTestChannelResponseBody(body, llm.APIFormatAnthropicMessage)
	require.NoError(t, err)
	require.Equal(t, "hello world", text)
}

func TestParseTestChannelResponseBody_OpenAI(t *testing.T) {
	body, err := json.Marshal(llm.Response{
		Choices: []llm.Choice{
			{
				Message: &llm.Message{
					Role: "assistant",
					Content: llm.MessageContent{
						Content: lo.ToPtr("ok"),
					},
				},
			},
		},
	})
	require.NoError(t, err)

	text, err := parseTestChannelResponseBody(body, llm.APIFormatOpenAIChatCompletion)
	require.NoError(t, err)
	require.Equal(t, "ok", text)
}
