package main

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	openAIResponsesFormat = "openai-response"
	codexAppNamespace     = "codex_app"
	createThreadTool      = "create_thread"
	sendMessageTool       = "send_message_to_thread"
	convertedOutputPrefix = "[Tool output from codex_app.%s]\n"
)

// rpcRequestInterceptRequest mirrors the host request with its optional callback ID.
type rpcRequestInterceptRequest struct {
	pluginapi.RequestInterceptRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

func handleRequestInterceptBefore(raw []byte) ([]byte, error) {
	var req rpcRequestInterceptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	response := pluginapi.RequestInterceptResponse{}
	if pluginEnabled() && req.SourceFormat == openAIResponsesFormat {
		if body, changed := rewriteOrphanedCodexAppOutputs(req.Body); changed {
			response.Body = body
		}
	}
	return okEnvelope(response)
}

// rewriteOrphanedCodexAppOutputs replaces only targeted, unpaired function outputs.
// It intentionally returns the original bytes for every unsupported or malformed shape.
func rewriteOrphanedCodexAppOutputs(body []byte) ([]byte, bool) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return body, false
	}
	inputRaw, ok := root["input"]
	if !ok {
		return body, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(inputRaw, &items); err != nil {
		return body, false
	}

	changed := false
	availableCallIDs := make(map[string]int)
	for index, rawItem := range items {
		var call functionCallItem
		if json.Unmarshal(rawItem, &call) == nil && call.Type == "function_call" && call.CallID != "" {
			availableCallIDs[call.CallID]++
			continue
		}
		var output functionCallOutputItem
		if json.Unmarshal(rawItem, &output) == nil && output.Type == "function_call_output" && output.CallID != "" {
			if availableCallIDs[output.CallID] > 0 {
				availableCallIDs[output.CallID]--
				continue
			}
		}
		replacement, ok := replacementFor(rawItem)
		if !ok {
			continue
		}
		items[index] = replacement
		changed = true
	}
	if !changed {
		return body, false
	}
	newInput, err := json.Marshal(items)
	if err != nil {
		return body, false
	}
	root["input"] = newInput
	rewritten, err := json.Marshal(root)
	if err != nil {
		return body, false
	}
	return rewritten, true
}

type functionCallItem struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
}

type functionCallOutputItem struct {
	Type      string          `json:"type"`
	CallID    string          `json:"call_id"`
	Namespace string          `json:"namespace"`
	Name      string          `json:"name"`
	Output    json.RawMessage `json:"output"`
}

func replacementFor(rawItem json.RawMessage) (json.RawMessage, bool) {
	var item functionCallOutputItem
	if err := json.Unmarshal(rawItem, &item); err != nil {
		return nil, false
	}
	if item.Type != "function_call_output" || item.Namespace != codexAppNamespace || !isTargetTool(item.Name) {
		return nil, false
	}
	output, ok := outputText(item.Output)
	if !ok {
		return nil, false
	}
	replacement, err := json.Marshal(struct {
		Role    string `json:"role"`
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}{
		Role: "user",
		Type: "message",
		Content: []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{
			{Type: "input_text", Text: formatConvertedOutput(item.Name, output)},
		},
	})
	if err != nil {
		return nil, false
	}
	return replacement, true
}

func outputText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "null", true
	}
	var output *string
	if err := json.Unmarshal(raw, &output); err == nil && output != nil {
		return *output, true
	}
	if !json.Valid(raw) {
		return "", false
	}
	return string(raw), true
}

func isTargetTool(name string) bool {
	return name == createThreadTool || name == sendMessageTool
}

func formatConvertedOutput(name, output string) string {
	return fmt.Sprintf(convertedOutputPrefix, name) + output
}
