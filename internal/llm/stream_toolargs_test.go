package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStreamFuncArgumentsString(t *testing.T) {
	var f streamFunc
	if err := json.Unmarshal([]byte(`{"name":"run_command","arguments":"{\"argv\":[\"echo\"]}"}`), &f); err != nil {
		t.Fatal(err)
	}
	if f.Name != "run_command" || !strings.Contains(f.Arguments, "echo") {
		t.Fatalf("%+v", f)
	}
}

func TestStreamFuncArgumentsObject(t *testing.T) {
	var f streamFunc
	raw := `{"name":"read_file","arguments":{"path":"a.txt"}}`
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		t.Fatal(err)
	}
	if f.Name != "read_file" {
		t.Fatal(f.Name)
	}
	if !strings.Contains(f.Arguments, `"path"`) || !strings.Contains(f.Arguments, "a.txt") {
		t.Fatalf("args=%s", f.Arguments)
	}
}

func TestReadOpenAIChatStream_toolCallObjectArgs(t *testing.T) {
	sse := "" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":{\"path\":\"x.csv\"}}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	content, _, tcs, fr, _, err := ReadOpenAIChatStream(strings.NewReader(sse), nil)
	if err != nil {
		t.Fatal(err)
	}
	if content != "" || fr != "tool_calls" {
		t.Fatalf("content=%q fr=%s", content, fr)
	}
	if len(tcs) != 1 || tcs[0].Function.Name != "read_file" {
		t.Fatalf("%+v", tcs)
	}
	if !strings.Contains(tcs[0].Function.Arguments, "x.csv") {
		t.Fatalf("args=%s", tcs[0].Function.Arguments)
	}
}
