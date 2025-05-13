package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func main() {
	// 从命令行参数获取端口号
	if len(os.Args) < 2 {
		fmt.Println("请提供一个或多个端口号")
		fmt.Println("用法: ./simpleServer [端口号...] [协议类型(可选,http/ws,默认http)]")
		return
	}

	// 检查最后一个参数是否为协议类型
	args := os.Args[1:]
	protocolType := "http" // 默认为HTTP协议

	if len(args) > 0 && (args[len(args)-1] == "http" || args[len(args)-1] == "ws") {
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

	// 为每个端口创建一个服务
	for _, port := range ports {
		wg.Add(1)
		go func(port string) {
			if port == "longtime" {
				longTime = true
			}
			defer wg.Done()
			// 创建 Gin 路由
			r := gin.Default()
			// 应用中间件
			r.Use(setServerHeader())

			// 定义路由，返回端口号并打印请求和响应信息
			r.GET("/", func(c *gin.Context) {
				requestInfo := gin.H{
					"method":  c.Request.Method,
					"url":     c.Request.URL.String(),
					"headers": c.Request.Header,
				}
				responseData := gin.H{
					"port": port,
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
			r.GET("/events", func(c *gin.Context) {
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
						fmt.Fprintf(c.Writer, "data: samwaf hello\n\n")
						c.Writer.Flush()
						fmt.Printf("已向 SSE 客户端 %s 发送消息: samwaf hello\n", c.Request.RemoteAddr)
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

			if port != "longtime" {
				// 启动服务器
				fmt.Printf("在端口 %s 上启动 %s 服务器\n", port, strings.ToUpper(protocolType))
				err := r.Run(":" + port)
				if err != nil {
					fmt.Printf("服务器在端口 %s 启动失败: %v\n", port, err)
				}
			}
		}(port)
	}

	// 等待所有 goroutine 完成
	wg.Wait()
}
