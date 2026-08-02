package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// SecretRedacted 是展示用脱敏占位；写回时若仍为此值则保留磁盘原值。
const SecretRedacted = "***hidden***"

// IsRedactedSecret 判断客户端是否表示「密钥未改」。
func IsRedactedSecret(s string) bool {
	switch s {
	case "", SecretRedacted, "***":
		return true
	default:
		return false
	}
}

// AppConfigKnownTopKeys 是 AppConfig 会占用的顶层 JSON 键（保存时覆盖这些键，其余保留）。
func AppConfigKnownTopKeys() []string {
	return []string{
		"brain", "llm", "server", "evolution", "exec",
		"workspace_files", "subagent", "chat", "mcp",
	}
}

// SplitAppConfigDocument 将 config.json 拆成已识别结构体与未知顶层键。
func SplitAppConfigDocument(data []byte) (cfg AppConfig, extras map[string]json.RawMessage, err error) {
	extras = map[string]json.RawMessage{}
	if len(bytes.TrimSpace(data)) == 0 {
		return cfg, extras, nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return cfg, nil, fmt.Errorf("parse config: %w", err)
	}
	known := map[string]struct{}{}
	for _, k := range AppConfigKnownTopKeys() {
		known[k] = struct{}{}
	}
	typed := map[string]json.RawMessage{}
	for k, v := range doc {
		if _, ok := known[k]; ok {
			typed[k] = v
		} else {
			extras[k] = v
		}
	}
	tb, err := json.Marshal(typed)
	if err != nil {
		return cfg, nil, err
	}
	if err := json.Unmarshal(tb, &cfg); err != nil {
		return cfg, nil, fmt.Errorf("parse known config: %w", err)
	}
	return cfg, extras, nil
}

// LoadAppConfigDocument 读盘并拆分；文件不存在时返回空文档。
func LoadAppConfigDocument() (cfg AppConfig, extras map[string]json.RawMessage, path string, err error) {
	path = getConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return AppConfig{}, map[string]json.RawMessage{}, path, nil
		}
		return AppConfig{}, nil, path, err
	}
	cfg, extras, err = SplitAppConfigDocument(data)
	return cfg, extras, path, err
}

// MergeAppConfigDocument 将 typed 字段写入文档，保留 extras 中的未知顶层键。
// extras 为 nil 时保留磁盘已有未知键；非 nil（含空 map）则以 extras 替换全部未知键。
func MergeAppConfigDocument(cfg *AppConfig, extras map[string]json.RawMessage) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	doc := map[string]json.RawMessage{}
	if data, err := os.ReadFile(getConfigPath()); err == nil {
		_ = json.Unmarshal(data, &doc)
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}

	known := map[string]struct{}{}
	for _, k := range AppConfigKnownTopKeys() {
		known[k] = struct{}{}
		delete(doc, k) // 先清已知键，避免 omitempty 导致无法清空旧值
	}

	typed, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var typedMap map[string]json.RawMessage
	if err := json.Unmarshal(typed, &typedMap); err != nil {
		return nil, err
	}
	for k, v := range typedMap {
		doc[k] = v
	}

	if extras != nil {
		for k := range doc {
			if _, ok := known[k]; !ok {
				delete(doc, k)
			}
		}
		for k, v := range extras {
			if _, ok := known[k]; ok {
				continue // 不允许 extras 覆盖已知节
			}
			doc[k] = v
		}
	}

	return marshalStableJSON(doc)
}

// SaveConfig 保存配置文件；合并写回，保留 config.json 中未知顶层键（如 llm_previous_qwen）。
func SaveConfig(config *AppConfig) error {
	configPath := getConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := MergeAppConfigDocument(config, nil)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return os.WriteFile(configPath, data, 0644)
}

// SaveAppConfigDocument 写回已知配置 + 显式 extras（用于设置页）。
func SaveAppConfigDocument(cfg *AppConfig, extras map[string]json.RawMessage) error {
	configPath := getConfigPath()
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	if extras == nil {
		extras = map[string]json.RawMessage{}
	}
	data, err := MergeAppConfigDocument(cfg, extras)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return os.WriteFile(configPath, data, 0644)
}

// ApplySecretPreserve 若 newVal 为脱敏占位则保留 oldVal。
func ApplySecretPreserve(newVal, oldVal string) string {
	if IsRedactedSecret(newVal) {
		return oldVal
	}
	return newVal
}

func marshalStableJSON(doc map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, k := range keys {
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.WriteString("  ")
		buf.Write(kb)
		buf.WriteString(": ")
		raw := bytes.TrimSpace(doc[k])
		if len(raw) == 0 {
			raw = []byte("null")
		}
		// 对对象/数组做缩进美化；标量保持原样
		var pretty bytes.Buffer
		if json.Indent(&pretty, raw, "  ", "  ") == nil {
			buf.Write(pretty.Bytes())
		} else {
			buf.Write(raw)
		}
		if i < len(keys)-1 {
			buf.WriteString(",\n")
		} else {
			buf.WriteByte('\n')
		}
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}
