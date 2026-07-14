package evolve

import "testing"

func TestParseDecision(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantAct string
		wantErr bool
	}{
		{
			name:    "plain json",
			raw:     `{"action":"idle","reason":"ok","learning":"","updates":[]}`,
			wantAct: "idle",
		},
		{
			name: "markdown fence",
			raw: "```json\n{\"action\":\"consolidate\",\"reason\":\"x\",\"learning\":\"y\",\"updates\":[]}\n```",
			wantAct: "consolidate",
		},
		{
			name: "prose prefix",
			raw:  "Here is the decision:\n{\"action\":\"idle\",\"reason\":\"noop\",\"updates\":[]}",
			wantAct: "idle",
		},
		{
			name:    "empty action defaults idle",
			raw:     `{"reason":"x","updates":[]}`,
			wantAct: "idle",
		},
		{
			name:    "no json",
			raw:     "just text",
			wantErr: true,
		},
		{
			name:    "truncated mid-object",
			raw:     `{"action":"update","reason":"long text without end`,
			wantErr: true,
		},
		{
			name: "nested incomplete must not parse",
			raw: `{"action":"update","updates":[{"path":"a.md","content":"x"}`,
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := parseDecision(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if d.Action != tc.wantAct {
				t.Fatalf("action=%q want %q", d.Action, tc.wantAct)
			}
		})
	}
}
