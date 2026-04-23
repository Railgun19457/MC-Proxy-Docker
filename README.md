# MC-Proxy

MC-Proxy 是一个面向 Minecraft 的轻量代理，支持：

1. TCP/UDP 转发
2. 多端口多规则并行
3. PROXY protocol v1/v2（UDP 仅 v2）
4. 在开启 PROXY protocol 时自动补头（缺失才补写）

## 配置原则

1. `listen_net` 仅支持 `tcp` 或 `udp`。
2. 同一服务端口只保留一条规则，避免行为分叉。
3. `backend_addr` 可以写主机名，程序会按协议自动拨号。
4. 推荐从 `example-config.yaml` 复制为 `config.yaml` 后再按需修改。

## 关键字段

| 字段 | 说明 | 常用值 |
| --- | --- | --- |
| `name` | 规则名，必须唯一 | `java` |
| `listen_net` | 监听协议 | `tcp` / `udp` |
| `listen_addr` | 监听地址 | `:25565` |
| `backend_addr` | 后端地址 | `velocity:25565` |
| `rule` | 转发规则 | `passthrough` / `proxy_protocol` |
| `proxy_version` | PROXY 版本 | `1` / `2`（常用 `2`） |
| `connect_timeout` | 后端连接超时 | `3s` |
| `read_buffer_size` | 读缓冲区 | `32768` |
| `write_buffer_size` | 写缓冲区 | `32768` |
| `udp_session_timeout` | UDP 会话超时 | `2m` |

## PROXY protocol 行为

当 `rule: "proxy_protocol"` 时：

1. 如果入站流量已经带 PROXY 头，MC-Proxy 直接透传，不会重复注入。
2. 如果入站流量没有 PROXY 头，MC-Proxy 会根据 `proxy_version` 自动补写。

这意味着同一入口可以同时处理：

1. 直连客户端（无头）
2. 经过 FRP/NLB 等前置链路（可能已注入头）

## FRP 接入建议

1. Java 入口推荐 `listen_net: tcp` + `rule: proxy_protocol`。
2. 如果 FRP 已明确注入 PROXY 头，仍可保持 `proxy_protocol`，MC-Proxy 会识别并透传。
3. 如果后端不接受 PROXY 头，请改为 `rule: passthrough`。

## 启动

在仓库根目录执行：

```bash
docker compose up --build -d
```

如果你的环境使用旧命令：

```bash
docker-compose up --build -d
```

## 日志与停止

查看日志：

```bash
docker compose logs -f
```

停止服务：

```bash
docker compose down
```

## 本地运行

```bash
go run . -config config.yaml
```

## 网络受限构建

如果构建阶段在 `go mod download` 超时，可使用：

```bash
GOPROXY=https://goproxy.cn,direct docker compose up --build -d
```

必要时临时关闭校验数据库：

```bash
GOPROXY=https://goproxy.cn,direct GOSUMDB=off docker compose up --build -d
```

