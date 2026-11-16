package conversation

import (
	"errors"
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func toolTypesOf(payloads []map[string]any) map[string]string {
	result := make(map[string]string, len(payloads))
	for _, payload := range payloads {
		result[modelOptionStringValue(payload["type"])] = modelOptionStringValue(payload["name"])
	}
	return result
}

func TestApplyVendorDefaultNativeToolsInjectsDeepSeekTools(t *testing.T) {
	payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", "", nil, nil)
	types := toolTypesOf(payloads)
	if len(types) != 2 {
		t.Fatalf("expected two deepseek default tools, got %#v", payloads)
	}
	if types["deepseek_web_search"] != "deepseek_web_search" || types["deepseek_read_url"] != "deepseek_read_url" {
		t.Fatalf("expected anthropic payloads to carry names, got %#v", payloads)
	}

	payloads = applyVendorDefaultNativeTools("openai_responses", "deepseek", "", nil, nil)
	types = toolTypesOf(payloads)
	if len(types) != 2 {
		t.Fatalf("expected two deepseek default tools for responses, got %#v", payloads)
	}
	if types["deepseek_web_search"] != "" || types["deepseek_read_url"] != "" {
		t.Fatalf("expected responses payloads without names, got %#v", payloads)
	}
}

func TestApplyVendorDefaultNativeToolsSkipsNonDeepSeekOrConfiguredModels(t *testing.T) {
	if payloads := applyVendorDefaultNativeTools("anthropic_messages", "", "", nil, nil); len(payloads) != 0 {
		t.Fatalf("expected no injection without vendor, got %#v", payloads)
	}
	if payloads := applyVendorDefaultNativeTools("anthropic_messages", "openai", "", nil, nil); len(payloads) != 0 {
		t.Fatalf("expected no injection for other vendors, got %#v", payloads)
	}
	if payloads := applyVendorDefaultNativeTools("gemini_generate_content", "deepseek", "", nil, nil); len(payloads) != 0 {
		t.Fatalf("expected no injection for unsupported protocol, got %#v", payloads)
	}
	// 只声明了其它厂商的服务端工具：DeepSeek 会忽略它们，应回落到厂商默认工具。
	foreign := `{"nativeTools":[{"key":"openai.web_search","protocol":"anthropic_messages","type":"web_search_20250305","enabled":true}]}`
	foreignRequested := []map[string]any{{"type": "web_search_20250305", "name": "web_search"}}
	if payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", foreign, foreignRequested, foreignRequested); len(payloads) != 2 {
		t.Fatalf("expected foreign native tools to be replaced by vendor defaults, got %#v", payloads)
	}
	// 声明了本厂商工具：完全按管理员声明执行（这里用户没有选中任何工具）。
	managedKeys := `{"nativeToolKeys":["deepseek.web_search"]}`
	if payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", managedKeys, nil, nil); len(payloads) != 0 {
		t.Fatalf("expected vendor nativeToolKeys to take over, got %#v", payloads)
	}
	managedDefaults := `{"defaultOptions":{"tools":[{"type":"deepseek_web_search"}]}}`
	if payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", managedDefaults, nil, nil); len(payloads) != 0 {
		t.Fatalf("expected vendor defaultOptions.tools to take over, got %#v", payloads)
	}
	managedTools := `{"nativeTools":[{"key":"deepseek.web_search","type":"deepseek_web_search","enabled":true}]}`
	selected := []map[string]any{{"type": "deepseek_web_search", "name": "deepseek_web_search"}}
	if payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", managedTools, selected, selected); len(payloads) != 1 {
		t.Fatalf("expected only the selected vendor tool, got %#v", payloads)
	}
	// 空数组是旧编辑器/导入的残留，不应被当作“管理员接管”，否则输入框开关会撒谎。
	for _, cleared := range []string{`{"nativeTools":[]}`, `{"nativeToolKeys":[]}`, `{"defaultOptions":{"tools":[]}}`} {
		if payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", cleared, nil, nil); len(payloads) != 2 {
			t.Fatalf("expected empty declarations in %s to keep vendor defaults, got %#v", cleared, payloads)
		}
	}
	if payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", "", []any{}, nil); len(payloads) != 0 {
		t.Fatalf("expected explicit empty tools to opt out, got %#v", payloads)
	}
	for _, disabled := range []string{`{"disableVendorNativeTools":true}`, `{"vendorNativeTools":false}`} {
		if payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", disabled, nil, nil); len(payloads) != 0 {
			t.Fatalf("expected explicit opt-out in %s to disable defaults, got %#v", disabled, payloads)
		}
	}
}

func TestApplyVendorDefaultNativeToolsMergesAndDeduplicates(t *testing.T) {
	requested := []map[string]any{
		{"type": "deepseek_web_search", "name": "deepseek_web_search"},
		{"type": "web_search_20250305", "name": "web_search"},
	}
	payloads := applyVendorDefaultNativeTools("anthropic_messages", "deepseek", "", requested, requested)
	if len(payloads) != 3 {
		t.Fatalf("expected deduplicated merge (2 requested + 1 missing default), got %#v", payloads)
	}
	counts := map[string]int{}
	for _, payload := range payloads {
		counts[modelOptionStringValue(payload["type"])]++
	}
	if counts["deepseek_web_search"] != 1 || counts["deepseek_read_url"] != 1 || counts["web_search_20250305"] != 1 {
		t.Fatalf("unexpected merged payloads: %#v", counts)
	}
}

func TestResolveChatModelOptionsInjectsVendorDefaults(t *testing.T) {
	options, _ := resolveChatModelOptions(nil, "anthropic_messages", modelOptionPolicyConfig{
		ModelVendor: "deepseek",
	})
	tools := providerToolOptionPayloads(options["tools"])
	if len(tools) != 2 {
		t.Fatalf("expected deepseek defaults from chat pipeline, got %#v", options)
	}

	options, _ = resolveChatModelOptions(map[string]any{"tools": []any{}}, "anthropic_messages", modelOptionPolicyConfig{
		ModelVendor: "deepseek",
	})
	if tools := providerToolOptionPayloads(options["tools"]); len(tools) != 0 {
		t.Fatalf("expected explicit empty tools to disable defaults, got %#v", options)
	}

	options, _ = resolveChatModelOptions(nil, "anthropic_messages", modelOptionPolicyConfig{
		ModelVendor:           "deepseek",
		ModelCapabilitiesJSON: `{"nativeTools":[{"key":"deepseek.web_search","protocol":"anthropic_messages","type":"deepseek_web_search","enabled":true}]}`,
	})
	if tools := providerToolOptionPayloads(options["tools"]); len(tools) != 0 {
		t.Fatalf("expected configured vendor tool model to skip defaults, got %#v", options)
	}

	// 管理员配的是 OpenAI 服务端搜索但路由厂商是 DeepSeek：DeepSeek 会忽略该声明，
	// 因此改用 DeepSeek 自家默认工具，避免“开关开着却没有联网”。
	options, _ = resolveChatModelOptions(nil, "anthropic_messages", modelOptionPolicyConfig{
		ModelVendor:           "deepseek",
		ModelCapabilitiesJSON: `{"nativeTools":[{"key":"openai.web_search","protocol":"anthropic_messages","type":"web_search_20250305","enabled":true}]}`,
	})
	fallbackTools := providerToolOptionPayloads(options["tools"])
	if len(fallbackTools) != 2 {
		t.Fatalf("expected foreign native tools to fall back to deepseek defaults, got %#v", options)
	}
	if modelOptionStringValue(fallbackTools[0]["type"]) != "deepseek_web_search" {
		t.Fatalf("unexpected fallback tools: %#v", fallbackTools)
	}
}

func TestHasAndWithoutVendorNativeTools(t *testing.T) {
	options := map[string]any{
		"temperature": 0.2,
		"tools": []any{
			map[string]any{"type": "deepseek_web_search", "name": "deepseek_web_search"},
			map[string]any{"type": "deepseek_read_url", "name": "deepseek_read_url"},
			map[string]any{"type": "web_search_20250305", "name": "web_search"},
		},
		"tool_choice": map[string]any{"type": "tool", "name": "deepseek_read_url"},
	}
	if !hasVendorNativeTools(options) {
		t.Fatal("expected vendor native tools to be detected")
	}
	peeled := withoutVendorNativeTools(options)
	tools := providerToolOptionPayloads(peeled["tools"])
	if len(tools) != 1 || modelOptionStringValue(tools[0]["type"]) != "web_search_20250305" {
		t.Fatalf("expected only foreign native tools to remain, got %#v", tools)
	}
	if _, ok := peeled["tool_choice"]; ok {
		t.Fatalf("expected forced tool_choice to be dropped, got %#v", peeled["tool_choice"])
	}
	if value, ok := peeled["temperature"]; !ok || value != 0.2 {
		t.Fatalf("expected unrelated options to be preserved, got %#v", peeled)
	}
	if _, ok := options["tools"].([]any); !ok || len(options["tools"].([]any)) != 3 {
		t.Fatalf("expected original options to stay untouched, got %#v", options)
	}

	onlyVendor := map[string]any{"tools": []any{map[string]any{"type": "deepseek_web_search"}}}
	peeledOnly := withoutVendorNativeTools(onlyVendor)
	if _, ok := peeledOnly["tools"]; ok {
		t.Fatalf("expected tools key to be removed when empty, got %#v", peeledOnly)
	}
	if hasVendorNativeTools(peeledOnly) {
		t.Fatal("expected no vendor native tools after peeling")
	}
}

func TestShouldRetryWithoutNativeTools(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "declared twice",
			err:  &llm.UpstreamError{StatusCode: 400, Message: "tools: `deepseek_web_search` can only be declared once"},
			want: true,
		},
		{
			name: "cannot combine with other tools",
			err:  &llm.UpstreamError{StatusCode: 400, Message: "other tools cannot be used together with `deepseek_web_search`, `deepseek_read_url`"},
			want: true,
		},
		{
			name: "unknown tool in body",
			err:  &llm.UpstreamError{StatusCode: 422, Body: `{"error":{"message":"unknown tool: deepseek_read_url"}}`},
			want: true,
		},
		{
			name: "generic unsupported tool",
			err:  &llm.UpstreamError{StatusCode: 400, Message: "unsupported tool type"},
			want: true,
		},
		{
			name: "content risk",
			err:  &llm.UpstreamError{StatusCode: 400, Message: "Content Exists Risk"},
			want: false,
		},
		{
			name: "context overflow",
			err:  &llm.UpstreamError{StatusCode: 400, Message: "This model's maximum context length is exceeded"},
			want: false,
		},
		{
			name: "rate limited",
			err:  &llm.UpstreamError{StatusCode: 429, Message: "rate limit reached for tools"},
			want: false,
		},
		{
			name: "server error without tool context",
			err:  &llm.UpstreamError{StatusCode: 500, Message: "internal server error"},
			want: false,
		},
		{
			name: "accepted request must not retry",
			err: llm.MarkRequestAccepted(&llm.UpstreamError{
				StatusCode: 400,
				Message:    "unknown tool: deepseek_web_search",
			}),
			want: false,
		},
		{
			name: "plain error",
			err:  errors.New("unknown tool: deepseek_web_search"),
			want: false,
		},
		{
			name: "nil",
			err:  nil,
			want: false,
		},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := shouldRetryWithoutNativeTools(item.err); got != item.want {
				t.Fatalf("shouldRetryWithoutNativeTools(%v) = %v, want %v", item.err, got, item.want)
			}
		})
	}
}
