package conversation

import (
	"context"
	"strings"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/application/channel"
	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"go.opentelemetry.io/otel/trace"
)

// nativeToolsStubGateway 记录每次上游调用收到的参数，并按顺序返回预设结果。
type nativeToolsStubGateway struct {
	calls  []llm.GenerateInput
	errors []error
	output *llm.GenerateOutput
}

func (g *nativeToolsStubGateway) Generate(_ context.Context, _ llm.RouteConfig, input llm.GenerateInput) (*llm.GenerateOutput, error) {
	g.calls = append(g.calls, input)
	if len(g.errors) > 0 {
		err := g.errors[0]
		g.errors = g.errors[1:]
		if err != nil {
			return nil, err
		}
	}
	if g.output != nil {
		return g.output, nil
	}
	return &llm.GenerateOutput{Text: "answer"}, nil
}

func (g *nativeToolsStubGateway) GenerateStream(_ context.Context, _ llm.RouteConfig, input llm.GenerateInput, _ func(llm.GenerateStreamEvent) error) (*llm.GenerateOutput, error) {
	return g.Generate(context.Background(), llm.RouteConfig{}, input)
}

func (g *nativeToolsStubGateway) RetrieveOpenAIResponse(context.Context, llm.RouteConfig, string) (*llm.GenerateOutput, error) {
	return &llm.GenerateOutput{}, nil
}

func (g *nativeToolsStubGateway) CancelOpenAIResponse(context.Context, llm.RouteConfig, string) (*llm.GenerateOutput, error) {
	return &llm.GenerateOutput{}, nil
}

func newNativeToolsTestRunner(gateway llmGateway) *messageGenerationRunner {
	return &messageGenerationRunner{
		service:      &Service{llmClient: gateway},
		input:        SendMessageInput{ConversationID: 7, RequestID: "req-1"},
		runID:        "run-1",
		preferStream: false,
		maxLLMCalls:  3,
		usage:        &messageUsageAccumulator{},
	}
}

func nativeToolsTestPlan() *routeGenerationPlan {
	route := &channel.ResolvedRoute{
		RouteID:           5,
		PlatformModelName: "deepseek-flash",
		ModelVendor:       "deepseek",
		Protocol:          llm.AdapterOpenAIResponses,
		UpstreamModel:     "deepseek-flash",
		BaseURL:           "https://api.deepseek.com/v1",
	}
	input := llm.GenerateInput{
		Messages: []llm.Message{{Role: "user", Content: "What is new in DeepSeek?"}},
		Options: map[string]any{
			"tools": []any{
				map[string]any{"type": "deepseek_web_search"},
				map[string]any{"type": "deepseek_read_url"},
			},
		},
	}
	return &routeGenerationPlan{
		route:           route,
		routeConfig:     llm.RouteConfig{Protocol: route.Protocol, UpstreamModel: route.UpstreamModel},
		generateInput:   input,
		fullLLMMessages: input.Messages,
		filteredOptions: input.Options,
	}
}

func TestRunRouteAttemptFallsBackWithoutNativeTools(t *testing.T) {
	gateway := &nativeToolsStubGateway{
		errors: []error{&llm.UpstreamError{StatusCode: 400, Message: "unknown tool: deepseek_web_search"}},
		output: &llm.GenerateOutput{Text: "plain answer"},
	}
	runner := newNativeToolsTestRunner(gateway)
	plan := nativeToolsTestPlan()

	output, err := runner.runRouteAttempt(context.Background(), plan, trace.SpanFromContext(context.Background()))
	if err != nil {
		t.Fatalf("expected fallback retry to succeed, got %v", err)
	}
	if output == nil || strings.TrimSpace(output.Text) != "plain answer" {
		t.Fatalf("unexpected fallback output: %#v", output)
	}
	if len(gateway.calls) != 2 {
		t.Fatalf("expected exactly one fallback retry, got %d calls", len(gateway.calls))
	}
	if !hasVendorNativeTools(gateway.calls[0].Options) {
		t.Fatalf("first call must declare deepseek native tools, got %#v", gateway.calls[0].Options)
	}
	if hasVendorNativeTools(gateway.calls[1].Options) {
		t.Fatalf("fallback call must drop deepseek native tools, got %#v", gateway.calls[1].Options)
	}
	if !plan.nativeToolsFallback || !runner.nativeToolsPeeled {
		t.Fatalf("fallback state must be recorded on plan/runner, got plan=%v runner=%v", plan.nativeToolsFallback, runner.nativeToolsPeeled)
	}
	if hasVendorNativeTools(plan.generateInput.Options) {
		t.Fatalf("plan must keep the peeled request shape, got %#v", plan.generateInput.Options)
	}
}

func TestRunRouteAttemptKeepsNativeToolsForUnrelatedFailures(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "content risk", err: &llm.UpstreamError{StatusCode: 400, Message: "Content Exists Risk"}},
		{name: "accepted", err: llm.MarkRequestAccepted(&llm.UpstreamError{StatusCode: 400, Message: "unknown tool: deepseek_web_search"})},
		{name: "rate limited", err: &llm.UpstreamError{StatusCode: 429, Message: "rate limit reached"}},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			gateway := &nativeToolsStubGateway{errors: []error{item.err}}
			runner := newNativeToolsTestRunner(gateway)
			plan := nativeToolsTestPlan()

			if _, err := runner.runRouteAttempt(context.Background(), plan, trace.SpanFromContext(context.Background())); err == nil {
				t.Fatal("expected the original failure to surface")
			}
			if len(gateway.calls) != 1 {
				t.Fatalf("expected no retry, got %d calls", len(gateway.calls))
			}
			if plan.nativeToolsFallback {
				t.Fatal("must not mark a native tools fallback")
			}
		})
	}
}

func TestGenerateWithNativeToolsFallbackPeelsToolsForFollowUps(t *testing.T) {
	gateway := &nativeToolsStubGateway{
		errors: []error{&llm.UpstreamError{StatusCode: 400, Message: "tools: `deepseek_read_url` is not supported"}},
		output: &llm.GenerateOutput{Text: "follow-up answer"},
	}
	runner := newNativeToolsTestRunner(gateway)
	plan := nativeToolsTestPlan()
	route := plan.route

	input := plan.generateInput
	output, err := runner.generateWithNativeToolsFallback(context.Background(), route, &input, plan.fullLLMMessages)
	if err != nil {
		t.Fatalf("expected follow-up fallback to succeed, got %v", err)
	}
	if output == nil || output.Text != "follow-up answer" {
		t.Fatalf("unexpected follow-up output: %#v", output)
	}
	if len(gateway.calls) != 2 || hasVendorNativeTools(gateway.calls[1].Options) {
		t.Fatalf("expected a single peeled retry, got %#v", gateway.calls)
	}
	if !runner.nativeToolsPeeled {
		t.Fatal("runner must remember the peeled request shape")
	}

	// 后续调用即使重新带上原生工具，也应被 runner 的记忆剔除。
	next := plan.generateInput
	if _, err := runner.generateWithNativeToolsFallback(context.Background(), route, &next, plan.fullLLMMessages); err != nil {
		t.Fatalf("follow-up call failed: %v", err)
	}
	if hasVendorNativeTools(gateway.calls[len(gateway.calls)-1].Options) {
		t.Fatalf("peeled state must survive later follow-up calls, got %#v", gateway.calls[len(gateway.calls)-1].Options)
	}
}
