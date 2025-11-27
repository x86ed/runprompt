package main

import (
	"testing"
)

func TestBasicInterpolation(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"simple variable", "Hello {{name}}!", map[string]interface{}{"name": "World"}, "Hello World!"},
		{"multiple variables", "{{a}} and {{b}}", map[string]interface{}{"a": "X", "b": "Y"}, "X and Y"},
		{"missing variable", "Hello {{name}}!", map[string]interface{}{}, "Hello !"},
		{"variable with spaces", "{{ name }}", map[string]interface{}{"name": "World"}, "World"},
		{"number variable", "Count: {{n}}", map[string]interface{}{"n": 42}, "Count: 42"},
		{"empty template", "", map[string]interface{}{"name": "World"}, ""},
		{"no variables", "Hello World!", map[string]interface{}{"name": "Test"}, "Hello World!"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestDotNotation(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"dot notation", "{{person.name}}", map[string]interface{}{"person": map[string]interface{}{"name": "Alice"}}, "Alice"},
		{"deep dot notation", "{{a.b.c}}", map[string]interface{}{"a": map[string]interface{}{"b": map[string]interface{}{"c": "deep"}}}, "deep"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestSections(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"section truthy", "{{#show}}yes{{/show}}", map[string]interface{}{"show": true}, "yes"},
		{"section falsy", "{{#show}}yes{{/show}}", map[string]interface{}{"show": false}, ""},
		{"section missing", "{{#show}}yes{{/show}}", map[string]interface{}{}, ""},
		{"section with string", "{{#name}}Hello {{name}}{{/name}}", map[string]interface{}{"name": "World"}, "Hello World"},
		{"section empty string", "{{#name}}yes{{/name}}", map[string]interface{}{"name": ""}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestSectionLists(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"section list", "{{#items}}{{.}}{{/items}}", map[string]interface{}{"items": []interface{}{"a", "b", "c"}}, "abc"},
		{"section list objects", "{{#people}}{{name}} {{/people}}",
			map[string]interface{}{"people": []interface{}{map[string]interface{}{"name": "Alice"}, map[string]interface{}{"name": "Bob"}}}, "Alice Bob "},
		{"section empty list", "{{#items}}x{{/items}}", map[string]interface{}{"items": []interface{}{}}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestInvertedSections(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"inverted truthy", "{{^show}}yes{{/show}}", map[string]interface{}{"show": true}, ""},
		{"inverted falsy", "{{^show}}yes{{/show}}", map[string]interface{}{"show": false}, "yes"},
		{"inverted missing", "{{^show}}yes{{/show}}", map[string]interface{}{}, "yes"},
		{"inverted empty list", "{{^items}}none{{/items}}", map[string]interface{}{"items": []interface{}{}}, "none"},
		{"inverted non-empty list", "{{^items}}none{{/items}}", map[string]interface{}{"items": []interface{}{1}}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestCombined(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"section and inverted", "{{#items}}have{{/items}}{{^items}}none{{/items}}",
			map[string]interface{}{"items": []interface{}{}}, "none"},
		{"section and inverted with items", "{{#items}}have{{/items}}{{^items}}none{{/items}}",
			map[string]interface{}{"items": []interface{}{1}}, "have"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestComments(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"simple comment", "Hello {{! this is a comment }}World", map[string]interface{}{}, "Hello World"},
		{"comment removes entirely", "{{! comment }}", map[string]interface{}{}, ""},
		{"comment with variable", "{{! ignore }}{{name}}", map[string]interface{}{"name": "Alice"}, "Alice"},
		{"multiline comment", "Hello {{! this\nis\nmultiline }}World", map[string]interface{}{}, "Hello World"},
		{"comment between variables", "{{a}}{{! middle }}{{b}}", map[string]interface{}{"a": "X", "b": "Y"}, "XY"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestLoopVariables(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"@index", "{{#items}}{{@index}}{{/items}}", map[string]interface{}{"items": []interface{}{"a", "b", "c"}}, "012"},
		{"@index with value", "{{#items}}{{@index}}:{{.}} {{/items}}",
			map[string]interface{}{"items": []interface{}{"a", "b", "c"}}, "0:a 1:b 2:c "},
		{"@first", "{{#items}}{{#@first}}first{{/@first}}{{.}}{{/items}}",
			map[string]interface{}{"items": []interface{}{"a", "b", "c"}}, "firstabc"},
		{"@last", "{{#items}}{{.}}{{#@last}}!{{/@last}}{{/items}}",
			map[string]interface{}{"items": []interface{}{"a", "b", "c"}}, "abc!"},
		{"@index with objects", "{{#people}}{{@index}}:{{name}} {{/people}}",
			map[string]interface{}{"people": []interface{}{map[string]interface{}{"name": "Alice"}, map[string]interface{}{"name": "Bob"}}}, "0:Alice 1:Bob "},
		{"@first @last single item", "{{#items}}{{#@first}}F{{/@first}}{{#@last}}L{{/@last}}{{/items}}",
			map[string]interface{}{"items": []interface{}{"x"}}, "FL"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestEachHelper(t *testing.T) {
	tests := []struct {
		name      string
		template  string
		variables map[string]interface{}
		expected  string
	}{
		{"each list", "{{#each items}}{{.}}{{/each}}",
			map[string]interface{}{"items": []interface{}{"a", "b", "c"}}, "abc"},
		{"each list with @index", "{{#each items}}{{@index}}:{{.}} {{/each}}",
			map[string]interface{}{"items": []interface{}{"a", "b", "c"}}, "0:a 1:b 2:c "},
		{"each list objects", "{{#each people}}{{name}} {{/each}}",
			map[string]interface{}{"people": []interface{}{map[string]interface{}{"name": "Alice"}, map[string]interface{}{"name": "Bob"}}}, "Alice Bob "},
		{"each empty list", "{{#each items}}x{{/each}}", map[string]interface{}{"items": []interface{}{}}, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := renderTemplate(tc.template, tc.variables)
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestYAMLParsing(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		expected map[string]interface{}
	}{
		{"simple key-value", "model: test", map[string]interface{}{"model": "test"}},
		{"boolean true", "enabled: true", map[string]interface{}{"enabled": true}},
		{"boolean false", "enabled: false", map[string]interface{}{"enabled": false}},
		{"integer", "count: 42", map[string]interface{}{"count": 42}},
		{"float", "rate: 3.14", map[string]interface{}{"rate": 3.14}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := parseYAML(tc.yaml)
			for k, expectedVal := range tc.expected {
				if result[k] != expectedVal {
					t.Errorf("For key %q: Expected %v, got %v", k, expectedVal, result[k])
				}
			}
		})
	}
}

func TestParseModelString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		provider string
		model    string
	}{
		{"test mode", "test", "test", ""},
		{"with provider", "anthropic/claude-3", "anthropic", "claude-3"},
		{"without provider", "gpt-4", "", "gpt-4"},
		{"openrouter style", "openrouter/anthropic/claude-3", "openrouter", "anthropic/claude-3"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, model := parseModelString(tc.input)
			if provider != tc.provider {
				t.Errorf("Provider: Expected %q, got %q", tc.provider, provider)
			}
			if model != tc.model {
				t.Errorf("Model: Expected %q, got %q", tc.model, model)
			}
		})
	}
}
