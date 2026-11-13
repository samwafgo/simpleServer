package main

// OpenAI 兼容接口，用于测试 SamWaf 对流式响应的转发。
//
// 直接复现 SamWaf issue #949 / #954：反代 text/event-stream 时，客户端收到首段内容后
// 连接在 response.completed 之前被断开，Codex 报
// "stream disconnected before completion: stream closed before response.completed"。
//
// 事件结构照 Codex 的 SSE 解析器实现（codex-rs/codex-api/src/sse/responses.rs），
// 因此不需要真模型、不花钱就能让 Codex 真正跑起来：
//   response.created / response.output_item.added / response.output_text.delta ...
//   → response.output_text.done / content_part.done / output_item.done / response.completed
//
// 复现原理：末尾那 4 条事件都携带**完整正文**，单条体积超过反代读缓冲(32KB)；
// 中间的 delta 事件只有几 KB，永远踩不到 —— 所以现象是"短回答正常、长回答必断"。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const mockModelName = "samwaf-mock"

// lenOverrideRe 允许在对话内容里写 len:200000 直接指定本次正文长度，
// 方便用 Codex 等真实客户端测试时不改配置就能调长度。
var lenOverrideRe = regexp.MustCompile(`len:(\d{2,8})`)

// registerOpenAIRoutes 挂载 OpenAI 兼容路由
func registerOpenAIRoutes(r *gin.Engine) {
	r.POST("/v1/responses", handleResponses)
	r.GET("/v1/responses", handleResponses)
	r.POST("/v1/chat/completions", handleChatCompletions)
	r.GET("/v1/models", handleModels)
	r.GET("/v1/models/:model", handleModelDetail)
}

// parseIntQuery 读取整型查询参数，缺失/非法/越界时回落到默认值
func parseIntQuery(c *gin.Context, name string, def, lo, hi int) int {
	v, err := strconv.Atoi(c.Query(name))
	if err != nil || v < lo || v > hi {
		return def
	}
	return v
}

// mockOptions 本次请求的模拟参数
type mockOptions struct {
	Size  int // 正文目标字节数
	Delta int // delta 事件条数
	Gap   int // 事件之间的间隔毫秒
}

// readMockOptions 参数优先级：查询串 > 请求体里的 len:N > 默认值。
// Codex 打过来是 POST /v1/responses 且没有查询串，所以留了 len:N 这条口子。
func readMockOptions(c *gin.Context, rawBody string) mockOptions {
	opt := mockOptions{
		Size:  parseIntQuery(c, "size", 100000, 1, 8*1024*1024),
		Delta: parseIntQuery(c, "delta", 20, 1, 500),
		Gap:   parseIntQuery(c, "gap", 30, 0, 5000),
	}
	if c.Query("size") == "" {
		if m := lenOverrideRe.FindStringSubmatch(rawBody); m != nil {
			if v, err := strconv.Atoi(m[1]); err == nil && v > 0 && v <= 8*1024*1024 {
				opt.Size = v
			}
		}
	}
	return opt
}

// buildMockText 造一段带行号的正文，长度不小于 size。
// 带行号是为了：一旦被截断，肉眼就能看出断在第几行、丢了多少。
func buildMockText(size int) string {
	var sb strings.Builder
	for i := 1; sb.Len() < size; i++ {
		fmt.Fprintf(&sb, "第 %06d 行 —— SamWaf SSE 长回答测试正文，用于验证反向代理是否会截断流式响应。\n", i)
	}
	return sb.String()
}

// splitIntoChunks 把正文切成 n 段，切点对齐到 UTF-8 字符边界
func splitIntoChunks(text string, n int) []string {
	runes := []rune(text)
	if n < 1 {
		n = 1
	}
	if n > len(runes) {
		n = len(runes)
	}
	per := len(runes) / n
	if per < 1 {
		per = 1
	}
	chunks := make([]string, 0, n)
	for i := 0; i < len(runes); i += per {
		end := i + per
		if end > len(runes) || len(chunks) == n-1 {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
		if end == len(runes) {
			break
		}
	}
	return chunks
}

// bodyReadTimeout 读请求体的上限时长。
// 反向代理如果声明了 Content-Length 却没把 body 发全，io.ReadAll 会永远等下去，
// 表现就是"后端收到请求头之后再无动静、客户端一直转圈"。这里宁可超时也不死等，
// 并把「声明了多少 / 实际收到多少」打出来，一眼就能判断是不是被中途截断了。
const bodyReadTimeout = 10 * time.Second

// readRawBody 读请求体原文，并打印收发字节对账。
// 必须整体读干净：Codex 的请求体带着完整对话与工具定义，动辄几十上百 KB，
// 只调一次 Read 既拿不全内容，也会让连接留着未读数据无法复用。
func readRawBody(c *gin.Context) string {
	if c.Request.Body == nil {
		return ""
	}
	const maxKeep = 8 << 20
	declared := c.Request.ContentLength

	// 给读操作设个截止时间，避免上游少发字节时永久阻塞
	rc := http.NewResponseController(c.Writer)
	deadlineOK := rc.SetReadDeadline(time.Now().Add(bodyReadTimeout)) == nil

	start := time.Now()
	data, err := io.ReadAll(io.LimitReader(c.Request.Body, maxKeep))
	cost := time.Since(start)

	if deadlineOK {
		_ = rc.SetReadDeadline(time.Time{})
	}

	switch {
	case err != nil && declared > 0 && int64(len(data)) < declared:
		fmt.Printf("[请求体] ⚠ 声明 Content-Length=%d，实际只收到 %d 字节（耗时 %v）后出错: %v\n"+
			"         → 上游(反向代理)没把请求体发全，这就是后端卡住的原因\n",
			declared, len(data), cost.Round(time.Millisecond), err)
	case err != nil:
		fmt.Printf("[请求体] 读取出错: %v（已收到 %d 字节）\n", err, len(data))
	case declared > 0 && int64(len(data)) != declared:
		fmt.Printf("[请求体] ⚠ 声明 Content-Length=%d，实际收到 %d 字节，对不上\n", declared, len(data))
	default:
		fmt.Printf("[请求体] 收到 %d 字节（耗时 %v）\n", len(data), cost.Round(time.Millisecond))
	}

	// 读空剩余，避免连接残留未读数据
	if n, _ := io.Copy(io.Discard, c.Request.Body); n > 0 {
		fmt.Printf("[请求体] 超过 %d 字节上限，多余 %d 字节已丢弃\n", maxKeep, n)
	}
	return string(data)
}

// isStreamRequested 请求体里 "stream": false 时走非流式
func isStreamRequested(rawBody string) bool {
	return !strings.Contains(strings.ReplaceAll(rawBody, " ", ""), `"stream":false`)
}

// ── /v1/responses（Codex 走这个）────────────────────────────────────────────

func handleResponses(c *gin.Context) {
	fmt.Printf("[/v1/responses] 收到请求 %s，开始读请求体...\n", c.Request.RemoteAddr)
	rawBody := readRawBody(c)
	opt := readMockOptions(c, rawBody)
	text := buildMockText(opt.Size)

	respID := fmt.Sprintf("resp_%d", time.Now().UnixNano())
	itemID := fmt.Sprintf("msg_%d", time.Now().UnixNano())
	createdAt := time.Now().Unix()

	// 最终的 message item —— 末尾几条大事件都带着它，这正是复现的关键
	finalItem := map[string]any{
		"type":    "message",
		"id":      itemID,
		"role":    "assistant",
		"status":  "completed",
		"content": []any{map[string]any{"type": "output_text", "text": text, "annotations": []any{}}},
	}
	usage := map[string]any{
		"input_tokens":          12,
		"input_tokens_details":  map[string]any{"cached_tokens": 0},
		"output_tokens":         len(text) / 4,
		"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		"total_tokens":          12 + len(text)/4,
	}
	responseObj := func(status string, output []any) map[string]any {
		o := map[string]any{
			"id": respID, "object": "response", "created_at": createdAt,
			"status": status, "model": mockModelName, "output": output,
		}
		if status == "completed" {
			o["usage"] = usage
		}
		return o
	}

	// 非流式：直接返回完整 response 对象
	if !isStreamRequested(rawBody) {
		c.JSON(http.StatusOK, responseObj("completed", []any{finalItem}))
		return
	}

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	fmt.Printf("[/v1/responses] %s 连接: size=%d(实际正文 %d 字节) delta=%d gap=%dms\n",
		c.Request.RemoteAddr, opt.Size, len(text), opt.Delta, opt.Gap)

	sent := 0
	send := func(eventType string, payload map[string]any) bool {
		payload["type"] = eventType
		data, err := json.Marshal(payload)
		if err != nil {
			fmt.Printf("[/v1/responses] 序列化失败: %v\n", err)
			return false
		}
		n, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventType, data)
		if err != nil {
			fmt.Printf("[/v1/responses] 写出失败(客户端已断开): %v\n", err)
			return false
		}
		sent += n
		c.Writer.Flush()
		if opt.Gap > 0 {
			time.Sleep(time.Duration(opt.Gap) * time.Millisecond)
		}
		return true
	}

	// ① 开场
	if !send("response.created", map[string]any{"response": responseObj("in_progress", []any{})}) {
		return
	}
	if !send("response.in_progress", map[string]any{"response": responseObj("in_progress", []any{})}) {
		return
	}
	if !send("response.output_item.added", map[string]any{
		"output_index": 0,
		"item": map[string]any{
			"type": "message", "id": itemID, "role": "assistant",
			"status": "in_progress", "content": []any{},
		},
	}) {
		return
	}
	if !send("response.content_part.added", map[string]any{
		"item_id": itemID, "output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
	}) {
		return
	}

	// ② 逐段吐正文（每条都远小于 32KB，永远不触发问题）
	for _, chunk := range splitIntoChunks(text, opt.Delta) {
		if !send("response.output_text.delta", map[string]any{
			"item_id": itemID, "output_index": 0, "content_index": 0, "delta": chunk,
		}) {
			return
		}
	}

	// ③ 收尾 —— 这 4 条都携带完整正文，单条超过 32KB，正是 #954 断在这里
	if !send("response.output_text.done", map[string]any{
		"item_id": itemID, "output_index": 0, "content_index": 0, "text": text,
	}) {
		return
	}
	if !send("response.content_part.done", map[string]any{
		"item_id": itemID, "output_index": 0, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
	}) {
		return
	}
	if !send("response.output_item.done", map[string]any{
		"output_index": 0, "item": finalItem,
	}) {
		return
	}
	if !send("response.completed", map[string]any{
		"response": responseObj("completed", []any{finalItem}),
	}) {
		return
	}

	fmt.Printf("[/v1/responses] 已完整下发 %d 字节（含 response.completed）\n", sent)
}

// ── /v1/chat/completions（Chat 协议，给 Cherry Studio / ChatBox / one-api 之类用）──

func handleChatCompletions(c *gin.Context) {
	rawBody := readRawBody(c)
	opt := readMockOptions(c, rawBody)
	text := buildMockText(opt.Size)

	chatID := fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	createdAt := time.Now().Unix()

	if !isStreamRequested(rawBody) {
		c.JSON(http.StatusOK, map[string]any{
			"id": chatID, "object": "chat.completion", "created": createdAt, "model": mockModelName,
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": text},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": len(text) / 4, "total_tokens": 12 + len(text)/4},
		})
		return
	}

	// Chat 协议天然只有小 chunk，不会自然复现截断；
	// bigfinal=1 时把全文塞进最后一个 chunk，用来构造同款大事件。
	bigFinal := c.Query("bigfinal") == "1"

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)
	c.Writer.Flush()

	fmt.Printf("[/v1/chat/completions] %s 连接: 正文 %d 字节 delta=%d gap=%dms bigfinal=%v\n",
		c.Request.RemoteAddr, len(text), opt.Delta, opt.Gap, bigFinal)

	sent := 0
	sendChunk := func(delta map[string]any, finish any) bool {
		payload := map[string]any{
			"id": chatID, "object": "chat.completion.chunk", "created": createdAt, "model": mockModelName,
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}},
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		n, err := fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		if err != nil {
			fmt.Printf("[/v1/chat/completions] 写出失败(客户端已断开): %v\n", err)
			return false
		}
		sent += n
		c.Writer.Flush()
		if opt.Gap > 0 {
			time.Sleep(time.Duration(opt.Gap) * time.Millisecond)
		}
		return true
	}

	if !sendChunk(map[string]any{"role": "assistant", "content": ""}, nil) {
		return
	}
	if bigFinal {
		if !sendChunk(map[string]any{"content": text}, nil) {
			return
		}
	} else {
		for _, chunk := range splitIntoChunks(text, opt.Delta) {
			if !sendChunk(map[string]any{"content": chunk}, nil) {
				return
			}
		}
	}
	if !sendChunk(map[string]any{}, "stop") {
		return
	}
	if n, err := fmt.Fprintf(c.Writer, "data: [DONE]\n\n"); err == nil {
		sent += n
		c.Writer.Flush()
	}

	fmt.Printf("[/v1/chat/completions] 已完整下发 %d 字节（含 [DONE]）\n", sent)
}

// ── /v1/models ────────────────────────────────────────────────────────────

func modelEntry(id string) map[string]any {
	return map[string]any{"id": id, "object": "model", "created": 1700000000, "owned_by": "samwaf"}
}

func handleModels(c *gin.Context) {
	c.JSON(http.StatusOK, map[string]any{
		"object": "list",
		"data": []any{
			modelEntry(mockModelName),
			modelEntry("gpt-5-codex"),
			modelEntry("gpt-4o"),
		},
	})
}

func handleModelDetail(c *gin.Context) {
	c.JSON(http.StatusOK, modelEntry(c.Param("model")))
}
