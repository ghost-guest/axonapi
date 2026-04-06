package orchestrator

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

type payloadSanitizationDecision struct {
	SourceAPIFormat                llm.APIFormat
	TargetAPIFormat                llm.APIFormat
	SourceChannelType              channel.Type
	TargetChannelType              channel.Type
	Rebuilt                        bool
	RemovedTransformerMetadataKeys []string
	KeptTransformerMetadataKeys    []string
	HadInstructions                bool
	HadInputMessages               bool
	HadMetadata                    bool
	HasTools                       bool
}

var openAIResponsesAllowedTransformerMetadataKeys = map[string]struct{}{
	"include":                {},
	"max_tool_calls":         {},
	"prompt_cache_retention": {},
	"truncation":             {},
	"include_obfuscation":    {},
}

func sanitizeRequestForCandidate(
	ctx context.Context,
	llmRequest *llm.Request,
	candidate *ChannelModelsCandidate,
	inboundAPIFormat llm.APIFormat,
) (*llm.Request, *payloadSanitizationDecision) {
	if llmRequest == nil {
		return nil, nil
	}

	decision := &payloadSanitizationDecision{
		SourceAPIFormat:   nonZeroAPIFormat(llmRequest.APIFormat, inboundAPIFormat),
		TargetAPIFormat:   resolveTargetAPIFormatForCandidate(candidate),
		SourceChannelType: resolveSourceChannelType(inboundAPIFormat),
		TargetChannelType: resolveTargetChannelType(candidate),
		HadMetadata:       len(llmRequest.Metadata) > 0,
		HasTools:          len(llmRequest.Tools) > 0,
	}

	sanitized := cloneLLMRequestForFallback(llmRequest)
	decision.HadInputMessages = len(sanitized.Messages) > 0
	decision.HadInstructions = hasInstructionMessages(sanitized.Messages)

	targetModel := candidateActualModel(candidate)
	if targetModel != "" {
		sanitized.Model = targetModel
	}

	if shouldRebuildRequestForCandidate(decision.SourceAPIFormat, decision.TargetAPIFormat, candidate) {
		decision.Rebuilt = true
		sanitized.APIFormat = decision.TargetAPIFormat
		sanitized.RequestType = normalizeRequestTypeForTarget(sanitized.RequestType, decision.TargetAPIFormat)
		sanitized.TransformerMetadata, decision.RemovedTransformerMetadataKeys, decision.KeptTransformerMetadataKeys =
			sanitizeTransformerMetadataForTarget(sanitized.TransformerMetadata, decision.TargetAPIFormat)
	} else {
		decision.KeptTransformerMetadataKeys = sortedAnyMapKeys(sanitized.TransformerMetadata)
		if sanitized.APIFormat == "" {
			sanitized.APIFormat = decision.TargetAPIFormat
		}
	}

	logPayloadSanitizationDecision(ctx, sanitized, decision)

	return sanitized, decision
}

func cloneLLMRequestForFallback(src *llm.Request) *llm.Request {
	if src == nil {
		return nil
	}

	cloned := *src
	cloned.Messages = cloneMessages(src.Messages)
	cloned.Tools = append([]llm.Tool(nil), src.Tools...)
	cloned.Metadata = maps.Clone(src.Metadata)
	cloned.LogitBias = maps.Clone(src.LogitBias)
	cloned.TransformerMetadata = cloneTransformerMetadata(src.TransformerMetadata)
	cloned.RawRequest = cloneRawHTTPRequest(src.RawRequest)

	return &cloned
}

func cloneMessages(src []llm.Message) []llm.Message {
	if len(src) == 0 {
		return nil
	}

	cloned := make([]llm.Message, len(src))
	for i, msg := range src {
		cloned[i] = msg
		cloned[i].ToolCalls = append([]llm.ToolCall(nil), msg.ToolCalls...)
		cloned[i].Content = cloneMessageContent(msg.Content)
	}

	return cloned
}

func cloneMessageContent(src llm.MessageContent) llm.MessageContent {
	cloned := llm.MessageContent{Content: src.Content}
	if len(src.MultipleContent) == 0 {
		return cloned
	}

	cloned.MultipleContent = make([]llm.MessageContentPart, len(src.MultipleContent))
	for i, part := range src.MultipleContent {
		cloned.MultipleContent[i] = part
		cloned.MultipleContent[i].TransformerMetadata = cloneTransformerMetadata(part.TransformerMetadata)
	}

	return cloned
}

func cloneRawHTTPRequest(src *httpclient.Request) *httpclient.Request {
	if src == nil {
		return nil
	}

	cloned := *src
	if src.Headers != nil {
		cloned.Headers = src.Headers.Clone()
	}
	if src.Query != nil {
		cloned.Query = url.Values{}
		for k, values := range src.Query {
			cloned.Query[k] = append([]string(nil), values...)
		}
	}
	cloned.Metadata = maps.Clone(src.Metadata)
	cloned.TransformerMetadata = cloneTransformerMetadata(src.TransformerMetadata)
	cloned.RawRequest = src.RawRequest

	return &cloned
}

func cloneTransformerMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}

	cloned := make(map[string]any, len(src))
	for k, v := range src {
		switch typed := v.(type) {
		case []string:
			cloned[k] = append([]string(nil), typed...)
		case []any:
			cloned[k] = append([]any(nil), typed...)
		case map[string]any:
			cloned[k] = maps.Clone(typed)
		default:
			cloned[k] = v
		}
	}

	return cloned
}

func sanitizeTransformerMetadataForTarget(metadata map[string]any, target llm.APIFormat) (map[string]any, []string, []string) {
	if len(metadata) == 0 {
		return nil, nil, nil
	}

	if target != llm.APIFormatOpenAIResponse && target != llm.APIFormatOpenAIResponseCompact {
		return metadata, nil, sortedAnyMapKeys(metadata)
	}

	kept := make(map[string]any)
	var removedKeys []string
	for key, value := range metadata {
		if _, ok := openAIResponsesAllowedTransformerMetadataKeys[key]; ok {
			kept[key] = value
			continue
		}
		removedKeys = append(removedKeys, key)
	}

	slices.Sort(removedKeys)
	keptKeys := sortedAnyMapKeys(kept)
	if len(kept) == 0 {
		return nil, removedKeys, keptKeys
	}

	return kept, removedKeys, keptKeys
}

func shouldRebuildRequestForCandidate(sourceFormat, targetFormat llm.APIFormat, candidate *ChannelModelsCandidate) bool {
	if candidate == nil || candidate.Channel == nil {
		return false
	}
	if targetFormat == "" {
		return false
	}
	if sourceFormat == "" {
		return false
	}
	if sourceFormat == targetFormat {
		return false
	}

	switch candidate.Channel.Type {
	case channel.TypeOpenaiResponses, channel.TypeCodex:
		return targetFormat == llm.APIFormatOpenAIResponse || targetFormat == llm.APIFormatOpenAIResponseCompact
	default:
		return false
	}
}

func resolveTargetAPIFormatForCandidate(candidate *ChannelModelsCandidate) llm.APIFormat {
	if candidate == nil || candidate.Channel == nil {
		return ""
	}
	if candidate.Channel.Outbound != nil {
		if apiFormat := candidate.Channel.Outbound.APIFormat(); apiFormat != "" {
			return apiFormat
		}
	}

	switch candidate.Channel.Type {
	case channel.TypeOpenaiResponses:
		return llm.APIFormatOpenAIResponse
	case channel.TypeCodex:
		return llm.APIFormatOpenAIResponseCompact
	case channel.TypeOpenai, channel.TypeGeminiOpenai:
		return llm.APIFormatOpenAIChatCompletion
	case channel.TypeAnthropic, channel.TypeAnthropicAWS, channel.TypeAnthropicGcp, channel.TypeClaudecode:
		return llm.APIFormatAnthropicMessage
	case channel.TypeGemini, channel.TypeGeminiVertex, channel.TypeAntigravity:
		return llm.APIFormatGeminiContents
	default:
		if candidate.Channel.Type.IsAnthropicLike() {
			return llm.APIFormatAnthropicMessage
		}
		if candidate.Channel.Type.IsOpenAI() {
			return llm.APIFormatOpenAIChatCompletion
		}
		return ""
	}
}

func normalizeRequestTypeForTarget(requestType llm.RequestType, target llm.APIFormat) llm.RequestType {
	if requestType == "" {
		requestType = llm.RequestTypeChat
	}
	switch target {
	case llm.APIFormatOpenAIResponseCompact:
		return llm.RequestTypeCompact
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIChatCompletion, llm.APIFormatAnthropicMessage, llm.APIFormatGeminiContents:
		return llm.RequestTypeChat
	default:
		return requestType
	}
}

func candidateActualModel(candidate *ChannelModelsCandidate) string {
	if candidate == nil || len(candidate.Models) == 0 {
		return ""
	}
	return candidate.Models[0].ActualModel
}

func hasInstructionMessages(messages []llm.Message) bool {
	for _, msg := range messages {
		if strings.EqualFold(msg.Role, "system") || strings.EqualFold(msg.Role, "developer") {
			return true
		}
	}
	return false
}

func nonZeroAPIFormat(values ...llm.APIFormat) llm.APIFormat {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func resolveSourceChannelType(apiFormat llm.APIFormat) channel.Type {
	switch apiFormat {
	case llm.APIFormatAnthropicMessage:
		return channel.TypeAnthropic
	case llm.APIFormatOpenAIResponse, llm.APIFormatOpenAIResponseCompact:
		return channel.TypeOpenaiResponses
	case llm.APIFormatOpenAIChatCompletion:
		return channel.TypeOpenai
	default:
		return ""
	}
}

func resolveTargetChannelType(candidate *ChannelModelsCandidate) channel.Type {
	if candidate == nil || candidate.Channel == nil {
		return ""
	}
	return candidate.Channel.Type
}

func sortedAnyMapKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func logPayloadSanitizationDecision(ctx context.Context, req *llm.Request, decision *payloadSanitizationDecision) {
	if decision == nil {
		return
	}

	fields := []log.Field{
		log.Bool("rebuilt", decision.Rebuilt),
		log.String("source_api_format", string(decision.SourceAPIFormat)),
		log.String("target_api_format", string(decision.TargetAPIFormat)),
		log.String("source_channel_type", decision.SourceChannelType.String()),
		log.String("target_channel_type", decision.TargetChannelType.String()),
		log.String("model", req.Model),
		log.Bool("has_instructions", decision.HadInstructions),
		log.Bool("has_input_messages", decision.HadInputMessages),
		log.Bool("has_metadata", decision.HadMetadata),
		log.Bool("has_tools", decision.HasTools),
		log.Any("kept_transformer_metadata_keys", decision.KeptTransformerMetadataKeys),
		log.Any("removed_transformer_metadata_keys", decision.RemovedTransformerMetadataKeys),
	}

	log.Debug(ctx, "fallback payload sanitization decision", fields...)
}

func formatSanitizationDecision(decision *payloadSanitizationDecision) string {
	if decision == nil {
		return ""
	}

	return fmt.Sprintf(
		"rebuilt=%t source_api_format=%s target_api_format=%s source_channel_type=%s target_channel_type=%s removed_transformer_metadata_keys=%v kept_transformer_metadata_keys=%v",
		decision.Rebuilt,
		decision.SourceAPIFormat,
		decision.TargetAPIFormat,
		decision.SourceChannelType,
		decision.TargetChannelType,
		decision.RemovedTransformerMetadataKeys,
		decision.KeptTransformerMetadataKeys,
	)
}
