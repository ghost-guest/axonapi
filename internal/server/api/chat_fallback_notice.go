package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

const fallbackNoticeLabel = "Current model: "

func annotateFallbackResult(
	handlers *ChatCompletionHandlers,
	result orchestrator.ChatCompletionResult,
) orchestrator.ChatCompletionResult {
	apiFormat, notice, ok := fallbackNoticeContext(handlers, result)
	if !ok {
		return result
	}

	switch apiFormat {
	case llm.APIFormatAnthropicMessage:
		if result.ChatCompletion != nil {
			if body, err := prependAnthropicFallbackNoticeToBody(result.ChatCompletion.Body, notice); err == nil {
				resp := *result.ChatCompletion
				resp.Body = body
				result.ChatCompletion = &resp
			}
		}

		if result.ChatCompletionStream != nil {
			result.ChatCompletionStream = &anthropicFallbackNoticeStream{
				source: result.ChatCompletionStream,
				notice: notice,
			}
		}
	case llm.APIFormatOpenAIResponse:
		if result.ChatCompletion != nil {
			if body, err := prependResponsesFallbackNoticeToBody(result.ChatCompletion.Body, notice); err == nil {
				resp := *result.ChatCompletion
				resp.Body = body
				result.ChatCompletion = &resp
			}
		}

		if result.ChatCompletionStream != nil {
			result.ChatCompletionStream = &responsesFallbackNoticeStream{
				source: result.ChatCompletionStream,
				notice: notice,
			}
		}
	}

	return result
}

func fallbackNoticeContext(
	handlers *ChatCompletionHandlers,
	result orchestrator.ChatCompletionResult,
) (llm.APIFormat, string, bool) {
	if handlers == nil || handlers.ChatCompletionOrchestrator == nil || handlers.ChatCompletionOrchestrator.Inbound == nil {
		return "", "", false
	}

	if !result.FallbackUsed || strings.TrimSpace(result.ActualModel) == "" {
		return "", "", false
	}

	return handlers.ChatCompletionOrchestrator.Inbound.APIFormat(), fallbackNoticeLabel + strings.TrimSpace(result.ActualModel) + "\n", true
}

func prependAnthropicFallbackNoticeToBody(body []byte, notice string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal anthropic response: %w", err)
	}

	content, ok := payload["content"].([]any)
	if !ok {
		return nil, fmt.Errorf("anthropic response missing content array")
	}

	if len(content) > 0 {
		if first, ok := content[0].(map[string]any); ok {
			if blockType, _ := first["type"].(string); blockType == "text" {
				if text, _ := first["text"].(string); text != "" {
					first["text"] = ensureNoticePrefix(text, notice)
					content[0] = first
					payload["content"] = content
					return json.Marshal(payload)
				}
			}
		}
	}

	prefixed := append([]any{map[string]any{
		"type": "text",
		"text": notice,
	}}, content...)
	payload["content"] = prefixed

	result, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal anthropic response: %w", err)
	}

	return result, nil
}

func prependResponsesFallbackNoticeToBody(body []byte, notice string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal responses response: %w", err)
	}

	output, ok := payload["output"].([]any)
	if !ok {
		return nil, fmt.Errorf("responses response missing output array")
	}

	for index, rawItem := range output {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		if itemType, _ := item["type"].(string); itemType != "message" {
			continue
		}

		content, ok := item["content"].([]any)
		if !ok {
			content = []any{}
		}

		for contentIndex, rawContent := range content {
			contentItem, ok := rawContent.(map[string]any)
			if !ok {
				continue
			}
			if contentType, _ := contentItem["type"].(string); contentType != "output_text" {
				continue
			}

			if text, _ := contentItem["text"].(string); text != "" {
				contentItem["text"] = ensureNoticePrefix(text, notice)
				content[contentIndex] = contentItem
				item["content"] = content
				output[index] = item
				payload["output"] = output

				return json.Marshal(payload)
			}
		}

		item["content"] = append([]any{map[string]any{
			"type": "output_text",
			"text": notice,
		}}, content...)
		output[index] = item
		payload["output"] = output

		return json.Marshal(payload)
	}

	payload["output"] = append(output, map[string]any{
		"type":   "message",
		"role":   "assistant",
		"status": "completed",
		"content": []any{
			map[string]any{
				"type": "output_text",
				"text": notice,
			},
		},
	})

	result, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal responses response: %w", err)
	}

	return result, nil
}

func ensureNoticePrefix(text string, notice string) string {
	if strings.HasPrefix(text, notice) {
		return text
	}

	return notice + text
}

type anthropicFallbackNoticeStream struct {
	source   streams.Stream[*httpclient.StreamEvent]
	notice   string
	injected bool
	current  *httpclient.StreamEvent
}

func (s *anthropicFallbackNoticeStream) Next() bool {
	if !s.source.Next() {
		return false
	}

	current := s.source.Current()
	if !s.injected {
		if event, ok := prependAnthropicNoticeToTextDelta(current, s.notice); ok {
			s.injected = true
			s.current = event
			return true
		}
	}

	s.current = current
	return true
}

func (s *anthropicFallbackNoticeStream) Current() *httpclient.StreamEvent {
	return s.current
}

func (s *anthropicFallbackNoticeStream) Err() error {
	return s.source.Err()
}

func (s *anthropicFallbackNoticeStream) Close() error {
	return s.source.Close()
}

func prependAnthropicNoticeToTextDelta(
	event *httpclient.StreamEvent,
	notice string,
) (*httpclient.StreamEvent, bool) {
	if event == nil || len(event.Data) == 0 {
		return event, false
	}

	if gjson.GetBytes(event.Data, "type").String() != "content_block_delta" {
		return event, false
	}
	if gjson.GetBytes(event.Data, "delta.type").String() != "text_delta" {
		return event, false
	}

	textResult := gjson.GetBytes(event.Data, "delta.text")
	if !textResult.Exists() || textResult.Type != gjson.String {
		return event, false
	}

	updated, err := sjson.SetBytes(event.Data, "delta.text", ensureNoticePrefix(textResult.String(), notice))
	if err != nil {
		return event, false
	}

	cloned := *event
	cloned.Data = updated

	return &cloned, true
}

type responsesFallbackNoticeStream struct {
	source   streams.Stream[*httpclient.StreamEvent]
	notice   string
	injected bool
	current  *httpclient.StreamEvent
}

func (s *responsesFallbackNoticeStream) Next() bool {
	if !s.source.Next() {
		return false
	}

	current := s.source.Current()
	if event, ok := annotateResponsesNoticeEvent(current, s.notice, &s.injected); ok {
		s.current = event
		return true
	}

	s.current = current
	return true
}

func (s *responsesFallbackNoticeStream) Current() *httpclient.StreamEvent {
	return s.current
}

func (s *responsesFallbackNoticeStream) Err() error {
	return s.source.Err()
}

func (s *responsesFallbackNoticeStream) Close() error {
	return s.source.Close()
}

func annotateResponsesNoticeEvent(
	event *httpclient.StreamEvent,
	notice string,
	injected *bool,
) (*httpclient.StreamEvent, bool) {
	if event == nil || len(event.Data) == 0 {
		return event, false
	}

	eventType := event.Type
	if eventType == "" {
		eventType = gjson.GetBytes(event.Data, "type").String()
	}

	switch eventType {
	case "response.output_text.delta":
		return prependJSONTextField(event, "delta", notice, injected, true)
	case "response.output_text.done":
		return prependJSONTextField(event, "text", notice, injected, false)
	case "response.content_part.done":
		if gjson.GetBytes(event.Data, "part.type").String() != "output_text" {
			return event, false
		}
		return prependJSONTextField(event, "part.text", notice, injected, false)
	case "response.output_item.done":
		return prependResponsesOutputMessageText(event, "item", notice)
	case "response.completed":
		return prependResponsesOutputMessageText(event, "response.output", notice)
	default:
		return event, false
	}
}

func prependJSONTextField(
	event *httpclient.StreamEvent,
	path string,
	notice string,
	injected *bool,
	markInjected bool,
) (*httpclient.StreamEvent, bool) {
	textResult := gjson.GetBytes(event.Data, path)
	if !textResult.Exists() || textResult.Type != gjson.String {
		return event, false
	}

	updated, err := sjson.SetBytes(event.Data, path, ensureNoticePrefix(textResult.String(), notice))
	if err != nil {
		return event, false
	}

	if markInjected && injected != nil {
		*injected = true
	}

	cloned := *event
	cloned.Data = updated

	return &cloned, true
}

func prependResponsesOutputMessageText(
	event *httpclient.StreamEvent,
	rootPath string,
	notice string,
) (*httpclient.StreamEvent, bool) {
	if rootPath == "item" {
		if gjson.GetBytes(event.Data, "item.type").String() != "message" {
			return event, false
		}

		content := gjson.GetBytes(event.Data, "item.content")
		if !content.Exists() || !content.IsArray() {
			return event, false
		}

		for contentIndex, contentItem := range content.Array() {
			if contentItem.Get("type").String() != "output_text" {
				continue
			}

			textResult := contentItem.Get("text")
			if !textResult.Exists() || textResult.Type != gjson.String {
				continue
			}

			path := fmt.Sprintf("item.content.%d.text", contentIndex)
			updated, err := sjson.SetBytes(event.Data, path, ensureNoticePrefix(textResult.String(), notice))
			if err != nil {
				return event, false
			}

			cloned := *event
			cloned.Data = updated

			return &cloned, true
		}

		return event, false
	}

	outputResult := gjson.GetBytes(event.Data, rootPath)
	if !outputResult.Exists() || !outputResult.IsArray() {
		return event, false
	}

	for itemIndex, item := range outputResult.Array() {
		if item.Get("type").String() != "message" {
			continue
		}

		content := item.Get("content")
		if !content.Exists() || !content.IsArray() {
			continue
		}

		for contentIndex, contentItem := range content.Array() {
			if contentItem.Get("type").String() != "output_text" {
				continue
			}

			textResult := contentItem.Get("text")
			if !textResult.Exists() || textResult.Type != gjson.String {
				continue
			}

			path := fmt.Sprintf("%s.%d.content.%d.text", rootPath, itemIndex, contentIndex)
			updated, err := sjson.SetBytes(event.Data, path, ensureNoticePrefix(textResult.String(), notice))
			if err != nil {
				return event, false
			}

			cloned := *event
			cloned.Data = updated

			return &cloned, true
		}
	}

	return event, false
}
