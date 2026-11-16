package conversation

import (
	"testing"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
)

func TestUpstreamServerToolRowsFeedWebSourcePresentation(t *testing.T) {
	output := &llm.GenerateOutput{ServerToolCalls: []llm.ToolCall{
		{
			ToolCallID:    "srvtoolu_1",
			ToolType:      "server_tool_use",
			ToolName:      "deepseek_web_search",
			Status:        "completed",
			ArgumentsJSON: `{"action":"search","queries":["iceland population"]}`,
			OutputJSON:    `{"kind":"search","queries":["iceland population"],"sources":[{"url":"https://www.huaon.com/x.html","title":"冰岛人口","snippet":"40.46万人","site_name":"华经情报网","site_icon":"https://cdn.deepseek.com/site-icons/huaon.com","published_at":"2025-10-15T16:00:00+00:00"}]}`,
		},
		{
			ToolCallID:    "srvtoolu_2",
			ToolType:      "server_tool_use",
			ToolName:      "deepseek_read_url",
			Status:        "completed",
			ArgumentsJSON: `{"action":"read_link","url":"https://example.com/a"}`,
			OutputJSON:    `{"kind":"read_link","page":{"url":"https://example.com/a","title":"Example Domain","published_at":"2026-01-01"}}`,
		},
	}}
	rows := upstreamServerToolCallRows(output, "run-1")
	if len(rows) != 2 {
		t.Fatalf("expected two tool call rows, got %#v", rows)
	}
	_, _, payload := buildToolTrace(rows)
	if payload == nil || len(payload.ToolCalls) != 2 {
		t.Fatalf("expected trace payload with two tool calls, got %#v", payload)
	}
	search := payload.ToolCalls[0]
	if search.OutputPresentation == nil || len(search.OutputPresentation.Sources) != 1 {
		t.Fatalf("expected search presentation source, got %#v", search.OutputPresentation)
	}
	searchSource := search.OutputPresentation.Sources[0]
	if searchSource.SiteName != "华经情报网" || searchSource.SiteIcon == "" || searchSource.PublishedAt == "" {
		t.Fatalf("expected search source metadata, got %#v", searchSource)
	}
	read := payload.ToolCalls[1]
	if read.OutputPresentation == nil || len(read.OutputPresentation.Sources) != 1 {
		t.Fatalf("expected read link presentation source, got %#v", read.OutputPresentation)
	}
	if read.OutputPresentation.Sources[0].URL != "https://example.com/a" {
		t.Fatalf("unexpected read link source: %#v", read.OutputPresentation.Sources[0])
	}
	if read.OutputPresentation.Sources[0].Kind != "read_link" {
		t.Fatalf("expected read_link kind, got %#v", read.OutputPresentation.Sources[0])
	}
}

func TestWithoutVendorNativeToolsKeepsOtherToolsForFollowUpCalls(t *testing.T) {
	options := map[string]any{
		"tools": []any{
			map[string]any{"type": "deepseek_web_search"},
			map[string]any{"type": "function", "name": "lookup_ticket"},
		},
	}
	peeled := withoutVendorNativeTools(options)
	tools := providerToolOptionPayloads(peeled["tools"])
	if len(tools) != 1 || modelOptionStringValue(tools[0]["type"]) != "function" {
		t.Fatalf("expected function tool to survive fallback, got %#v", tools)
	}
}
