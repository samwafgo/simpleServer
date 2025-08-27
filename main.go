package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"github.com/andybalholm/brotli"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/xuri/excelize/v2"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 全局变量：定时器间隔时间
var tickerInterval = 300 * time.Second

// TCP 服务器函数
func startTCPServer(port string) {
	listen, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Printf("TCP服务器在端口 %s 启动失败: %v\n", port, err)
		return
	}
	defer listen.Close()

	fmt.Printf("在端口 %s 上启动 TCP 服务器\n", port)

	for {
		conn, err := listen.Accept()
		if err != nil {
			fmt.Printf("TCP连接接受失败: %v\n", err)
			continue
		}

		// 为每个连接启动一个goroutine
		go handleTCPConnection(conn)
	}
}

// 处理TCP连接
func handleTCPConnection(conn net.Conn) {
	defer conn.Close()

	clientAddr := conn.RemoteAddr().String()
	fmt.Printf("TCP客户端连接: %s\n", clientAddr)

	// 立即发送连接成功消息
	welcomeMessage := fmt.Sprintf("TCP连接成功! 欢迎 %s，服务器将每秒发送时间信息\n", clientAddr)
	_, err := conn.Write([]byte(welcomeMessage))
	if err != nil {
		fmt.Printf("TCP发送欢迎消息失败: %v\n", err)
		return
	}
	fmt.Printf("TCP发送欢迎消息给 %s: %s", clientAddr, welcomeMessage)

	// 每秒发送当前时间
	ticker := time.NewTicker(tickerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			currentTime := time.Now().Format("2006-01-02 15:04:05")
			message := fmt.Sprintf("TCP时间: %s\n", currentTime)

			_, err := conn.Write([]byte(message))
			if err != nil {
				fmt.Printf("TCP发送失败，客户端 %s 断开连接: %v\n", clientAddr, err)
				return
			}

			fmt.Printf("TCP发送给 %s: %s", clientAddr, message)
		}
	}
}

// UDP 服务器函数
func startUDPServer(port string) {
	addr, err := net.ResolveUDPAddr("udp", ":"+port)
	if err != nil {
		fmt.Printf("UDP地址解析失败: %v\n", err)
		return
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Printf("UDP服务器在端口 %s 启动失败: %v\n", port, err)
		return
	}
	defer conn.Close()

	fmt.Printf("在端口 %s 上启动 UDP 服务器\n", port)

	// 存储客户端地址
	clients := make(map[string]*net.UDPAddr)
	var clientsMutex sync.RWMutex

	// 启动定时发送goroutine
	go func() {
		ticker := time.NewTicker(tickerInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				currentTime := time.Now().Format("2006-01-02 15:04:05")
				message := fmt.Sprintf("UDP时间: %s", currentTime)

				clientsMutex.RLock()
				for clientKey, clientAddr := range clients {
					_, err := conn.WriteToUDP([]byte(message), clientAddr)
					if err != nil {
						fmt.Printf("UDP发送失败，客户端 %s: %v\n", clientKey, err)
					} else {
						fmt.Printf("UDP发送给 %s: %s\n", clientKey, message)
					}
				}
				clientsMutex.RUnlock()
			}
		}
	}()

	// 监听客户端消息
	buffer := make([]byte, 1024)
	for {
		n, clientAddr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			fmt.Printf("UDP读取失败: %v\n", err)
			continue
		}

		clientKey := clientAddr.String()
		message := string(buffer[:n])

		// 检查是否为新客户端
		clientsMutex.Lock()
		isNewClient := false
		if _, exists := clients[clientKey]; !exists {
			clients[clientKey] = clientAddr
			isNewClient = true
			fmt.Printf("UDP新客户端连接: %s\n", clientKey)
		}
		clientsMutex.Unlock()

		// 如果是新客户端，立即发送欢迎消息
		if isNewClient {
			welcomeMessage := fmt.Sprintf("UDP连接成功! 欢迎 %s，服务器将每秒发送时间信息", clientKey)
			_, err := conn.WriteToUDP([]byte(welcomeMessage), clientAddr)
			if err != nil {
				fmt.Printf("UDP发送欢迎消息失败，客户端 %s: %v\n", clientKey, err)
			} else {
				fmt.Printf("UDP发送欢迎消息给 %s: %s\n", clientKey, welcomeMessage)
			}
		}

		fmt.Printf("UDP收到来自 %s 的消息: %s\n", clientKey, message)
	}
}

// Web服务器函数
func startWebServer(port string, protocolType string, longTime bool) {
	// 创建设置Server头的中间件
	setServerHeader := func() gin.HandlerFunc {
		return func(c *gin.Context) {
			c.Writer.Header().Set("Server", "SamWaf TestServer")
			c.Writer.Header().Set("X-Powered-By", "Net")
			c.Next()
		}
	}

	// WebSocket升级器
	var upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(r *http.Request) bool {
			return true // 允许所有来源
		},
	}

	// 创建 Gin 路由
	r := gin.Default()
	// 应用中间件
	r.Use(setServerHeader())

	// 定义路由，返回端口号并打印请求和响应信息
	r.GET("/", func(c *gin.Context) {
		requestInfo := gin.H{
			"method":  c.Request.Method,
			"url":     c.Request.URL.String(),
			"host":    c.Request.Host,
			"headers": c.Request.Header,
		}
		responseData := gin.H{
			"port": port,
			"敏感词0": "小额贷款", //测试敏感词
		}

		// 打印请求信息
		fmt.Printf("请求信息: %+v\n", requestInfo)

		// 返回响应
		c.JSON(200, responseData)

		// 打印响应信息
		fmt.Printf("响应信息: %+v\n", responseData)

		if longTime {
			fmt.Printf("准备休眠: 300s \n")
			//给一个长久的时间sleep
			time.Sleep(time.Duration(300) * time.Second)
		}
	})

	// 添加支持POST的接口
	r.POST("/postdata", func(c *gin.Context) {
		// 读取请求体
		bodyBytes, err := ioutil.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(500, gin.H{"error": "读取请求体失败", "details": err.Error()})
			return
		}

		// 将请求体转换为字符串
		bodyString := string(bodyBytes)

		// 打印请求信息
		fmt.Printf("收到POST请求: %s\n", c.Request.URL.String())
		fmt.Printf("请求体内容: %s\n", bodyString)

		// 返回请求体内容
		c.String(200, bodyString)

		// 打印响应信息
		fmt.Printf("已将请求体内容返回给客户端\n")
	})

	// 如果是WebSocket协议，添加WebSocket处理路由
	if protocolType == "ws" {
		r.GET("/ws", func(c *gin.Context) {
			conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
			if err != nil {
				fmt.Printf("WebSocket升级失败: %v\n", err)
				return
			}
			defer conn.Close()

			fmt.Printf("WebSocket客户端已连接: %s\n", c.Request.RemoteAddr)

			// 创建一个定时器，每秒发送一次消息
			ticker := time.NewTicker(1 * time.Second)
			defer ticker.Stop()

			// 创建一个通道用于通知发送 goroutine 停止
			done := make(chan struct{})
			defer close(done)

			// 在另一个goroutine中处理发送消息
			go func() {
				for {
					select {
					case <-ticker.C:
						err := conn.WriteMessage(websocket.TextMessage, []byte("samwaf hello"))
						if err != nil {
							fmt.Printf("发送消息失败: %v\n", err)
							return
						}
						fmt.Printf("已向 %s 发送消息: samwaf hello\n", c.Request.RemoteAddr)
					case <-done:
						// 收到停止信号，结束 goroutine
						fmt.Printf("客户端 %s 断开连接，停止发送消息\n", c.Request.RemoteAddr)
						return
					}
				}
			}()

			// 保持连接并读取消息
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
						fmt.Printf("读取消息错误: %v\n", err)
					} else {
						fmt.Printf("客户端 %s 断开连接\n", c.Request.RemoteAddr)
					}
					break
				}
				fmt.Printf("收到消息: %s\n", message)
			}
		})
	}

	// 添加 Server-Sent Events (event-stream) 路由
	r.POST("/events", func(c *gin.Context) {
		// 设置响应头
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")

		// 清空缓冲区
		c.Writer.Flush()

		// 创建一个通道，用于检测客户端是否断开连接
		clientGone := c.Request.Context().Done()

		// 创建一个定时器，每秒发送一次消息
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		fmt.Printf("SSE 客户端已连接: %s\n", c.Request.RemoteAddr)

		// 循环发送事件
		for {
			select {
			case <-ticker.C:
				// 构建 SSE 消息格式
				// 格式: data: message\n\n
				fmt.Fprintf(c.Writer, "data: samwaf hello event-stream\n\n")
				c.Writer.Flush()
				fmt.Printf("已向 SSE 客户端 %s 发送消息: samwaf hello event-stream\n", c.Request.RemoteAddr)
			case <-clientGone:
				// 客户端断开连接
				fmt.Printf("SSE 客户端 %s 断开连接\n", c.Request.RemoteAddr)
				return
			}
		}
	})

	// 添加新路由 /gettext 用于加载 demo.txt 文件
	r.GET("/gettext", func(c *gin.Context) {
		// 获取当前工作目录
		currentDir, err := os.Getwd()
		if err != nil {
			c.JSON(500, gin.H{"error": "无法获取当前工作目录", "details": err.Error()})
			return
		}

		// 构建 demo.txt 的完整路径
		filePath := filepath.Join(currentDir, "demo.txt")

		// 检查文件是否存在
		_, err = os.Stat(filePath)
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"error": "demo.txt 文件不存在"})
			return
		}

		// 读取文件内容
		content, err := ioutil.ReadFile(filePath)
		if err != nil {
			c.JSON(500, gin.H{"error": "无法读取文件", "details": err.Error()})
			return
		}

		// 使用gzip压缩内容
		var compressedData bytes.Buffer
		gzipWriter := gzip.NewWriter(&compressedData)
		_, err = gzipWriter.Write(content)
		if err != nil {
			c.JSON(500, gin.H{"error": "压缩内容失败", "details": err.Error()})
			return
		}
		err = gzipWriter.Close()
		if err != nil {
			c.JSON(500, gin.H{"error": "关闭gzip写入器失败", "details": err.Error()})
			return
		}

		// 设置响应头，表明内容已被gzip压缩
		c.Writer.Header().Set("Content-Encoding", "gzip")

		// 直接返回压缩后的内容
		c.Data(200, "text/plain", compressedData.Bytes())

		// 打印响应信息
		fmt.Printf("已读取文件 %s 并返回gzip压缩内容\n", filePath)
	})

	// 添加新路由 /gettextbr 用于加载 demo.txt 文件并使用 Brotli 压缩
	r.GET("/gettextbr", func(c *gin.Context) {
		// 获取当前工作目录
		currentDir, err := os.Getwd()
		if err != nil {
			c.JSON(500, gin.H{"error": "无法获取当前工作目录", "details": err.Error()})
			return
		}

		// 构建 demo.txt 的完整路径
		filePath := filepath.Join(currentDir, "demo.txt")

		// 检查文件是否存在
		_, err = os.Stat(filePath)
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"error": "demo.txt 文件不存在"})
			return
		}

		// 读取文件内容
		content, err := ioutil.ReadFile(filePath)
		if err != nil {
			c.JSON(500, gin.H{"error": "无法读取文件", "details": err.Error()})
			return
		}

		// 使用 Brotli 压缩内容
		var compressedData bytes.Buffer
		brotliWriter := brotli.NewWriter(&compressedData)
		_, err = brotliWriter.Write(content)
		if err != nil {
			c.JSON(500, gin.H{"error": "Brotli压缩内容失败", "details": err.Error()})
			return
		}
		err = brotliWriter.Close()
		if err != nil {
			c.JSON(500, gin.H{"error": "关闭Brotli写入器失败", "details": err.Error()})
			return
		}

		// 设置响应头，表明内容已被 Brotli 压缩
		c.Writer.Header().Set("Content-Encoding", "br")
		c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		// 直接返回压缩后的内容
		c.Data(200, "text/plain", compressedData.Bytes())

		// 打印响应信息
		fmt.Printf("已读取文件 %s 并返回Brotli压缩内容\n", filePath)
	})

	// 添加新路由 /gettextgbk 用于加载 demo.txt 文件并返回GBK编码
	r.GET("/gettextgbk", func(c *gin.Context) {
		// 获取当前工作目录
		currentDir, err := os.Getwd()
		if err != nil {
			c.JSON(500, gin.H{"error": "无法获取当前工作目录", "details": err.Error()})
			return
		}

		// 构建 demo.txt 的完整路径
		filePath := filepath.Join(currentDir, "demo.txt")

		// 检查文件是否存在
		_, err = os.Stat(filePath)
		if os.IsNotExist(err) {
			c.JSON(404, gin.H{"error": "demo.txt 文件不存在"})
			return
		}

		// 读取文件内容
		content, err := ioutil.ReadFile(filePath)
		if err != nil {
			c.JSON(500, gin.H{"error": "无法读取文件", "details": err.Error()})
			return
		}

		// 设置响应头
		c.Writer.Header().Set("Content-Type", "text/html; charset=GBK")
		c.Writer.Header().Set("Pragma", "no-cache")
		c.Writer.Header().Set("Cache-Control", "no-store")
		c.Writer.Header().Set("Set-Cookie", "JSESSIONID=1cpudox7br7hm16jsfo61gwmew;Path=/")
		c.Writer.Header().Set("Expires", "Thu, 01 Jan 1970 00:00:00 GMT")
		c.Writer.Header().Set("Transfer-Encoding", "chunked")

		// 直接返回原始内容
		c.Data(200, "", content)

		// 打印响应信息
		fmt.Printf("已读取文件 %s 并返回GBK编码内容\n", filePath)
	})
	// 添加新路由 /getjsonnotcharset  不包含 charset=utf-8
	r.GET("/getjsonnotcharset", func(c *gin.Context) {
		// 设置响应头
		c.Writer.Header().Set("Content-Type", "application/json")
		// 创建响应数据
		responseData := gin.H{
			"content": "你好test123",
		}
		// 手动编码为JSON
		jsonData, err := json.Marshal(responseData)
		if err != nil {
			c.JSON(500, gin.H{"error": "JSON编码失败", "details": err.Error()})
			return
		}

		// 直接返回JSON数据
		c.Writer.WriteHeader(200)
		c.Writer.Write(jsonData)

	})
	// 添加新路由 /.well-known/acme-challenge/2NKiiETgQdPmmjlM88mH5uo6jM98PrgWwsDslaN8
	r.GET("/.well-known/acme-challenge/2NKiiETgQdPmmjlM88mH5uo6jM98PrgWwsDslaN8", func(c *gin.Context) {
		// 设置响应头
		c.Writer.Header().Set("Content-Type", "application/json")
		// 创建响应数据
		responseData := gin.H{
			"content": "这是验证",
		}
		// 手动编码为JSON
		jsonData, err := json.Marshal(responseData)
		if err != nil {
			c.JSON(500, gin.H{"error": "JSON编码失败", "details": err.Error()})
			return
		}

		// 直接返回JSON数据
		c.Writer.WriteHeader(200)
		c.Writer.Write(jsonData)

	})
	// 添加新路由 /xls 返回真正的 Excel 文件
	r.GET("/xls", func(c *gin.Context) {
		// 创建一个新的 Excel 文件
		f := excelize.NewFile()
		defer func() {
			if err := f.Close(); err != nil {
				fmt.Println(err)
			}
		}()

		// 设置工作表名称
		sheetName := "Sheet1"

		// 设置标题行
		f.SetCellValue(sheetName, "A1", "标题")
		f.SetCellValue(sheetName, "B1", "功能")

		// 设置内容行
		f.SetCellValue(sheetName, "A2", "测试内容")
		f.SetCellValue(sheetName, "B2", "测试功能")

		// 设置标题行样式
		style, err := f.NewStyle(&excelize.Style{
			Font: &excelize.Font{
				Bold: true,
				Size: 12,
			},
			Fill: excelize.Fill{
				Type:    "pattern",
				Color:   []string{"#E0E0E0"},
				Pattern: 1,
			},
		})
		if err != nil {
			c.JSON(500, gin.H{"error": "创建样式失败", "details": err.Error()})
			return
		}

		// 应用样式到标题行
		f.SetCellStyle(sheetName, "A1", "B1", style)

		// 设置列宽
		f.SetColWidth(sheetName, "A", "B", 15)

		// 将 Excel 文件保存到内存缓冲区
		buf, err := f.WriteToBuffer()
		if err != nil {
			c.JSON(500, gin.H{"error": "生成Excel文件失败", "details": err.Error()})
			return
		}

		// 获取文件大小
		fileSize := buf.Len()

		// 设置响应头
		c.Writer.Header().Set("Cache-Control", "no-cache, no-store, max-age=0, must-revalidate")
		c.Writer.Header().Set("Content-Disposition", "attachment;filename=%E5%B7%A5%E4%BD%9C%E7%B0%BF1.xlsx")
		c.Writer.Header().Set("Content-Length", strconv.Itoa(fileSize))
		c.Writer.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")

		// 返回 Excel 文件
		c.Data(200, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", buf.Bytes())

		// 打印响应信息
		fmt.Printf("已返回 Excel 文件下载，文件大小: %d 字节\n", fileSize)
	})

	if port != "longtime" {
		// 启动服务器
		fmt.Printf("在端口 %s 上启动 %s 服务器\n", port, strings.ToUpper(protocolType))
		err := r.Run(":" + port)
		if err != nil {
			fmt.Printf("服务器在端口 %s 启动失败: %v\n", port, err)
		}
	}
}

func main() {
	// 从命令行参数获取端口号
	if len(os.Args) < 2 {
		fmt.Println("请提供一个或多个端口号")
		fmt.Println("用法: ./simpleServer [端口号...] [协议类型(可选,http/ws/tcp/udp,默认http)]")
		return
	}

	// 检查最后一个参数是否为协议类型
	args := os.Args[1:]
	protocolType := "http" // 默认为HTTP协议

	if len(args) > 0 && (args[len(args)-1] == "http" || args[len(args)-1] == "ws" || args[len(args)-1] == "tcp" || args[len(args)-1] == "udp") {
		protocolType = args[len(args)-1]
		args = args[:len(args)-1] // 移除协议类型参数
	}

	// 如果没有端口号，则显示错误
	if len(args) == 0 {
		fmt.Println("请提供至少一个端口号")
		return
	}

	ports := args

	var wg sync.WaitGroup
	longTime := false

	// 为每个端口创建一个服务
	for _, port := range ports {
		wg.Add(1)
		go func(port string) {
			if port == "longtime" {
				longTime = true
			}
			defer wg.Done()

			// 根据协议类型启动不同的服务器
			switch protocolType {
			case "tcp":
				startTCPServer(port)
			case "udp":
				startUDPServer(port)
			default: // http 和 ws
				startWebServer(port, protocolType, longTime)
			}
		}(port)
	}

	// 等待所有 goroutine 完成
	wg.Wait()
}
