package main

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"os"
	"sync"
)

func main() {
	// 从命令行参数获取端口号
	if len(os.Args) < 2 {
		fmt.Println("请提供一个或多个端口号")
		return
	}

	ports := os.Args[1:]

	var wg sync.WaitGroup

	// 为每个端口创建一个服务
	for _, port := range ports {
		wg.Add(1)
		go func(port string) {
			defer wg.Done()
			// 创建 Gin 路由
			r := gin.Default()

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
			})

			// 启动服务器
			err := r.Run(":" + port)
			if err != nil {
				fmt.Printf("服务器在端口 %s 启动失败: %v\n", port, err)
			}
		}(port)
	}

	// 等待所有 goroutine 完成
	wg.Wait()
}
