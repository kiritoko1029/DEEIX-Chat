package llm

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	portllm "github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestDeepSeekAnthropicWebToolResults(t *testing.T) {
	tests := []struct {
		name, tool, kind, input, block, output, errorCode string
	}{
		{
			name: "search metadata", tool: "deepseek_web_search", kind: "search",
			input:  `{"action":"search","queries":["q1","q2"]}`,
			block:  `{"type":"web_search_tool_result","content":[{"type":"web_search_result","url":"https://example.com/a","title":"标题","snippet":"摘要","site_name":"站点","site_icon":"https://cdn.example.com/icon","page_age":"2026-01-02","published_at":"ignored","image_url":"https://example.com/i.png","encrypted_content":"opaque","signature":"opaque"},{"title":"missing URL"}]}`,
			output: `{"kind":"search","queries":["q1","q2"],"sources":[{"url":"https://example.com/a","title":"标题","snippet":"摘要","site_name":"站点","site_icon":"https://cdn.example.com/icon","published_at":"2026-01-02","image_url":"https://example.com/i.png"}]}`,
		},
		{
			name: "image kind inherited from use", tool: "deepseek_web_search", kind: "deep_image_search",
			input:  `{"action":"search","queries":[]}`,
			block:  `{"type":"web_search_tool_result","content":[{"url":"https://example.com/image","image_url":"https://example.com/i.png","title":null,"site_name":null,"page_age":null}]}`,
			output: `{"kind":"deep_image_search","sources":[{"url":"https://example.com/image","image_url":"https://example.com/i.png"}]}`,
		},
		{
			name: "read link", tool: "deepseek_read_url", kind: "read_link",
			input:  `{"action":"read_link","url":"https://example.com/a"}`,
			block:  `{"type":"web_fetch_tool_result","kind":"read_link","signature":"opaque","content":{"type":"web_fetch_result","url":"https://example.com/a","content":{"title":"页面","source":{"data":"","encrypted_content":"opaque"}},"published_at":"2026-01-01"}}`,
			output: `{"kind":"read_link","page":{"url":"https://example.com/a","title":"页面","published_at":"2026-01-01"}}`,
		},
		{
			name: "deep search find", tool: "deepseek_web_search", kind: "deep_search",
			input:  `{"action":"find_in_page","url":"https://example.com/a","pattern":"330"}`,
			block:  `{"type":"web_fetch_tool_result","content":{"url":"https://example.com/a","content":{"title":null},"published_at":null}}`,
			output: `{"kind":"deep_search","page":{"url":"https://example.com/a","published_at":null},"pattern":"330"}`,
		},
		{
			name: "failed search", tool: "deepseek_web_search", kind: "search",
			input:  `{"action":"search","queries":["q"]}`,
			block:  `{"type":"web_search_tool_result","content":[{"type":"web_search_tool_result_error","error_code":"unavailable","signature":"opaque"}]}`,
			output: `{"kind":"search","error_code":"unavailable"}`, errorCode: "unavailable",
		},
		{
			name: "failed read", tool: "deepseek_read_url", kind: "read_link",
			input:  `{"action":"read_link","url":"https://example.com/a"}`,
			block:  `{"type":"web_fetch_tool_result","content":{"type":"web_fetch_tool_result_error","error_code":"url_not_accessible"}}`,
			output: `{"kind":"read_link","error_code":"url_not_accessible"}`, errorCode: "url_not_accessible",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			use := map[string]any{"type": "server_tool_use", "id": "srv_1", "name": tt.tool, "kind": tt.kind, "input": mustDecodeObject(t, tt.input)}
			block := mustDecodeObject(t, tt.block)
			block["tool_use_id"] = "srv_1"
			payload := map[string]any{"content": []any{use, block}}
			body, _ := json.Marshal(payload)
			result, err := parseAnthropicResponse(body, anthropicToolClassifier{})
			if err != nil || len(result.ServerToolCalls) != 1 || len(result.ToolCalls) != 0 {
				t.Fatalf("parse result: %#v, %v", result, err)
			}
			call := result.ServerToolCalls[0]
			if call.ToolName != tt.tool || call.ToolType != "server_tool_use" {
				t.Fatalf("unexpected identity: %#v", call)
			}
			assertJSONEqual(t, call.OutputJSON, tt.output)
			if tt.errorCode == "" {
				if call.Status != "completed" || call.ErrorJSON != "" {
					t.Fatalf("unexpected success: %#v", call)
				}
			} else {
				if call.Status != "failed" {
					t.Fatalf("expected failed status: %#v", call)
				}
				assertJSONEqual(t, call.ErrorJSON, `{"code":"`+tt.errorCode+`"}`)
			}
			streamed := &portllm.GenerateOutput{}
			var events []portllm.ToolCall
			for index, item := range []map[string]any{use, block} {
				err := applyAnthropicStreamEvent(map[string]any{"type": "content_block_start", "index": index, "content_block": item}, "", streamed, func(event portllm.GenerateStreamEvent) error {
					if event.ServerToolCall != nil {
						events = append(events, *event.ServerToolCall)
					}
					return nil
				}, anthropicToolClassifier{})
				if err != nil {
					t.Fatal(err)
				}
			}
			compactAnthropicStreamToolCalls(streamed)
			if !reflect.DeepEqual(streamed.ServerToolCalls, result.ServerToolCalls) || len(events) != 2 {
				t.Fatalf("stream/non-stream mismatch: %#v, %#v", streamed.ServerToolCalls, result.ServerToolCalls)
			}
			assertJSONEqual(t, events[1].OutputJSON, tt.output)
		})
	}
}

func TestDeepSeekAnthropicCitationsAndThinkingSignature(t *testing.T) {
	result, err := parseAnthropicResponse([]byte(`{"content":[
		{"type":"thinking","thinking":"think","signature":"thinking-signature"},
		{"type":"text","text":"a","citations":[{"url":"https://example.com/a"}]},
		{"type":"text","text":"b","citations":[{"url":"https://example.com/a"},{"url":"https://example.com/b"}]}
	]}`), anthropicToolClassifier{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Citations, []string{"https://example.com/a", "https://example.com/b"}) || result.Reasoning.Signature != "thinking-signature" {
		t.Fatalf("missing citations or thinking signature: %#v", result)
	}
	stream := &portllm.GenerateOutput{}
	for _, raw := range []string{
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","citations":[{"url":"https://example.com/a"}]}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"url":"https://example.com/a"}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"citations_delta","citation":{"url":"https://example.com/b"}}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"signature_delta","signature":"thinking-signature"}}`,
	} {
		if err := applyAnthropicStreamEvent(mustDecodeObject(t, raw), raw, stream, nil, anthropicToolClassifier{}); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(stream.Citations, result.Citations) || stream.Reasoning.Signature != "thinking-signature" {
		t.Fatalf("stream mismatch: %#v", stream)
	}
}

func TestDeepSeekAnthropicNativeClientToolUseRejected(t *testing.T) {
	for _, name := range []string{"deepseek_web_search", "deepseek_read_url"} {
		t.Run(name, func(t *testing.T) {
			payload := map[string]any{"tools": []any{map[string]any{"type": name}}}
			normalizeAnthropicNativeTools(payload)
			if getString(anthropicToolPayloads(payload["tools"])[0]["name"]) != name {
				t.Fatalf("native name not supplied: %#v", payload)
			}
			classifier := newAnthropicToolClassifier(payload, nil)
			block := map[string]any{"type": "tool_use", "id": "bad", "name": name, "input": map[string]any{}}
			body, _ := json.Marshal(map[string]any{"content": []any{block}})
			if _, err := parseAnthropicResponse(body, classifier); err == nil {
				t.Fatal("expected native tool_use rejection")
			}
			stream := &portllm.GenerateOutput{}
			if err := applyAnthropicStreamEvent(map[string]any{"type": "content_block_start", "index": 0, "content_block": block}, string(body), stream, nil, classifier); err == nil || len(stream.ToolCalls) != 0 {
				t.Fatalf("expected rejection without local execution: %#v, %v", stream, err)
			}
		})
	}
}

func TestDeepSeekResponsesWebToolItems(t *testing.T) {
	tests := []struct{ name, item, tool, output, status string }{
		{"search", `{"kind":"search","action":{"type":"search","queries":["q"],"sources":[{"url":"https://example.com/a","title":"a","snippet":"s","site_name":"site","site_icon":"https://example.com/icon","published_at":"2026-01-02","signature":"opaque"}]}}`, "deepseek_web_search", `{"kind":"search","queries":["q"],"sources":[{"url":"https://example.com/a","title":"a","snippet":"s","site_name":"site","site_icon":"https://example.com/icon","published_at":"2026-01-02"}]}`, "completed"},
		{"read", `{"kind":"read_link","action":{"type":"open_page","url":"https://example.com/a"},"page":{"url":"https://example.com/a","title":"a","published_at":"2026-01-01","signature":"opaque"},"signature":"opaque"}`, "deepseek_read_url", `{"kind":"read_link","page":{"url":"https://example.com/a","title":"a","published_at":"2026-01-01"}}`, "completed"},
		{"read action", `{"action":{"type":"read_link","url":"https://example.com/a"},"page":{"url":"https://example.com/a"}}`, "deepseek_read_url", `{"kind":"read_link","page":{"url":"https://example.com/a"}}`, "completed"},
		{"deep find", `{"kind":"deep_search","action":{"type":"find_in_page","url":"https://example.com/a","pattern":"330"},"page":{"url":"https://example.com/a","published_at":null}}`, "deepseek_web_search", `{"kind":"deep_search","page":{"url":"https://example.com/a","published_at":null},"pattern":"330"}`, "completed"},
		{"image", `{"kind":"image_search","action":{"type":"search","queries":[]},"sources":[{"url":"https://example.com/a","image_url":"https://example.com/i.png","encrypted_content":"opaque"}]}`, "deepseek_web_search", `{"kind":"image_search","sources":[{"url":"https://example.com/a","image_url":"https://example.com/i.png"}]}`, "completed"},
		{"error", `{"kind":"read_link","status":"failed","error":{"code":"url_not_allowed","signature":"opaque"}}`, "deepseek_read_url", `{"kind":"read_link","error_code":"url_not_allowed"}`, "failed"},
		{"error without status", `{"kind":"search","error":{"code":"unavailable"}}`, "deepseek_web_search", `{"kind":"search","error_code":"unavailable"}`, "failed"},
		{"failed without error", `{"kind":"search","status":"failed"}`, "deepseek_web_search", `{"kind":"search","error_code":"unavailable"}`, "failed"},
		{"existing OpenAI", `{"action":{"type":"search","query":"q","sources":[{"url":"https://example.com/a","signature":"opaque","encrypted_content":"opaque"}]}}`, "web_search", `{"type":"search","query":"q","sources":[{"url":"https://example.com/a"}]}`, "completed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item := mustDecodeObject(t, tt.item)
			item["type"], item["id"] = "web_search_call", "srv_1"
			result := buildGenerateOutputFromParsedForAdapter(portllm.EndpointResponses, portllm.AdapterOpenAIResponses, map[string]any{"output": []any{item}}, false)
			if len(result.ServerToolCalls) != 1 || len(result.ToolCalls) != 0 {
				t.Fatalf("unexpected tools: %#v", result)
			}
			call := result.ServerToolCalls[0]
			if call.ToolName != tt.tool || call.ToolType != "web_search_call" || call.Status != tt.status {
				t.Fatalf("unexpected call: %#v", call)
			}
			assertJSONEqual(t, call.OutputJSON, tt.output)
			if strings.Contains(call.ArgumentsJSON, "sources") {
				t.Fatalf("sources leaked into action: %s", call.ArgumentsJSON)
			}
			if tt.status == "failed" && !strings.Contains(call.ErrorJSON, `"code"`) {
				t.Fatalf("missing ErrorJSON: %#v", call)
			}
		})
	}
}

func TestDeepSeekResponsesStreamMergesPartialEvents(t *testing.T) {
	result := &portllm.GenerateOutput{}
	var events []portllm.ToolCall
	for _, raw := range []string{
		`{"type":"response.output_item.added","item":{"type":"web_search_call","id":"search","kind":"deep_image_search","action":{"type":"search","queries":["q"]}}}`,
		`{"type":"response.output_item.added","item":{"type":"web_search_call","id":"read","kind":"read_link","action":{"type":"open_page","url":"https://example.com/a"}}}`,
		`{"type":"response.web_search_call.searching","item_id":"search"}`,
		`{"type":"response.web_search_call.completed","item_id":"search","sources":[{"url":"https://example.com/image","image_url":"https://example.com/i.png","signature":"opaque"}]}`,
		`{"type":"response.web_search_call.completed","item_id":"read","page":{"url":"https://example.com/a","title":"a"},"signature":"opaque"}`,
		`{"type":"response.output_item.done","item":{"type":"web_search_call","id":"search"}}`,
		`{"type":"response.output_item.done","item":{"type":"web_search_call","id":"read","status":"completed","kind":"read_link","page":{"url":"https://example.com/a","title":"final","published_at":"2026-01-01"},"signature":"opaque"}}`,
		`{"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://example.com/a"}}`,
		`{"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://example.com/a"}}`,
		`{"type":"response.incomplete","response":{"id":"resp_1","status":"incomplete","usage":{"input_tokens":10,"output_tokens":2,"server_tool_use":{"tool_search":2,"tool_fetch":1}},"output":[]}}`,
	} {
		if err := applyResponsesStreamEvent(portllm.AdapterOpenAIResponses, "", mustDecodeObject(t, raw), raw, result, func(event portllm.GenerateStreamEvent) error {
			if event.ServerToolCall != nil {
				events = append(events, *event.ServerToolCall)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(result.ServerToolCalls) != 2 || len(events) != 7 || events[0].Status != "in_progress" {
		t.Fatalf("unexpected stream calls/events: %#v, %#v", result.ServerToolCalls, events)
	}
	search, read := result.ServerToolCalls[0], result.ServerToolCalls[1]
	if search.ToolName != "deepseek_web_search" || read.ToolName != "deepseek_read_url" || search.Status != "completed" || read.Status != "completed" {
		t.Fatalf("lost names or status: %#v", result.ServerToolCalls)
	}
	assertJSONEqual(t, search.OutputJSON, `{"kind":"deep_image_search","queries":["q"],"sources":[{"url":"https://example.com/image","image_url":"https://example.com/i.png"}]}`)
	assertJSONEqual(t, read.OutputJSON, `{"kind":"read_link","page":{"url":"https://example.com/a","title":"final","published_at":"2026-01-01"}}`)
	if !strings.Contains(read.ArgumentsJSON, "open_page") || !strings.Contains(events[4].OutputJSON, `"page"`) || result.ServerSideToolUsage["tool_fetch"] != 1 {
		t.Fatalf("lost arguments, event page or usage: %#v", result)
	}
	if !reflect.DeepEqual(result.Citations, []string{"https://example.com/a", "https://example.com/image"}) {
		t.Fatalf("unexpected citations: %#v", result.Citations)
	}
}

func TestDeepSeekResponsesConcurrentCallsCorrelateByOutputIndex(t *testing.T) {
	raw := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":2,"item":{"type":"web_search_call","id":"a","kind":"read_link","action":{"type":"open_page","url":"https://example.com/a"}}}`,
		``,
		`data: {"type":"response.output_item.added","output_index":5,"item":{"type":"web_search_call","id":"b","kind":"read_link","action":{"type":"open_page","url":"https://example.com/b"}}}`,
		``,
		`data: {"type":"response.web_search_call.completed","output_index":5,"page":{"url":"https://example.com/b","title":"b"}}`,
		``,
		`data: {"type":"response.web_search_call.completed","output_index":2,"page":{"url":"https://example.com/a","title":"a"}}`,
		``,
	}, "\n")
	result := &portllm.GenerateOutput{}
	if err := consumeOpenAIGenerateStream(portllm.EndpointResponses, portllm.AdapterOpenAIResponses, strings.NewReader(raw), result, nil, false); err != nil {
		t.Fatal(err)
	}
	if len(result.ServerToolCalls) != 2 || result.ServerToolCalls[0].ToolCallID != "a" || result.ServerToolCalls[1].ToolCallID != "b" {
		t.Fatalf("lost concurrent calls: %#v", result.ServerToolCalls)
	}
	for index, id := range []string{"a", "b"} {
		call := result.ServerToolCalls[index]
		if call.Status != "completed" || call.ToolName != "deepseek_read_url" {
			t.Fatalf("lost completion: %#v", call)
		}
		assertJSONEqual(t, call.OutputJSON, `{"kind":"read_link","page":{"url":"https://example.com/`+id+`","title":"`+id+`"}}`)
	}
}

func TestDeepSeekChatNonStreamingServerTools(t *testing.T) {
	payload := mustDecodeObject(t, `{
		"choices":[{"message":{"content":"answer","reasoning_content":"think","tool_calls":[{"id":"fn","type":"function","function":{"name":"memory.save","arguments":"{}"}}],
		"annotations":[{"type":"url_citation","url_citation":{"url":"https://example.com/a"}},{"type":"url_citation","url_citation":{"url":"https://example.com/a"}}],
		"server_tool_calls":[{"index":0,"calls":[
			{"id":"read","type":"deepseek_read_url","kind":"read_link","action":{"type":"read_link","url":"https://example.com/a"},"status":"completed","page":{"url":"https://example.com/a","title":"a","published_at":"2026-01-01"},"signature":"opaque"},
			{"id":"failed","type":"deepseek_read_url","kind":"read_link","action":{"type":"read_link","url":"https://example.com/b"},"status":"failed","error":{"code":"url_not_accessible"}}
		]},{"index":1,"calls":[
			{"id":"search","type":"deepseek_web_search","kind":"search","action":{"type":"search","queries":["q"]},"status":"completed","sources":[{"url":"https://example.com/c","title":"c","snippet":"s","site_name":"site","site_icon":"https://example.com/icon","published_at":"2026-01-02","image_url":"https://example.com/i.png","encrypted_content":"opaque"}]}
		]}]}}],
		"usage":{"prompt_tokens":11,"completion_tokens":9,"server_tool_use":{"web_search_requests":99,"tool_search":2,"tool_fetch":1,"tool_image_search":0}}
	}`)
	result := buildGenerateOutputFromParsedForAdapter(portllm.EndpointChatCompletions, portllm.AdapterOpenAIChatCompletions, payload, false)
	if len(result.ServerToolCalls) != 3 || len(result.ToolCalls) != 1 || result.ToolCalls[0].ToolName != "memory.save" || result.Text != "answer" || result.Reasoning.Text != "think" {
		t.Fatalf("missing chat output: %#v", result)
	}
	read, failed, search := result.ServerToolCalls[0], result.ServerToolCalls[1], result.ServerToolCalls[2]
	if read.ToolCallID != "read" || read.ToolType != "deepseek_read_url" || read.ToolName != "deepseek_read_url" || read.Status != "completed" {
		t.Fatalf("unexpected call identity: %#v", read)
	}
	assertJSONEqual(t, read.ArgumentsJSON, `{"type":"read_link","url":"https://example.com/a"}`)
	assertJSONEqual(t, read.OutputJSON, `{"kind":"read_link","page":{"url":"https://example.com/a","title":"a","published_at":"2026-01-01"}}`)
	assertJSONEqual(t, failed.OutputJSON, `{"kind":"read_link","error_code":"url_not_accessible"}`)
	assertJSONEqual(t, failed.ErrorJSON, `{"code":"url_not_accessible"}`)
	if failed.Status != "failed" || search.ToolCallID != "search" {
		t.Fatalf("wrong flattened order/status: %#v", result.ServerToolCalls)
	}
	assertJSONEqual(t, search.OutputJSON, `{"kind":"search","queries":["q"],"sources":[{"url":"https://example.com/c","title":"c","snippet":"s","site_name":"site","site_icon":"https://example.com/icon","published_at":"2026-01-02","image_url":"https://example.com/i.png"}]}`)
	if !reflect.DeepEqual(result.Citations, []string{"https://example.com/a"}) || !reflect.DeepEqual(result.ServerSideToolUsage, map[string]int64{"tool_search": 2, "tool_fetch": 1}) || result.Usage.InputTokens != 11 || result.Usage.OutputTokens != 9 {
		t.Fatalf("missing citations/usage: %#v", result)
	}
}

func TestDeepSeekChatStreamEventsAndFinalReconciliation(t *testing.T) {
	chunks := []string{
		`{"id":"chat_1","choices":[{"delta":{"reasoning_content":"think","tool_calls":[{"index":0,"id":"fn","type":"function","function":{"name":"memory.save","arguments":"{"}}]},"server_tool_event":{"type":"step_reasoning_done","step":0}}]}`,
		`{"choices":[{"delta":{},"server_tool_event":{"type":"call_started","step":0,"call":{"id":"read","type":"deepseek_read_url","kind":"read_link","action":{"type":"read_link","url":"https://example.com/a"}}}}]}`,
		`{"choices":[{"delta":{},"server_tool_event":{"type":"call_started","step":0,"call":{"id":"search","type":"deepseek_web_search","kind":"search","action":{"type":"search","queries":["q"]}}}}]}`,
		`{"choices":[{"delta":{},"server_tool_event":{"type":"call_completed","step":0,"call":{"id":"search","status":"completed","sources":[{"url":"https://example.com/c","encrypted_content":"opaque"}]}}}]}`,
		`{"choices":[{"delta":{},"server_tool_event":{"type":"call_completed","step":0,"call":{"id":"read","status":"completed","page":{"url":"https://example.com/a","title":"a"}}}}]}`,
		`{"choices":[{"delta":{"content":"answer","tool_calls":[{"index":0,"function":{"arguments":"}"}}],"annotations":[{"type":"url_citation","url_citation":{"url":"https://example.com/a"}},{"type":"url_citation","url_citation":{"url":"https://example.com/a"}}],"server_tool_calls":[{"index":0,"calls":[{"id":"read","type":"deepseek_read_url","kind":"read_link","action":{"type":"read_link","url":"https://example.com/a"},"status":"completed","page":{"url":"https://example.com/a","title":"final","published_at":"2026-01-01"},"signature":"opaque"},{"id":"search","type":"deepseek_web_search","kind":"search","action":{"type":"search","queries":["q"]},"status":"completed","sources":[{"url":"https://example.com/c"}]},{"id":"failed","type":"deepseek_read_url","kind":"read_link","status":"failed","error":{"code":"unavailable"}}]}]},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":9,"server_tool_use":{"tool_search":2,"tool_fetch":1}}}`,
	}
	var raw strings.Builder
	for _, chunk := range chunks {
		raw.WriteString("data: " + chunk + "\n\n")
	}
	raw.WriteString("data: [DONE]\n\n")
	result := &portllm.GenerateOutput{}
	var events []portllm.ToolCall
	err := consumeOpenAIGenerateStream(portllm.EndpointChatCompletions, portllm.AdapterOpenAIChatCompletions, strings.NewReader(raw.String()), result, func(event portllm.GenerateStreamEvent) error {
		if event.ServerToolCall != nil {
			events = append(events, *event.ServerToolCall)
		}
		return nil
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ServerToolCalls) != 3 || len(events) != 7 || events[0].Status != "in_progress" || events[2].Status != "completed" || events[3].Status != "completed" {
		t.Fatalf("unexpected calls/events: %#v, %#v", result.ServerToolCalls, events)
	}
	assertJSONEqual(t, events[2].ArgumentsJSON, `{"type":"search","queries":["q"]}`)
	assertJSONEqual(t, events[3].ArgumentsJSON, `{"type":"read_link","url":"https://example.com/a"}`)
	assertJSONEqual(t, result.ServerToolCalls[0].OutputJSON, `{"kind":"read_link","page":{"url":"https://example.com/a","title":"final","published_at":"2026-01-01"}}`)
	assertJSONEqual(t, result.ServerToolCalls[1].OutputJSON, `{"kind":"search","queries":["q"],"sources":[{"url":"https://example.com/c"}]}`)
	if result.ServerToolCalls[2].Status != "failed" || len(result.ToolCalls) != 1 || result.ToolCalls[0].ArgumentsJSON != "{}" || result.Reasoning.Text != "think" || result.Text != "answer" {
		t.Fatalf("lost existing chat semantics: %#v", result)
	}
	if !reflect.DeepEqual(result.Citations, []string{"https://example.com/a"}) || result.ServerSideToolUsage["tool_search"] != 2 || result.Usage.OutputTokens != 9 {
		t.Fatalf("lost citations/usage: %#v", result)
	}
}

func TestDeepSeekServerToolUsageIgnoresDisplayCounters(t *testing.T) {
	payload := mustDecodeObject(t, `{"usage":{"server_tool_use":{"web_search_requests":99,"web_fetch_requests":88,"tool_search":2,"tool_fetch":3,"tool_image_search":4},"server_side_tool_usage":{"web_search":999}}}`)
	want := map[string]int64{"tool_search": 2, "tool_fetch": 3, "tool_image_search": 4}
	for name, parse := range map[string]func(map[string]any) map[string]int64{"anthropic": parseAnthropicServerSideToolUsage, "responses/chat": parseServerSideToolUsage} {
		t.Run(name, func(t *testing.T) {
			if got := parse(payload); !reflect.DeepEqual(got, want) {
				t.Fatalf("billed counts differ: %#v", got)
			}
			zero := mustDecodeObject(t, `{"usage":{"server_tool_use":{"web_search_requests":99,"tool_search":0,"tool_fetch":0,"tool_image_search":0}}}`)
			if got := parse(zero); len(got) != 0 {
				t.Fatalf("zero billed counts must not fall back to display counts: %#v", got)
			}
		})
	}
}

func TestServerToolOutputDropsOpaqueFieldsRecursively(t *testing.T) {
	block := mustDecodeObject(t, `{"type":"code_execution_tool_result","signature":"opaque","content":[{"encrypted_content":"opaque","encryptedContent":"opaque","nested":{"signature":"opaque","text":"safe"}}]}`)
	output := mergeAnthropicServerToolResult(portllm.ToolCall{}, block).OutputJSON
	assertJSONEqual(t, output, `{"type":"code_execution_tool_result","content":[{"nested":{"text":"safe"}}]}`)
	call := parseResponseServerToolCall(mustDecodeObject(t, `{"type":"shell_call","id":"s","output":"{\"signature\":\"opaque\",\"content\":[{\"encrypted_content\":\"opaque\",\"text\":\"safe\"}]}"}`))
	assertJSONEqual(t, call.OutputJSON, `{"content":[{"text":"safe"}]}`)
}
