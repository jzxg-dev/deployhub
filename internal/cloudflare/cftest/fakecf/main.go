// fakecf 启动模拟的 Cloudflare 接口，在本机验证 Workers 部署（不连真实的 Cloudflare）：
//
//	go run ./internal/cloudflare/cftest/fakecf -addr 127.0.0.1:8787
//
// 然后用 CLOUDFLARE_API_BASE_URL=http://127.0.0.1:8787/client/v4 启动 deployhub，凭据填下面打印的账号 ID 和 Token。
// 测试仓库用 cftest.WriteFakeWrangler 写出的假 wrangler，构建命令用 cftest.InstallCommand。
package main

import (
	"flag"
	"log"
	"net/http"

	"deployhub/internal/cloudflare/cftest"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8787", "监听地址")
	account := flag.String("account", "0123456789abcdef0123456789abcdef", "模拟的账号 ID")
	token := flag.String("token", "fake-token-for-local-test", "有效的 API Token")
	flag.Parse()
	log.Printf("模拟 Cloudflare 接口：http://%s/client/v4（账号 ID %s，Token %s）", *addr, *account, *token)
	log.Printf("模拟的 Worker：http://%s/__worker/<名称>", *addr)
	log.Fatal(http.ListenAndServe(*addr, cftest.New(*account, *token)))
}
