package provider

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/maxpetrusenko/expense-policy-agent-go/internal/failure"
)

const toolName = "record_expense"

// Anthropic uses the official SDK with forced tool use: the model must call
// record_expense, whose input_schema is the extraction schema, so the output
// is a JSON object shaped by the schema rather than free text.
type Anthropic struct {
	client anthropic.Client
	model  string
}

// NewAnthropic builds the provider. apiKey and baseURL default to
// ANTHROPIC_API_KEY and the SDK default endpoint when empty. SDK-level retries
// are disabled because the pipeline owns retry policy.
func NewAnthropic(model, apiKey, baseURL string) *Anthropic {
	opts := []option.RequestOption{option.WithMaxRetries(0)}
	if apiKey != "" {
		opts = append(opts, option.WithAPIKey(apiKey))
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	if model == "" {
		model = string(anthropic.ModelClaudeSonnet5_5)
	}
	return &Anthropic{client: anthropic.NewClient(opts...), model: model}
}

func (a *Anthropic) Name() string { return "anthropic:" + a.model }

func (a *Anthropic) Extract(ctx context.Context, r Request) ([]byte, error) {
	blocks := []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(r.User)}
	if r.ImagePath != "" {
		img, err := imageBlock(r.ImagePath)
		if err != nil {
			return nil, failure.New(failure.InvalidDoc, "provider", err)
		}
		blocks = append(blocks, img)
	}
	schema := r.Schema
	msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: 1024,
		System:    []anthropic.TextBlockParam{{Text: r.System}},
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(blocks...)},
		Tools: []anthropic.ToolUnionParam{{OfTool: &anthropic.ToolParam{
			Name:        toolName,
			Description: anthropic.String("Record the fields extracted from the document."),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties:  schema["properties"],
				Required:    schema["required"].([]string),
				ExtraFields: map[string]any{"additionalProperties": false},
			},
		}}},
		ToolChoice: anthropic.ToolChoiceParamOfTool(toolName),
	})
	if err != nil {
		return nil, wrapAnthropic(err)
	}
	for _, b := range msg.Content {
		if b.Type == "tool_use" && b.Name == toolName {
			return b.Input, nil
		}
	}
	return nil, failure.New(failure.Schema, "provider", fmt.Errorf("no %s tool call in response (stop_reason=%s)", toolName, msg.StopReason))
}

func wrapAnthropic(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		return &failure.StatusError{Status: apiErr.StatusCode, Err: err}
	}
	return err // transport errors keep their net.Error / context identity
}

func imageBlock(path string) (anthropic.ContentBlockParamUnion, error) {
	b, mt, err := readImage(path)
	if err != nil {
		return anthropic.ContentBlockParamUnion{}, err
	}
	return anthropic.NewImageBlockBase64(mt, base64.StdEncoding.EncodeToString(b)), nil
}

func readImage(path string) ([]byte, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	mt := mime.TypeByExtension(filepath.Ext(path))
	switch mt {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return b, mt, nil
	}
	return nil, "", fmt.Errorf("unsupported image type %q for %s", mt, path)
}
