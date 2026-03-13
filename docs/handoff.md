# Credits Ledger 工程交接（handoff）

> 本文只做工程交接，不重复设计细节。设计与规则以已定文档为准。

---

## 1) 交付范围与权威文档

实施团队请以下列文档作为唯一事实来源（SoT）：

1. 总体方案与阶段：`plan.md`
2. 领域规则与不变量：`domain.md`
3. 数据库结构与约束：`DDL.md`
4. 接口协议与验签：`API.md`
5. 编码规则（UUIDv7 + 数字外码）：`CODE_RULES.md`

如实现与文档冲突，以以上顺序从上到下判定，冲突项需提 PR 明确修订。

---

## 2) 当前已冻结决策（只列结论）

1. V1 不做冻结/解冻。
2. 三户一本：Merchant / Customer / Account / AccountBook。
3. `support_expiry=true` 才启用 `account_book`；且仅一层。
4. 交易主类型：`TRANSFER | REFUND`。
5. 幂等键：`(merchant_id, out_trade_no)`。
6. 鉴权：`merchant_id + merchant_secret`，服务端存 `secret_ciphertext`（`local_v1`）。
7. 编码：
   - 内部主键统一 UUIDv7；
   - 外部可输入编码统一数字码（`merchant_no/customer_no/account_no`）；
   - `txn_no/event_id/book_no` 为纯 UUIDv7（无业务前缀）。
8. `merchant/customer` 去掉冗余自增 `id`，以 `merchant_id/customer_id` 作为主键。

---

## 3) 建议实施顺序（可直接排期）

### Phase A — 基础骨架（必须先完成）
- 建仓与分层：`api/application/domain/infrastructure/worker`
- 统一错误码、响应体、request_id
- 配置与密钥模块（支持 `local_v1`）

**完成标准**：服务可启动，健康检查通过，基础中间件可用。

### Phase B — 存储与模型落地
- 严格按 `DDL.md` 建表/索引/约束
- 建立 repo 层与事务模板
- 落地编码生成器（引用 `CODE_RULES.md`）

**完成标准**：可通过迁移脚本一键建库；关键唯一键/检查约束生效。

### Phase C — 核心交易链路
- 开户（商户+预算/收款账户）
- 客户开户、账户开户
- TRANSFER/REFUND 主流程 + 状态机 + CAS/行锁

**完成标准**：最小交易闭环跑通，幂等与余额一致性通过测试。

### Phase D — Outbox/Webhook/补偿
- Outbox 事件落库与投递
- Webhook 重试退避
- 卡单补偿任务

**完成标准**：故障注入后可最终收敛，通知可重试到终态。

### Phase E — 验收与上线准备
- 对账脚本、巡检任务、告警指标
- 压测与容量基线
- 灰度与回滚预案

**完成标准**：满足上线门槛（见第 6 节）。

---

## 4) 团队分工建议（按角色并行）

1. **Domain/Txn 负责人**
   - 状态机、扣减/入账规则、退款并发控制、领域不变量。
2. **Storage/Infra 负责人**
   - DDL 迁移、Repo、事务边界、Redis 幂等与锁策略、Outbox。
3. **API/Gateway 负责人**
   - 验签、参数校验、幂等冲突语义、错误码一致性。
4. **Worker/SRE 负责人**
   - 补偿任务、Webhook 投递、监控告警、运行手册。

每个负责人都应在 PR 模板中绑定对应文档引用章节。

---

## 5) 实施硬约束（代码评审必须卡）

1. 不允许绕开幂等键。
2. 不允许在事务外更新余额。
3. 不允许 `support_expiry=false` 账户写 `account_book`。
4. 不允许把 `merchant_secret` 明文落库/日志。
5. 不允许新增与文档冲突的编码格式。

---

## 6) 上线前验收清单（Go/No-Go）

### 功能
- [ ] 开户链路原子成功/失败回滚
- [ ] TRANSFER/REFUND 正常与异常路径完整
- [ ] 幂等重复请求返回一致

### 一致性
- [ ] 余额与流水一致
- [ ] 并发退款不超退
- [ ] 过期账户与账本汇总一致

### 安全
- [ ] 验签、防重放生效
- [ ] 密钥仅密文存储（`local_v1`）
- [ ] 敏感字段脱敏日志

### 稳定性
- [ ] Outbox 最终投递
- [ ] 补偿任务可推进卡单
- [ ] 告警覆盖关键失败场景

---

## 7) 交接输出物要求（技术团队交付）

1. `README`（仅运行与部署，不重复业务规则）。
2. `migrations/` 全量 SQL 与回滚脚本。
3. `openapi` 或接口测试集合（Postman/HTTP file）。
4. 压测报告（TPS、P99、失败率、数据库负载）。
5. Runbook（故障排查、补偿操作、回滚步骤）。

---

## 8) 变更管理

- 任何偏离 `plan/domain/DDL/API/CODE_RULES` 的实现，必须先改文档再改代码。
- 变更 PR 标题建议：`[Spec Change] ...` / `[Impl] ...`。
- 发布前做一次“文档-实现一致性审计”。
