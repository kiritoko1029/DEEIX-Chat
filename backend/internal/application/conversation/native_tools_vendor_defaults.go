package conversation

import (
	"encoding/json"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/nativetool"
)

// applyVendorDefaultNativeTools 按路由厂商在用户未表态时默认带上官方原生工具。
//
// 规则按优先级：
//  1. 管理员在模型能力 JSON 里声明过原生工具（nativeTools/nativeToolKeys/defaultOptions.tools，
//     哪怕被显式清空为 []）时完全接管，不再注入；
//  2. 用户显式提交空 tools 数组表示关闭，不注入；
//  3. 否则把厂商在 catalog 中标记为默认开启的工具追加到请求工具栏，并按类型去重。
//
// 该函数只负责请求形状，是否真的被上游接受由 adapter 与自动回退负责。
func applyVendorDefaultNativeTools(
	protocolKey string,
	vendor string,
	capabilitiesJSON string,
	rawTools any,
	requested []map[string]any,
) []map[string]any {
	vendor = strings.TrimSpace(vendor)
	if vendor == "" {
		return requested
	}
	if capabilityDeclaresNativeTools(capabilitiesJSON) {
		return requested
	}
	if explicitEmptyNativeToolsOption(rawTools) {
		return requested
	}
	defaults := nativetool.DefaultPayloads(vendor, protocolKey)
	if len(defaults) == 0 {
		return requested
	}
	seen := make(map[string]struct{}, len(requested)+len(defaults))
	for _, tool := range requested {
		seen[nativeToolPayloadIdentity(tool)] = struct{}{}
	}
	result := make([]map[string]any, 0, len(requested)+len(defaults))
	result = append(result, requested...)
	for _, payload := range defaults {
		identity := nativeToolPayloadIdentity(payload)
		if identity == "" {
			continue
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, payload)
	}
	if len(result) == 0 {
		return requested
	}
	return result
}

// capabilityDeclaresNativeTools 判断模型能力 JSON 是否显式声明了原生工具配置。
func capabilityDeclaresNativeTools(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	var payload struct {
		NativeTools    json.RawMessage `json:"nativeTools"`
		NativeToolKeys json.RawMessage `json:"nativeToolKeys"`
		DefaultOptions struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"defaultOptions"`
	}
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return false
	}
	present := func(rawValue json.RawMessage) bool {
		trimmed := strings.TrimSpace(string(rawValue))
		return trimmed != "" && trimmed != "null"
	}
	return present(payload.NativeTools) || present(payload.NativeToolKeys) || present(payload.DefaultOptions.Tools)
}

// explicitEmptyNativeToolsOption 判断用户是否显式提交了空 tools 数组（表示关闭原生工具）。
func explicitEmptyNativeToolsOption(raw any) bool {
	switch typed := raw.(type) {
	case []any:
		return len(typed) == 0
	case []map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

// nativeToolPayloadIdentity 返回原生工具声明的去重标识。
// 同一 type 在上游只能声明一次，因此优先用 type 归一。
func nativeToolPayloadIdentity(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if toolType := strings.TrimSpace(modelOptionStringValue(payload["type"])); toolType != "" {
		return toolType
	}
	if toolType := strings.TrimSpace(modelOptionStringValue(payload["name"])); toolType != "" {
		return toolType
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(raw)
}
