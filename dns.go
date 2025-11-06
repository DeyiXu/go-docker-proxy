package main

import (
	"context"
	"log"
	"net"
	"time"
)

// initCustomDNS 初始化自定义DNS解析器
func initCustomDNS(config *Config) {
	if !config.DNSEnabled {
		log.Println("使用系统默认DNS解析器")
		return
	}

	if len(config.DNSServers) == 0 {
		log.Println("DNS_ENABLED=true 但未配置 DNS_SERVERS，使用系统默认DNS")
		return
	}

	// 解析超时时间
	timeout, err := time.ParseDuration(config.DNSTimeout)
	if err != nil {
		log.Printf("DNS超时配置解析失败: %v, 使用默认值 5s", err)
		timeout = 5 * time.Second
	}

	// 设置全局默认DNS resolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: timeout,
			}
			// 尝试配置的所有DNS服务器
			var lastErr error
			for _, server := range config.DNSServers {
				conn, err := d.DialContext(ctx, network, server)
				if err == nil {
					if config.Debug {
						log.Printf("[DEBUG] 使用DNS服务器: %s", server)
					}
					return conn, nil
				}
				lastErr = err
				if config.Debug {
					log.Printf("[DEBUG] DNS服务器 %s 连接失败: %v, 尝试下一个", server, err)
				}
			}
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, &net.DNSError{
				Err:  "all DNS servers failed",
				Name: address,
			}
		},
	}

	log.Printf("自定义DNS解析器已启用，服务器: %v, 超时: %v", config.DNSServers, timeout)
}
