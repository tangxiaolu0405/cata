package server

import (
	"testing"

	"cata/internal/llm"
)

func TestPartitionChatToolBatches(t *testing.T) {
	calls := []llm.ToolCall{
		{ID: "1", Function: llm.ToolCallFunction{Name: "read_file"}},
		{ID: "2", Function: llm.ToolCallFunction{Name: "list_files"}},
		{ID: "3", Function: llm.ToolCallFunction{Name: "run_command"}},
		{ID: "4", Function: llm.ToolCallFunction{Name: "delegate_task"}},
		{ID: "5", Function: llm.ToolCallFunction{Name: "read_file"}},
	}
	batches := partitionChatToolBatches(calls)
	if len(batches) != 3 {
		t.Fatalf("batches=%d want 3", len(batches))
	}
	if !batches[0].parallel || len(batches[0].calls) != 2 {
		t.Fatalf("batch0: parallel=%v len=%d", batches[0].parallel, len(batches[0].calls))
	}
	if batches[1].parallel || batches[1].calls[0].Function.Name != "run_command" {
		t.Fatalf("batch1: %+v", batches[1])
	}
	if !batches[2].parallel || len(batches[2].calls) != 2 {
		t.Fatalf("batch2: parallel=%v len=%d", batches[2].parallel, len(batches[2].calls))
	}
}

func TestChatToolParallelSafe(t *testing.T) {
	for _, name := range []string{"read_file", "read_skill", "list_files", "delegate_task"} {
		if !chatToolParallelSafe(name) {
			t.Fatalf("%s should be parallel", name)
		}
	}
	for _, name := range []string{"run_command", "ask_user", "delegate_wait", "search_replace"} {
		if chatToolParallelSafe(name) {
			t.Fatalf("%s should be sequential", name)
		}
	}
}

func TestPartitionSingleParallelTool(t *testing.T) {
	calls := []llm.ToolCall{{ID: "1", Function: llm.ToolCallFunction{Name: "read_file"}}}
	batches := partitionChatToolBatches(calls)
	if len(batches) != 1 || len(batches[0].calls) != 1 {
		t.Fatalf("single read: %+v", batches)
	}
}

func TestManageMCPToolSequential(t *testing.T) {
	if chatToolParallelSafe("manage_mcp") {
		t.Fatal("manage_mcp should be sequential (mutates config + reloads MCP)")
	}
}
