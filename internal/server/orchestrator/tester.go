package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/tidwall/gjson"

	entchannel "github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/pkg/xjson"
	"github.com/looplj/axonhub/internal/server/biz"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/pipeline/stream"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// TestChannelOrchestrator handles channel testing functionality.
// It is stateless and can be reused across multiple test requests.
type TestChannelOrchestrator struct {
	channelService              *biz.ChannelService
	requestService              *biz.RequestService
	systemService               *biz.SystemService
	usageLogService             *biz.UsageLogService
	promptProtectionRuleService *biz.PromptProtectionRuleService
	httpClient                  *httpclient.HttpClient
	modelCircuitBreaker         *biz.ModelCircuitBreaker
	modelMapper                 *ModelMapper
	loadBalancer                *LoadBalancer
	connectionTracking          ConnectionTracker
}

// NewTestChannelOrchestrator creates a new TestChannelOrchestrator.
func NewTestChannelOrchestrator(
	channelService *biz.ChannelService,
	requestService *biz.RequestService,
	systemService *biz.SystemService,
	usageLogService *biz.UsageLogService,
	promptProtectionRuleService *biz.PromptProtectionRuleService,
	httpClient *httpclient.HttpClient,
) *TestChannelOrchestrator {
	return &TestChannelOrchestrator{
		channelService:              channelService,
		requestService:              requestService,
		systemService:               systemService,
		usageLogService:             usageLogService,
		promptProtectionRuleService: promptProtectionRuleService,
		httpClient:                  httpClient,
		modelCircuitBreaker:         biz.NewModelCircuitBreaker(),
		modelMapper:                 NewModelMapper(),
		loadBalancer:                NewLoadBalancer(systemService, channelService, NewWeightStrategy()),
		connectionTracking:          NewDefaultConnectionTracker(100),
	}
}

// TestChannelRequest represents a channel test request.
type TestChannelRequest struct {
	ChannelID objects.GUID
	ModelID   *string
}

// TestChannelResult represents the result of a channel test.
type TestChannelResult struct {
	Latency float64
	Success bool
	Message *string
	Error   *string
}

// TestChannel tests a specific channel with a simple request.
func (processor *TestChannelOrchestrator) TestChannel(
	ctx context.Context,
	channelID objects.GUID,
	modelID *string,
	proxy *httpclient.ProxyConfig,
) (*TestChannelResult, error) {
	channel, err := processor.channelService.GetChannel(ctx, channelID.ID)
	if err != nil {
		return nil, err
	}

	inbound := selectTestChannelInboundTransformer(channel, lo.FromPtr(modelID))

	// Create ChatCompletionOrchestrator for this test request
	chatProcessor := &ChatCompletionOrchestrator{
		channelSelector: NewSpecifiedChannelSelector(processor.channelService, channelID),
		RequestService:  processor.requestService,
		ChannelService:  processor.channelService,
		PromptProvider:  &stubPromptProvider{},
		PromptProtecter: processor.promptProtectionRuleService,
		PipelineFactory: pipeline.NewFactory(processor.httpClient),
		Middlewares: []pipeline.Middleware{
			stream.EnsureUsage(),
		},
		Inbound:                    inbound,
		SystemService:              processor.systemService,
		UsageLogService:            processor.usageLogService,
		proxy:                      proxy,
		ModelMapper:                processor.modelMapper,
		selectedChannelIds:         []int{},
		adaptiveLoadBalancer:       processor.loadBalancer,
		failoverLoadBalancer:       processor.loadBalancer,
		circuitBreakerLoadBalancer: processor.loadBalancer,
		connectionTracker:          processor.connectionTracking,
		modelCircuitBreaker:        processor.modelCircuitBreaker,
	}

	testModel := lo.FromPtr(modelID)
	if testModel == "" {
		testModel = channel.DefaultTestModel
	}

	// Check if the channel requires streaming
	useStream := channel != nil && channel.Policies.Stream == objects.CapabilityPolicyRequire

	// Create a simple test request. Anthropic message requests require max_tokens,
	// while the unified request model primarily uses max_completion_tokens.
	llmRequest := buildTestChannelRequest(testModel, useStream, inbound.APIFormat())
	body, err := json.Marshal(llmRequest)
	if err != nil {
		return nil, err
	}

	// Measure latency
	startTime := time.Now()
	rawResponse, err := chatProcessor.Process(ctx, &httpclient.Request{
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: body,
	})

	rawErr := inbound.TransformError(ctx, err)
	message := gjson.GetBytes(rawErr.Body, "error.message").String()

	if err != nil {
		return &TestChannelResult{
			Latency: time.Since(startTime).Seconds(),
			Success: false,
			Message: lo.ToPtr(""),
			Error:   lo.ToPtr(message),
		}, nil
	}

	// Handle streaming response
	if rawResponse.ChatCompletionStream != nil {
		return processor.handleStreamResponse(ctx, rawResponse.ChatCompletionStream, startTime, inbound)
	}

	latency := time.Since(startTime).Seconds()

	messageText, err := parseTestChannelResponseBody(rawResponse.ChatCompletion.Body, inbound.APIFormat())
	if err != nil {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: lo.ToPtr(""),
			Error:   lo.ToPtr(err.Error()),
		}, nil
	}

	return &TestChannelResult{
		Latency: latency,
		Success: true,
		Message: lo.ToPtr(messageText),
		Error:   nil,
	}, nil
}

func buildTestChannelRequest(modelID string, useStream bool, apiFormat llm.APIFormat) *llm.Request {
	req := &llm.Request{
		Model: modelID,
		Messages: []llm.Message{
			{
				Role: "system",
				Content: llm.MessageContent{
					Content: lo.ToPtr("You are a helpful assistant."),
				},
			},
			{
				Role: "user",
				Content: llm.MessageContent{
					MultipleContent: []llm.MessageContentPart{
						{
							Type: "text",
							Text: lo.ToPtr("Hello world, I'm AxonHub."),
						},
						{
							Type: "text",
							Text: lo.ToPtr("Please tell me who you are?"),
						},
					},
				},
			},
		},
		MaxCompletionTokens: lo.ToPtr(int64(256)),
		Stream:              lo.ToPtr(useStream),
	}

	if apiFormat == llm.APIFormatAnthropicMessage {
		req.MaxTokens = lo.ToPtr(int64(256))
	}

	return req
}

func selectTestChannelInboundTransformer(channel *biz.Channel, modelID string) transformer.Inbound {
	if shouldUseAnthropicMessages(channel, modelID) {
		return anthropic.NewInboundTransformer()
	}

	return openai.NewInboundTransformer()
}

func shouldUseAnthropicMessages(channel *biz.Channel, modelID string) bool {
	if channel == nil {
		return strings.HasPrefix(strings.ToLower(modelID), "claude-")
	}

	if channel.Type.IsAnthropic() || channel.Type == entchannel.TypeAnthropicAWS || channel.Type == entchannel.TypeAnthropicGcp {
		return true
	}

	if channel.Type.IsAnthropicLike() || channel.Type == entchannel.TypeClaudecode {
		return true
	}

	return strings.HasPrefix(strings.ToLower(modelID), "claude-")
}

// handleStreamResponse processes a streaming response and accumulates the content.
func (processor *TestChannelOrchestrator) handleStreamResponse(
	ctx context.Context,
	stream streams.Stream[*httpclient.StreamEvent],
	startTime time.Time,
	inbound transformer.Inbound,
) (*TestChannelResult, error) {
	defer func() {
		_ = stream.Close()
	}()

	var chunks []*httpclient.StreamEvent

	for stream.Next() {
		select {
		case <-ctx.Done():
			return &TestChannelResult{
				Latency: time.Since(startTime).Seconds(),
				Success: false,
				Message: lo.ToPtr(""),
				Error:   lo.ToPtr(ctx.Err().Error()),
			}, nil
		default:
		}

		event := stream.Current()
		if event == nil {
			continue
		}

		chunks = append(chunks, event)
	}

	// Calculate latency after processing all stream events
	latency := time.Since(startTime).Seconds()

	if err := ctx.Err(); err != nil {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: lo.ToPtr(""),
			Error:   lo.ToPtr(err.Error()),
		}, nil
	}

	if stream.Err() != nil {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: lo.ToPtr(""),
			Error:   lo.ToPtr(stream.Err().Error()),
		}, nil
	}

	body, _, err := inbound.AggregateStreamChunks(ctx, chunks)
	if err != nil {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: lo.ToPtr(""),
			Error:   lo.ToPtr(err.Error()),
		}, nil
	}

	messageText, err := parseTestChannelResponseBody(body, inbound.APIFormat())
	if err != nil {
		return &TestChannelResult{
			Latency: latency,
			Success: false,
			Message: lo.ToPtr(""),
			Error:   lo.ToPtr(err.Error()),
		}, nil
	}

	return &TestChannelResult{
		Latency: latency,
		Success: true,
		Message: lo.ToPtr(messageText),
		Error:   nil,
	}, nil
}

func parseTestChannelResponseBody(body []byte, apiFormat llm.APIFormat) (string, error) {
	switch apiFormat {
	case llm.APIFormatAnthropicMessage:
		resp, err := xjson.To[anthropic.Message](body)
		if err != nil {
			return "", err
		}

		var textParts []string
		for _, block := range resp.Content {
			if block.Type == "text" && block.Text != nil {
				textParts = append(textParts, *block.Text)
			}
		}

		if len(textParts) > 0 {
			return strings.Join(textParts, ""), nil
		}

		if resp.ID != "" || len(resp.Content) > 0 {
			return "", nil
		}

		return "", fmt.Errorf("no message in response")
	default:
		resp, err := xjson.To[llm.Response](body)
		if err != nil {
			return "", err
		}

		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("no message in response")
		}

		if resp.Choices[0].Message.Content.Content != nil {
			return *resp.Choices[0].Message.Content.Content, nil
		}

		if len(resp.Choices[0].Message.Content.MultipleContent) > 0 {
			var textParts []string
			for _, part := range resp.Choices[0].Message.Content.MultipleContent {
				if part.Type == "text" && part.Text != nil {
					textParts = append(textParts, *part.Text)
				}
			}

			return strings.Join(textParts, ""), nil
		}

		return "", nil
	}
}
