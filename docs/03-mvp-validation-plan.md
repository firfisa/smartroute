# SmartRoute MVP 与验证计划

版本：v0.1

## 1. MVP 要回答的问题

MVP 不是为了证明“能够自动写 YAML”，而是回答五个可证伪问题：

1. 真实用户流量中，有多少连接被现有规则送到了次优路径？
2. Direct/Proxy 对照能否比单边失败可靠地识别需要代理的目标？
3. 错峰竞争能否降低失败等待，同时把额外开销控制在可接受范围？
4. 学习后的策略能否在网络变化后保持准确，不频繁抖动？
5. 用户是否愿意信任并持续开启该功能？

## 2. MVP 范围

包含：

- macOS 优先，之后验证 Windows/Linux。
- Mihomo/Clash Verge Rev 用户自带节点和配置。
- SOCKS5 TCP sidecar。
- HTTPS/TLS 1.2/1.3（不复制 0-RTT）。
- 完整域名 + 端口 + TCP + 网络画像。
- SQLite 本地观测库。
- Shadow、Suggest、Auto 三种模式。
- 用户锁定、隐私列表、TTL、撤销和导出。
- 本地诊断页或 CLI；无需先做完整桌面 UI。

不包含：

- UDP/QUIC 自适应竞争。
- 云端同步和众包规则。
- 自动域名后缀合并。
- 移动端。
- 自研代理协议或订阅系统。
- 黑盒 ML 模型。

## 3. 分阶段路线

以下时间以 1 名熟悉 Go/Rust 和网络编程的开发者为粗略估计，不是交付承诺。

### Phase 0：基线与技术 Spike（1–2 周）

交付：

- 验证 Mihomo → sidecar → 两个固定 listener 的拓扑不会循环。
- 验证系统代理和 TUN 两种入口。
- 建立一组 Direct/Proxy/DNS 可控故障测试目标。
- 测量本地 sidecar 额外开销。
- 定义日志 schema 和失败分类。
- 用 20–50 个代表域名建立静态规则基线。
- 同期访谈 8–12 名目标用户，收集最近发生过的真实错误分流案例。

当前工程进度：

| 项目 | 状态 | 验证方式 |
| --- | --- | --- |
| 最小 SOCKS5 server/client relay | 实验性完成 | `go test -race ./...` 与 Test Lab 字节回显 |
| Direct-first 错峰候选竞争 | 实验性完成 | Direct 快速、Proxy 恢复、双失败场景 |
| 已完成的反事实路径证据 | 实验性完成 | Direct/Proxy 先失败后另一条成功会保留成对证据；取消与未启动明确为空 |
| 域名形式目标保留 | 实验性完成 | `echo.test` 经 sidecar 和 fake gateway 断言 |
| 隔离故障目标 | 第一批完成 | `go run ./cmd/smartroute-testlab` |
| 独立 Mihomo listener 拓扑 | macOS arm64、Linux amd64/v1.19.29 已完成 | `make mihomo-lab`；临时目录、随机端口、独立子进程 |
| SOCKS readiness 契约 | 已识别安全缺口并封闸 | Mihomo L1 候选产生 `candidate_below_commit_stage`，不得提交或学习 |
| TLS readiness gate | 实验性完成 | 分片 ClientHello、early-data 预拨号拒绝、ServerHello L3、精确预读回放 |
| adaptive engine 可用性 Guard | 单元与拓扑代码完成；隔离运行待复验 | 引擎拒绝/握手卡死时同连接回原策略；停止、回退、重启、恢复场景 |
| Direct 探测隐私门控 | 实验性完成 | privacy-first、精确/后缀 deny、无效/缺失策略 fail-closed；Proxy-only 仍需 L3 |
| Guard/engine 进程 supervisor | 实验性完成 | 独立启动/退出故障、连续失败退避、封顶、稳定窗口重置、父进程取消 |
| 受限本地观测记录器 | 实验性完成 | 默认 HMAC、明文显式开关、暂停/恢复、容量与时间裁剪、确认清空、无盐导出、stdout 去重 |
| 系统代理与 TUN | 待执行且仅手动 opt-in | 不使用活动 Clash 实例 |

退出条件：

- TUN 路径无法稳定保留 hostname，或跨平台循环无法可靠规避。
- sidecar 空载 p95 额外延迟大到抵消预期收益。
- 无法可靠区分候选被取消和真实失败。

### Phase 1：Shadow Mode（1–2 周）

只观测，不改变用户路由：

- 从现有连接元数据收集目标和当前命中规则。
- 只对非敏感、用户允许的目标做低频对照探测。
- 记录 Direct/Proxy 成功层级、延迟和 DNS 差异。
- 输出“如果启用 SmartRoute，理论上会改变哪些连接”。

目标是先证明“次优路由比例”足够大。

### Phase 2：TCP/TLS Adaptive（2–4 周）

- 未知 TCP 目标进入 sidecar。
- Direct 先发，Proxy 错峰启动。
- 实现 TLS ClientHello 完整缓冲和安全握手竞争。
- 实现策略状态机、缓存、TTL 和网络画像。
- 支持 Suggest 与 Auto 模式。
- 加入故障冻结、速率限制和回滚。

### Phase 3：可用性与小范围试用（2–4 周）

- 本地 UI：解释决策、锁定、撤销、隐私控制。
- 5–20 名目标用户试用。
- A/B 或交叉实验比较静态基线与 SmartRoute。
- 修复休眠唤醒、网络切换、代理切换和 Captive Portal 场景。

### Phase 4：产品化决策

只有指标通过后再决定：

- 是否 fork/integrate Clash Verge Rev。
- 是否把 adaptive dialer 移入 Mihomo fork，或向上游提案。
- 是否支持 Windows/Linux 安装器。
- 是否研发 QUIC/UDP。

## 4. 基线与对照组

至少比较四组：

1. 用户当前配置。
2. 一套质量较高且更新及时的静态规则配置。
3. SmartRoute Shadow 推演，不改变路由。
4. SmartRoute Auto 实际选路。

这样可以避免把“公共规则太差”误当成“自适应算法很好”。项目必须证明它优于“把静态规则整理好”这个更便宜的方案。

## 5. 核心指标

### 5.1 用户体验

- 连接成功率，按目标和网络画像分层。
- p50/p95/p99 time-to-ready；HTTPS 优先测到有效 TLS 服务端响应。
- 首次未知目标恢复时间。
- 用户可感知失败：需要刷新、应用重试或手工切换的次数。
- 路由抖动次数。

### 5.2 路由质量

- `avoidable_proxy_ratio`：可可靠直连且满足 SLO，却使用代理的连接/字节比例。
- `missed_proxy_ratio`：Direct 失败而 Proxy 成功，却选择 Direct 的比例。
- `oracle_regret_ms`：实际选路时间相对当次最佳可用路径的额外耗时。
- 自动晋升准确率：晋升后未来窗口内仍成立的比例。
- 策略撤销率和用户反转率。

### 5.3 代价与安全

- 每个未知目标的额外候选连接数。
- 额外 SYN、TLS 握手和代理流量。
- sidecar CPU、内存和电量影响。
- 直连探测的域名数量。
- 在提交路径前发送了非握手业务字节的次数，目标必须为 0。
- 配置回滚和断网事件，目标必须为 0。

### 5.4 产品信号

- 功能启用率和 7/30 日保留启用率。
- Shadow 用户升级到 Suggest/Auto 的比例。
- 用户锁定、撤销和加入隐私列表的原因。
- “看懂决策原因”的定性访谈结果。

## 6. 建议通过门槛

门槛需要根据基线校准，以下仅作为首轮 Go/No-Go 值：

- 不降低总体连接成功率；95% 置信区间内不能出现有意义的负向差异。
- 在目标用户样本中，`avoidable_proxy_ratio` 或代理字节下降至少 15%。
- 受限未知 TCP 目标的首次恢复 p95 相比“等待完整直连超时后重试”下降至少 50%。
- 对高置信度自动策略，后续验证准确率至少 98%。
- 用户手工反转自动决策低于 1%，且无集中在支付、登录或企业服务的严重错误。
- sidecar 已知策略的本地转发额外 p95 开销低于 5ms；未知竞争的额外连接有明确上限。
- 试用用户中至少一半愿意持续开启 Suggest 或 Auto；否则可能只有诊断工具价值。

这些阈值不应被当作行业标准。真正重要的是事先写定并用成对数据评估，避免看到结果后移动目标。

## 7. 测试矩阵

### 网络环境

- 家庭宽带 Wi-Fi。
- 手机热点/5G。
- 公司或校园网络。
- IPv4-only、双栈、IPv6 异常。
- 有/无本机 VPN。
- Captive Portal。
- 高丢包、高 RTT 和代理节点波动。

### 目标类型

- 稳定境内 CDN。
- 可直连国际站点。
- Direct TCP timeout、reset、TLS 阶段失败的受限目标。
- 两边都失败的目标。
- 仅 DNS 路径异常的目标。
- 同一 eTLD+1 下直连/代理结论不同的子域。
- 共享 CDN IP、ECH、HTTP/2 连接复用。
- 登录、支付、上传等不得重放场景。
- SSH、Git、自定义 TCP 协议。

### 系统事件

- 休眠/唤醒。
- Wi-Fi 切换。
- 节点切换、订阅更新和内核重启。
- adaptive engine 崩溃、Guard 崩溃、数据库损坏、端口被占用。
- 系统时间变化。
- 规则 provider 写入失败。

## 8. 故障注入

建立可重复实验环境，不依赖真实站点长期保持某种封锁状态：

- 丢弃 SYN，模拟静默 timeout。
- TCP RST。
- TCP 成功后丢弃或重置 TLS ClientHello。
- Direct 成功、Proxy 失败，反之亦然。
- 两边不同延迟和抖动。
- 本地 DNS 返回 NXDOMAIN、错误 IP、慢响应。
- IPv6 黑洞，IPv4 正常。
- 代理握手成功但目标连接失败。

每个故障用例都应断言：

- 最终选择路径。
- 选择耗时。
- 失败分类。
- 是否晋升策略。
- 是否复制了不允许的数据。
- loser 是否及时取消。

### 8.1 测试执行隔离

默认 CI 运行纯单元测试和进程内回环 Test Lab。显式的 Mihomo Lab 及其独立 workflow 使用锁定版本子进程、临时 home/config 和专用回环端口。两类自动测试都不读取 Clash Verge Rev 配置、不访问 controller、不复用固定端口、不访问公网，也不修改系统代理或 TUN；涉及活动网络环境的测试不进入 CI，并且需要用户针对具体动作显式授权。

## 9. 用户研究

在写完整 UI 前访谈 8–12 名符合画像的用户：

- 当前用什么规则模式，多久遇到一次错误分流？
- 遇到问题时会刷新、切全局、改规则还是放弃？
- 更在意速度、流量、可靠性还是隐私？
- 是否愿意让未知域名先做一次 Direct 探测？哪些目标绝不允许？
- 对“自动应用”与“只给建议”的接受度分别如何？
- 一条决策需要展示哪些证据才可信？

关键不是询问“你会不会用这个酷功能”，而是让用户回忆最近三次真实故障，并观察他们当前如何解决。

## 10. 发布策略

建议分级：

```text
0. Off       完全不介入
1. Observe   记录当前路径，不主动对照
2. Shadow    对照探测，但不改真实连接
3. Suggest   给出策略建议，用户确认
4. Auto      高置信度自动应用
```

默认从 `Shadow` 或 `Suggest` 开始，不能安装后直接把所有未知域名送去 Direct 探测。

版本回滚要求：

- sidecar 不可用时回到用户原始 `MATCH` 策略。
- 配置修改采用临时文件、语法预检和原子替换。
- 始终保留最近一个可工作的配置快照。
- 数据库迁移必须可备份；损坏时只丢学习结果，不影响基本联网。

## 11. 第一批工程任务

1. 确定实现语言：优先 Go，便于复用网络生态并与 Mihomo 行为对齐；Rust 也可，但开发验证成本可能更高。
2. 实现最小 SOCKS5 server/client relay。
3. 用两个 Mihomo listener 验证 Direct/Proxy 路径隔离。macOS arm64 与 Linux amd64/v1.19.29 已完成；其 SOCKS ACK 只证明 L1。
4. 实现候选拨号、延迟启动、取消和结构化事件。
5. 实现 TLS record 与跨包 ClientHello 解析；明确拒绝复制 early data。最小安全切片已完成，完整真实 TLS 握手兼容矩阵仍待扩展。
6. 建立 SQLite schema 和 deterministic state machine。
7. 加入网络画像、控制探针和学习冻结。
8. 做 CLI：状态、观测、锁定、撤销、隐私列表、导出。
9. 跑故障注入与静态规则基线。
10. 只有核心指标通过后，再做 Tauri/Clash Verge Rev UI。

## 12. 当前需要保留的开放问题

- 目标 Mihomo 版本中，SOCKS outbound 是否在所有 TUN/Fake-IP 组合下保留原 hostname。
- 专用 listener 的 `proxy` 字段对 mixed/socks listener 的实际行为及热重载稳定性。
- Clash Verge Rev 当前服务模式下，如何以最小权限安装和管理 sidecar。
- macOS Network Extension/TUN 捕获下是否存在 loopback、进程识别或休眠恢复差异。
- TLS 1.3 early data、ECH、分片 ClientHello 和 uTLS 的完整兼容范围。
- HTTP/2/QUIC 连接复用对按域名统计的偏差。
- 自动策略是否应默认只保留在 sidecar，还是稳定后写入 rule-provider。
- 哪一种网络画像既稳定又不过度收集隐私信息。

这些问题都可以在 Phase 0–2 用代码和实验回答，不需要现在靠架构猜测定死。
