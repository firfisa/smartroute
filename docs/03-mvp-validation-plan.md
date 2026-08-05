# SmartRoute MVP 与验证计划

版本：v0.3

## 1. MVP 要回答的问题

MVP 不是为了证明“能够自动写 YAML”，而是回答五个可证伪问题：

1. 真实用户流量中，有多少连接被现有规则送到了次优路径？
2. 首次 Direct/Proxy readiness 判断能否产生可复用的正确路径？
3. 首判后的单路复用能否减少延迟和代理消耗，同时把 sidecar 开销控制在可接受范围？
4. 已记住的路径失效时，提交前 fallback 能否在有界超时内成功切换并立即覆盖？
5. 用户是否愿意信任并持续开启该功能？

## 2. MVP 范围

包含：

- macOS 优先，之后验证 Windows/Linux。
- Mihomo/Clash Verge Rev 用户自带节点和配置。
- SOCKS5 TCP sidecar。
- HTTPS/TLS 1.2/1.3（不复制 0-RTT）。
- 完整域名 + 端口 + TCP + 网络画像。
- SQLite 本地精确 last-known-good 映射。
- 默认 `auto`：首次 readiness 成功即持久化，不需要逐目标审批、晋级次数或 TTL。
- 隐私列表、容量上限、一键清除和可恢复的试用回滚。
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
| 默认自动固定路径 | 实验性完成 | 首次 readiness 立即记住、精确 scope、后续单候选、提交前 fallback、反向成功覆盖、无 TTL/晋级/审批 |
| 旧 Shadow/ephemeral/health 路径 | 兼容诊断、非 MVP | 单元测试保留；默认 `auto` 不实例化或查询这些状态机 |
| SQLite 策略/诊断 schema 与异步 writer | 实验性完成、本地默认开启 | HMAC 目标键、自动策略、独立诊断会话、迁移/重开/并发、损坏/未来版本拒绝、裁剪、明文扫描、队列背压/排空；诊断阈值不参与路由 |
| 持久证据生命周期 | 实验性完成 | 缺失不创建、只读无迁移、聚合状态、online backup、SHA-256 清单、临时副本验证、恢复到新路径、拒绝覆盖/不完整产物 |
| 跨会话 Shadow 评估 | 实验性完成、不应用 | wins + distinct sessions 双阈值、无证据/不足/建议/双向冲突矩阵、异步事件、离线精确目标评估、路由零影响 |
| 自动持久策略 | 实验性完成、默认开启 | `auto`；首次 readiness 即记住 last-known-good、HMAC 有界启动索引、顺序单候选+提交前 fallback、反向成功覆盖 |
| 自动层资源边界 | 实验性完成 | policy/index `max_entries`、policy queue 256、连接查询零 SQLite；只在映射新增/反向变化时异步写一行，`auto` 不写 evidence/session |
| 人工固定策略管理面 | 实验性完成、不应用 | 独立明文精确目标 SQLite；永久/TTL lock、事务替换历史、read-only list、revoke、损坏/未来 schema 拒绝；runtime 零读取，激活语义待 ADR |
| Shadow 聚合报告 | 实验性完成 | 只读按目标分组、无 target key/身份输出、保留期 cutoff、类别/reason/阈值计数、明确样本分母 |
| 域名形式目标保留 | 实验性完成 | `echo.test` 经 sidecar 和 fake gateway 断言 |
| 隔离故障与自动学习闭环 | 已完成 report v2 | `go run ./cmd/smartroute-testlab`；3 个基础场景 + 4 步 last-known-good TLS 场景，Preflight 严格验证全部字段 |
| 无网络试用控制面演练 | 已完成 | `make trial-lab`；临时 schema-5 记录、report v7、完整 session 通过与混入 session fail-closed；不产生 preflight 证据 |
| Sidecar 空载本地开销 | 2 gateway × 2 protocol 四格隔离证据完成 | TCP fake/Mihomo 最差 run p95 256/231µs；TLS ServerHello fake/Mihomo 为 249/254µs，均为显式 `-enforce` 5×200；尚不覆盖 TUN/完整握手，负载另见下一行 |
| 并发 Relay 负载与需求容量 | fake 与锁定 Mihomo 隔离证据完成；极限门槛未通过，需求容量边界已定位 | 非节拍长流量两层都收敛约 0.665 ratio；固定 sweep 显示分配主要随连接增长；client-paced 两层均满足 100–5000 Mbps，8000 Mbps baseline 满足而 sidecar 超容差；不下调门槛，不冒充 WAN 仿真 |
| 独立 Mihomo listener 拓扑 | macOS arm64、Linux amd64/v1.19.29 已完成 | `make mihomo-lab`；临时目录、随机端口、独立子进程 |
| Clash Verge 最终 MATCH 变换 | 首个受控真实试用完成并继续自用 | 合成图语义/幂等/五端口冲突与 pinned Mihomo 解析通过；按 baseline→armed→running 安装并重载，原脚本回滚源保留且持久候选验证通过 |
| 完整进程 Runtime Lab | 已完成 | `make runtime-lab`；真实 composer/transform、pinned Mihomo、`smartroute supervise` 子进程、policy-only SQLite，验证首判、两次重启复用、静默路径半预算 fallback 和反向覆盖 |
| 真实流量窗口 | 描述性验证完成、用户选择继续自用 | 最新保留窗口 585 次 ready selection：302 Direct/283 Proxy、489 次已有路径复用、96 次新/反向记忆；累计策略 271 条。仍不等于验证过的应用成功率或反事实节省 |
| SOCKS readiness 契约 | 已识别安全缺口并封闸 | Mihomo L1 候选产生 `candidate_below_commit_stage`，不得提交或学习 |
| TLS readiness gate | 实验性完成 | 分片 ClientHello、early-data 预拨号拒绝、ServerHello L3、精确预读回放 |
| adaptive engine 可用性 Guard | 单元与锁定 Mihomo 隔离拓扑均通过 | 引擎拒绝/握手卡死时同连接回原策略；2026-08-02 验证停止、回退、重启、恢复完整场景 |
| Direct 探测隐私门控 | 实验性完成 | privacy-first、精确/后缀 deny、无效/缺失策略 fail-closed；Proxy-only 仍需 L3 |
| Guard/engine 进程 supervisor | 实验性完成并有 macOS 外部所有者 | 独立启动/退出故障、连续失败退避、封顶、稳定窗口重置、父进程取消；Application Support LaunchAgent 强制终止后新 PID 与五端点恢复 |
| 运行时连接关闭边界 | 实验性完成 | context 关闭 handshake/relay 两端，Sidecar/Guard 等待已接受 handler 全部退出；`net.Pipe` race 测试不依赖端口 |
| 受限本地观测记录器 | 实验性完成 | 默认 HMAC、明文显式开关、暂停/恢复、容量与时间裁剪、确认清空、无盐导出、stdout 去重 |
| 连接级 readiness 报告 | 实验性完成 | report v7 paused 严格读取、无身份聚合、Direct/Proxy、Guard、自动学习/写入 reasons 与 p50/p95/p99；验证过的应用结果仍缺失 |
| Post-commit relay 报告 | 实验性完成 | JSONL schema 2、按 Direct/Proxy 双向字节和 duration、远端有字节覆盖、ended/canceled；不含静态流量/ClientHello/应用成功，schema 1 仍可读 |
| 连接级 terminal/outcome 关联 | 实验性完成 | JSONL schema 3 随机非语义 scope；精确 target/path 配对、窗口截断计数、冲突拒绝；报告不输出 ID，schema 1/2 仍可读 |
| 原 `Other/MATCH` 声明基线 | 实验性完成 | JSONL schema 4；统计相同/改道选择和改道 winner 实际 relay，preflight 单独确认声明；不冒充实时规则命中或反事实节省，schema 1–3 仍可读 |
| Post-commit 方向终止分类 | 实验性完成 | JSONL schema 5；双向 EOF/timeout/reset/closed/I/O-error/canceled 固定聚合，旧 relay 显式 unclassified；不记录 raw error，不作为应用结果或学习证据 |
| Post-trial 数据质量闸门 | 实验性完成 | preflight 预注册 session/config/window/thresholds；评估只接受该计划并检查预期 session、暂停、sample/scope/pair/cancellation 与内部守恒；只授权描述性分析 |
| 受控试用 session scope | 实验性完成 | supervisor/Guard/engine 共享随机非语义 ID，重启保持；旧行显式 unscoped，聚合不输出 ID |
| 只读受控试用 preflight | 实验性完成 | 稳定 pass/warn/fail JSON；严格验证确认项、暂停状态、durable 备份及新鲜隔离证据，并生成带 digest 的预注册 assessment plan |
| 活动 TUN 真实链路 | 首个手动 opt-in 已完成验证并继续自用 | 未改变 TUN/系统代理开关；五端点 running、真实兜底流量、自动持久复用、运行目录迁移与 Supervisor 恢复均已验证 |

退出条件：

- TUN 路径无法稳定保留 hostname，或跨平台循环无法可靠规避。
- sidecar 空载 p95 额外延迟大到抵消预期收益。
- 无法可靠区分候选被取消和真实失败。

### Phase 1：单机受控 Auto 试用（1–2 周）

- 只把最终 `MATCH/Other` 放入 SmartRoute，高置信规则保持不变。
- 直接启用“首判 → 立即持久化 → 单路复用 → 失效覆盖”，不让用户审批域名。
- 本地有界记录 readiness、选路、fallback、连接耗时和 relay 结果，不记录 payload、URL 或凭据。
- 与试用前的原 `MATCH` 体验比较：页面失败、首连/重连延迟、Proxy 使用比例和 Guard 回退。
- 发生 DNS/TUN/广泛可达性回归时立即恢复候选包中的原脚本。

目标是用真实使用数据判断产品是否实际更快、更少误分流，而不是先证明一套复杂学习模型。

### Phase 2：TCP/TLS Adaptive（2–4 周）

- 未知 TCP 目标进入 sidecar。
- Direct 先发，Proxy 错峰启动。
- 实现 TLS ClientHello 完整缓冲和安全握手竞争。
- 用受控试用数据调整首判 head-start、总超时和固定路径半预算 fallback，不做逐目标调参。
- 只在数据证明有必要时，再增加网络画像自动识别或其他生命周期能力。
- 完成一键启停、回滚和最小本地状态页。

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
6. 建立 SQLite schema 和 deterministic state machine。schema v2、进程内状态机、opt-in 异步写入、生命周期工具、Shadow assessment、last-known-good 自动策略、资源边界与健康冻结已完成；真实网络收益统计、活动 Clash 接入和完整 UI 控制仍待实现。
7. 接入网络画像、控制探针及 captive portal 自动信号源；复用已实现的学习冻结入口。
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
