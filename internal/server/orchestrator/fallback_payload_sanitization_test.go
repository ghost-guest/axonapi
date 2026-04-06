package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	openairesponses "github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func TestSanitizeRequestForCandidate_OpenAIResponsesFallbackFromAnthropic(t *testing.T) {
	inputText := "hello from user"
	systemText := "system safety"
	toolCallID := "call_1"

	req := &llm.Request{
		Model:       "claude-sonnet-4",
		APIFormat:   llm.APIFormatAnthropicMessage,
		RequestType: llm.RequestTypeChat,
		Metadata: map[string]string{
			"user_id": "user-123",
		},
		TransformOptions: llm.TransformOptions{ArrayInstructions: lo.ToPtr(true)},
		TransformerMetadata: map[string]any{
			"include":                           []string{"reasoning.encrypted_content"},
			"max_tool_calls":                    int64(7),
			"prompt_cache_retention":            "24h",
			"truncation":                        "auto",
			"include_obfuscation":               true,
			"anthropic_ephemeral_cache_control": true,
			"anthropic_reasoning_signature":     "sig",
			"anthropic_tool_result_format":      "blocks",
		},
		Messages: []llm.Message{
			{
				Role: "system",
				Content: llm.MessageContent{
					Content: &systemText,
				},
			},
			{
				Role: "user",
				Content: llm.MessageContent{
					Content: &inputText,
				},
			},
			{
				Role:       "tool",
				ToolCallID: &toolCallID,
				Content: llm.MessageContent{
					Content: lo.ToPtr(`{"ok":true}`),
				},
			},
		},
		RawRequest: &httpclient.Request{
			Headers: http.Header{
				"X-Test": []string{"1"},
			},
			Query: map[string][]string{"q": {"1"}},
		},
	}

	candidate := &ChannelModelsCandidate{
		Channel: &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "gpt-fallback", Type: channel.TypeOpenaiResponses}},
		Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4"}},
	}

	sanitized, decision := sanitizeRequestForCandidate(context.Background(), req, candidate, llm.APIFormatAnthropicMessage)
	require.NotNil(t, sanitized)
	require.NotSame(t, req, sanitized)

	require.Equal(t, "gpt-5.4", sanitized.Model)
	require.Equal(t, llm.APIFormatOpenAIResponse, sanitized.APIFormat)
	require.Equal(t, llm.RequestTypeChat, sanitized.RequestType)
	require.NotSame(t, req.RawRequest, sanitized.RawRequest)
	require.Equal(t, req.RawRequest.Headers.Get("X-Test"), sanitized.RawRequest.Headers.Get("X-Test"))
	require.Equal(t, req.RawRequest.Query.Get("q"), sanitized.RawRequest.Query.Get("q"))
	require.Nil(t, sanitized.Metadata)

	require.Equal(t, []string{"reasoning.encrypted_content"}, sanitized.TransformerMetadata["include"])
	require.Equal(t, int64(7), sanitized.TransformerMetadata["max_tool_calls"])
	require.Equal(t, "24h", sanitized.TransformerMetadata["prompt_cache_retention"])
	require.Equal(t, "auto", sanitized.TransformerMetadata["truncation"])
	require.Equal(t, true, sanitized.TransformerMetadata["include_obfuscation"])
	require.NotContains(t, sanitized.TransformerMetadata, "anthropic_ephemeral_cache_control")
	require.NotContains(t, sanitized.TransformerMetadata, "anthropic_reasoning_signature")
	require.NotContains(t, sanitized.TransformerMetadata, "anthropic_tool_result_format")
	require.NotNil(t, decision)
	require.Equal(t, llm.APIFormatAnthropicMessage, decision.SourceAPIFormat)
	require.Equal(t, llm.APIFormatOpenAIResponse, decision.TargetAPIFormat)
	require.True(t, decision.Rebuilt)
	require.Contains(t, decision.RemovedTransformerMetadataKeys, "anthropic_ephemeral_cache_control")
	require.Contains(t, decision.RemovedTransformerMetadataKeys, "anthropic_reasoning_signature")

	outbound, err := openairesponses.NewOutboundTransformerWithConfig(&openairesponses.Config{
		BaseURL:        "https://api.openai.example/v1",
		APIKeyProvider: staticKeyProvider("test-key"),
	})
	require.NoError(t, err)

	httpReq, err := outbound.TransformRequest(context.Background(), sanitized)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(httpReq.Body, &payload))
	require.Equal(t, "gpt-5.4", payload["model"])
	require.Equal(t, systemText, payload["instructions"])
	require.NotContains(t, payload, "messages")
	require.NotContains(t, payload, "system")
	require.NotContains(t, payload, "anthropic_version")
	require.NotContains(t, payload, "thinking")
	require.NotContains(t, payload, "metadata")

	inputItems, ok := payload["input"].([]any)
	require.True(t, ok)
	require.Len(t, inputItems, 2)

	require.Equal(t, "message", inputItems[0].(map[string]any)["type"])
	require.Equal(t, "function_call_output", inputItems[1].(map[string]any)["type"])
	require.Equal(t, toolCallID, inputItems[1].(map[string]any)["call_id"])
}

func TestSanitizeRequestForCandidate_CodexFallbackNormalizesCompactRequestType(t *testing.T) {
	req := &llm.Request{
		Model:       "claude-sonnet-4",
		APIFormat:   llm.APIFormatAnthropicMessage,
		RequestType: llm.RequestTypeChat,
		TransformerMetadata: map[string]any{
			"include":                           []string{"reasoning.encrypted_content"},
			"anthropic_reasoning_signature":     "sig",
			"anthropic_ephemeral_cache_control": true,
		},
	}

	candidate := &ChannelModelsCandidate{
		Channel: &biz.Channel{Channel: &ent.Channel{ID: 3, Name: "codex-fallback", Type: channel.TypeCodex}},
		Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-5.4-codex", ActualModel: "gpt-5.4-codex"}},
	}

	sanitized, decision := sanitizeRequestForCandidate(context.Background(), req, candidate, llm.APIFormatAnthropicMessage)
	require.NotNil(t, sanitized)
	require.NotNil(t, decision)
	require.True(t, decision.Rebuilt)
	require.Equal(t, llm.APIFormatOpenAIResponseCompact, sanitized.APIFormat)
	require.Equal(t, llm.RequestTypeCompact, sanitized.RequestType)
	require.Equal(t, "gpt-5.4-codex", sanitized.Model)
	require.Nil(t, sanitized.Metadata)
	require.Equal(t, []string{"reasoning.encrypted_content"}, sanitized.TransformerMetadata["include"])
	require.NotContains(t, sanitized.TransformerMetadata, "anthropic_reasoning_signature")
	require.NotContains(t, sanitized.TransformerMetadata, "anthropic_ephemeral_cache_control")
	require.Equal(t, []string{"include"}, decision.KeptTransformerMetadataKeys)
	require.Contains(t, decision.RemovedTransformerMetadataKeys, "anthropic_reasoning_signature")
}

func TestSanitizeRequestForCandidate_DoesNotSanitizeNonResponsesTargets(t *testing.T) {
	t.Run("openai chat fallback keeps anthropic metadata", func(t *testing.T) {
		req := &llm.Request{
			Model:       "claude-sonnet-4",
			APIFormat:   llm.APIFormatAnthropicMessage,
			RequestType: llm.RequestTypeChat,
			TransformerMetadata: map[string]any{
				"anthropic_reasoning_signature":     "sig",
				"anthropic_ephemeral_cache_control": true,
			},
		}

		candidate := &ChannelModelsCandidate{
			Channel: &biz.Channel{Channel: &ent.Channel{ID: 4, Name: "openai-chat", Type: channel.TypeOpenai}},
			Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4.1", ActualModel: "gpt-4.1"}},
		}

		sanitized, decision := sanitizeRequestForCandidate(context.Background(), req, candidate, llm.APIFormatAnthropicMessage)
		require.NotNil(t, sanitized)
		require.NotNil(t, decision)
		require.False(t, decision.Rebuilt)
		require.Equal(t, llm.APIFormatAnthropicMessage, sanitized.APIFormat)
		require.Equal(t, llm.RequestTypeChat, sanitized.RequestType)
		require.Equal(t, "sig", sanitized.TransformerMetadata["anthropic_reasoning_signature"])
		require.Equal(t, true, sanitized.TransformerMetadata["anthropic_ephemeral_cache_control"])
		require.Empty(t, decision.RemovedTransformerMetadataKeys)
	})

	t.Run("claudecode target keeps anthropic path untouched", func(t *testing.T) {
		req := &llm.Request{
			Model:       "claude-sonnet-4",
			APIFormat:   llm.APIFormatAnthropicMessage,
			RequestType: llm.RequestTypeChat,
			TransformerMetadata: map[string]any{
				"anthropic_reasoning_signature": "sig",
			},
		}

		candidate := &ChannelModelsCandidate{
			Channel: &biz.Channel{Channel: &ent.Channel{ID: 5, Name: "claudecode", Type: channel.TypeClaudecode}},
			Models:  []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4", ActualModel: "claude-sonnet-4"}},
		}

		sanitized, decision := sanitizeRequestForCandidate(context.Background(), req, candidate, llm.APIFormatAnthropicMessage)
		require.NotNil(t, sanitized)
		require.NotNil(t, decision)
		require.False(t, decision.Rebuilt)
		require.Equal(t, llm.APIFormatAnthropicMessage, sanitized.APIFormat)
		require.Equal(t, llm.RequestTypeChat, sanitized.RequestType)
		require.Equal(t, "sig", sanitized.TransformerMetadata["anthropic_reasoning_signature"])
		require.Empty(t, decision.RemovedTransformerMetadataKeys)
	})
}

type staticKeyProvider string

func (s staticKeyProvider) Get(context.Context) string { return string(s) }
