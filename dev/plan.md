# MC-Proxy 开发基线文档

## 1. 项目概述
- 项目名称：MC-Proxy
- 开发语言：Go 1.21+
- 项目定位：Minecraft 流量代理，处理 TCP/UDP 转发，并在需要时补充 PROXY Protocol 头，以解决 FRP 穿透、IPv6 直连和后端真实 IP 透传问题。
- 交付形式：单一可执行文件 + Docker 镜像，优先选择 scratch，必要时可切换 alpine。

## 2. 目标与边界
### 2.1 目标
- 支持多端口、多协议监听。
- 支持 passthrough 与 proxy_protocol 两种转发策略。
- 支持 TCP 与 UDP 场景。
- 支持 PROXY Protocol V1/V2，其中 UDP 仅使用 V2。
- 在 Linux + Docker 环境下保持低延迟、低内存占用和可预测的资源使用。

### 2.2 非目标
- 不做应用层协议解析。
- 不做账号认证、限流、鉴权等复杂治理。
- 不承诺跨平台零差异零拷贝表现，零拷贝仅作为 Linux 上的性能优化路径。

## 3. 典型场景
- IPv6 直连玩家进入代理后，由代理补全 PROXY Protocol 头再转发给后端。
- FRP 反向代理已经携带真实来源信息时，代理仅做透明转发。
- 基岩版 UDP 流量通过代理保持会话映射并维持源地址语义。

## 4. 功能需求
- 通过配置文件声明多个独立代理实例。
- 每个实例可单独指定 listen_net、listen_addr、backend_addr、rule 和 proxy_version。
- TCP 连接支持双向转发、优雅关闭和错误回收。
- UDP 连接支持基于客户端地址的会话表、后台端口复用和超时清理。
- 启动、停止和配置加载过程必须可观测、可诊断、可重复。
- 需要提供可运行的 Dockerfile 和默认配置示例。

## 5. 架构设计
### 5.1 核心模块
- Config Manager：解析 YAML 或 JSON 配置，负责配置校验和默认值补全。
- Listener Manager：按配置启动 tcp、udp 监听器。
- TCP Proxy Core：建立后端连接后执行规则分流，负责 header 注入与数据转发。
- UDP Proxy Core：维护客户端会话和后端映射，处理包转发与超时回收。
- Protocol Encoder：构建 PROXY Protocol V1/V2 报文头。
- Lifecycle Manager：统一处理启动、退出、上下文取消和资源清理。

### 5.2 数据流
1. 读取配置并完成校验。
2. 按实例启动监听器。
3. 收到新连接或数据包后，根据 rule 选择 passthrough 或 proxy_protocol。
4. 创建后端连接并可选写入 PROXY Protocol 头。
5. 使用流拷贝或会话映射完成数据转发。
6. 在连接结束或超时后释放资源。

## 6. 配置规范
### 6.1 配置结构
- proxies：代理实例数组。
- name：实例名称，用于日志和排障。
- listen_net：tcp、udp。
- listen_addr：监听地址。
- backend_addr：后端地址。
- rule：passthrough 或 proxy_protocol。
- proxy_version：1 或 2，默认建议 2。
- udp_session_timeout：UDP 会话超时时间，可选。
- read_buffer_size / write_buffer_size：缓冲区大小，可选。
- connect_timeout：后端连接超时，可选。

### 6.2 YAML 示例
```yaml
proxies:
  # 场景1：IPv6 直连玩家补全 PROXY Protocol 头
  - name: "mc-ipv6-direct"
    listen_net: "tcp"
    listen_addr: "[::]:25565"
    backend_addr: "127.0.0.1:25566"
    rule: "proxy_protocol"
    proxy_version: 2

  # 场景2：FRP 进来的 IPv4 流量直接透明转发
  - name: "mc-ipv4-frp"
    listen_net: "tcp"
    listen_addr: "0.0.0.0:25565"
    backend_addr: "127.0.0.1:25566"
    rule: "passthrough"

  # 场景3：基岩版 UDP 透明转发
  - name: "bedrock-udp"
    listen_net: "udp"
    listen_addr: "0.0.0.0:19132"
    backend_addr: "127.0.0.1:19133"
    rule: "passthrough"
    udp_session_timeout: "2m"
```

## 7. 核心实现要点
### 7.1 TCP 转发
- 代理链路应尽量保持为原生 net.Conn，不要额外包裹会破坏 Go 运行时的优化路径。
- 在 Linux 上，io.Copy 处理 TCPConn 到 TCPConn 时可利用内核态优化路径，目标是尽可能减少用户态拷贝。
- 在无法走优化路径或需要更细粒度控制时，使用 io.CopyBuffer 配合 sync.Pool 复用缓冲区。
- 双向转发必须保证任一方向关闭后能及时回收另一侧连接，避免 goroutine 泄漏。

### 7.2 PROXY Protocol
- V1 为纯文本格式，仅用于 TCP。
- V2 为二进制格式，可支持 TCP 和 UDP。
- 发送顺序必须是：先建立后端连接，再写入 PROXY 头，最后开始转发业务流量。
- 只有明确配置了 proxy_protocol 时才写入头，passthrough 不应额外注入任何数据。
- 后端必须显式开启 accept-proxy 或等价能力，否则会把头部误当业务数据。

### 7.3 UDP 会话管理
- UDP 需要按客户端源地址维护会话状态，推荐以客户端 IP:端口 为键。
- 每个会话应记录后端连接、最后活跃时间和关闭状态。
- 需要后台清理协程按超时回收闲置会话，避免内存和句柄持续增长。
- 并发访问会话表必须加锁，或者采用分片锁、sync.Map 等并发安全结构。
- 对于响应包，需要确保返回到正确的客户端源地址，避免跨会话串包。

### 7.4 资源与退出
- 所有 goroutine 都应绑定 context。
- 关闭流程需要幂等，重复关闭不应产生 panic。
- 配置错误、后端不可达和协议头构造失败都应返回可读日志，便于排障。
- buffer、session 和连接句柄都必须在退出路径上被释放。

## 8. 部署方案
### 8.1 构建建议
- 使用 CGO_ENABLED=0 进行静态编译。
- 使用 go build -trimpath 和 -ldflags="-s -w" 减小体积。
- 产物以 Linux amd64/arm64 为主，必要时通过交叉编译覆盖更多平台。

### 8.2 Dockerfile 示例
```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o mc-proxy .

FROM scratch
WORKDIR /app
COPY --from=builder /app/mc-proxy /app/mc-proxy
COPY config.yaml /app/config.yaml
ENTRYPOINT ["/app/mc-proxy", "-config", "/app/config.yaml"]
```

### 8.3 部署约定
- 配置文件通过挂载方式提供，不建议把环境特定配置写死进镜像。
- 如果后续需要 TLS 证书、时区文件或调试工具，可将运行镜像从 scratch 切换为 alpine 或 distroless。
- 容器启动失败时应输出明确的配置错误信息。

## 9. 测试与验收
### 9.1 单元测试
- 配置解析与默认值补全。
- PROXY Protocol V1/V2 编码结果。
- UDP 会话创建、刷新和过期逻辑。
- 地址解析和端口校验。

### 9.2 集成测试
- TCP passthrough 的双向转发。
- TCP proxy_protocol 注入后后端可正确识别真实客户端地址。
- UDP 场景下会话复用与超时清理。
- 容器启动、配置挂载和端口暴露流程。

### 9.3 验收标准
- 功能可通过配置切换，不需要改代码。
- 代理可长期运行，连接和会话不会无界增长。
- 后端能够稳定拿到预期的源地址或保持透明转发语义。
- 镜像可成功构建并运行。

## 10. 里程碑建议
- 第一阶段：完成配置解析、TCP passthrough 和基础日志。
- 第二阶段：完成 PROXY Protocol V1/V2 和规则切换。
- 第三阶段：完成 UDP 会话管理、超时回收和并发控制。
- 第四阶段：补齐 Docker 化、测试和文档。
- 第五阶段：基准测试与性能调优。

## 11. 风险与注意事项
- Linux 下的零拷贝属于优化能力，不应作为功能正确性的前提。
- UDP 使用 PROXY Protocol V2 时，后端必须确认协议兼容性。
- scratch 镜像体积最小，但不适合调试阶段使用。
- 如果后续需要更复杂的监控或指标导出，需提前规划日志与 metrics 接口。
