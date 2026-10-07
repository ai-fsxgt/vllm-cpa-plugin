# vLLM 接入 CPA 的协议兼容插件

用于补充 CLIProxyAPI（CPA）在 Claude Messages API 与 OpenAI 兼容接口之间的参数和响应兼容处理，主要面向 vLLM 上游。

## 支持功能

- 修复 Claude → OpenAI 请求中没有有效 `tools`，却仍携带 `tool_choice` 导致的请求校验错误。
- 清理无工具请求中无效的 `tool_choice`、`parallel_tool_calls` 和空 `tools`。
- 将 OpenAI/vLLM 响应中的 `reasoning` 补充为 CPA Claude 响应转换需要的 `reasoning_content`，支持普通响应和流式响应。
- 在非流式 Claude 响应中，将 `thinking` 和 `redacted_thinking` 内容块排列在正文及工具调用之前。
- 为未携带签名的思考块补充 Claude 官方结构要求的 `signature` 字段，值为空字符串；该值不代表 Anthropic 签名校验通过。
- 自动移除流式中间数据块中不含缓存明细的临时用量，让 CPA 使用末尾完整用量生成结束事件。
- 将 vLLM 的 `created_cache_tokens` 转换为 CPA 识别的缓存写入字段，最终输出官方 `cache_creation_input_tokens` 和 `cache_read_input_tokens`。
- 仅处理 Claude → OpenAI 请求和 OpenAI → Claude 响应，不修改其他协议转换流程。

## 缓存用量与模型接入约定

缓存用量修复随插件自动生效，不需要模型名单或额外环境变量。符合以下用量约定的模型共用同一条 OpenAI → Claude 转换流程，后续接入新模型无需修改插件。

接入新模型时，请同时核对流式用量，不仅确认接口路径兼容：请求开启 `stream_options.include_usage=true`，上游应在 `[DONE]` 前返回 `choices: []` 的完整 `usage` 数据块。标准 OpenAI 响应的中间 `usage` 为 `null`；vLLM 额外返回的不含缓存明细的连续用量会由插件清理。只在带 `choices` 的数据块中返回用量、没有独立完整用量数据块的服务，不符合本插件的接入约定。规范见 [OpenAI 官方流式响应文档](https://developers.openai.com/api/reference/resources/chat/subresources/completions/streaming-events#chat.completion.chunk)。

缓存读取使用上游真实返回的 `prompt_tokens_details.cached_tokens`；缓存写入使用标准 `cache_write_tokens`，vLLM 的 `created_cache_tokens` 会自动映射为该字段。vLLM 需开启前缀缓存及用量明细，插件不估算或补造缓存统计。

转换后的 Claude 用量遵循：

```text
总输入 token = input_tokens + cache_read_input_tokens + cache_creation_input_tokens
```

`input_tokens` 不包含缓存读取和写入 token；OpenAI/vLLM 的 `prompt_tokens_details` 等字段不会直接暴露在 Claude 响应中。字段定义见 [Claude 官方缓存文档](https://platform.claude.com/docs/en/build-with-claude/prompt-caching)，事件格式见 [Claude 官方流式文档](https://platform.claude.com/docs/en/build-with-claude/streaming)。

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

原有插件配置保持不变，缓存用量修复自动生效。更新插件后重启 CPA 即可。
