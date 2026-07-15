package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/crestenstclair/crest-spec/internal/config"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpecToolHelpersRejectInvalidArgumentsBeforeCallingSpec(t *testing.T) {
	type arguments struct {
		Value string `json:"value"`
	}
	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "malformed", raw: json.RawMessage(`{"value":`), want: "unexpected EOF"},
		{name: "non object", raw: json.RawMessage(`[]`), want: "JSON object"},
		{name: "null", raw: json.RawMessage(`null`), want: "JSON object"},
		{name: "unknown field", raw: json.RawMessage(`{"other":"value"}`), want: `unknown field "other"`},
		{name: "wrong type", raw: json.RawMessage(`{"value":42}`), want: "cannot unmarshal number"},
		{name: "multiple values", raw: json.RawMessage(`{"value":"one"} {"value":"two"}`), want: "more than one JSON value"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			queryCalls := 0
			query := specTool("query", func(context.Context, arguments) (any, error) {
				queryCalls++
				return nil, nil
			})
			queryResult := query(context.Background(), test.raw)
			require.True(t, queryResult.IsError)
			assert.Contains(t, queryResult.Content[0].Text, "invalid arguments:")
			assert.Contains(t, queryResult.Content[0].Text, test.want)
			assert.Zero(t, queryCalls)

			commandCalls := 0
			command := specToolErr("command", map[string]bool{"ok": true}, func(context.Context, arguments) error {
				commandCalls++
				return nil
			})
			commandResult := command(context.Background(), test.raw)
			require.True(t, commandResult.IsError)
			assert.Equal(t, queryResult.Content[0].Text, commandResult.Content[0].Text)
			assert.Zero(t, commandCalls)
		})
	}
}

func TestSpecToolHelpersPreserveEmptyObjectCompatibility(t *testing.T) {
	called := false
	handler := specTool("empty", func(context.Context, struct{}) (any, error) {
		called = true
		return map[string]bool{"ok": true}, nil
	})

	result := handler(context.Background(), nil)
	require.False(t, result.IsError)
	assert.True(t, called)
}

func TestRegisteredToolsRejectMalformedAndUnknownArguments(t *testing.T) {
	server := New(&fakeSpec{}, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "malformed", raw: json.RawMessage(`{`), want: "invalid arguments:"},
		{name: "array", raw: json.RawMessage(`[]`), want: "JSON object"},
		{name: "null", raw: json.RawMessage(`null`), want: "JSON object"},
		{name: "multiple", raw: json.RawMessage(`{} {}`), want: "more than one JSON value"},
		{name: "unknown", raw: json.RawMessage(`{"__unexpected":true}`), want: `unknown field "__unexpected"`},
	}

	for _, definition := range server.tools {
		definition := definition
		for _, test := range tests {
			test := test
			t.Run(definition.Name+"/"+test.name, func(t *testing.T) {
				result := server.toolFns[definition.Name](context.Background(), test.raw)
				require.True(t, result.IsError)
				assert.Contains(t, result.Content[0].Text, test.want)
			})
		}
	}
}

func TestStubAndLiveToolCatalogsAreIdentical(t *testing.T) {
	live := New(&fakeSpec{}, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})
	stub := New(nil, strings.NewReader(""), io.Discard, zerolog.Nop(), &config.Config{})

	require.Equal(t, live.tools, stub.tools, "stub mode must use the canonical live tool descriptors")
	seen := make(map[string]struct{}, len(live.tools))
	for _, definition := range live.tools {
		assert.NotEmpty(t, definition.Name)
		assert.NotEmpty(t, definition.Description)
		assert.True(t, json.Valid(definition.InputSchema), "tool %q has an invalid schema", definition.Name)
		assert.NotNil(t, live.toolFns[definition.Name], "tool %q has no live handler", definition.Name)
		assert.NotNil(t, stub.toolFns[definition.Name], "tool %q has no stub handler", definition.Name)
		_, duplicate := seen[definition.Name]
		assert.False(t, duplicate, "tool %q is registered more than once", definition.Name)
		seen[definition.Name] = struct{}{}
	}
}
