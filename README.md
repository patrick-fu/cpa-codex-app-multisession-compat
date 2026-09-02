# CPA Codex App Multisession Compatibility

`cpa-codex-app-multisession-compat` is a native [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) v7.2.147 plugin for a narrow Codex App replay-compatibility case. It is disabled by default and ships only a Darwin/arm64 binary in v0.1.0.

## What it does

Before CPA selects credentials, and only for `SourceFormat: openai-response`, the plugin examines a Responses API `input` array. It replaces an item only when all of these are true:

- `type` is `function_call_output`;
- `namespace` is `codex_app`;
- `name` is `create_thread` or `send_message_to_thread`;
- `output` is a JSON string; and
- `call_id` is missing or empty, or no preceding `function_call` in this request has the same non-empty `call_id`.

The replacement is an ordinary `role: user`, `type: message` item with one `input_text` part. Its text begins with an explicit source label, followed by the original output string. The plugin never creates a tool call, call ID, tool name, or any other tool state.

This protects paired and parallel tool-call history: an output whose `call_id` matches an earlier `function_call` in the `input` array is kept as-is. An output that precedes its call is downgraded, avoiding an invalid tool result before the call. The plugin also leaves all non-allowlisted namespaces/names, custom outputs, non-string outputs, malformed request roots or `input`, and non-Responses requests untouched. Streaming request shape does not change this rule.

中文要点：仅把没有配对 `function_call` 的 Codex App `create_thread` / `send_message_to_thread` 文本输出降级为带来源标签的普通用户文本；默认关闭，配对工具历史绝不改写。

## Threat boundary

This is a best-effort, stateless compatibility rewrite, not a general tool-history repairer or an authorization control. It does not inspect credentials, persist state, log request bodies, thread IDs, call IDs, or credentials, and it cannot reject a request. It declares only CPA's `request_interceptor` capability; its after-auth callback is a no-op. It does not support object, array, or multimodal output conversion.

Review the source and release checksum before enabling it. The plugin is unaffiliated with OpenAI, Codex, or CLIProxyAPI; it is an independent community project.

## Compatibility

- CPA: **v7.2.147 only**
- Plugin ABI: v1; RPC schema negotiated with CPA v7.2.147
- Release v0.1.0: macOS/Darwin arm64 only

No Linux, Windows, or Intel macOS artifact is built or published for this release.

## Install

1. Download `codex-app-multisession-compat_darwin_arm64.zip` and `checksums.txt` from the `v0.1.0` release.
2. Verify it before extraction:

   ```bash
   shasum -a 256 -c checksums.txt
   ```

3. Extract the single `.dylib` into CPA's plugin location. With CPA's default plugin directory:

   ```bash
   mkdir -p plugins/darwin/arm64
   unzip -j codex-app-multisession-compat_darwin_arm64.zip \
     -d plugins/darwin/arm64
   ```

4. Enable CPA plugins and this plugin explicitly. The default is off:

   ```yaml
   plugins:
     enabled: true
     configs:
       codex-app-multisession-compat:
         enabled: true
   ```

Restart or reload CPA according to its normal configuration lifecycle.

## Verify and manage

Use CPA's management API/UI after the reload. `GET /v0/management/plugins` should report the plugin as discovered, registered, and effectively enabled. Its exact artifact name is `codex-app-multisession-compat.dylib`.

To disable it without deleting the binary, set `plugins.configs.codex-app-multisession-compat.enabled: false` (or use CPA's `PATCH /v0/management/plugins/codex-app-multisession-compat/enabled` endpoint), then reload CPA. This makes the feature inactive immediately on the next configuration application.

## Uninstall

First disable the plugin and reload CPA. Then remove only this artifact from the configured CPA plugin directory:

```bash
rm plugins/darwin/arm64/codex-app-multisession-compat.dylib
```

Remove the corresponding `plugins.configs.codex-app-multisession-compat` block if it is no longer wanted, then reload CPA again.

## Build and test

Requires Go 1.26 and a CPA v7.2.147-compatible macOS arm64 toolchain.

```bash
go test ./...
go vet ./...
mkdir -p dist
go build -buildmode=c-shared \
  -o dist/codex-app-multisession-compat.dylib ./plugin
rm dist/codex-app-multisession-compat.h
nm -gU dist/codex-app-multisession-compat.dylib | \
  rg 'cliproxy_plugin_init|cliproxyPluginCall|cliproxyPluginFree|cliproxyPluginShutdown'
```

The generated C ABI exports registration, call, free-buffer, and shutdown entry points. Tests cover the allowlist, missing/empty/stale/matched call IDs, paired/parallel calls, non-target and non-text outputs, malformed roots/input, idempotence, disabled/source gating, and streaming request shape.

## License

MIT. See [LICENSE](LICENSE).
