package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

// OpenAI talks to any OpenAI-compatible chat completions endpoint (OpenAI,
// Ollama, vLLM, LM Studio) through the official SDK, using response_format
// json_schema so the endpoint constrains the output where it supports that.
type OpenAI struct {
	client openai.Client
	model  string
}

// NewOpenAI builds the provider. baseURL (for example
// http://localhost:11434/v1 for Ollama) and apiKey fall back to
// OPENAI_BASE_URL and OPENAI_API_KEY via the SDK. A non-empty baseURL with no
// key uses a placeholder, since local servers ignore it.
func NewOpenAI(model, baseURL, apiKey string) *OpenAI {
	opts := []option.RequestOption{option.WithMaxRetries(0)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	switch {
	case apiKey != "":
		opts = append(opts, option.WithAPIKey(apiKey))
	case baseURL != "":
		opts = append(opts, option.WithAPIKey("not-needed"))
	}
	if model == "" {
		model = "gpt-4.1-mini"
	}
	return &OpenAI{client: openai.NewClient(opts...), model: model}
}

func (o *OpenAI) Name() string { return "openai:" + o.model }

func (o *OpenAI) Extract(ctx context.Context, r Request) ([]byte, error) {
	var user openai.ChatCompletionMessageParamUnion
	if r.ImagePath != "" {
		b, mt, err := readImage(r.ImagePath)
		if err != nil {
			return nil, failure.New(failure.InvalidDoc, "provider", err)
		}
		url := "data:" + mt + ";base64," + base64.StdEncoding.EncodeToString(b)
		user = openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
			openai.TextContentPart(r.User),
			openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{URL: url}),
		})
	} else {
		user = openai.UserMessage(r.User)
	}
	resp, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    o.model,
		Messages: []openai.ChatCompletionMessageParamUnion{openai.SystemMessage(r.System), user},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
					Name:   "expense_extraction",
					Strict: openai.Bool(true),
					Schema: r.Schema,
				},
			},
		},
	})
	if err != nil {
		var apiErr *openai.Error
		if errors.As(err, &apiErr) {
			return nil, &failure.StatusError{Status: apiErr.StatusCode, Err: err}
		}
		return nil, err
	}
	if len(resp.Choices) == 0 || resp.Choices[0].Message.Content == "" {
		return nil, failure.New(failure.Schema, "provider", fmt.Errorf("empty completion"))
	}
	return []byte(resp.Choices[0].Message.Content), nil
}
