package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestRewriteFixtures(t *testing.T) {
	cases := []struct {
		name          string
		changed       bool
		wantConverted int
	}{
		{"allowlist", true, 0},
		{"missing-call-id", true, 0},
		{"empty-call-id", true, 0},
		{"stale-call-id", true, 0},
		{"matched-call-id", false, 0},
		{"paired", false, 0},
		{"parallel-pairs", false, 0},
		{"out-of-order-call", true, 0},
		{"non-target", false, 0},
		{"text-only", true, 0},
		{"same-call-id-double-output", true, 0},
		{"malformed-root", false, 0},
		{"malformed-input", false, 0},
		{"stream-shape", true, 0},
		{"heartbeat-missing-call-id", true, 1},
		{"heartbeat-empty-call-id", true, 1},
		{"heartbeat-paired", false, 0},
		{"heartbeat-mixed-orphans", true, 4},
		{"heartbeat-gemini-missing-call-id", true, 1},
		{"heartbeat-with-metadata", true, 1},
		{"heartbeat-with-id", true, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := mustReadFixture(t, tc.name+".json")
			got, changed := rewriteOrphanedCodexAppOutputs(body)
			if changed != tc.changed {
				t.Fatalf("changed = %v, want %v; body=%s", changed, tc.changed, got)
			}
			if !changed && !bytes.Equal(got, body) {
				t.Fatalf("unchanged body was modified\n got: %s\nwant: %s", got, body)
			}
			if changed {
				assertOnlyExpectedConversions(t, got, tc.wantConverted)
			}
		})
	}
}

func TestRewriteIsIdempotent(t *testing.T) {
	body := mustReadFixture(t, "allowlist.json")
	first, changed := rewriteOrphanedCodexAppOutputs(body)
	if !changed {
		t.Fatal("first rewrite did not change fixture")
	}
	second, changed := rewriteOrphanedCodexAppOutputs(first)
	if changed || !bytes.Equal(first, second) {
		t.Fatalf("second rewrite changed=%v\n got: %s\nwant: %s", changed, second, first)
	}
}

func TestRewriteIncrementalCodexAppOutputBoundaries(t *testing.T) {
	for _, toolName := range []string{createThreadTool, sendMessageTool, automationUpdateTool} {
		t.Run(toolName, func(t *testing.T) {
			incremental := []byte(`{"previous_response_id":"resp_123","input":[{"type":"function_call_output","call_id":"call_123","namespace":"codex_app","name":"` + toolName + `","output":"incremental output"}]}`)
			got, changed := rewriteOrphanedCodexAppOutputs(incremental)
			if changed || !bytes.Equal(got, incremental) {
				t.Fatalf("incremental output changed=%v\n got: %s\nwant: %s", changed, got, incremental)
			}

			responseAppend := []byte(`{"type":"response.append","input":[{"type":"function_call_output","call_id":"call_123","namespace":"codex_app","name":"` + toolName + `","output":"continuation output"}]}`)
			got, changed = rewriteOrphanedCodexAppOutputs(responseAppend)
			if changed || !bytes.Equal(got, responseAppend) {
				t.Fatalf("response.append output changed=%v\n got: %s\nwant: %s", changed, got, responseAppend)
			}

			missingCallID := []byte(`{"previous_response_id":"resp_123","input":[{"type":"function_call_output","namespace":"codex_app","name":"` + toolName + `","output":"orphan output"}]}`)
			got, changed = rewriteOrphanedCodexAppOutputs(missingCallID)
			if !changed {
				t.Fatal("orphaned incremental output without call ID was not rewritten")
			}
			if text := convertedOutputText(t, got); text != formatConvertedOutput(toolName, "orphan output") {
				t.Fatalf("converted output = %q, want %q", text, formatConvertedOutput(toolName, "orphan output"))
			}

			staleCallID := []byte(`{"input":[{"type":"function_call_output","call_id":"stale_123","namespace":"codex_app","name":"` + toolName + `","output":"stale output"}]}`)
			got, changed = rewriteOrphanedCodexAppOutputs(staleCallID)
			if !changed {
				t.Fatal("stale output without previous response ID was not rewritten")
			}
			if text := convertedOutputText(t, got); text != formatConvertedOutput(toolName, "stale output") {
				t.Fatalf("converted output = %q, want %q", text, formatConvertedOutput(toolName, "stale output"))
			}
		})
	}
}

func TestRewriteIncrementalNonTargetOutputsAreUntouched(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{
			name: "previous-response-id other namespace",
			body: []byte(`{"previous_response_id":"resp_123","input":[{"type":"function_call_output","call_id":"call_123","namespace":"other","name":"create_thread","output":"non-target output"}]}`),
		},
		{
			name: "previous-response-id other name",
			body: []byte(`{"previous_response_id":"resp_123","input":[{"type":"function_call_output","call_id":"call_123","namespace":"codex_app","name":"other_tool","output":"non-target output"}]}`),
		},
		{
			name: "response-append other namespace",
			body: []byte(`{"type":"response.append","input":[{"type":"function_call_output","call_id":"call_123","namespace":"other","name":"create_thread","output":"non-target output"}]}`),
		},
		{
			name: "response-append other name",
			body: []byte(`{"type":"response.append","input":[{"type":"function_call_output","call_id":"call_123","namespace":"codex_app","name":"other_tool","output":"non-target output"}]}`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := rewriteOrphanedCodexAppOutputs(tc.body)
			if changed || !bytes.Equal(got, tc.body) {
				t.Fatalf("non-target incremental output changed=%v\n got: %s\nwant: %s", changed, got, tc.body)
			}
		})
	}
}

func TestBeforeAuthIsDisabledByDefaultAndScopedToResponses(t *testing.T) {
	resetConfig()
	body := mustReadFixture(t, "allowlist.json")
	response := invokeBefore(t, "openai-response", false, nil, body)
	if len(response.Body) != 0 {
		t.Fatal("disabled plugin returned a body")
	}
	if err := configure([]byte("enabled: true\n")); err != nil {
		t.Fatal(err)
	}
	response = invokeBefore(t, "openai", false, nil, body)
	if len(response.Body) != 0 {
		t.Fatal("non-Responses source was modified")
	}
	response = invokeBefore(t, "openai-response", true, nil, body)
	if len(response.Body) == 0 {
		t.Fatal("streaming Responses request was not modified")
	}
	if response.Terminate || response.StatusCode != 0 || len(response.ResponseBody) != 0 {
		t.Fatalf("plugin declared a rejection response: %#v", response)
	}
}

func TestBeforeAuthDoesNotRequireSubagentHeader(t *testing.T) {
	resetConfig()
	if err := configure([]byte("enabled: true\n")); err != nil {
		t.Fatal(err)
	}
	body := mustReadFixture(t, "allowlist.json")
	for _, tc := range []struct {
		name    string
		headers http.Header
		changed bool
	}{
		{name: "missing", changed: true},
		{name: "collab-spawn", headers: http.Header{"X-Openai-Subagent": {"collab_spawn"}}, changed: true},
		{name: "mixed-case header and value", headers: http.Header{"x-openai-subagent": {"COLLAB_SPAWN"}}, changed: true},
		{name: "other", headers: http.Header{"X-Openai-Subagent": {"other"}}, changed: true},
		{name: "unrelated header", headers: http.Header{"X-Other": {"value"}}, changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := invokeBefore(t, openAIResponsesFormat, false, tc.headers, body)
			if (len(response.Body) > 0) != tc.changed {
				t.Fatalf("response body changed = %v, want %v", len(response.Body) > 0, tc.changed)
			}
		})
	}
}

func TestRewriteConsumesPairedCallIDOnce(t *testing.T) {
	body := mustReadFixture(t, "same-call-id-double-output.json")
	got, changed := rewriteOrphanedCodexAppOutputs(body)
	if !changed {
		t.Fatal("duplicate output was not rewritten after the paired output consumed its call ID")
	}
	var root struct {
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(got, &root); err != nil {
		t.Fatal(err)
	}
	var first functionCallOutputItem
	if err := json.Unmarshal(root.Input[1], &first); err != nil || first.Type != "function_call_output" {
		t.Fatalf("first output was not preserved: %s; error = %v", root.Input[1], err)
	}
	assertOnlyExpectedConversions(t, got, 0)
}

func TestRewriteHeartbeatAutomationUpdateKeepsPairedAndNormalToolOutput(t *testing.T) {
	body := mustReadFixture(t, "heartbeat-mixed-orphans.json")
	got, changed := rewriteOrphanedCodexAppOutputs(body)
	if !changed {
		t.Fatal("mixed heartbeat orphans were not rewritten")
	}
	var root struct {
		Model string            `json:"model"`
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(got, &root); err != nil {
		t.Fatal(err)
	}
	if root.Model != "grok-4.6" {
		t.Fatalf("model = %q, want grok-4.6", root.Model)
	}
	if len(root.Input) != 7 {
		t.Fatalf("input len = %d, want 7", len(root.Input))
	}
	var execOutput functionCallOutputItem
	if err := json.Unmarshal(root.Input[1], &execOutput); err != nil {
		t.Fatal(err)
	}
	if execOutput.Type != "function_call_output" || execOutput.CallID != "call-exec-1" {
		t.Fatalf("paired exec_command output was rewritten: %s", root.Input[1])
	}
	converted := 0
	for _, raw := range root.Input {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatal(err)
		}
		if item["type"] == "function_call_output" && item["name"] == "automation_update" {
			t.Fatalf("heartbeat orphan remained a function_call_output: %s", raw)
		}
		content, _ := item["content"].([]any)
		if item["type"] == "message" && len(content) > 0 {
			part, _ := content[0].(map[string]any)
			text, _ := part["text"].(string)
			if len(text) >= len("[Tool output from codex_app.") && text[:len("[Tool output from codex_app.")] == "[Tool output from codex_app." {
				converted++
			}
		}
	}
	if converted != 4 {
		t.Fatalf("converted messages = %d, want 4 create/send/heartbeat items", converted)
	}
	assertOnlyExpectedConversions(t, got, 4)
}

func TestRewriteHeartbeatAutomationUpdateOnStreamAndGeminiRequests(t *testing.T) {
	wantHeartbeat := "<heartbeat>\n  <automation_id>fixture-automation</automation_id>\n</heartbeat>"
	for _, tc := range []struct {
		name string
		file string
	}{
		{name: "grok", file: "heartbeat-missing-call-id.json"},
		{name: "gemini", file: "heartbeat-gemini-missing-call-id.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := mustReadFixture(t, tc.file)
			got, changed := rewriteOrphanedCodexAppOutputs(body)
			if !changed {
				t.Fatal("heartbeat orphan was not rewritten")
			}
			if text := convertedOutputText(t, got); text != formatConvertedOutput(automationUpdateTool, wantHeartbeat) {
				t.Fatalf("converted output = %q", text)
			}
		})
	}
	streamBody := []byte(`{"stream":true,"input":[{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":"<heartbeat/>"}]}`)
	got, changed := rewriteOrphanedCodexAppOutputs(streamBody)
	if !changed {
		t.Fatal("streaming heartbeat orphan was not rewritten")
	}
	if text := convertedOutputText(t, got); text != formatConvertedOutput(automationUpdateTool, "<heartbeat/>") {
		t.Fatalf("stream converted output = %q", text)
	}
}

func TestRewriteHeartbeatAutomationUpdateDoesNotInventCallPairing(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","call_id":"call-other","name":"exec_command"},{"type":"function_call_output","namespace":"codex_app","name":"automation_update","output":"<heartbeat/>"}]}`)
	got, changed := rewriteOrphanedCodexAppOutputs(body)
	if !changed {
		t.Fatal("unmatched heartbeat output was not rewritten")
	}
	var root struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(got, &root); err != nil {
		t.Fatal(err)
	}
	if root.Input[1]["type"] != "message" {
		t.Fatalf("unmatched heartbeat was paired or left as tool output: %s", got)
	}
	if _, ok := root.Input[1]["call_id"]; ok {
		t.Fatalf("rewrite invented call_id: %s", got)
	}
}

func TestRewriteHeartbeatPayloadsDropPassthroughAndKeepPrefix(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{
			file: "heartbeat-with-metadata.json",
			want: formatConvertedOutput(automationUpdateTool, "<heartbeat>\n  <automation_id>fixture-automation</automation_id>\n  <current_time_iso>2026-01-01T00:00:00.000Z</current_time_iso>\n</heartbeat>"),
		},
		{
			file: "heartbeat-with-id.json",
			want: formatConvertedOutput(automationUpdateTool, "<heartbeat>\n  <automation_id>fixture-automation</automation_id>\n</heartbeat>"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			body := mustReadFixture(t, tc.file)
			got, changed := rewriteOrphanedCodexAppOutputs(body)
			if !changed {
				t.Fatal("heartbeat payload was not rewritten")
			}
			var root struct {
				Input []map[string]any `json:"input"`
			}
			if err := json.Unmarshal(got, &root); err != nil {
				t.Fatal(err)
			}
			if len(root.Input) != 1 {
				t.Fatalf("input len = %d, want 1", len(root.Input))
			}
			item := root.Input[0]
			if item["type"] != "message" || item["role"] != "user" {
				t.Fatalf("heartbeat payload was not converted to user message: %s", got)
			}
			if _, ok := item["id"]; ok {
				t.Fatalf("converted message kept function call id: %s", got)
			}
			if _, ok := item["internal_chat_message_metadata_passthrough"]; ok {
				t.Fatalf("converted message kept passthrough metadata: %s", got)
			}
			if text := convertedOutputText(t, got); text != tc.want {
				t.Fatalf("converted output = %q, want %q", text, tc.want)
			}
			assertOnlyExpectedConversions(t, got, 1)
		})
	}
}

func TestRewriteConvertsNonStringOutputsToJSONText(t *testing.T) {
	for _, tc := range []struct {
		name       string
		output     string
		wantOutput string
	}{
		{name: "object", output: `{"thread":"new"}`, wantOutput: `{"thread":"new"}`},
		{name: "array", output: `["one",2]`, wantOutput: `["one",2]`},
		{name: "null", output: `null`, wantOutput: `null`},
		{name: "missing", wantOutput: `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			item := `{"type":"function_call_output","namespace":"codex_app","name":"create_thread"`
			if tc.output != "" {
				item += `,"output":` + tc.output
			}
			item += `}`
			got, changed := rewriteOrphanedCodexAppOutputs([]byte(`{"input":[` + item + `]}`))
			if !changed {
				t.Fatal("non-string output was not rewritten")
			}
			if text := convertedOutputText(t, got); text != formatConvertedOutput(createThreadTool, tc.wantOutput) {
				t.Fatalf("converted output = %q, want %q", text, formatConvertedOutput(createThreadTool, tc.wantOutput))
			}
		})
	}
}

func TestRegistrationDeclaresOnlyRequestInterceptor(t *testing.T) {
	registration := pluginRegistration(4)
	if !registration.Capabilities.RequestInterceptor {
		t.Fatal("request interceptor not declared")
	}
	encoded, err := json.Marshal(registration)
	if err != nil {
		t.Fatal(err)
	}
	var capabilities map[string]bool
	if err := json.Unmarshal(encoded, &struct {
		Capabilities *map[string]bool `json:"capabilities"`
	}{Capabilities: &capabilities}); err != nil {
		t.Fatal(err)
	}
	if len(capabilities) != 1 || !capabilities["request_interceptor"] {
		t.Fatalf("capabilities = %#v", capabilities)
	}
}

func TestRegistrationReconfigureAndShutdownLifecycle(t *testing.T) {
	resetConfig()
	for _, tc := range []struct {
		host       uint32
		negotiated uint32
	}{
		{host: 4, negotiated: 4},
		{host: 5, negotiated: 5},
		{host: 6, negotiated: 5},
		{host: 7, negotiated: 5},
	} {
		t.Run(fmt.Sprintf("host-%d", tc.host), func(t *testing.T) {
			registerRequest, err := json.Marshal(lifecycleRequest{
				ConfigYAML:    []byte("enabled: false\n"),
				SchemaVersion: tc.host,
			})
			if err != nil {
				t.Fatal(err)
			}
			response, err := handleMethod(pluginabi.MethodPluginRegister, registerRequest)
			if err != nil {
				t.Fatalf("register: %v", err)
			}
			if pluginEnabled() {
				t.Fatal("register did not apply disabled config")
			}
			assertRegistrationResponse(t, "register", response, tc.negotiated)

			reconfigureRequest, err := json.Marshal(lifecycleRequest{
				ConfigYAML:    []byte("enabled: true\n"),
				SchemaVersion: tc.host,
			})
			if err != nil {
				t.Fatal(err)
			}
			response, err = handleMethod(pluginabi.MethodPluginReconfigure, reconfigureRequest)
			if err != nil {
				t.Fatalf("reconfigure: %v", err)
			}
			if !pluginEnabled() {
				t.Fatal("reconfigure did not apply enabled config")
			}
			assertRegistrationResponse(t, "reconfigure", response, tc.negotiated)
		})
	}
	if _, err := handleMethod(pluginabi.MethodPluginShutdown, nil); err != nil {
		t.Fatal(err)
	}
	if pluginEnabled() {
		t.Fatal("shutdown did not restore the disabled default")
	}
}

func TestRegistrationRejectsDifferentCPASchema(t *testing.T) {
	resetConfig()
	if err := configure([]byte("enabled: true\n")); err != nil {
		t.Fatal(err)
	}
	oldSchema, err := json.Marshal(lifecycleRequest{
		ConfigYAML:    []byte("enabled: false\n"),
		SchemaVersion: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	missingSchema, err := json.Marshal(struct {
		ConfigYAML []byte `json:"config_yaml"`
	}{ConfigYAML: []byte("enabled: false\n")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		raw     []byte
		version uint32
	}{
		{name: "schema-3", raw: oldSchema, version: 3},
		{name: "missing-schema", raw: missingSchema, version: 0},
	} {
		for _, method := range []string{pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				_, err := handleMethod(method, tc.raw)
				if err == nil {
					t.Fatalf("%s accepted unsupported CPA schema %d", method, tc.version)
				}
				want := fmt.Sprintf("unsupported plugin schema version %d", tc.version)
				if err.Error() != want {
					t.Fatalf("%s error = %v, want %s", method, err, want)
				}
				if !pluginEnabled() {
					t.Fatal("rejected schema changed already enabled config")
				}
			})
		}
	}
}

func assertRegistrationResponse(t *testing.T, method string, response []byte, schemaVersion uint32) {
	t.Helper()
	var envelope struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil || !envelope.OK {
		t.Fatalf("%s response = %s, error = %v", method, response, err)
	}
	var registration registration
	if err := json.Unmarshal(envelope.Result, &registration); err != nil {
		t.Fatalf("%s registration = %s, error = %v", method, envelope.Result, err)
	}
	if registration.SchemaVersion != schemaVersion {
		t.Fatalf("%s registration schema version = %d, want %d", method, registration.SchemaVersion, schemaVersion)
	}
}

func TestAfterAuthIsAlwaysPassThrough(t *testing.T) {
	body := mustReadFixture(t, "allowlist.json")
	raw, err := json.Marshal(rpcRequestInterceptRequest{RequestInterceptRequest: pluginapi.RequestInterceptRequest{Body: body}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := handleMethod(pluginabi.MethodRequestInterceptAfter, raw)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		t.Fatal(err)
	}
	var interceptorResponse pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(envelope.Result, &interceptorResponse); err != nil {
		t.Fatal(err)
	}
	if len(interceptorResponse.Body) != 0 || interceptorResponse.Terminate {
		t.Fatalf("after-auth response = %#v", interceptorResponse)
	}
}

func invokeBefore(t *testing.T, source string, stream bool, headers http.Header, body []byte) pluginapi.RequestInterceptResponse {
	t.Helper()
	raw, err := json.Marshal(rpcRequestInterceptRequest{RequestInterceptRequest: pluginapi.RequestInterceptRequest{
		SourceFormat: source,
		Stream:       stream,
		Headers:      headers,
		Body:         body,
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := handleRequestInterceptBefore(raw)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(result, &envelope); err != nil {
		t.Fatal(err)
	}
	var response pluginapi.RequestInterceptResponse
	if err := json.Unmarshal(envelope.Result, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func convertedOutputText(t *testing.T, body []byte) string {
	t.Helper()
	var root struct {
		Input []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(body, &root); err != nil {
		t.Fatal(err)
	}
	if len(root.Input) != 1 || len(root.Input[0].Content) != 1 {
		t.Fatalf("converted input = %s", body)
	}
	return root.Input[0].Content[0].Text
}

func assertOnlyExpectedConversions(t *testing.T, body []byte, wantConverted int) {
	t.Helper()
	var request struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	converted := 0
	prefix := "[Tool output from codex_app."
	for _, item := range request.Input {
		if item["type"] == "function_call_output" {
			namespace, _ := item["namespace"].(string)
			name, _ := item["name"].(string)
			callID, _ := item["call_id"].(string)
			if namespace == codexAppNamespace && isTargetTool(name) && callID == "" {
				t.Fatalf("unmatched allowlisted output remained: %#v", item)
			}
			continue
		}
		if item["type"] != "message" {
			continue
		}
		content, ok := item["content"].([]any)
		if !ok || len(content) != 1 {
			continue
		}
		part, ok := content[0].(map[string]any)
		if !ok || part["type"] != "input_text" {
			continue
		}
		text, _ := part["text"].(string)
		if len(text) < len(prefix) || text[:len(prefix)] != prefix {
			continue
		}
		if item["role"] != "user" {
			t.Fatalf("converted role = %#v", item["role"])
		}
		converted++
	}
	if converted == 0 {
		t.Fatal("expected at least one converted item")
	}
	if wantConverted > 0 && converted != wantConverted {
		t.Fatalf("converted messages = %d, want %d", converted, wantConverted)
	}
}

func mustReadFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return body
}
