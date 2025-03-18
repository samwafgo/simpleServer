package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"github.com/gin-gonic/gin"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"
)

func main() {
	// 从命令行参数获取端口号
	if len(os.Args) < 2 {
		fmt.Println("请提供一个或多个端口号")
		return
	}

	ports := os.Args[1:]

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

			if port != "longtime" {
				// 启动服务器
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
