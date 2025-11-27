package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Provider configuration
type Provider struct {
	URL string
	Env string
}

var providers = map[string]Provider{
	"openrouter": {
		URL: "https://openrouter.ai/api/v1/chat/completions",
		Env: "OPENROUTER_API_KEY",
	},
	"googleai": {
		URL: "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions",
		Env: "GOOGLE_API_KEY",
	},
	"anthropic": {
		URL: "https://api.anthropic.com/v1/messages",
		Env: "ANTHROPIC_API_KEY",
	},
	"openai": {
		URL: "https://api.openai.com/v1/chat/completions",
		Env: "OPENAI_API_KEY",
	},
}

const (
	red     = "\033[31m"
	reset   = "\033[0m"
	timeout = 120 * time.Second
)

var verbose = false
var promptPath = ""

func log(msg string) {
	if verbose {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// parsePromptFile reads and parses a .prompt file
func parsePromptFile(path string) (map[string]interface{}, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}

	contentStr := string(content)
	if !strings.HasPrefix(contentStr, "---") {
		return map[string]interface{}{}, strings.TrimSpace(contentStr), nil
	}

	parts := strings.SplitN(contentStr, "---", 3)
	if len(parts) < 3 {
		return map[string]interface{}{}, strings.TrimSpace(contentStr), nil
	}

	metaStr := strings.TrimSpace(parts[1])
	template := strings.TrimSpace(parts[2])
	meta := parseYAML(metaStr)

	return meta, template, nil
}

// parseYAML is a simple YAML parser for frontmatter
func parseYAML(s string) map[string]interface{} {
	result := make(map[string]interface{})
	type stackItem struct {
		obj    map[string]interface{}
		indent int
	}
	stack := []stackItem{{result, -1}}

	lines := strings.Split(s, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		// Pop stack while indent <= top indent
		for len(stack) > 1 && indent <= stack[len(stack)-1].indent {
			stack = stack[:len(stack)-1]
		}

		// Match key: value
		re := regexp.MustCompile(`^(\s*)([^:]+):\s*(.*)`)
		match := re.FindStringSubmatch(line)
		if match == nil {
			continue
		}

		key := strings.TrimSpace(match[2])
		value := strings.TrimSpace(match[3])
		parent := stack[len(stack)-1].obj

		if value != "" {
			parent[key] = parseYAMLValue(value)
		} else {
			newMap := make(map[string]interface{})
			parent[key] = newMap
			stack = append(stack, stackItem{newMap, indent})
		}
	}

	return result
}

// parseYAMLValue parses a YAML value string
func parseYAMLValue(s string) interface{} {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if strings.ToLower(s) == "true" {
		return true
	}
	if strings.ToLower(s) == "false" {
		return false
	}
	// Integer
	if matched, _ := regexp.MatchString(`^-?\d+$`, s); matched {
		if i, err := strconv.Atoi(s); err == nil {
			return i
		}
	}
	// Float
	if matched, _ := regexp.MatchString(`^-?\d+\.\d+$`, s); matched {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	// Try JSON or nested YAML
	if strings.Contains(s, "\n") || strings.HasPrefix(s, "{") {
		var jsonVal interface{}
		if err := json.Unmarshal([]byte(s), &jsonVal); err == nil {
			return jsonVal
		}
		parsed := parseYAML(s)
		if len(parsed) > 0 {
			return parsed
		}
	}
	return s
}

// renderTemplate renders a Handlebars-style template
func renderTemplate(template string, variables map[string]interface{}) string {
	return render(template, variables)
}

func lookup(name string, ctx map[string]interface{}) interface{} {
	name = strings.TrimSpace(name)
	if name == "." {
		if v, ok := ctx["."]; ok {
			return v
		}
		return ctx
	}
	// Handle @index, @first, @last, @key
	if strings.HasPrefix(name, "@") {
		if v, ok := ctx[name]; ok {
			return v
		}
		return ""
	}
	parts := strings.Split(name, ".")
	var current interface{} = ctx
	for _, part := range parts {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[part]
		} else {
			return ""
		}
	}
	if current == nil {
		return ""
	}
	return current
}

// findMatchingClose finds the closing tag for a section
func findMatchingClose(tmpl string, key string, openTag string, closeTag string) int {
	depth := 1
	pos := 0
	for depth > 0 && pos < len(tmpl) {
		nextOpen := strings.Index(tmpl[pos:], openTag)
		nextClose := strings.Index(tmpl[pos:], closeTag)

		if nextClose == -1 {
			return -1
		}

		if nextOpen != -1 && nextOpen < nextClose {
			depth++
			pos += nextOpen + len(openTag)
		} else {
			depth--
			if depth == 0 {
				return pos + nextClose
			}
			pos += nextClose + len(closeTag)
		}
	}
	return -1
}

// processSection finds and processes {{#key}}...{{/key}} or {{^key}}...{{/key}}
func processSection(tmpl string, ctx map[string]interface{}, inverted bool) string {
	var result strings.Builder
	pos := 0

	prefix := "{{#"
	if inverted {
		prefix = "{{^"
	}

	for pos < len(tmpl) {
		// Find next section start
		startIdx := strings.Index(tmpl[pos:], prefix)
		if startIdx == -1 {
			result.WriteString(tmpl[pos:])
			break
		}

		// Write content before section
		result.WriteString(tmpl[pos : pos+startIdx])
		pos += startIdx

		// Find the key
		keyStart := pos + len(prefix)
		keyEnd := strings.Index(tmpl[keyStart:], "}}")
		if keyEnd == -1 {
			result.WriteString(tmpl[pos:])
			break
		}
		key := strings.TrimSpace(tmpl[keyStart : keyStart+keyEnd])

		openTag := fmt.Sprintf("%s%s}}", prefix, key)
		closeTag := fmt.Sprintf("{{/%s}}", key)

		// Find the matching close tag
		innerStart := pos + len(openTag)
		closeIdx := findMatchingClose(tmpl[innerStart:], key, openTag, closeTag)
		if closeIdx == -1 {
			result.WriteString(tmpl[pos:])
			break
		}

		inner := tmpl[innerStart : innerStart+closeIdx]
		val := lookup(key, ctx)

		if inverted {
			// Inverted section - render if falsy
			switch v := val.(type) {
			case []interface{}:
				if len(v) == 0 {
					result.WriteString(render(inner, ctx))
				}
			case bool:
				if !v {
					result.WriteString(render(inner, ctx))
				}
			case string:
				if v == "" {
					result.WriteString(render(inner, ctx))
				}
			case nil:
				result.WriteString(render(inner, ctx))
			}
		} else {
			// Normal section
			switch v := val.(type) {
			case []interface{}:
				for i, item := range v {
					itemCtx := make(map[string]interface{})
					if m, ok := item.(map[string]interface{}); ok {
						for k, val := range m {
							itemCtx[k] = val
						}
					} else {
						itemCtx["_value"] = item
					}
					itemCtx["@index"] = i
					itemCtx["@first"] = i == 0
					itemCtx["@last"] = i == len(v)-1
					itemCtx["."] = item
					result.WriteString(render(inner, itemCtx))
				}
			case bool:
				if v {
					result.WriteString(render(inner, ctx))
				}
			case string:
				if v != "" {
					result.WriteString(render(inner, ctx))
				}
			case int, int64, float64:
				result.WriteString(render(inner, ctx))
			case map[string]interface{}:
				result.WriteString(render(inner, v))
			case nil:
				// Don't render
			default:
				if val != nil {
					result.WriteString(render(inner, ctx))
				}
			}
		}

		pos = innerStart + closeIdx + len(closeTag)
	}

	return result.String()
}

// processEach finds and processes {{#each key}}...{{/each}}
func processEach(tmpl string, ctx map[string]interface{}) string {
	eachRe := regexp.MustCompile(`\{\{#each\s+(\w+)\}\}`)
	var result strings.Builder
	pos := 0

	for pos < len(tmpl) {
		loc := eachRe.FindStringIndex(tmpl[pos:])
		if loc == nil {
			result.WriteString(tmpl[pos:])
			break
		}

		result.WriteString(tmpl[pos : pos+loc[0]])

		match := eachRe.FindStringSubmatch(tmpl[pos:])
		if match == nil {
			result.WriteString(tmpl[pos:])
			break
		}
		key := match[1]

		closeTag := "{{/each}}"

		innerStart := pos + loc[1]
		closeIdx := strings.Index(tmpl[innerStart:], closeTag)
		if closeIdx == -1 {
			result.WriteString(tmpl[pos:])
			break
		}

		inner := tmpl[innerStart : innerStart+closeIdx]
		val := lookup(key, ctx)

		switch v := val.(type) {
		case []interface{}:
			for i, item := range v {
				itemCtx := make(map[string]interface{})
				if m, ok := item.(map[string]interface{}); ok {
					for k, val := range m {
						itemCtx[k] = val
					}
				}
				itemCtx["@index"] = i
				itemCtx["@first"] = i == 0
				itemCtx["@last"] = i == len(v)-1
				itemCtx["."] = item
				result.WriteString(render(inner, itemCtx))
			}
		case map[string]interface{}:
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			for i, k := range keys {
				item := v[k]
				itemCtx := make(map[string]interface{})
				if m, ok := item.(map[string]interface{}); ok {
					for key, val := range m {
						itemCtx[key] = val
					}
				}
				itemCtx["@key"] = k
				itemCtx["@index"] = i
				itemCtx["@first"] = i == 0
				itemCtx["@last"] = i == len(keys)-1
				itemCtx["."] = item
				result.WriteString(render(inner, itemCtx))
			}
		}

		pos = innerStart + closeIdx + len(closeTag)
	}

	return result.String()
}

func render(tmpl string, ctx map[string]interface{}) string {
	// Remove comments: {{! ... }}
	commentRe := regexp.MustCompile(`(?s)\{\{!.*?\}\}`)
	tmpl = commentRe.ReplaceAllString(tmpl, "")

	// Process {{#each key}}...{{/each}}
	tmpl = processEach(tmpl, ctx)

	// Process sections: {{#key}}...{{/key}}
	tmpl = processSection(tmpl, ctx, false)

	// Process inverted sections: {{^key}}...{{/key}}
	tmpl = processSection(tmpl, ctx, true)

	// Process variables
	varRe := regexp.MustCompile(`\{\{([^#^/}]+)\}\}`)
	tmpl = varRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		submatches := varRe.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		key := strings.TrimSpace(submatches[1])
		val := lookup(key, ctx)
		// Handle special "." lookup for non-dict items in lists
		if key == "." {
			if dotVal, ok := ctx["."]; ok {
				return fmt.Sprintf("%v", dotVal)
			}
		}
		return fmt.Sprintf("%v", val)
	})

	return tmpl
}

// parseModelString parses "provider/model" format
func parseModelString(modelStr string) (string, string) {
	if modelStr == "test" {
		return "test", ""
	}
	parts := strings.SplitN(modelStr, "/", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}

// getProviderConfig returns URL and API key for a provider
func getProviderConfig(provider string) (string, string) {
	config, ok := providers[provider]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown provider: %s\n", provider)
		os.Exit(1)
	}
	apiKey := os.Getenv(config.Env)
	if apiKey == "" {
		fmt.Fprintf(os.Stderr, "Missing API key: %s\n", config.Env)
		os.Exit(1)
	}
	return config.URL, apiKey
}

// buildSchemaTool builds a tool definition from output schema
func buildSchemaTool(schema map[string]interface{}) map[string]interface{} {
	properties := make(map[string]interface{})
	required := []string{}

	for key, value := range schema {
		cleanKey := strings.TrimSuffix(key, "?")
		isOptional := strings.HasSuffix(key, "?")

		var typeStr, description string
		if s, ok := value.(string); ok {
			parts := strings.SplitN(s, ",", 2)
			typeStr = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				description = strings.TrimSpace(parts[1])
			}
		} else {
			typeStr = "string"
		}

		jsonType := "string"
		switch typeStr {
		case "number":
			jsonType = "number"
		case "boolean":
			jsonType = "boolean"
		}

		prop := map[string]interface{}{"type": jsonType}
		if description != "" {
			prop["description"] = description
		}
		properties[cleanKey] = prop

		if !isOptional {
			required = append(required, cleanKey)
		}
	}

	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name":        "extract",
			"description": "Extract structured data",
			"parameters": map[string]interface{}{
				"type":       "object",
				"properties": properties,
				"required":   required,
			},
		},
	}
}

// extractErrorMessage extracts error message from API response
func extractErrorMessage(errorBody string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(errorBody), &data); err != nil {
		return errorBody
	}

	if errVal, ok := data["error"]; ok {
		switch e := errVal.(type) {
		case map[string]interface{}:
			errType, _ := e["type"].(string)
			message, _ := e["message"].(string)
			if errType != "" && message != "" {
				return fmt.Sprintf("%s: %s", errType, message)
			}
			if message != "" {
				return message
			}
			if errType != "" {
				return errType
			}
		case string:
			return e
		}
	}
	if message, ok := data["message"].(string); ok {
		return message
	}
	return errorBody
}

// loadTestResponse loads a .test-response file
func loadTestResponse(path string) map[string]interface{} {
	testFile := path + ".test-response"
	content, err := os.ReadFile(testFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Test response file not found: %s\n", testFile)
		os.Exit(1)
	}
	log(fmt.Sprintf("Loaded test response from: %s", testFile))

	var response map[string]interface{}
	if err := json.Unmarshal(content, &response); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing test response: %v\n", err)
		os.Exit(1)
	}
	return response
}

// saveResponse saves API response to file
func saveResponse(response map[string]interface{}, provider, savePath string) {
	responseWithProvider := map[string]interface{}{"_provider": provider}
	for k, v := range response {
		responseWithProvider[k] = v
	}

	data, _ := json.MarshalIndent(responseWithProvider, "", "  ")
	if err := os.WriteFile(savePath, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error saving response: %v\n", err)
	}
	log(fmt.Sprintf("Saved response to: %s", savePath))
}

// makeRequest makes an API request to the provider
func makeRequest(url, apiKey, model, prompt string, outputConfig map[string]interface{}, provider string) map[string]interface{} {
	client := &http.Client{Timeout: timeout}

	var body map[string]interface{}
	headers := map[string]string{
		"Content-Type": "application/json",
	}

	if provider == "anthropic" {
		headers["x-api-key"] = apiKey
		headers["anthropic-version"] = "2023-06-01"
		body = map[string]interface{}{
			"model":      model,
			"max_tokens": 4096,
			"messages":   []map[string]interface{}{{"role": "user", "content": prompt}},
		}
		if outputConfig != nil {
			if schema, ok := outputConfig["schema"].(map[string]interface{}); ok && len(schema) > 0 {
				tool := buildSchemaTool(schema)
				funcDef := tool["function"].(map[string]interface{})
				body["tools"] = []map[string]interface{}{{
					"name":         funcDef["name"],
					"description":  funcDef["description"],
					"input_schema": funcDef["parameters"],
				}}
				body["tool_choice"] = map[string]interface{}{"type": "tool", "name": "extract"}
			}
		}
	} else {
		headers["Authorization"] = fmt.Sprintf("Bearer %s", apiKey)
		body = map[string]interface{}{
			"model":    model,
			"messages": []map[string]interface{}{{"role": "user", "content": prompt}},
		}
		if outputConfig != nil {
			if schema, ok := outputConfig["schema"].(map[string]interface{}); ok && len(schema) > 0 {
				tool := buildSchemaTool(schema)
				body["tools"] = []interface{}{tool}
				body["tool_choice"] = map[string]interface{}{
					"type":     "function",
					"function": map[string]interface{}{"name": "extract"},
				}
			}
		}
	}

	jsonBody, _ := json.Marshal(body)
	log(fmt.Sprintf("Request URL: %s", url))
	log(fmt.Sprintf("Request body: %s", string(jsonBody)))

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s%v%s\n", red, err, reset)
		os.Exit(1)
	}
	defer resp.Body.Close()

	responseBody, _ := io.ReadAll(resp.Body)
	log(fmt.Sprintf("Response: %s", string(responseBody)))

	if resp.StatusCode >= 400 {
		message := extractErrorMessage(string(responseBody))
		fmt.Fprintf(os.Stderr, "%s%s%s\n", red, message, reset)
		os.Exit(1)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	return response
}

// extractResponse extracts the content from API response
func extractResponse(response map[string]interface{}, outputConfig map[string]interface{}, provider string) string {
	if provider == "anthropic" {
		content, ok := response["content"].([]interface{})
		if !ok {
			return ""
		}
		for _, block := range content {
			b, ok := block.(map[string]interface{})
			if !ok {
				continue
			}
			if b["type"] == "tool_use" {
				input, _ := b["input"].(map[string]interface{})
				result, _ := json.MarshalIndent(input, "", "  ")
				return string(result)
			}
			if b["type"] == "text" {
				text, _ := b["text"].(string)
				return text
			}
		}
		return ""
	}

	// OpenAI-compatible format
	choices, ok := response["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return ""
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return ""
	}
	message, ok := choice["message"].(map[string]interface{})
	if !ok {
		return ""
	}
	toolCalls, ok := message["tool_calls"].([]interface{})
	if ok && len(toolCalls) > 0 {
		tc, ok := toolCalls[0].(map[string]interface{})
		if ok {
			fn, ok := tc["function"].(map[string]interface{})
			if ok {
				args, _ := fn["arguments"].(string)
				return args
			}
		}
	}
	content, _ := message["content"].(string)
	return content
}

// applyOverrides applies RUNPROMPT_* environment variable overrides
func applyOverrides(meta map[string]interface{}) map[string]interface{} {
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := parts[0]
		value := parts[1]
		if strings.HasPrefix(key, "RUNPROMPT_") {
			metaKey := strings.ToLower(key[10:])
			parsed := parseYAMLValue(value)
			if parsed != nil {
				log(fmt.Sprintf("Override from env %s: %v", key, parsed))
				meta[metaKey] = parsed
			}
		}
	}
	return meta
}

// parseArgs parses command line arguments
func parseArgs(args []string) (bool, string, map[string]interface{}, []string) {
	verboseFlag := false
	saveResponsePath := ""
	overrides := make(map[string]interface{})
	remaining := []string{}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-v" {
			verboseFlag = true
		} else if arg == "--save-response" {
			if i+1 < len(args) {
				i++
				saveResponsePath = args[i]
			} else {
				fmt.Fprintln(os.Stderr, "--save-response requires a file path")
				os.Exit(1)
			}
		} else if strings.HasPrefix(arg, "--save-response=") {
			saveResponsePath = arg[len("--save-response="):]
		} else if strings.HasPrefix(arg, "--") {
			if strings.Contains(arg, "=") {
				parts := strings.SplitN(arg[2:], "=", 2)
				overrides[parts[0]] = parseYAMLValue(parts[1])
			} else {
				key := arg[2:]
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					i++
					overrides[key] = parseYAMLValue(args[i])
				} else {
					overrides[key] = true
				}
			}
		} else {
			remaining = append(remaining, arg)
		}
	}

	return verboseFlag, saveResponsePath, overrides, remaining
}

// readStdin reads from stdin if available
func readStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return ""
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func main() {
	verboseFlag, saveResponsePath, argOverrides, remaining := parseArgs(os.Args[1:])
	verbose = verboseFlag

	if len(remaining) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: runprompt [-v] [--save-response <file>] [--key=value ...] <prompt_file>")
		os.Exit(1)
	}

	promptPath = remaining[0]
	meta, template, err := parsePromptFile(promptPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading prompt file: %v\n", err)
		os.Exit(1)
	}

	meta = applyOverrides(meta)
	for key, value := range argOverrides {
		log(fmt.Sprintf("Override from arg --%s: %v", key, value))
		meta[key] = value
	}

	modelStr, _ := meta["model"].(string)
	if modelStr == "" {
		fmt.Fprintln(os.Stderr, "No model specified in prompt file")
		os.Exit(1)
	}

	provider, model := parseModelString(modelStr)
	if provider == "" {
		fmt.Fprintln(os.Stderr, "No provider in model string")
		os.Exit(1)
	}

	rawInput := readStdin()
	variables := map[string]interface{}{"STDIN": rawInput}

	if rawInput != "" {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(rawInput), &parsed); err == nil {
			for k, v := range parsed {
				variables[k] = v
			}
			log("Parsed input as JSON")
		} else {
			log("Input is not JSON, treating as raw string")
			if inputConfig, ok := meta["input"].(map[string]interface{}); ok {
				if inputSchema, ok := inputConfig["schema"].(map[string]interface{}); ok && len(inputSchema) > 0 {
					// Get first key from schema
					for firstKey := range inputSchema {
						variables[firstKey] = rawInput
						break
					}
				} else {
					variables["input"] = rawInput
				}
			} else {
				variables["input"] = rawInput
			}
		}
	}

	prompt := renderTemplate(template, variables)
	log(fmt.Sprintf("Rendered prompt: %s", prompt))

	outputConfig, _ := meta["output"].(map[string]interface{})

	var result string
	if provider == "test" {
		response := loadTestResponse(promptPath)
		testProvider, _ := response["_provider"].(string)
		if testProvider == "" {
			testProvider = "openai"
		}
		result = extractResponse(response, outputConfig, testProvider)
	} else {
		url, apiKey := getProviderConfig(provider)
		response := makeRequest(url, apiKey, model, prompt, outputConfig, provider)
		if saveResponsePath != "" {
			saveResponse(response, provider, saveResponsePath)
		}
		result = extractResponse(response, outputConfig, provider)
	}

	fmt.Println(result)
}
