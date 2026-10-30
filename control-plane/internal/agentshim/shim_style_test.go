package agentshim

import "testing"

func TestPickLLMStyle(t *testing.T) {
	cases := []struct {
		styles []string
		want   string
	}{
		{nil, "openai"},
		{[]string{"openai"}, "openai"},
		{[]string{"anthropic"}, "anthropic"},
		{[]string{"anthropic", "openai"}, "openai"},
	}
	for _, c := range cases {
		if got := PickLLMStyle(c.styles); got != c.want {
			t.Errorf("PickLLMStyle(%v) = %q, want %q", c.styles, got, c.want)
		}
	}
}
