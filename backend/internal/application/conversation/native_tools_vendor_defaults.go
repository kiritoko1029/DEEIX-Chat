package conversation

import (
	"encoding/json"
	"strings"

	"github.com/DEEIX-AI/DEEIX-Chat/backend/internal/shared/nativetool"
)

// applyVendorDefaultNativeTools 按路由厂商在用户未表态时默认带上官方原生工具。
//
// 规则按优先级：
//  1. 管理员用 disableVendorNativeTools/vendorNativeTools=false 显式关闭注入时不处理；
//  2. 用户显式提交空 tools 数组表示关闭，不处理；
//  3. 管理员声明了本厂商原生工具（例如 deepseek_web_search）时完全按其声明执行；
//  4. 管理员只声明了其它厂商的服务端工具时，当前厂商会忽略这些声明（DeepSeek 文档：
//     未识别的工具类型被忽略），因此回落到厂商默认工具，避免“开关开着但没有联网”；
//  5. 未声明原生工具时追加厂商默认工具，按类型去重。
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
	if capabilityDisablesVendorNativeTools(capabilitiesJSON) {
		return requested
	}
	defaults := nativetool.DefaultPayloads(vendor, protocolKey)
	if len(defaults) == 0 {
		return requested
	}
	if explicitEmptyNativeToolsOption(rawTools) {
		return requested
	}
	if capabilityDeclaresNativeTools(capabilitiesJSON) {
		if capabilityDeclaresVendorNativeTools(capabilitiesJSON, vendor) {
			// 管理员声明了本厂商原生工具：完全按其声明与用户选择执行，不再追加默认值。
			return filterVendorOwnedNativeTools(requested, vendor)
		}
		// 管理员只声明了其它厂商的服务端工具：当前厂商会忽略它们，回落到厂商默认工具。
		return mergeVendorDefaultNativeTools(nil, defaults)
	}
	return mergeVendorDefaultNativeTools(requested, defaults)
}

// filterVendorOwnedNativeTools 只保留当前厂商自己声明的服务端工具。
func filterVendorOwnedNativeTools(tools []map[string]any, vendor string) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		if isVendorOwnedNativeTool(tool, vendor) {
			result = append(result, tool)
		}
	}
	return result
}

func isVendorOwnedNativeTool(tool map[string]any, vendor string) bool {
	if len(tool) == 0 {
		return false
	}
	for _, key := range []string{"type", "name"} {
		if nativetool.IsVendorOwnedType(vendor, modelOptionStringValue(tool[key])) {
			return true
		}
	}
	return false
}

// mergeVendorDefaultNativeTools 把厂商默认工具追加到已选工具之后，并按类型去重。
func mergeVendorDefaultNativeTools(requested []map[string]any, defaults []map[string]any) []map[string]any {
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
// 空数组视为“未声明”：管理端编辑器与旧版本导入会写入空数组，若按“出现即接管”处理，
// 用户在输入框打开的联网开关会看着生效，实际请求却不带任何工具。
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
	declared := func(rawValue json.RawMessage) bool {
		trimmed := strings.TrimSpace(string(rawValue))
		if trimmed == "" || trimmed == "null" {
			return false
		}
		var items []json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &items); err == nil {
			return len(items) > 0
		}
		return true
	}
	return declared(payload.NativeTools) || declared(payload.NativeToolKeys) || declared(payload.DefaultOptions.Tools)
}

// capabilityDeclaresVendorNativeTools 判断模型能力里声明的原生工具是否包含当前厂商自己的工具。
func capabilityDeclaresVendorNativeTools(raw string, vendor string) bool {
	value := strings.TrimSpace(raw)
	if value == "" || strings.TrimSpace(vendor) == "" {
		return false
	}
	var payload struct {
		NativeTools []struct {
			Key     string         `json:"key"`
			ToolKey string         `json:"toolKey"`
			Type    string         `json:"type"`
			Payload map[string]any `json:"payload"`
			Enabled *bool          `json:"enabled"`
		} `json:"nativeTools"`
		NativeToolKeys []string `json:"nativeToolKeys"`
		DefaultOptions struct {
			Tools []map[string]any `json:"tools"`
		} `json:"defaultOptions"`
	}
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return false
	}
	for _, tool := range payload.NativeTools {
		if tool.Enabled != nil && !*tool.Enabled {
			continue
		}
		names := []string{tool.Type, tool.Key, tool.ToolKey, modelOptionStringValue(tool.Payload["type"])}
		for _, name := range names {
			if nativetool.IsVendorOwnedType(vendor, name) {
				return true
			}
		}
	}
	for _, key := range payload.NativeToolKeys {
		if nativetool.IsVendorOwnedType(vendor, key) {
			return true
		}
	}
	for _, tool := range payload.DefaultOptions.Tools {
		if nativetool.IsVendorOwnedType(vendor, modelOptionStringValue(tool["type"])) {
			return true
		}
	}
	return false
}

// capabilityDisablesVendorNativeTools 判断管理员是否显式关闭厂商默认原生工具注入。
// 声明方式（二选一）：{"disableVendorNativeTools": true} 或 {"vendorNativeTools": false}。
func capabilityDisablesVendorNativeTools(raw string) bool {
	value := strings.TrimSpace(raw)
	if value == "" {
		return false
	}
	var payload struct {
		DisableVendorNativeTools *bool `json:"disableVendorNativeTools"`
		VendorNativeTools        *bool `json:"vendorNativeTools"`
	}
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return false
	}
	if payload.DisableVendorNativeTools != nil && *payload.DisableVendorNativeTools {
		return true
	}
	return payload.VendorNativeTools != nil && !*payload.VendorNativeTools
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
