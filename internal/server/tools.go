package server

import (
	"context"
	"fmt"
	"net"
	"sort"

	"cata/internal/llm"
)

// Tool is the interface all built-in tools implement.
type Tool interface {
	Name() string
	Schema() llm.Tool
	Execute(ctx context.Context, conn net.Conn, argsJSON string) (string, error)
}

// ToolRegistry holds registered built-in tools.
type ToolRegistry struct {
	tools map[string]Tool
	order []string // preserve insertion order for deterministic schemas
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}}
}

func (r *ToolRegistry) Register(t Tool) {
	name := t.Name()
	if _, ok := r.tools[name]; !ok {
		r.order = append(r.order, name)
	}
	r.tools[name] = t
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Schemas returns tool schemas in registration order for LLM context.
func (r *ToolRegistry) Schemas() []llm.Tool {
	out := make([]llm.Tool, 0, len(r.tools))
	for _, name := range r.order {
		out = append(out, r.tools[name].Schema())
	}
	return out
}

// Names returns all registered tool names.
func (r *ToolRegistry) Names() []string {
	names := make([]string, len(r.order))
	copy(names, r.order)
	sort.Strings(names)
	return names
}

// Dispatch finds and executes a tool by name. Returns error for unknown tools.
func (r *ToolRegistry) Dispatch(ctx context.Context, conn net.Conn, name, argsJSON string) (string, error) {
	t, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	return t.Execute(ctx, conn, argsJSON)
}
