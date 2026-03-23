# COIN

COIN 是一个面向商户场景的资金账户与交易核心服务，提供账户、账本（book）、交易提交与异步推进能力，并配套后台运营管理系统与 Go SDK。

## 核心能力

- 账户体系：商户、客户、系统账户、能力开关（可借记/可贷记/可转账/可透支等）
- 交易能力：`ISSUE`（发币/充值）、`CONSUME`（扣币）、`P2P`（转账）、`REFUND`（退款）
- 账本能力：支持启用/禁用 book，支持按过期结构入账
- 异步链路：交易阶段推进、补偿恢复、Webhook outbox 投递
- 运营后台：Admin Web（商户/客户/账户/交易/通知/审计）
- 接入 SDK：`sdk/go/coin`

## 项目结构

```text
cmd/
  server/                # API 服务入口
  perf-core-txn/         # 核心交易压测程序
internal/
  api/                   # Gin 路由与 HTTP 处理
  service/               # 业务服务层
  domain/                # 领域模型与错误定义
  db/                    # Repository + SQL + sqlc 代码
migrations/              # PostgreSQL 迁移脚本
web/admin/               # Admin Web（Next.js）
sdk/go/coin/             # 商户侧 Go SDK
docs/                    # 设计与接口文档
```

## 快速开始

### 方式一：Docker 一键部署（推荐）

```bash
cp deploy/docker/.env.example deploy/docker/.env
# 编辑 deploy/docker/.env，至少设置：
#   LOCAL_KMS_KEY_V1
#   ADMIN_JWT_SECRET

./deploy/docker/deploy.sh up
```

启动后访问：

- Admin Web: `http://127.0.0.1:3000/login`
- API 健康检查: `http://127.0.0.1:8080/healthz`

首次启动可通过 Admin Setup 引导初始化管理员与默认商户：

- `GET /admin/api/v1/setup/status`
- `POST /admin/api/v1/setup/initialize`

详情见 [docs/deploy-docker.md](docs/deploy-docker.md)。

### 方式二：本地开发模式

1. 启动依赖（PostgreSQL/Redis，可用 Docker）
2. 执行数据库 migration（`migrations/*.up.sql`）
3. 启动后端：
   `LOCAL_KMS_KEY_V1=... POSTGRES_DSN=... go run ./cmd/server`
4. 启动 Admin Web：
   `cd web/admin && npm install && npm run dev`

也可使用一键本地脚本（前后端同时启动）：

```bash
sh start-dev.sh
```

## 常用命令

- `make test`：运行 e2e + integration + unit 测试
- `make perf`：运行核心交易压测
- `make sqlc`：根据 SQL 重新生成 `sqlc` 代码
- `scripts/test/test.sh`：测试脚本入口
- `scripts/test/perf_core_txn_real.sh`：压测脚本入口

生成 `core_sqls.md`（SQL 执行顺序与耗时）：

```bash
/bin/zsh -lc "COIN_SQL_DEBUG_TRACE=1 COIN_SQL_DEBUG_SLOW_MS=1 PERF_REQUESTS=1 PERF_CONCURRENCY=1 PERF_WARMUP=0 scripts/test/perf_core_txn_real.sh > /tmp/perf_sql_debug.log 2>&1"
scripts/test/gen_core_sqls_from_sql_debug_log.pl /tmp/perf_sql_debug.log core_sqls.md
```

## 文档导航

### 设计与规范

- [docs/requirements-design.md](docs/requirements-design.md)：需求设计
- [docs/functional-design.md](docs/functional-design.md)：功能设计
- [docs/detailed-design.md](docs/detailed-design.md)：详细设计
- [docs/domain.md](docs/domain.md)：领域模型与业务规则
- [docs/DDL.md](docs/DDL.md)：数据表与索引设计
- [docs/CODE_RULES.md](docs/CODE_RULES.md)：编码与约束规范

### 接口与后台

- [docs/API.md](docs/API.md)：业务 API 契约
- [docs/admin-mvp.md](docs/admin-mvp.md)：Admin API 与后台能力
- [web/admin/README.md](web/admin/README.md)：Admin Web 说明

### 部署、压测与交接

- [docs/deploy-docker.md](docs/deploy-docker.md)：Docker 部署与初始化
- [docs/release-automation.md](docs/release-automation.md)：PR Label 驱动自动发版
- [perf.md](perf.md)：性能相关记录

### SDK

- [sdk/go/coin/README.md](sdk/go/coin/README.md)：Go SDK 使用说明

## 安全说明

- 不要在日志、文档或代码中暴露明文 `merchant_secret`
- 上线前务必替换生产环境密钥与 `ADMIN_JWT_SECRET`
- `deploy/docker/admin_setup_result.json` 可能包含密钥，已默认加入 `.gitignore`
