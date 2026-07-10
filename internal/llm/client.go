package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// Config configures an OpenAIClient.
type Config struct {
	APIKey     string
	BaseURL    string
	Model      string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// DefaultTimeout is the per-request LLM timeout.
const DefaultTimeout = 120 * time.Second

// NewClient creates an OpenAI-compatible LLM client using the openai-go SDK.
// The same client works across OpenAI-compatible providers (GLM, Kimi, etc.)
// by varying the base URL and model.
func NewClient(cfg Config) *OpenAIClient {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: cfg.Timeout}
	}
	opts := []option.RequestOption{
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.BaseURL),
		option.WithHTTPClient(cfg.HTTPClient),
		option.WithRequestTimeout(cfg.Timeout),
	}
	c := openai.NewClient(opts...)
	return &OpenAIClient{
		client: c,
		model:  shared.ChatModel(cfg.Model),
		tools:  ToolParams(),
	}
}

// OpenAIClient implements Client via the openai-go SDK.
type OpenAIClient struct {
	client openai.Client
	model  shared.ChatModel
	tools  []openai.ChatCompletionToolParam
}

// Complete sends the message history to the chat completions endpoint and
// returns the first choice's assistant message.
func (c *OpenAIClient) Complete(ctx context.Context, messages []Message) (Response, error) {
	params := openai.ChatCompletionNewParams{
		Model:    c.model,
		Messages: toMessageParams(messages),
		Tools:    c.tools,
	}
	resp, err := c.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return Response{}, fmt.Errorf("llm chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return Response{}, fmt.Errorf("llm returned no choices")
	}
	choice := resp.Choices[0]
	out := Response{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
	}
	for _, tc := range choice.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return out, nil
}

// toMessageParams converts internal messages to the openai-go param union type.
func toMessageParams(messages []Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages))
	for _, m := range messages {
		switch m.Role {
		case RoleSystem:
			out = append(out, openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ChatCompletionSystemMessageParamContentUnion{
						OfString: openai.String(m.Content),
					},
				},
			})
		case RoleUser:
			out = append(out, openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(m.Content),
					},
				},
			})
		case RoleAssistant:
			a := &openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				a.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(m.Content),
				}
			}
			if len(m.ToolCalls) > 0 {
				a.ToolCalls = make([]openai.ChatCompletionMessageToolCallParam, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					a.ToolCalls[i] = openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.Arguments,
						},
					}
				}
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: a})
		case RoleTool:
			out = append(out, openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Content: openai.ChatCompletionToolMessageParamContentUnion{
						OfString: openai.String(m.Content),
					},
					ToolCallID: m.ToolCallID,
				},
			})
		}
	}
	return out
}
