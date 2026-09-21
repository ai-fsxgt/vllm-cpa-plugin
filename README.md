# vLLM 接入 CPA 的协议兼容插件

用于补充 CLIProxyAPI（CPA）在 Claude Messages API 与 OpenAI 兼容接口之间的参数和响应兼容处理，主要面向 vLLM 上游。

## 支持功能

- 修复 Claude → OpenAI 请求中没有有效 `tools`，却仍携带 `tool_choice` 导致的请求校验错误。
- 清理无工具请求中无效的 `tool_choice`、`parallel_tool_calls` 和空 `tools`。
- 将 OpenAI/vLLM 响应中的 `reasoning` 补充为 CPA Claude 响应转换需要的 `reasoning_content`，支持普通响应和流式响应。
- 在非流式 Claude 响应中，将 `thinking` 和 `redacted_thinking` 内容块排列在正文及工具调用之前。
- 仅处理 Claude → OpenAI 请求和 OpenAI → Claude 响应，不修改其他协议转换流程。

## 安装

当前发布产物适用于 Linux AMD64。

1. 停止 CPA。
2. 下载插件到 CPA 配置的插件目录，默认目录为 `plugins`：

```bash
mkdir -p plugins
curl -fL \
  https://github.com/ai-fsxgt/vllm-cpa-plugin/releases/latest/download/vllm-reasoning-normalizer.so \
  -o plugins/vllm-reasoning-normalizer.so
```

3. 在 CPA 的 `config.yaml` 中启用插件：

```yaml
plugins:
  enabled: true
  dir: "plugins"
  configs:
    vllm-reasoning-normalizer:
      enabled: true
      priority: 1
```

4. 启动 CPA。启动日志中没有插件加载错误即可生效。

插件 ID 来自文件名，因此文件必须命名为 `vllm-reasoning-normalizer.so`，并与 `plugins.configs.vllm-reasoning-normalizer` 保持一致。

## 更新插件后如何生效

CPA 会将 `.so` 插件加载到进程内。直接覆盖同一路径的文件不会让正在运行的 CPA 自动使用新版本，必须重启 CPA：

1. 停止 CPA。
2. 使用上面的下载命令覆盖 `plugins/vllm-reasoning-normalizer.so`。
3. 重新启动 CPA。

如果使用 Docker，请先停止容器，再替换宿主机挂载到 CPA `plugins` 目录的文件，最后重新启动：

```bash
docker compose stop
# 替换宿主机 plugins/vllm-reasoning-normalizer.so
docker compose up -d
```

无需修改原有插件配置。
