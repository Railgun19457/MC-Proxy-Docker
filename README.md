# MC-Proxy

一个轻量的 Minecraft 流量代理工具，用于统一处理多渠道 Minecraft 连接，支持 TCP/UDP 转发和 PROXY protocol。

## 准备配置

1. 按需编辑 `config.yaml`。
2. 当前 `docker-compose.yml` 默认映射 `25565/tcp` 和 `19132/udp`，如果启用其他监听端口，请同步增加端口映射。
3. `backend_addr` 与 `listen_net` 不要求同地址族（程序会按 tcp/udp 自动拨号）；如果后端服务跑在宿主机上，建议将 `backend_addr` 改成宿主机可达地址

## 启动服务

在仓库根目录执行：

```bash
docker compose up --build -d
```

如果你的环境使用旧命令，也可以执行：

```bash
docker-compose up --build -d
```

## 构建超时排查

如果构建阶段卡在 `go mod download` 并出现 i/o timeout，可指定 Go 模块代理后重试：

```bash
GOPROXY=https://goproxy.cn,direct docker compose up --build -d
```

如果你的网络无法访问 sum.golang.org，也可以临时关闭校验数据库：

```bash
GOPROXY=https://goproxy.cn,direct GOSUMDB=off docker compose up --build -d
```

你也可以在仓库根目录创建 `.env`，Compose 会自动读取：

```env
GOPROXY=https://goproxy.cn,direct
# 可选：网络受限时再启用
# GOSUMDB=off
```

构建日志中的 `current commit information was not captured` 只是元数据提示，不会影响容器运行。

## 查看日志

```bash
docker compose logs -f
```

## 停止服务

```bash
docker compose down
```

## 本地运行（可选）

```bash
go run . -config config.yaml
```

