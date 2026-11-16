package conversation

import (
	"errors"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/ports/llm"
	"go.uber.org/zap"
)

// deepSeekNativeWebToolTypes 是本轮唯一允许被自动回退剥离的厂商原生工具。
// 仅移除这两个声明，保留 MCP/function 工具与其他厂商原生工具。
var deepSeekNativeWebToolTypes = map[string]struct{}{
	"deepseek_web_search": {},
	"deepseek_read_url":   {},
}

const nativeToolsFallbackMessage = "当前上游不支持联网搜索/阅读链接，已自动回退为普通回答"

// hasVendorNativeTools 判断请求参数里是否声明了可回退的 DeepSeek 原生联网工具。
func hasVendorNativeTools(options map[string]any) bool {
	if len(options) == 0 {
		return false
	}
	for _, tool := range providerToolOptionPayloads(options["tools"]) {
		if isVendorNativeWebTool(tool) {
			return true
		}
	}
	return false
}

// withoutVendorNativeTools 返回剥离 DeepSeek 原生联网工具后的参数副本（不修改入参）。
// 其他工具与参数保留；tool_choice 若指向已被移除的工具则退回 auto。
func withoutVendorNativeTools(options map[string]any) map[string]any {
	if len(options) == 0 {
		return options
	}
	result := make(map[string]any, len(options))
	for key, value := range options {
		result[key] = value
	}
	rawTools, hasTools := result["tools"]
	if hasTools {
		tools := providerToolOptionPayloads(rawTools)
		filtered := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			if isVendorNativeWebTool(tool) {
				continue
			}
			filtered = append(filtered, tool)
		}
		if len(filtered) == 0 {
			delete(result, "tools")
		} else {
			result["tools"] = filtered
		}
	}
	if rawChoice, ok := result["tool_choice"]; ok && toolChoiceTargetsVendorNativeTool(rawChoice) {
		// 被强制的工具已移除：回到上游默认的自动选择，避免留下指向不存在工具的声明。
		delete(result, "tool_choice")
	}
	return result
}

func isVendorNativeWebTool(tool map[string]any) bool {
	if len(tool) == 0 {
		return false
	}
	for _, key := range []string{"type", "name"} {
		if _, ok := deepSeekNativeWebToolTypes[strings.ToLower(strings.TrimSpace(modelOptionStringValue(tool[key])))]; ok {
			return true
		}
	}
	return false
}

func toolChoiceTargetsVendorNativeTool(raw any) bool {
	switch typed := raw.(type) {
	case map[string]any:
		if _, ok := deepSeekNativeWebToolTypes[strings.ToLower(strings.TrimSpace(modelOptionStringValue(typed["name"])))]; ok {
			return true
		}
		return toolChoiceTargetsVendorNativeTool(typed["function"])
	case []any:
		for _, item := range typed {
			if toolChoiceTargetsVendorNativeTool(item) {
				return true
			}
		}
		return false
	case []map[string]any:
		for _, item := range typed {
			if toolChoiceTargetsVendorNativeTool(item) {
				return true
			}
		}
		return false
	default:
		value := strings.ToLower(strings.TrimSpace(modelOptionStringValue(raw)))
		if value == "" {
			return false
		}
		_, ok := deepSeekNativeWebToolTypes[value]
		return ok
	}
}

// shouldRetryWithoutNativeTools 判断上游是否明确因为原生工具声明而拒绝了请求。
// 只有“请求未被接受 + 明确的工具参数类错误”才允许去掉工具重发一次，避免重复计费或掩盖真实错误。
func shouldRetryWithoutNativeTools(err error) bool {
	if err == nil || llm.RequestWasAccepted(err) {
		return false
	}
	var upstreamErr *llm.UpstreamError
	if !errors.As(err, &upstreamErr) || upstreamErr == nil {
		return false
	}
	switch upstreamErr.StatusCode {
	case 400, 404, 415, 422, 500, 502:
	default:
		return false
	}
	text := strings.ToLower(upstreamErr.Message + "\n" + upstreamErr.Body)
	if text == "" {
		return false
	}
	// 内容审核、上下文超限、鉴权、余额与限流等错误与工具声明无关，禁止触发回退。
	for _, blocked := range []string{
		"content exists risk",
		"context length",
		"context_length",
		"maximum context",
		"context window",
		"safety",
		"moderation",
		"balance",
		"insufficient",
		"quota",
		"payment",
		"authentication",
		"api key",
		"api_key",
		"invalid key",
		"unauthorized",
		"forbidden",
		"rate limit",
		"too many requests",
	} {
		if strings.Contains(text, blocked) {
			return false
		}
	}
	if strings.Contains(text, "deepseek_web_search") || strings.Contains(text, "deepseek_read_url") {
		return true
	}
	if !strings.Contains(text, "tool") {
		return false
	}
	for _, marker := range []string{
		"unknown",
		"unsupported",
		"not supported",
		"invalid",
		"cannot be used together",
		"can only be declared once",
		"unrecognized",
		"not allowed",
		"not permitted",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

// recordNativeToolsFallbackTrace 在过程轨迹里留下“已回退为无联网回答”的可见说明。
func recordNativeToolsFallbackTrace(traceRecorder *messageTraceRecorder, err error) {
	if traceRecorder == nil || !traceRecorder.enabled() {
		return
	}
	payload := &tracePayload{
		Status:   "fallback",
		Reason:   "native_tools_rejected",
		Fallback: "native_tools",
	}
	if err != nil {
		// 只记录脱敏后的错误摘要，避免把上游地址或密钥带进轨迹。
		payload.Error = sanitizeNativeToolsFallbackError(err)
	}
	traceRecorder.appendProcessSection(
		nativeToolsFallbackMessage,
		"- "+nativeToolsFallbackMessage+"。",
		payload,
		messageTraceStatusCompleted,
	)
}

func sanitizeNativeToolsFallbackError(err error) string {
	if err == nil {
		return ""
	}
	var upstreamErr *llm.UpstreamError
	if errors.As(err, &upstreamErr) && upstreamErr != nil {
		return truncateTraceText(strings.TrimSpace(upstreamErr.Message), 240)
	}
	return ""
}

func truncateTraceText(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxChars <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "…"
}

// logVendorNativeToolsUnused 在上游接受请求但没有产出任何联网动作时记录一条告警。
// 这是“网关静默丢弃服务端工具”的唯一可观测线索：请求没有失败，所以不会触发自动回退。
func logVendorNativeToolsUnused(
	logger *zap.Logger,
	protocol string,
	vendor string,
	upstreamModel string,
	runID string,
	options map[string]any,
	output *llm.GenerateOutput,
) {
	if logger == nil || output == nil || !hasVendorNativeTools(options) {
		return
	}
	if len(output.ServerToolCalls) > 0 || len(output.Citations) > 0 {
		return
	}
	logger.Warn("vendor_native_tools_declared_but_unused",
		zap.String("run_id", strings.TrimSpace(runID)),
		zap.String("protocol", strings.TrimSpace(protocol)),
		zap.String("vendor", strings.TrimSpace(vendor)),
		zap.String("upstream_model", strings.TrimSpace(upstreamModel)),
	)
}
