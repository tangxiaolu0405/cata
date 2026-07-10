package client

import (
	"strings"
	"testing"
)

func TestAppendAgentsSidebarSection(t *testing.T) {
	m := &model{
		streaming: true,
		stats: paneStats{
			promptProfile: "task",
			state:         "model round 2",
		},
		subagents: []subagentRecord{
			{ID: "sub-1", Profile: "minimal", Status: "running", Round: 1},
		},
		width: sidebarActivateWidth,
	}
	text := m.sidebarText()
	if !strings.Contains(text, "主agent") || !strings.Contains(text, "子agent") || !strings.Contains(text, "sub-1") {
		t.Fatalf("sidebar=%q", text)
	}
}

func TestFinishSubagentRemovesFromSidebar(t *testing.T) {
	m := &model{
		width: sidebarActivateWidth,
		subagents: []subagentRecord{
			{ID: "sub-1", Profile: "minimal", Status: "running"},
		},
	}
	m.finishSubagent("sub-1", true, "ok")
	if len(m.subagents) != 0 {
		t.Fatalf("expected removed, got %d", len(m.subagents))
	}
}

func TestSubagentIDFromSidebarLine(t *testing.T) {
	id := subagentIDFromSidebarLine("\t子agent  minimal  r2  sub-3")
	if id != "sub-3" {
		t.Fatalf("got %q", id)
	}
}
