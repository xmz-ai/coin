# Credits Ledger - 编码规则（V2）

> 目标：统一所有编码，兼顾系统可靠性与人工输入友好性。

---

## 1. 总体原则

1. **内部主键统一 UUIDv7**：用于系统内关联、分布式生成、低冲突。
2. **外部展示/输入优先数字编码**：便于电话沟通、人工录入、客服排障。
3. **禁止暴露自增主键**：`BIGSERIAL id` 仅数据库内部使用。
4. **幂等靠业务键，不靠发号算法**：`UNIQUE (merchant_id, out_trade_no)` 必须保留。
5. **编码不得包含敏感信息**：不嵌入手机号、证件号等 PII。

---

## 2. 编码总览

| 对象 | 字段 | 类型 | 示例 | 规则 |
|---|---|---|---|---|
| 商户（内部） | `merchant_id` | UUIDv7 | `01956f4e-7b3e-7a4d-9f6b-4d9de4f7c001` | 系统生成，36位标准UUID串 |
| 商户（外部） | `merchant_no` | 数字编码 | `1000123456789012` | 16位数字，末位Luhn校验 |
| 客户（内部） | `customer_id` | UUIDv7 | `01956f4e-8c11-71aa-b2d2-2b079f7e1001` | 系统生成，36位标准UUID串 |
| 客户（外部） | `customer_no` | 数字编码 | `2000123456789018` | 16位数字，末位Luhn校验 |
| 账户（外部） | `account_no` | 数字编码 | `6217701201001234567` | 19位数字，银行卡风格+Luhn |
| 交易（内部） | `txn_no` | UUIDv7 | `01956f4e-9d22-73bc-8e11-3f5e9c7a2001` | 系统生成，36位标准UUID串 |
| 事件（内部） | `event_id` | UUIDv7 | `01956f4e-ae33-75cd-90a2-4c6f9d8b3001` | 系统生成，36位标准UUID串 |
| 账本（内部） | `book_no` | UUIDv7 | `01956f4e-bf44-77de-a1b3-5d7a0e9c4001` | 系统生成，36位标准UUID串 |
| 外部订单 | `out_trade_no` | 商户自定义 | `ord_20260313_000001` | `^[A-Za-z0-9_\-]{1,64}$` |

---

## 3. 数字编码规则

## 3.1 account_no（19位，银行卡风格）

结构：`BBBBBB MMMM TT SSSSSS C`

- `BBBBBB`：平台BIN（示例 `621770`）
- `MMMM`：商户映射段（`hash(merchant_id) % 10000`，左补零）
- `TT`：账户类型码（01预算、02收款、10客户通用、11商户通用）
- `SSSSSS`：序列号（按 `BIN+MMMM+TT` 维度递增）
- `C`：Luhn校验位

校验：
- 正则 `^[0-9]{19}$`
- Luhn 必须通过

## 3.2 merchant_no / customer_no（16位）

建议结构：`P RRR T SSSSSSSSS C`

- `P`（1位）：主体类型（`1`=merchant，`2`=customer）
- `RRR`（3位）：区域/机房码（预留）
- `T`（1位）：版本位（预留）
- `SSSSSSSSS`（9位）：序列号
- `C`（1位）：Luhn校验位

校验：
- 正则 `^[0-9]{16}$`
- Luhn 必须通过

---

## 4. UUIDv7 字段规则（内部）

适用字段：`merchant_id/customer_id/txn_no/event_id/book_no`

- 格式：标准 UUID 字符串（36位，小写）
- 正则：`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
- 前缀规则：**不加业务前缀**（即不使用 `txn_xxx` / `evt_xxx` / `book_xxx` 形式）
- 生成：应用层统一 UUIDv7 生成器
- 要求：仅系统生成，不允许客户端自定义

---

## 5. 生成与落库建议

1. 应用层统一生成器：
   - `newMerchantIdV7()` / `newCustomerIdV7()`
   - `newTxnNoV7()` / `newEventIdV7()` / `newBookNoV7()`
   - `newMerchantNo()` / `newCustomerNo()` / `newAccountNo()`
2. 唯一键冲突重试（最多3次）。
3. API 网关先做格式校验，再进入业务校验。
4. 日志统一打点：`merchant_id, merchant_no, customer_id, customer_no, account_no, txn_no, out_trade_no, event_id`。

---

## 6. 兼容策略

1. 历史示例值（旧短码）仅演示用途。
2. 新环境按本规则生成；存量可通过映射表平滑迁移。
3. 对外优先展示 `merchant_no/customer_no/account_no`；内部与接口透传仍可保留 `*_id`。

---

## 7. 快速检查清单

- [ ] 内部主键统一 UUIDv7
- [ ] 商户/客户/账户有数字外码
- [ ] `account_no` 为19位+Luhn
- [ ] `merchant_no/customer_no` 为16位+Luhn
- [ ] 幂等仍为 `(merchant_id, out_trade_no)`
- [ ] DDL/API 与本文一致
