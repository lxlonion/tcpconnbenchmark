# tcpconnbenchmark

<img src="assets/tcpconn-logo.svg" alt="tcpconnbenchmark Logo" width="160">

[English](README.en.md)

使用 Go 和 [MyGo Native UI](https://github.com/egoist/mygo) 编写的 TCP 并发连接测试工具，提供图形界面和命令行模式。仅进行 DNS 解析和 TCP 连接，不发送 HTTP 请求或进行 TLS 握手。

## 功能

- 测试 TCP 80、443 或两个端口，实时查看连接统计和日志。
- 选择出口网卡或 IPv4 / IPv6 源地址，支持自定义域名、网址和 IP 地址。
- 设置测试时长、连接发起速率和失败停止阈值；成功连接保持到本轮结束。
- 按需导出完整连接记录和摘要。

默认测试 360 秒，每秒发起 125 次连接，累计失败 50 次停止；速率可调整为 1–1000 次/秒。

## 构建和运行

需要 Go 1.27.3 或更高版本，构建时禁用 CGO。以下平台要求参考 [MyGo 文档](https://github.com/egoist/mygo/blob/main/docs/README.md#requirements)，不代表所有系统和架构均已实测。

### macOS

需要 macOS 12 或更高版本，使用 Metal。

```bash
CGO_ENABLED=0 go build -trimpath -o tcpconnbenchmark .
./tcpconnbenchmark
```

也可运行 `make app` 生成 `tcpconnbenchmark.app`。打包会尝试 ad-hoc 签名；公开分发需要自行签名和公证。

### Linux

支持 x64 和 arm64；运行图形界面需要 GTK 3 和 OpenGL，无需 WebKitGTK。

```bash
CGO_ENABLED=0 go build -trimpath -o tcpconnbenchmark .
./tcpconnbenchmark
```

绑定网卡可能需要 `CAP_NET_RAW`；如果仅选择网卡，权限不足时可回退到源地址绑定，显式指定源地址时绑定失败会报错。

### Windows

支持 Windows 10/11 的 x64 和 arm64；图形界面使用 Direct3D 11，无需 WebView2。使用 PowerShell 构建和运行：

```powershell
$env:CGO_ENABLED = "0"
go build -trimpath -o tcpconnbenchmark.exe .
.\tcpconnbenchmark.exe
```

### 命令行

```bash
./tcpconnbenchmark -cli -duration 5 -mode both -rate 10 -domains "127.0.0.1"
./tcpconnbenchmark -list-interfaces
```

Windows 下将 `./tcpconnbenchmark` 替换为 `.\tcpconnbenchmark.exe`。示例连接本机 80 和 443 端口，没有相应服务时连接会被拒绝。使用 `-save` 保存结果，运行 `-h` 查看全部参数。

## 数据和注意事项

- 自定义域名保存在可执行文件旁的 `domains.json`；macOS `.app` 使用 `.app` 所在目录。
- 结果默认不导出。通过界面“保存结果”或 CLI 的 `-save` 保存到便携目录下的 `logs/`，包含 `results.csv` 和 `summary.txt`。未保存的结果在开始新测试或退出后丢失。
- 成功率为成功数除以成功与失败之和；停止时中止的连接不计入分母。
- 高速或长时间测试可能耗尽文件句柄，也可能触发目标的防火墙或限流。请仅在授权范围内使用，并注意本机及目标的承受能力。

## 开发

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go vet ./...
```
