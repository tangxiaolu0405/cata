package llm

import (
	"encoding/json"
	"testing"
)

func TestParseEmbeddedToolCalls_FunctionEqDialect(t *testing.T) {
	content := "先列一下目录\n\n<tool_call>\n<function=list_files>\n<parameter=path>\n\n</parameter>\n</function>\n</tool_call>\n"
	calls, stripped := ParseEmbeddedToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("calls=%d want 1; stripped=%q", len(calls), stripped)
	}
	if calls[0].Function.Name != "list_files" {
		t.Fatalf("name=%q", calls[0].Function.Name)
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatalf("args json: %v (%q)", err, calls[0].Function.Arguments)
	}
	if path, _ := args["path"].(string); path != "" {
		t.Fatalf("path=%q want empty", path)
	}
	if stripped != "先列一下目录" {
		t.Fatalf("stripped=%q", stripped)
	}
}

func TestParseEmbeddedToolCalls_FunctionEqWithPath(t *testing.T) {
	content := `<tool_call>
<function=read_file>
<parameter=path>
internal/llm/embedded_tool.go
</parameter>
</function>
</tool_call>`
	calls, _ := ParseEmbeddedToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["path"] != "internal/llm/embedded_tool.go" {
		t.Fatalf("path=%q", args["path"])
	}
}

func TestParseEmbeddedToolCalls_BareFunctionEq(t *testing.T) {
	content := `<function=list_files>
<parameter=path>.</parameter>
</function>`
	calls, stripped := ParseEmbeddedToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	if calls[0].Function.Name != "list_files" {
		t.Fatalf("name=%q", calls[0].Function.Name)
	}
	if stripped != "" {
		t.Fatalf("stripped=%q", stripped)
	}
	var args map[string]string
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if args["path"] != "." {
		t.Fatalf("path=%q", args["path"])
	}
}

func TestParseEmbeddedToolCalls_FunctionEqArgv(t *testing.T) {
	content := `<tool_call>
<function=run_command>
<parameter=argv>["ls","-la"]</parameter>
</function>
</tool_call>`
	calls, _ := ParseEmbeddedToolCalls(content)
	if len(calls) != 1 {
		t.Fatalf("calls=%d", len(calls))
	}
	var args map[string][]string
	if err := json.Unmarshal([]byte(calls[0].Function.Arguments), &args); err != nil {
		t.Fatal(err)
	}
	if len(args["argv"]) != 2 || args["argv"][0] != "ls" || args["argv"][1] != "-la" {
		t.Fatalf("argv=%v", args["argv"])
	}
}

func TestParseEmbeddedToolCalls_LegacyToolNameDropped(t *testing.T) {
	content := `<tool_call><tool name="list_files"><param name="path">.</param></tool></tool_call>`
	calls, _ := ParseEmbeddedToolCalls(content)
	if len(calls) != 0 {
		t.Fatalf("legacy <tool name> dialect should be ignored, got %+v", calls)
	}
}
