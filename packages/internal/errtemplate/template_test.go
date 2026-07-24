package errtemplate

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromPattern(t *testing.T) {
	parsed, err := FromPattern("used %v%% of %v", []string{"percent", "quota"})
	require.NoError(t, err)
	assert.Equal(t, "used {{.percent}}% of {{.quota}}", parsed.Text())
	assert.Equal(t, []string{"percent", "quota"}, parsed.Slots())

	rendered, missing := parsed.Render(map[string]any{"percent": 80}, "UNKNOWN")
	assert.Equal(t, "used 80% of UNKNOWN", rendered)
	assert.Equal(t, []string{"quota"}, missing)
}

func TestFromPatternRejectsInvalidGrammar(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		names   []string
	}{
		{name: "unsupported verb", pattern: "%s", names: []string{"value"}},
		{name: "dangling percent", pattern: "value %"},
		{name: "too few names", pattern: "%v"},
		{name: "too many names", pattern: "value", names: []string{"value"}},
		{name: "invalid name", pattern: "%v", names: []string{"UserID"}},
		{name: "duplicate name", pattern: "%v %v", names: []string{"value", "value"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := FromPattern(test.pattern, test.names)
			require.Error(t, err)
		})
	}
}

func TestParseOnlyAcceptsSimpleNamedSlots(t *testing.T) {
	valid, err := Parse("user {{.user_id}} in {{.org}}")
	require.NoError(t, err)
	assert.Equal(t, []string{"user_id", "org"}, valid.Slots())

	for _, invalid := range []string{
		"{{ .user_id }}",
		"{{if .user_id}}yes{{end}}",
		"{{.user_id | printf}}",
		"{{.UserID}}",
		"unclosed {{.user_id",
		"unexpected }}",
	} {
		t.Run(invalid, func(t *testing.T) {
			_, err := Parse(invalid)
			require.Error(t, err)
		})
	}
}
