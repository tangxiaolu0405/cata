package llm

import (
	"encoding/json"
	"testing"
)

func TestNormalizeToolArguments_literalNewlinesInString(t *testing.T) {
	// DeepSeek 等：arguments 解成 Go string 后，JSON 字符串值内常含裸换行。
	raw := "{\"argv\":[\"wsl.exe\",\"-e\",\"bash\",\"-lc\",\"cd /mnt/d/stock && python3 -c \\\"\nimport csv\nprint(1)\n\\\"\"]}"
	if json.Valid([]byte(raw)) {
		t.Fatal("fixture should be invalid JSON before repair")
	}
	norm := NormalizeToolArguments("run_command", raw)
	if norm == "" {
		t.Fatal("expected repair")
	}
	if !json.Valid([]byte(norm)) {
		t.Fatalf("still invalid: %q", norm)
	}
	var p struct {
		Argv []string `json:"argv"`
	}
	if err := json.Unmarshal([]byte(norm), &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Argv) != 5 || p.Argv[0] != "wsl.exe" {
		t.Fatalf("%+v", p)
	}
	if !json.Valid([]byte(NormalizeToolArguments("run_command", norm))) {
		t.Fatal("idempotent")
	}
}

func TestNormalizeToolCalls_keepsRepairedRunCommand(t *testing.T) {
	raw := "{\"argv\":[\"wsl.exe\",\"-e\",\"bash\",\"-lc\",\"echo hi\n\"]}"
	calls := NormalizeToolCalls([]ToolCall{{
		ID:   "c1",
		Type: "function",
		Function: ToolCallFunction{
			Name:      "run_command",
			Arguments: raw,
		},
	}})
	if len(calls) != 1 {
		t.Fatalf("dropped: %d", len(calls))
	}
}

func TestToolCallFunctionUnmarshalObjectArgs(t *testing.T) {
	var tc ToolCall
	body := []byte(`{"id":"1","type":"function","function":{"name":"run_command","arguments":{"argv":["echo","hi"]}}}`)
	if err := json.Unmarshal(body, &tc); err != nil {
		t.Fatal(err)
	}
	if tc.Function.Name != "run_command" {
		t.Fatal(tc.Function.Name)
	}
	if !json.Valid([]byte(tc.Function.Arguments)) {
		t.Fatalf("args=%s", tc.Function.Arguments)
	}
}
