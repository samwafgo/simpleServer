# simpleServer

Test SamWaf loadBalance

usage:
simpleServer.exe port

example:

./simpleServer.exe 6001 6002

```
curl 127.0.0.1:6001
{"port":"6001"}
```

./simpleServer.exe longtime 6001 6002

longtime : sleep 300s with per request


websocket:

./simpleServer.exe longtime 6001 6002 ws

 

## OpenAI 兼容接口（SamWaf 流式转发测试 / issue #949 #954）

模拟大模型服务端，用来验证 SamWaf 反代流式响应是否会截断。事件结构照 Codex 的 SSE 解析器
（`codex-rs/codex-api/src/sse/responses.rs`）实现，**不需要真模型、不花钱**就能让 Codex 真跑起来。

| 接口 | 说明 |
|------|------|
| `POST /v1/responses` | OpenAI **Responses API**，Codex 用的就是这个（`wire_api = "responses"`） |
| `GET /v1/responses` | 同上，方便 `curl -N` 直接测 |
| `POST /v1/chat/completions` | OpenAI **Chat Completions**，给 Cherry Studio / ChatBox / one-api 之类用 |
| `GET /v1/models` | 模型列表，供客户端探测 |

请求体带 `"stream": false` 时返回非流式的完整 JSON。

### 参数

| 参数 | 默认 | 说明 |
|------|------|------|
| `size` | 100000 | 正文目标字节数。**>32KB 才会复现截断**，调到 20000 则不复现 |
| `delta` | 20 | `output_text.delta` 事件条数（每条几 KB，永远不触发问题） |
| `gap` | 30 | 事件之间的间隔毫秒，设 0 最快跑完 |
| `bigfinal` | - | 仅 chat/completions：`=1` 把全文塞进最后一个 chunk，构造同款大事件 |

Codex 打过来是 `POST /v1/responses` 且没有查询串，所以另留了一条口子：
**在对话内容里写 `len:250000` 就能指定本次正文长度**。

### 复现原理

一次 `/v1/responses` 的事件序列：

```
response.created / in_progress / output_item.added / content_part.added   ← 都很小
response.output_text.delta  × 20                                          ← 每条约 5KB
response.output_text.done                                                 ← 携带完整正文
response.content_part.done                                                ← 携带完整正文
response.output_item.done                                                 ← 携带完整正文
response.completed                                                        ← 携带完整正文
```

`size=100000` 时**末尾 4 条各约 101KB**，单条就超过反向代理的读缓冲（net/http 默认 32KB）；
中间的 delta 只有 5KB 永远踩不到 —— 所以现象是**短回答正常、长回答必断**。

### curl 验证

```bash
./simpleServer.exe 8111

# 直连基准
curl -s -N "http://127.0.0.1:8111/v1/responses?size=100000&gap=0" -o direct.sse

# 经 SamWaf（站点后端指向 127.0.0.1:8111）
curl -s -N "http://<你的站点>/v1/responses?size=100000&gap=0" -o viawaf.sse

cmp direct.sse viawaf.sse && echo "字节级一致，通过"
```

正文带行号（`第 000123 行 ——`），一旦被截断，肉眼就能看出断在第几行。

### 用真 Codex 测

`~/.codex/config.toml`：

```toml
model = "samwaf-mock"
model_provider = "samwaf_test"

[model_providers.samwaf_test]
name = "via SamWaf"
base_url = "http://<你的站点>/v1"
env_key = "OPENAI_API_KEY"
wire_api = "responses"
```

```bash
export OPENAI_API_KEY=anything      # 本 mock 不校验
codex
> 给我一段 len:250000 的长回答
```

修复前会报 `stream disconnected before completion: stream closed before response.completed`；
修复后能完整跑完。
