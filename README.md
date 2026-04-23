# MC-Proxy

一个轻量的 Minecraft 流量代理工具，用于统一处理多渠道 Minecraft 连接，支持 TCP/UDP 转发和 PROXY protocol。

## 准备配置

1. 按需编辑 `config.yaml`。
2. 当前 `docker-compose.yml` 默认映射 `25565/tcp` 和 `19132/udp`，如果启用其他监听端口，请同步增加端口映射。
3. 如果后端服务跑在宿主机上，建议将 `backend_addr` 改成宿主机可达地址

## 启动服务

在仓库根目录执行：

```bash
docker compose up --build -d
```

如果你的环境使用旧命令，也可以执行：

```bash
docker-compose up --build -d
```

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

