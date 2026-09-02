package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestRewriteFixtures(t *testing.T) {
	cases := []struct {
		name    string
		changed bool
	}{
		{"allowlist", true},
		{"missing-call-id", true},
		{"empty-call-id", true},
		{"stale-call-id", true},
		{"matched-call-id", false},
		{"paired", false},
		{"parallel-pairs", false},
		{"out-of-order-call", true},
		{"non-target", false},
		{"text-only", false},
		{"malformed-root", false},
		{"malformed-input", false},
		{"stream-shape", true},
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
				assertOnlyExpectedConversions(t, got)
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

func TestBeforeAuthIsDisabledByDefaultAndScopedToResponses(t *testing.T) {
	resetConfig()
	body := mustReadFixture(t, "allowlist.json")
	response := invokeBefore(t, "openai-response", false, body)
	if len(response.Body) != 0 {
		t.Fatal("disabled plugin returned a body")
	}
	if err := configure([]byte("enabled: true\n")); err != nil {
		t.Fatal(err)
	}
	response = invokeBefore(t, "openai", false, body)
	if len(response.Body) != 0 {
		t.Fatal("non-Responses source was modified")
	}
	response = invokeBefore(t, "openai-response", true, body)
	if len(response.Body) == 0 {
		t.Fatal("streaming Responses request was not modified")
	}
	if response.Terminate || response.StatusCode != 0 || len(response.ResponseBody) != 0 {
		t.Fatalf("plugin declared a rejection response: %#v", response)
	}
}

func TestRegistrationDeclaresOnlyRequestInterceptor(t *testing.T) {
	registration := pluginRegistration()
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
	request, err := json.Marshal(lifecycleRequest{
		ConfigYAML:    []byte("enabled: true\n"),
		SchemaVersion: pluginabi.SchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure} {
		response, err := handleMethod(method, request)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if !pluginEnabled() {
			t.Fatalf("%s did not apply enabled config", method)
		}
		var envelope struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(response, &envelope); err != nil || !envelope.OK {
			t.Fatalf("%s response = %s, error = %v", method, response, err)
		}
	}
	if _, err := handleMethod(pluginabi.MethodPluginShutdown, nil); err != nil {
		t.Fatal(err)
	}
	if pluginEnabled() {
		t.Fatal("shutdown did not restore the disabled default")
	}
}

func TestRegistrationRejectsDifferentCPASchema(t *testing.T) {
	raw, err := json.Marshal(lifecycleRequest{SchemaVersion: pluginabi.SchemaVersion + 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handleMethod(pluginabi.MethodPluginRegister, raw); err == nil {
		t.Fatal("registration accepted an unsupported CPA schema")
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

func invokeBefore(t *testing.T, source string, stream bool, body []byte) pluginapi.RequestInterceptResponse {
	t.Helper()
	raw, err := json.Marshal(rpcRequestInterceptRequest{RequestInterceptRequest: pluginapi.RequestInterceptRequest{
		SourceFormat: source,
		Stream:       stream,
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

func assertOnlyExpectedConversions(t *testing.T, body []byte) {
	t.Helper()
	var request struct {
		Input []map[string]any `json:"input"`
	}
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	converted := 0
	for _, item := range request.Input {
		if item["type"] != "message" {
			continue
		}
		if item["role"] != "user" {
			t.Fatalf("converted role = %#v", item["role"])
		}
		content, ok := item["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("converted content = %#v", item["content"])
		}
		part, ok := content[0].(map[string]any)
		if !ok || part["type"] != "input_text" {
			t.Fatalf("converted content part = %#v", content[0])
		}
		converted++
	}
	if converted == 0 {
		t.Fatal("expected at least one converted item")
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
