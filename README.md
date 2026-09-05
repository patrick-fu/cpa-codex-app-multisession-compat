# CPA Codex App Multisession Compatibility

`cpa-codex-app-multisession-compat` is a native [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) v7.2.151 plugin for a narrow Codex App replay-compatibility case. It is disabled by default.

## What it does

Before CPA selects credentials, and only for `SourceFormat: openai-response`, the plugin examines a Responses API `input` array. It replaces an item only when all of these are true:

- `type` is `function_call_output`;
- `namespace` is `codex_app`;
- `name` is `create_thread` or `send_message_to_thread`;
- `output` is a JSON value (or is missing); and
- `call_id` is missing or empty, or no preceding unpaired `function_call` in this request has the same non-empty `call_id`.

The replacement is an ordinary `role: user`, `type: message` item with one `input_text` part. Its text begins with an explicit source label, followed by the original output string. The plugin never creates a tool call, call ID, tool name, or any other tool state. It is the fallback for `codex_app.create_thread` / `send_message_to_thread` outputs that reach this interceptor through dynamic or host transport. `X-Openai-Subagent: collab_spawn` is an upstream native-spawn scoping signal, not a plugin entry condition: missing, `collab_spawn`, and other values all follow the same fallback path.

This protects paired and parallel tool-call history: each preceding `function_call` can keep one output with its `call_id` as-is. A second output with that same ID, or an output that precedes its call, is downgraded, avoiding invalid tool results. String output is preserved as text; other JSON output is preserved as JSON text, and a missing `output` becomes `null`. The plugin leaves all non-allowlisted namespaces/names, custom outputs, malformed request roots or `input`, and non-Responses requests untouched. Streaming request shape does not change this rule.

中文要点：这是 `codex_app.create_thread` / `send_message_to_thread` 的 fallback；`X-Openai-Subagent: collab_spawn` 只约束上游 native spawn 范围，不是插件入口。无 header、`collab_spawn` 或其他值都会走同一目标路径；默认关闭，每个已配对调用只保留一个同 ID 输出。

## Threat boundary

This is a best-effort, stateless compatibility rewrite, not a general tool-history repairer or an authorization control. It does not inspect credentials, persist state, log request bodies, thread IDs, call IDs, or credentials, and it cannot reject a request. It declares only CPA's `request_interceptor` capability; its after-auth callback is a no-op. It preserves non-string JSON output only as text; it does not structurally convert object, array, or multimodal content.

Review the source and release checksum before enabling it. The plugin is unaffiliated with OpenAI, Codex, or CLIProxyAPI; it is an independent community project.

## Compatibility

- CPA: **v7.2.151 only**
- Plugin ABI: v1; RPC schema: **5**
- Release v0.2.0: macOS/Darwin arm64 and Linux amd64

Windows and Intel macOS artifacts are not built or published.

## Install

1. Download the platform ZIP and `checksums.txt` from the `v0.2.0` release:

   - macOS Apple Silicon: `codex-app-multisession-compat_darwin_arm64.zip`
   - Linux x86_64: `codex-app-multisession-compat_linux_amd64.zip`
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

   On Linux amd64, use the matching directory and artifact instead:

   ```bash
   mkdir -p plugins/linux/amd64
   unzip -j codex-app-multisession-compat_linux_amd64.zip \
     -d plugins/linux/amd64
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

Use CPA's management API/UI after the reload. `GET /v0/management/plugins` should report the plugin as discovered, registered, and effectively enabled. Its artifact name is `codex-app-multisession-compat.dylib` on Darwin and `codex-app-multisession-compat.so` on Linux.

To disable it without deleting the binary, set `plugins.configs.codex-app-multisession-compat.enabled: false` (or use CPA's `PATCH /v0/management/plugins/codex-app-multisession-compat/enabled` endpoint), then reload CPA. This makes the feature inactive immediately on the next configuration application.

## Uninstall

First disable the plugin and reload CPA. Then remove only this artifact from the configured CPA plugin directory:

```bash
rm plugins/darwin/arm64/codex-app-multisession-compat.dylib
```

Remove the corresponding `plugins.configs.codex-app-multisession-compat` block if it is no longer wanted, then reload CPA again.

## Build and test

Requires Go 1.26 and a CPA v7.2.151-compatible native toolchain for the target platform.

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
