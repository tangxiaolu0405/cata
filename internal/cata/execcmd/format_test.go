package execcmd

import "testing"

func TestFormatLine(t *testing.T) {
	cases := []struct {
		argv []string
		want string
	}{
		{nil, ""},
		{[]string{"git", "status"}, "git status"},
		{[]string{"echo", "hello world"}, `echo "hello world"`},
		{[]string{"echo", ""}, `echo ""`},
		{[]string{"ls", "-la", "/tmp/a b"}, `ls -la "/tmp/a b"`},
		{[]string{`grep`, `a"b`}, `grep "a\"b"`},
		{[]string{"sh", "-c", `echo 'x'`}, `sh -c "echo 'x'"`},
	}
	for _, c := range cases {
		if got := FormatLine(c.argv); got != c.want {
			t.Fatalf("FormatLine(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
}
