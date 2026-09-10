# CPA Codex App Multisession Compatibility

`cpa-codex-app-multisession-compat` is a native [CLIProxyAPI (CPA)](https://github.com/router-for-me/CLIProxyAPI) plugin for a narrow Codex App replay-compatibility case. It is disabled by default.

## What it does

Before CPA selects credentials, and only for `SourceFormat: openai-response`, the plugin examines a Responses API `input` array. Request headers, including a missing or non-`collab_spawn` `X-Openai-Subagent` value, do not change this rule. It replaces an item only when all of these are true:

- `type` is `function_call_output`;
- `namespace` is `codex_app`;
- `name` is `create_thread` or `send_message_to_thread`;
- `output` is a JSON value (or is missing); and
- `call_id` is missing or empty; or, when the root has neither a non-empty `previous_response_id` nor `type: "response.append"`, no preceding unpaired `function_call` in this request has the same non-empty `call_id`.

The replacement is an ordinary `role: user`, `type: message` item with one `input_text` part. Its text begins with an explicit source label, followed by the original output string. The plugin never creates a tool call, call ID, tool name, or any other tool state. It is the fallback for `codex_app.create_thread` / `send_message_to_thread` outputs that reach this interceptor. `X-Openai-Subagent: collab_spawn` is an upstream native-spawn scoping signal, not a plugin entry condition: missing, `collab_spawn`, and other values all follow the same fallback path.

For a stateless incremental request, either a non-empty root `previous_response_id` or root `type: "response.append"`, plus a non-empty allowlisted output `call_id`, is conservatively preserved as-is; the plugin does not infer whether that call belongs to earlier history. A real orphan without a `call_id` is still downgraded. Without either continuation marker, the normal same-request paired/parallel boundary applies: each preceding `function_call` can keep one output with its `call_id` as-is, while a second output with that same ID, a stale ID, or an output that precedes its call is downgraded. String output is preserved as text; other JSON output is preserved as JSON text, and a missing `output` becomes `null`. The plugin leaves all non-allowlisted namespaces/names, custom outputs, malformed request roots or `input`, and non-Responses requests untouched. Streaming request shape does not change this rule.

中文要点：这是 `codex_app.create_thread` / `send_message_to_thread` 的 fallback；默认关闭，仅改写 `codex_app` 允许名单内的目标输出。`X-Openai-Subagent: collab_spawn` 只约束上游 native spawn 范围，不是插件入口。无 header、`collab_spawn` 或其他值都会走同一目标路径。根对象有非空 `previous_response_id` 或 `type: "response.append"` 时，带非空 `call_id` 的目标输出保守保持原状；没有 `call_id` 的真实 orphan 仍会降级。两种 continuation 标记都没有时，每个已配对调用只保留一个同 ID 输出。ABI v1 下 schema 协商返回 `min(宿主, 5)`，宿主 schema < 4 拒绝；已验证 CPA 7.2.151/schema5 与 7.2.157/schema6，schema4 有合成测试，未发布宿主不保证全面兼容。

## Threat boundary

This is a best-effort, stateless compatibility rewrite, not a general tool-history repairer or an authorization control. It does not inspect credentials, persist state, log request bodies, thread IDs, call IDs, or credentials, and it cannot reject a request. It declares only CPA's `request_interceptor` capability; its after-auth callback is a no-op. It preserves non-string JSON output only as text; it does not structurally convert object, array, or multimodal content.

Review the source and release checksum before enabling it. The plugin is unaffiliated with OpenAI, Codex, or CLIProxyAPI; it is an independent community project.

## Compatibility

- CPA: verified against **v7.2.151 (plugin schema 5)** and **v7.2.157 (plugin schema 6)**
- Plugin ABI: v1
- Plugin schema: minimum **4**, implemented maximum **5**. `plugin.register` and `plugin.reconfigure` return `min(host schema, 5)` and reject host schema < 4
- Schema 4 lifecycle is covered by synthetic tests. Future schema negotiation does not guarantee full compatibility with unpublished hosts
- Plugin version: v0.2.3 (macOS/Darwin arm64 and Linux amd64 builds)

Windows and Intel macOS artifacts are not built or published.

## Install

1. Obtain a v0.2.3 platform ZIP and matching `checksums.txt` from your approved distribution channel:

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

Requires Go 1.26 and a CPA v7.2.151/v7.2.157-compatible native toolchain for the target platform.

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

The generated C ABI exports registration, call, free-buffer, and shutdown entry points. Tests cover the allowlist, missing/empty/stale/matched call IDs, paired/parallel calls, non-target and non-text outputs, malformed roots/input, idempotence, disabled/source gating, headerless and arbitrary-header rewrite, streaming request shape, and schema negotiation on register/reconfigure.

## License

MIT. See [LICENSE](LICENSE).
