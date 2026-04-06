package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/request"
	"github.com/looplj/axonhub/internal/ent/requestexecution"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/streams"
)

// mockTransformer is a simple mock transformer for testing.
type mockTransformer struct {
	aggregatedResponse []byte
	aggregatedMeta     llm.ResponseMeta
	aggregatedErr      error
	apiFormat          llm.APIFormat
}

func (m *mockTransformer) TransformRequest(ctx context.Context, req *llm.Request) (*httpclient.Request, error) {
	body, err := json.Marshal(map[string]any{
		"model":       req.Model,
		"messages":    req.Messages,
		"temperature": 0.5,
		"max_tokens":  1000,
	})
	if err != nil {
		return nil, err
	}

	return &httpclient.Request{
		Method: "POST",
		URL:    "https://api.example.com/v1/chat/completions",
		Body:   body,
	}, nil
}

func (m *mockTransformer) TransformResponse(ctx context.Context, resp *httpclient.Response) (*llm.Response, error) {
	return &llm.Response{}, nil
}

func (m *mockTransformer) TransformStream(ctx context.Context, stream streams.Stream[*httpclient.StreamEvent]) (streams.Stream[*llm.Response], error) {
	return nil, nil
}

func (m *mockTransformer) TransformError(ctx context.Context, err *httpclient.Error) *llm.ResponseError {
	return nil
}

func (m *mockTransformer) AggregateStreamChunks(ctx context.Context, chunks []*httpclient.StreamEvent) ([]byte, llm.ResponseMeta, error) {
	return m.aggregatedResponse, m.aggregatedMeta, m.aggregatedErr
}

func (m *mockTransformer) APIFormat() llm.APIFormat {
	if m.apiFormat != "" {
		return m.apiFormat
	}

	return llm.APIFormatOpenAIChatCompletion
}

func TestPersistentOutboundTransformer_TransformRequest_OriginalModelRestoration(t *testing.T) {
	tests := []struct {
		name               string
		originalModel      string
		inputModel         string
		actualModel        string
		expectedFinalModel string
	}{
		{
			name:               "no original model - should use candidate ActualModel",
			originalModel:      "",
			inputModel:         "gpt-4",
			actualModel:        "gpt-4",
			expectedFinalModel: "gpt-4",
		},
		{
			name:               "has original model - should use candidate ActualModel (not OriginalModel)",
			originalModel:      "gpt-3.5-turbo",
			inputModel:         "mapped-gpt-4",
			actualModel:        "gpt-4",
			expectedFinalModel: "gpt-4",
		},
		{
			name:               "candidate ActualModel different from input - should use ActualModel",
			originalModel:      "gpt-4",
			inputModel:         "mapped-gpt-4",
			actualModel:        "claude-3-opus",
			expectedFinalModel: "claude-3-opus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			ctx := context.Background()

			channel := &biz.Channel{
				Channel: &ent.Channel{
					ID:              1,
					Name:            "test-channel",
					SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"},
					Settings:        nil,
				},
				Outbound: &mockTransformer{},
			}

			processor := &PersistentOutboundTransformer{
				wrapped: &mockTransformer{},
				state: &PersistenceState{
					OriginalModel:    tt.originalModel,
					CurrentCandidate: &ChannelModelsCandidate{Channel: channel},
					ChannelModelsCandidates: []*ChannelModelsCandidate{
						{Channel: channel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: tt.inputModel, ActualModel: tt.actualModel}}},
					},
					CurrentCandidateIndex: 0,
					RequestExec:           &ent.RequestExecution{ID: 1}, // Dummy to skip creation
				},
			}

			text := "Hello"
			llmRequest := &llm.Request{
				Model: tt.inputModel,
				Messages: []llm.Message{
					{
						Role: "user",
						Content: llm.MessageContent{
							Content: &text,
						},
					},
				},
			}

			// Execute
			channelRequest, err := processor.TransformRequest(ctx, llmRequest)

			// Assert
			require.NoError(t, err)
			require.NotNil(t, channelRequest)

			// Verify model restoration in the request body
			bodyStr := string(channelRequest.Body)
			model := gjson.Get(bodyStr, "model")
			require.Equal(t, tt.expectedFinalModel, model.String())

			// Verify the original llmRequest remains untouched after sanitization clone
			require.Equal(t, tt.inputModel, llmRequest.Model)
		})
	}
}

func TestPersistentOutboundTransformer_TransformRequest_UsesAnthropicCompatOutboundForClaudeOnResponsesChannel(t *testing.T) {
	ctx := context.Background()

	channelModel := &biz.Channel{
		Channel: &ent.Channel{
			ID:       2,
			Name:     "muyuan",
			Type:     channel.TypeOpenaiResponses,
			BaseURL:  "https://muyuan.do/v1",
			Settings: &objects.ChannelSettings{},
			Credentials: objects.ChannelCredentials{
				APIKey: "test-key",
			},
		},
		Outbound: &mockTransformer{apiFormat: llm.APIFormatOpenAIResponse},
	}

	processor := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			OriginalModel:    "claude-sonnet-4-5",
			InboundAPIFormat: llm.APIFormatAnthropicMessage,
			ChannelModelsCandidates: []*ChannelModelsCandidate{
				{
					Channel: channelModel,
					Models: []biz.ChannelModelEntry{
						{RequestModel: "claude-sonnet-4-5", ActualModel: "claude-sonnet-4-5"},
					},
				},
			},
		},
	}

	req, err := processor.TransformRequest(ctx, &llm.Request{
		Model: "claude-sonnet-4-5",
		Messages: []llm.Message{{
			Role: "user",
			Content: llm.MessageContent{
				Content: lo.ToPtr("reply ok"),
			},
		}},
		MaxTokens: lo.ToPtr(int64(32)),
	})
	require.NoError(t, err)
	require.NotNil(t, req)
	require.Equal(t, "https://muyuan.do/v1/messages", req.URL)
	require.Equal(t, llm.APIFormatAnthropicMessage, processor.wrapped.APIFormat())
	require.NotNil(t, req.Auth)
	require.Equal(t, httpclient.AuthTypeBearer, req.Auth.Type)
	require.Equal(t, "2023-06-01", req.Headers.Get("Anthropic-Version"))
	require.Equal(t, "claude-sonnet-4-5", gjson.GetBytes(req.Body, "model").String())
}

func TestPersistentOutboundTransformer_TransformRequest_KeepsResponsesOutboundForOpenAIResponsesRequests(t *testing.T) {
	ctx := context.Background()

	channelModel := &biz.Channel{
		Channel: &ent.Channel{
			ID:       3,
			Name:     "responses",
			Type:     channel.TypeOpenaiResponses,
			BaseURL:  "https://responses.example/v1",
			Settings: &objects.ChannelSettings{},
			Credentials: objects.ChannelCredentials{
				APIKey: "test-key",
			},
		},
		Outbound: &mockTransformer{apiFormat: llm.APIFormatOpenAIResponse},
	}

	processor := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			OriginalModel:    "gpt-5.4",
			InboundAPIFormat: llm.APIFormatOpenAIResponse,
			ChannelModelsCandidates: []*ChannelModelsCandidate{
				{
					Channel: channelModel,
					Models: []biz.ChannelModelEntry{
						{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4"},
					},
				},
			},
		},
	}

	req, err := processor.TransformRequest(ctx, &llm.Request{
		Model: "gpt-5.4",
		Messages: []llm.Message{{
			Role: "user",
			Content: llm.MessageContent{
				Content: lo.ToPtr("reply ok"),
			},
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, req)
	require.Equal(t, "https://api.example.com/v1/chat/completions", req.URL)
	require.Equal(t, llm.APIFormatOpenAIResponse, processor.wrapped.APIFormat())
}

func TestPersistentOutboundTransformer_PrepareForRetry(t *testing.T) {
	// Setup
	ctx := context.Background()

	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	t.Run("same-channel retry is disabled in favor of immediate failover", func(t *testing.T) {
		processor := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
				CurrentCandidateIndex: 0,
				CurrentModelIndex:     0,
				RequestExec:           &ent.RequestExecution{ID: 1},
				ChannelModelsCandidates: []*ChannelModelsCandidate{
					{Channel: channel, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}}},
				},
			},
		}

		err := processor.PrepareForRetry(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "same-channel retry disabled")
	})
}

func TestPersistentOutboundTransformer_CanRetry(t *testing.T) {
	channel := &biz.Channel{
		Channel: &ent.Channel{
			ID:   1,
			Name: "test-channel",
		},
		Outbound: &mockTransformer{},
	}

	retryableErr := &httpclient.Error{StatusCode: http.StatusTooManyRequests}
	nonRetryableErr := &httpclient.Error{StatusCode: http.StatusBadRequest}

	t.Run("no current candidate", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: nil,
			},
		}

		require.False(t, outbound.CanRetry(retryableErr))
	})

	t.Run("nil error", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
			},
		}

		require.False(t, outbound.CanRetry(nil))
	})

	t.Run("non-retryable error", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
			},
		}

		require.False(t, outbound.CanRetry(nonRetryableErr))
	})

	t.Run("skip-by-circuit-breaker should not trigger same-channel retry", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models: []biz.ChannelModelEntry{
						{RequestModel: "gpt-4", ActualModel: "gpt-4"},
						{RequestModel: "gpt-3.5-turbo", ActualModel: "gpt-3.5-turbo"},
					},
				},
				CurrentModelIndex: 0,
			},
		}

		require.False(t, outbound.CanRetry(errSkipCandidateByCircuitBreaker))
	})

	t.Run("retryable error still does not trigger same-channel retry", func(t *testing.T) {
		outbound := &PersistentOutboundTransformer{
			wrapped: &mockTransformer{},
			state: &PersistenceState{
				CurrentCandidate: &ChannelModelsCandidate{
					Channel: channel,
					Models:  []biz.ChannelModelEntry{{RequestModel: "gpt-4", ActualModel: "gpt-4"}},
				},
				CurrentModelIndex: 0,
				TotalAttempts:     1,
			},
		}

		require.False(t, outbound.CanRetry(retryableErr))
	})
}

func TestPersistentOutboundTransformer_PrepareForRetry_MarksCurrentCandidateAndAdvances(t *testing.T) {
	channelA := &biz.Channel{
		Channel:  &ent.Channel{ID: 1, Name: "bad-channel"},
		Outbound: &mockTransformer{},
	}
	channelB := &biz.Channel{
		Channel:  &ent.Channel{ID: 2, Name: "next-channel"},
		Outbound: &mockTransformer{},
	}

	processor := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			ChannelModelsCandidates: []*ChannelModelsCandidate{
				{Channel: channelA, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4-6", ActualModel: "claude-sonnet-4-6"}}},
				{Channel: channelB, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4-5", ActualModel: "claude-sonnet-4-5"}}},
			},
			CurrentCandidate:      &ChannelModelsCandidate{Channel: channelA, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4-6", ActualModel: "claude-sonnet-4-6"}}},
			CurrentCandidateIndex: 0,
			CurrentModelIndex:     0,
			RequestExec:           &ent.RequestExecution{ID: 1},
			TotalAttempts:         1,
		},
	}

	err := processor.PrepareForRetry(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "same-channel retry disabled")
	require.Equal(t, 0, processor.state.CurrentCandidateIndex)
	require.Equal(t, 0, processor.state.CurrentModelIndex)
	require.NotNil(t, processor.state.RequestExec)
	require.Nil(t, processor.state.TriedCandidateIndices)
	require.NotNil(t, processor.state.CurrentCandidate)
}

func TestPersistentOutboundTransformer_CanRetry_StopsAtThreeAttemptsAndNoRepeatCandidates(t *testing.T) {
	channelA := &biz.Channel{
		Channel:  &ent.Channel{ID: 1, Name: "bad-channel"},
		Outbound: &mockTransformer{},
	}
	outbound := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			CurrentCandidate:      &ChannelModelsCandidate{Channel: channelA, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4-6", ActualModel: "claude-sonnet-4-6"}}},
			CurrentCandidateIndex: 0,
			CurrentModelIndex:     0,
			TotalAttempts:         3,
			TriedCandidateIndices: map[int]struct{}{0: {}},
			ChannelModelsCandidates: []*ChannelModelsCandidate{
				{Channel: channelA, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4-6", ActualModel: "claude-sonnet-4-6"}}},
			},
		},
	}

	require.False(t, outbound.CanRetry(&httpclient.Error{StatusCode: http.StatusServiceUnavailable}))
}

func TestIsCompletedAggregatedOutboundResponse(t *testing.T) {
	t.Run("usage means completed", func(t *testing.T) {
		require.True(t, isCompletedAggregatedOutboundResponse(llm.ResponseMeta{Usage: &llm.Usage{TotalTokens: 15}}))
	})

	t.Run("missing usage is not completed", func(t *testing.T) {
		require.False(t, isCompletedAggregatedOutboundResponse(llm.ResponseMeta{}))
	})
}

type sliceEventStream struct {
	events []*httpclient.StreamEvent
	index  int
	err    error
	closed bool
}

func (s *sliceEventStream) Next() bool {
	if s.index >= len(s.events) {
		return false
	}

	s.index++
	return true
}

func (s *sliceEventStream) Current() *httpclient.StreamEvent {
	if s.index == 0 || s.index > len(s.events) {
		return nil
	}

	return s.events[s.index-1]
}

func (s *sliceEventStream) Err() error {
	return s.err
}

func (s *sliceEventStream) Close() error {
	s.closed = true
	return nil
}

func TestOutboundPersistentStream_Close_AggregatedResponsesCompletionHandling(t *testing.T) {
	ctx := context.Background()
	ctx = authz.WithTestBypass(ctx)

	t.Run("response in_progress without terminal event is not completed", func(t *testing.T) {
		client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
		defer client.Close()

		ctx := ent.NewContext(ctx, client)
		project := createTestProject(t, ctx, client)
		ch := createTestChannel(t, ctx, client)
		_, requestService, _, usageLogService := setupTestServices(t, client)

		req, err := client.Request.Create().
			SetProjectID(project.ID).
			SetChannelID(ch.ID).
			SetModelID("gpt-4.1").
			SetStatus(request.StatusPending).
			SetRequestBody([]byte(`{"stream":true}`)).
			Save(ctx)
		require.NoError(t, err)

		exec, err := client.RequestExecution.Create().
			SetRequestID(req.ID).
			SetProjectID(project.ID).
			SetChannelID(ch.ID).
			SetModelID("gpt-4.1").
			SetRequestBody([]byte(`{"stream":true}`)).
			SetFormat("openai/responses").
			SetStatus(requestexecution.StatusPending).
			SetStream(true).
			Save(ctx)
		require.NoError(t, err)

		stream := &sliceEventStream{
			events: []*httpclient.StreamEvent{{Type: "response.in_progress", Data: []byte(`{"type":"response.in_progress"}`)}},
		}
		transformer := &mockTransformer{
			apiFormat:          llm.APIFormatOpenAIResponse,
			aggregatedResponse: []byte(`{"id":"resp_123","status":"in_progress"}`),
		}
		state := &PersistenceState{}

		persistentStream := NewOutboundPersistentStream(ctx, stream, req, exec, requestService, usageLogService, transformer, nil, state)
		for persistentStream.Next() {
			_ = persistentStream.Current()
		}
		require.NoError(t, persistentStream.Close())

		dbExec, err := client.RequestExecution.Get(ctx, exec.ID)
		require.NoError(t, err)
		require.NotEqual(t, requestexecution.StatusCompleted, dbExec.Status)
		require.Equal(t, requestexecution.StatusFailed, dbExec.Status)
		require.Contains(t, dbExec.ErrorMessage, "stream ended without terminal event or completed response")
		require.False(t, state.StreamCompleted)
	})

	t.Run("aggregated completed response without terminal event is completed", func(t *testing.T) {
		client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
		defer client.Close()

		ctx := ent.NewContext(ctx, client)
		project := createTestProject(t, ctx, client)
		ch := createTestChannel(t, ctx, client)
		_, requestService, _, usageLogService := setupTestServices(t, client)

		req, err := client.Request.Create().
			SetProjectID(project.ID).
			SetChannelID(ch.ID).
			SetModelID("gpt-4.1").
			SetStatus(request.StatusPending).
			SetRequestBody([]byte(`{"stream":true}`)).
			Save(ctx)
		require.NoError(t, err)

		exec, err := client.RequestExecution.Create().
			SetRequestID(req.ID).
			SetProjectID(project.ID).
			SetChannelID(ch.ID).
			SetModelID("gpt-4.1").
			SetRequestBody([]byte(`{"stream":true}`)).
			SetFormat("openai/responses").
			SetStatus(requestexecution.StatusPending).
			SetStream(true).
			Save(ctx)
		require.NoError(t, err)

		aggregated := []byte(`{"id":"resp_456","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`)
		stream := &sliceEventStream{
			events: []*httpclient.StreamEvent{{Type: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","delta":"hi"}`)}},
		}
		transformer := &mockTransformer{
			apiFormat:          llm.APIFormatOpenAIResponse,
			aggregatedResponse: aggregated,
			aggregatedMeta: llm.ResponseMeta{
				ID: "resp_456",
				Usage: &llm.Usage{
					PromptTokens:     10,
					CompletionTokens: 2,
					TotalTokens:      12,
				},
			},
		}
		state := &PersistenceState{}

		persistentStream := NewOutboundPersistentStream(ctx, stream, req, exec, requestService, usageLogService, transformer, nil, state)
		for persistentStream.Next() {
			_ = persistentStream.Current()
		}
		require.NoError(t, persistentStream.Close())

		dbExec, err := client.RequestExecution.Get(ctx, exec.ID)
		require.NoError(t, err)
		require.Equal(t, requestexecution.StatusCompleted, dbExec.Status)
		require.JSONEq(t, string(aggregated), string(dbExec.ResponseBody))
		require.Equal(t, "resp_456", dbExec.ExternalID)
		require.True(t, state.StreamCompleted)
	})

	t.Run("canceled client with aggregated completed response is still completed", func(t *testing.T) {
		client := enttest.NewEntClient(t, "sqlite3", "file:ent?mode=memory&_fk=0")
		defer client.Close()

		baseCtx := ent.NewContext(ctx, client)
		project := createTestProject(t, baseCtx, client)
		ch := createTestChannel(t, baseCtx, client)
		_, requestService, _, usageLogService := setupTestServices(t, client)

		req, err := client.Request.Create().
			SetProjectID(project.ID).
			SetChannelID(ch.ID).
			SetModelID("gpt-4.1").
			SetStatus(request.StatusPending).
			SetRequestBody([]byte(`{"stream":true}`)).
			Save(baseCtx)
		require.NoError(t, err)

		exec, err := client.RequestExecution.Create().
			SetRequestID(req.ID).
			SetProjectID(project.ID).
			SetChannelID(ch.ID).
			SetModelID("gpt-4.1").
			SetRequestBody([]byte(`{"stream":true}`)).
			SetFormat("openai/responses").
			SetStatus(requestexecution.StatusPending).
			SetStream(true).
			Save(baseCtx)
		require.NoError(t, err)

		aggregated := []byte(`{"id":"resp_codex_like","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"done"}]}]}`)
		stream := &sliceEventStream{
			events: []*httpclient.StreamEvent{{Type: "response.output_text.delta", Data: []byte(`{"type":"response.output_text.delta","delta":"done"}`)}},
			err:    context.Canceled,
		}
		transformer := &mockTransformer{
			apiFormat:          llm.APIFormatOpenAIResponse,
			aggregatedResponse: aggregated,
			aggregatedMeta: llm.ResponseMeta{
				ID: "resp_codex_like",
				Usage: &llm.Usage{
					PromptTokens:     20,
					CompletionTokens: 1,
					TotalTokens:      21,
				},
			},
		}
		state := &PersistenceState{}

		requestCtx, cancel := context.WithCancel(baseCtx)
		cancel()

		persistentStream := NewOutboundPersistentStream(requestCtx, stream, req, exec, requestService, usageLogService, transformer, nil, state)
		for persistentStream.Next() {
			_ = persistentStream.Current()
		}
		require.NoError(t, persistentStream.Close())

		dbExec, err := client.RequestExecution.Get(baseCtx, exec.ID)
		require.NoError(t, err)
		require.Equal(t, requestexecution.StatusCompleted, dbExec.Status)
		require.JSONEq(t, string(aggregated), string(dbExec.ResponseBody))
		require.Equal(t, "resp_codex_like", dbExec.ExternalID)
		require.True(t, state.StreamCompleted)
	})
}

func TestPersistentOutboundTransformer_TransformRequest_WithPrepopulatedState(t *testing.T) {
	// Setup
	ctx := context.Background()

	// Pre-populate channels (now done by inbound transformer)
	testChannel := &biz.Channel{
		Channel: &ent.Channel{
			ID:              1,
			Name:            "test-channel",
			SupportedModels: []string{"gpt-4", "gpt-3.5-turbo"}, // Add gpt-3.5-turbo
			Settings:        nil,
		},
		Outbound: &mockTransformer{},
	}

	processor := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			OriginalModel: "gpt-3.5-turbo",
			ChannelModelsCandidates: []*ChannelModelsCandidate{
				{Channel: testChannel, Priority: 0, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-3.5-turbo", ActualModel: "gpt-3.5-turbo"}}},
			}, // Pre-populated by inbound
			CurrentCandidateIndex: 0,
			RequestExec:           &ent.RequestExecution{ID: 1}, // Dummy to skip creation
		},
	}

	text := "Hello"
	llmRequest := &llm.Request{
		Model: "mapped-gpt-4", // This was mapped by inbound transformer
		Messages: []llm.Message{
			{
				Role: "user",
				Content: llm.MessageContent{
					Content: &text,
				},
			},
		},
	}

	// Execute
	channelRequest, err := processor.TransformRequest(ctx, llmRequest)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, channelRequest)

	// Verify candidate-selected model is applied on outbound dispatch
	require.Equal(t, "gpt-3.5-turbo", gjson.GetBytes(channelRequest.Body, "model").String())
	require.Equal(t, "mapped-gpt-4", llmRequest.Model)

	// Verify channel was used
	require.Equal(t, testChannel, processor.state.CurrentCandidate.Channel)
}

func TestPersistentOutboundTransformer_NextChannel_ActivatesFallbackState(t *testing.T) {
	channelA := &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "claude-primary"}, Outbound: &mockTransformer{apiFormat: llm.APIFormatAnthropicMessage}}
	channelB := &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "gpt-fallback"}, Outbound: &mockTransformer{apiFormat: llm.APIFormatOpenAIResponse}}
	processor := &PersistentOutboundTransformer{
		wrapped: &mockTransformer{},
		state: &PersistenceState{
			OriginalModel:    "claude-sonnet-4",
			InboundAPIFormat: llm.APIFormatAnthropicMessage,
			ChannelModelsCandidates: []*ChannelModelsCandidate{
				{Channel: channelA, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4", ActualModel: "claude-sonnet-4"}}},
				{Channel: channelB, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4"}}},
			},
			CurrentCandidate:      &ChannelModelsCandidate{Channel: channelA, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4", ActualModel: "claude-sonnet-4"}}},
			CurrentCandidateIndex: 0,
			CurrentModelIndex:     0,
			RequestExec:           &ent.RequestExecution{ID: 99},
			TotalAttempts:         1,
		},
	}

	err := processor.NextChannel(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, processor.state.CurrentCandidateIndex)
	require.Equal(t, 0, processor.state.CurrentModelIndex)
	require.Equal(t, "gpt-5.4", processor.GetCurrentModelID())
	require.Nil(t, processor.state.RequestExec)
	require.Contains(t, processor.state.TriedCandidateIndices, 0)
}

func TestPersistentOutboundTransformer_TransformRequest_UsesActivatedFallbackCandidate(t *testing.T) {
	channelA := &biz.Channel{Channel: &ent.Channel{ID: 1, Name: "claude-primary", Type: channel.TypeOpenaiResponses, BaseURL: "https://claude.example.com"}, Outbound: &mockTransformer{apiFormat: llm.APIFormatOpenAIResponse}}
	channelB := &biz.Channel{Channel: &ent.Channel{ID: 2, Name: "gpt-fallback", Type: channel.TypeOpenaiResponses, BaseURL: "https://gpt.example.com"}, Outbound: &mockTransformer{apiFormat: llm.APIFormatOpenAIResponse}}
	processor := &PersistentOutboundTransformer{
		wrapped: channelA.Outbound,
		state: &PersistenceState{
			OriginalModel:    "claude-sonnet-4",
			InboundAPIFormat: llm.APIFormatAnthropicMessage,
			ChannelModelsCandidates: []*ChannelModelsCandidate{
				{Channel: channelA, Models: []biz.ChannelModelEntry{{RequestModel: "claude-sonnet-4", ActualModel: "claude-sonnet-4"}}},
				{Channel: channelB, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4"}}},
			},
			CurrentCandidateIndex: 1,
			CurrentCandidate:      &ChannelModelsCandidate{Channel: channelB, Models: []biz.ChannelModelEntry{{RequestModel: "gpt-5.4", ActualModel: "gpt-5.4"}}},
		},
	}

	req, err := processor.TransformRequest(context.Background(), &llm.Request{Model: "claude-sonnet-4", Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: lo.ToPtr("hi")}}}})
	require.NoError(t, err)
	require.NotNil(t, req)
	require.Equal(t, 1, processor.state.TotalAttempts)
	require.Equal(t, "gpt-5.4", processor.state.AttemptHistory[0].ActualModel)
	require.Equal(t, "gpt-fallback", processor.state.AttemptHistory[0].ChannelName)
}
