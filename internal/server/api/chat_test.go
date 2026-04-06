package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/server/orchestrator"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// errorAfterStream emits items then returns an error.
type errorAfterStream struct {
	items []*httpclient.StreamEvent
	idx   int
	err   error
}

func (s *errorAfterStream) Next() bool {
	if s.idx < len(s.items) {
		return true
	}

	return false
}

func (s *errorAfterStream) Current() *httpclient.StreamEvent {
	item := s.items[s.idx]
	s.idx++

	return item
}

func (s *errorAfterStream) Err() error {
	if s.idx >= len(s.items) {
		return s.err
	}

	return nil
}

func (s *errorAfterStream) Close() error { return nil }

func TestWriteSSEStream_Success(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	events := []*httpclient.StreamEvent{
		{Type: "", Data: []byte(`{"id":"1","choices":[{"delta":{"content":"Hi"}}]}`)},
		{Type: "", Data: []byte(`[DONE]`)},
	}
	stream := streams.SliceStream(events)

	WriteSSEStream(c, stream)

	body := w.Body.String()
	assert.Contains(t, body, `{"id":"1","choices":[{"delta":{"content":"Hi"}}]}`)
	assert.Contains(t, body, `[DONE]`)
}

func TestWriteSSEStream_ErrorFormatsAsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	streamErr := errors.New("upstream connection reset")
	stream := &errorAfterStream{
		items: []*httpclient.StreamEvent{
			{Type: "", Data: []byte(`{"id":"1","choices":[{"delta":{"content":"He"}}]}`)},
		},
		err: streamErr,
	}

	WriteSSEStream(c, stream)

	body := w.Body.String()

	// The error event should be JSON-formatted, not a plain string
	assert.Contains(t, body, "event:error")

	// Extract the data line from the error event
	lines := strings.Split(body, "\n")

	var errorData string

	foundError := false

	for i, line := range lines {
		if strings.HasPrefix(line, "event:error") {
			foundError = true
			// The next line should be the data
			if i+1 < len(lines) {
				errorData = strings.TrimPrefix(lines[i+1], "data:")
			}

			break
		}
	}

	require.True(t, foundError, "should contain an error event")
	require.NotEmpty(t, errorData, "error event should have data")

	// Parse the JSON error
	var errObj map[string]any

	err := json.Unmarshal([]byte(errorData), &errObj)
	require.NoError(t, err, "error data should be valid JSON: %s", errorData)

	// Verify structure
	errorField, ok := errObj["error"].(map[string]any)
	require.True(t, ok, "should have 'error' field")
	assert.Equal(t, "upstream connection reset", errorField["message"])
	assert.Equal(t, "server_error", errorField["type"])
	_, hasCode := errorField["code"]
	assert.True(t, hasCode)
}

func TestWriteSSEStream_HttpClientError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	httpErr := &httpclient.Error{
		StatusCode: 429,
		Body:       []byte(`{"error":{"message":"Rate limit exceeded","type":"rate_limit_error"}}`),
	}
	stream := &errorAfterStream{err: httpErr}

	WriteSSEStream(c, stream)

	body := w.Body.String()

	// Extract error data
	lines := strings.Split(body, "\n")

	var errorData string

	for i, line := range lines {
		if strings.HasPrefix(line, "event:error") {
			if i+1 < len(lines) {
				errorData = strings.TrimPrefix(lines[i+1], "data:")
			}

			break
		}
	}

	require.NotEmpty(t, errorData)

	var errObj map[string]any

	err := json.Unmarshal([]byte(errorData), &errObj)
	require.NoError(t, err)

	errorField, ok := errObj["error"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Rate limit exceeded", errorField["message"])
	assert.Equal(t, "rate_limit_error", errorField["type"])
	assert.Empty(t, errorField["code"])
}

func TestWriteSSEStream_CustomErrorFormatter(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	streamErr := errors.New("custom error")
	stream := &errorAfterStream{err: streamErr}

	customFormatter := func(_ context.Context, err error) any {
		return gin.H{"custom_error": err.Error()}
	}

	WriteSSEStreamWithErrorFormatter(c, stream, customFormatter)

	body := w.Body.String()
	lines := strings.Split(body, "\n")

	var errorData string

	for i, line := range lines {
		if strings.HasPrefix(line, "event:error") {
			if i+1 < len(lines) {
				errorData = strings.TrimPrefix(lines[i+1], "data:")
			}

			break
		}
	}

	require.NotEmpty(t, errorData)

	var errObj map[string]any

	err := json.Unmarshal([]byte(errorData), &errObj)
	require.NoError(t, err)
	assert.Equal(t, "custom error", errObj["custom_error"])
}

func TestWriteSSEStream_NoError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	stream := streams.SliceStream([]*httpclient.StreamEvent{
		{Type: "", Data: []byte(`[DONE]`)},
	})

	WriteSSEStream(c, stream)

	body := w.Body.String()
	assert.NotContains(t, body, "event:error")
}

func TestFormatStreamError_PlainError(t *testing.T) {
	err := errors.New("something went wrong")
	result := FormatStreamError(context.Background(), err)

	data, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	errorField := parsed["error"].(map[string]any)
	assert.Equal(t, "something went wrong", errorField["message"])
	assert.Equal(t, "server_error", errorField["type"])
	assert.Equal(t, "", errorField["code"])
}

func TestFormatStreamError_HttpClientError(t *testing.T) {
	httpErr := &httpclient.Error{
		StatusCode: 500,
		Body:       []byte(`{"error":{"message":"Internal server error","type":"internal_error"}}`),
	}
	result := FormatStreamError(context.Background(), httpErr)

	data, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	errorField := parsed["error"].(map[string]any)
	assert.Equal(t, "Internal server error", errorField["message"])
	assert.Equal(t, "internal_error", errorField["type"])
	assert.Equal(t, "", errorField["code"])
}

func TestFormatStreamError_LlmResponseError_PassesCodeAndRequestID(t *testing.T) {
	respErr := &llm.ResponseError{
		Detail: llm.ErrorDetail{
			Code:      "1311",
			Message:   "当前订阅套餐暂未开放GPT-6权限",
			Type:      "permission_error",
			RequestID: "202603112254417d15bd26697445b0",
		},
	}

	result := FormatStreamError(context.Background(), respErr)
	data, marshalErr := json.Marshal(result)
	require.NoError(t, marshalErr)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(data, &parsed))

	errorField := parsed["error"].(map[string]any)
	assert.Equal(t, "当前订阅套餐暂未开放GPT-6权限", errorField["message"])
	assert.Equal(t, "permission_error", errorField["type"])
	assert.Equal(t, "1311", errorField["code"])
	assert.Equal(t, "202603112254417d15bd26697445b0", parsed["request_id"])
}

func TestPrependAnthropicFallbackNoticeToBody_PrependsTextBlock(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"model":"glm-5.1"}`)

	updated, err := prependAnthropicFallbackNoticeToBody(body, "Current model: glm-5.1\n")
	require.NoError(t, err)

	assert.Contains(t, string(updated), `Current model: glm-5.1`)
	assert.Contains(t, string(updated), `Hello`)
}

func TestPrependResponsesFallbackNoticeToBody_PrependsOutputText(t *testing.T) {
	body := []byte(`{"id":"resp_1","object":"response","model":"gpt-5.4","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}]}`)

	updated, err := prependResponsesFallbackNoticeToBody(body, "Current model: gpt-5.4\n")
	require.NoError(t, err)

	assert.Contains(t, string(updated), `Current model: gpt-5.4`)
	assert.Contains(t, string(updated), `Hello`)
}

func TestAnthropicFallbackNoticeStream_PrependsFirstTextDelta(t *testing.T) {
	stream := &anthropicFallbackNoticeStream{
		source: streams.SliceStream([]*httpclient.StreamEvent{
			{
				Type: "content_block_delta",
				Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`),
			},
			{
				Type: "message_stop",
				Data: []byte(`{"type":"message_stop"}`),
			},
		}),
		notice: "Current model: glm-5.1\n",
	}

	require.True(t, stream.Next())
	assert.Contains(t, string(stream.Current().Data), `Current model: glm-5.1\nHello`)

	require.True(t, stream.Next())
	assert.Equal(t, "message_stop", stream.Current().Type)
}

func TestResponsesFallbackNoticeStream_PatchesDeltaAndCompleted(t *testing.T) {
	stream := &responsesFallbackNoticeStream{
		source: streams.SliceStream([]*httpclient.StreamEvent{
			{
				Type: "response.output_text.delta",
				Data: []byte(`{"type":"response.output_text.delta","item_id":"msg_1","output_index":0,"content_index":0,"delta":"Hello"}`),
			},
			{
				Type: "response.output_text.done",
				Data: []byte(`{"type":"response.output_text.done","item_id":"msg_1","output_index":0,"content_index":0,"text":"Hello"}`),
			},
			{
				Type: "response.completed",
				Data: []byte(`{"type":"response.completed","response":{"id":"resp_1","object":"response","model":"gpt-5.4","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}]}}`),
			},
		}),
		notice: "Current model: gpt-5.4\n",
	}

	require.True(t, stream.Next())
	assert.Contains(t, string(stream.Current().Data), `Current model: gpt-5.4\nHello`)

	require.True(t, stream.Next())
	assert.Contains(t, string(stream.Current().Data), `Current model: gpt-5.4\nHello`)

	require.True(t, stream.Next())
	assert.Contains(t, string(stream.Current().Data), `Current model: gpt-5.4\nHello`)
}

func TestAnnotateFallbackResult_AnthropicFallback(t *testing.T) {
	handlers := &ChatCompletionHandlers{
		ChatCompletionOrchestrator: &orchestrator.ChatCompletionOrchestrator{
			Inbound: anthropic.NewInboundTransformer(),
		},
	}

	result := annotateFallbackResult(handlers, orchestrator.ChatCompletionResult{
		ChatCompletion: &httpclient.Response{
			Body: []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"Hello"}],"model":"glm-5.1"}`),
		},
		RequestedModel: "claude-sonnet-4-6",
		ActualModel:    "glm-5.1",
		FallbackUsed:   true,
	})

	require.NotNil(t, result.ChatCompletion)
	assert.Contains(t, string(result.ChatCompletion.Body), `Current model: glm-5.1`)
}

func TestAnnotateFallbackResult_ResponsesFallback(t *testing.T) {
	handlers := &ChatCompletionHandlers{
		ChatCompletionOrchestrator: &orchestrator.ChatCompletionOrchestrator{
			Inbound: responses.NewInboundTransformer(),
		},
	}

	result := annotateFallbackResult(handlers, orchestrator.ChatCompletionResult{
		ChatCompletion: &httpclient.Response{
			Body: []byte(`{"id":"resp_1","object":"response","model":"gpt-5.4","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}]}`),
		},
		RequestedModel: "codex-mini-latest",
		ActualModel:    "gpt-5.4",
		FallbackUsed:   true,
	})

	require.NotNil(t, result.ChatCompletion)
	assert.Contains(t, string(result.ChatCompletion.Body), `Current model: gpt-5.4`)
}
