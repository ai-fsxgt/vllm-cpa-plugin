package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	void* call;
	void* free_buffer;
} cliproxy_host_api;

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unsafe"
)

const abiVersion uint32 = 1

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type responseTransformRequest struct {
	FromFormat string
	ToFormat   string
	Stream     bool
	Body       []byte
}

type requestTransformRequest struct {
	FromFormat string
	ToFormat   string
	Body       []byte
}

type payloadResponse struct {
	Body []byte
}

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(_ *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	plugin.abi_version = C.uint32_t(abiVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}

	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelope("plugin_error", errHandle.Error()))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		return okEnvelopeJSON(`{"schema_version":1,"metadata":{"Name":"vLLM reasoning normalizer","Version":"1.0.0","Author":"local","GitHubRepository":"https://github.com/router-for-me/CLIProxyAPI","ConfigFields":[]},"capabilities":{"request_normalizer":true,"response_before_translator":true,"response_after_translator":true}}`)
	case "request.normalize":
		return normalizeRequest(request)
	case "response.normalize_before":
		return normalizeResponseBefore(request)
	case "response.normalize_after":
		return normalizeResponseAfter(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}

func normalizeRequest(raw []byte) ([]byte, error) {
	var request requestTransformRequest
	if errUnmarshal := json.Unmarshal(raw, &request); errUnmarshal != nil {
		return nil, fmt.Errorf("decode request transform request: %w", errUnmarshal)
	}
	if request.FromFormat != "claude" || request.ToFormat != "openai" {
		return okEnvelope(payloadResponse{Body: request.Body})
	}

	body, errNormalize := normalizeToolChoice(request.Body)
	if errNormalize != nil {
		return nil, errNormalize
	}
	return okEnvelope(payloadResponse{Body: body})
}

func normalizeToolChoice(body []byte) ([]byte, error) {
	var root map[string]json.RawMessage
	if errUnmarshal := json.Unmarshal(body, &root); errUnmarshal != nil {
		return nil, fmt.Errorf("decode OpenAI request: %w", errUnmarshal)
	}

	removeEmptyTools := false
	toolsRaw, hasToolsField := root["tools"]
	if hasToolsField && !bytes.Equal(bytes.TrimSpace(toolsRaw), []byte("null")) {
		var tools []json.RawMessage
		if errUnmarshal := json.Unmarshal(toolsRaw, &tools); errUnmarshal != nil || len(tools) > 0 {
			return body, nil
		}
		removeEmptyTools = true
	}

	toolChoice, hasToolChoice := root["tool_choice"]
	if !hasToolChoice || bytes.Equal(bytes.TrimSpace(toolChoice), []byte("null")) {
		return body, nil
	}

	var choice string
	if errUnmarshal := json.Unmarshal(toolChoice, &choice); errUnmarshal == nil {
		switch choice {
		case "none":
			return body, nil
		case "auto", "required":
		default:
			return body, nil
		}
	} else {
		var namedChoice struct {
			Type     string `json:"type"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		}
		if errNamedChoice := json.Unmarshal(toolChoice, &namedChoice); errNamedChoice != nil ||
			namedChoice.Type != "function" || namedChoice.Function.Name == "" {
			return body, nil
		}
	}

	if removeEmptyTools {
		delete(root, "tools")
	}
	delete(root, "tool_choice")
	delete(root, "parallel_tool_calls")
	return marshalRequest(root)
}

func marshalRequest(root map[string]json.RawMessage) ([]byte, error) {
	body, errMarshal := json.Marshal(root)
	if errMarshal != nil {
		return nil, fmt.Errorf("encode normalized OpenAI request: %w", errMarshal)
	}
	return body, nil
}

func normalizeResponseBefore(raw []byte) ([]byte, error) {
	var request responseTransformRequest
	if errUnmarshal := json.Unmarshal(raw, &request); errUnmarshal != nil {
		return nil, fmt.Errorf("decode response transform request: %w", errUnmarshal)
	}
	if request.FromFormat != "openai" || request.ToFormat != "claude" {
		return okEnvelope(payloadResponse{Body: request.Body})
	}

	body, errNormalize := normalizeBody(request.Body)
	if errNormalize != nil {
		return nil, errNormalize
	}
	return okEnvelope(payloadResponse{Body: body})
}

func normalizeResponseAfter(raw []byte) ([]byte, error) {
	var request responseTransformRequest
	if errUnmarshal := json.Unmarshal(raw, &request); errUnmarshal != nil {
		return nil, fmt.Errorf("decode response transform request: %w", errUnmarshal)
	}
	if request.FromFormat != "openai" || request.ToFormat != "claude" || request.Stream {
		return okEnvelope(payloadResponse{Body: request.Body})
	}

	body, errNormalize := orderClaudeThinkingFirst(request.Body)
	if errNormalize != nil {
		return nil, errNormalize
	}
	return okEnvelope(payloadResponse{Body: body})
}

func normalizeBody(body []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(body)
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		payload := bytes.TrimSpace(trimmed[len("data:"):])
		if bytes.Equal(payload, []byte("[DONE]")) {
			return body, nil
		}
		normalized, changed, errNormalize := normalizeJSON(payload)
		if errNormalize != nil || !changed {
			return body, errNormalize
		}

		payloadStart := bytes.Index(body, payload)
		if payloadStart < 0 {
			return nil, fmt.Errorf("locate streaming JSON payload")
		}
		out := make([]byte, 0, len(body)-len(payload)+len(normalized))
		out = append(out, body[:payloadStart]...)
		out = append(out, normalized...)
		out = append(out, body[payloadStart+len(payload):]...)
		return out, nil
	}

	normalized, changed, errNormalize := normalizeJSON(trimmed)
	if errNormalize != nil || !changed {
		return body, errNormalize
	}
	return normalized, nil
}

func normalizeJSON(raw []byte) ([]byte, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var root map[string]any
	if errDecode := decoder.Decode(&root); errDecode != nil {
		return nil, false, fmt.Errorf("decode OpenAI response: %w", errDecode)
	}

	changed := false
	choices, _ := root["choices"].([]any)
	for _, rawChoice := range choices {
		choice, _ := rawChoice.(map[string]any)
		for _, field := range []string{"message", "delta"} {
			container, _ := choice[field].(map[string]any)
			if normalizeReasoningField(container) {
				changed = true
			}
		}
	}
	if !changed {
		return raw, false, nil
	}

	out, errMarshal := json.Marshal(root)
	if errMarshal != nil {
		return nil, false, fmt.Errorf("encode normalized OpenAI response: %w", errMarshal)
	}
	return out, true, nil
}

func normalizeReasoningField(container map[string]any) bool {
	if container == nil || !isEmptyReasoning(container["reasoning_content"]) {
		return false
	}
	reasoning, exists := container["reasoning"]
	if !exists || isEmptyReasoning(reasoning) {
		return false
	}
	switch reasoning.(type) {
	case string, []any, map[string]any:
	default:
		return false
	}
	container["reasoning_content"] = reasoning
	return true
}

func orderClaudeThinkingFirst(body []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var root map[string]any
	if errDecode := decoder.Decode(&root); errDecode != nil {
		return nil, fmt.Errorf("decode Claude response: %w", errDecode)
	}

	content, _ := root["content"].([]any)
	thinking := make([]any, 0, len(content))
	other := make([]any, 0, len(content))
	seenOther := false
	needsReorder := false
	for _, rawBlock := range content {
		block, _ := rawBlock.(map[string]any)
		blockType, _ := block["type"].(string)
		if blockType == "thinking" || blockType == "redacted_thinking" {
			thinking = append(thinking, rawBlock)
			if seenOther {
				needsReorder = true
			}
			continue
		}
		seenOther = true
		other = append(other, rawBlock)
	}
	if !needsReorder {
		return body, nil
	}

	root["content"] = append(thinking, other...)
	out, errMarshal := json.Marshal(root)
	if errMarshal != nil {
		return nil, fmt.Errorf("encode ordered Claude response: %w", errMarshal)
	}
	return out, nil
}

func isEmptyReasoning(value any) bool {
	if value == nil {
		return true
	}
	text, isString := value.(string)
	return isString && text == ""
}

func okEnvelope(value any) ([]byte, error) {
	result, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		return nil, errMarshal
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func okEnvelopeJSON(result string) ([]byte, error) {
	return json.Marshal(envelope{OK: true, Result: json.RawMessage(result)})
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
