# COIN Admin MVP

## 1. 服务入口

- API 前缀：`/admin/api/v1`
- 鉴权：`Authorization: Bearer <access_token>`（登录接口除外）

## 2. 登录接口

- `GET /admin/api/v1/setup/status`
- `POST /admin/api/v1/setup/initialize`
- `POST /admin/api/v1/auth/login`
- `POST /admin/api/v1/auth/refresh`
- `GET /admin/api/v1/auth/me`
- `POST /admin/api/v1/auth/logout`

## 3. 运营能力接口

- Dashboard
  - `GET /admin/api/v1/dashboard/overview`
- Merchant
  - `POST /admin/api/v1/merchants`
  - `GET /admin/api/v1/merchants`
  - `GET /admin/api/v1/merchants/:merchant_no`
  - `PATCH /admin/api/v1/merchants/:merchant_no/features`
  - `POST /admin/api/v1/merchants/:merchant_no/secret:rotate`
  - `GET /admin/api/v1/merchants/:merchant_no/webhooks/config`
  - `PUT /admin/api/v1/merchants/:merchant_no/webhooks/config`
- Customer
  - `POST /admin/api/v1/customers`
  - `GET /admin/api/v1/customers/by-out-user-id`
- Account
  - `POST /admin/api/v1/accounts`
  - `PATCH /admin/api/v1/accounts/:account_no/capability`
  - `GET /admin/api/v1/accounts/:account_no/balance`
- Transaction
  - `POST /admin/api/v1/transactions/credit`
  - `POST /admin/api/v1/transactions/debit`
  - `POST /admin/api/v1/transactions/transfer`
  - `POST /admin/api/v1/transactions/refund`
  - `GET /admin/api/v1/transactions/:txn_no`
  - `GET /admin/api/v1/transactions/by-out-trade-no`
  - `GET /admin/api/v1/transactions`
- Notify & Audit
  - `GET /admin/api/v1/notify/outbox-events`
  - `GET /admin/api/v1/audit/logs`

## 4. 数据表

新增迁移：

- `migrations/000011_admin_console.up.sql`
- `migrations/000012_admin_setup_state.up.sql`

- `admin_user`
- `admin_audit_log`
- `admin_setup_state`

## 5. 关键环境变量

- `ADMIN_ENABLED`（默认 `true`）
- `ADMIN_JWT_SECRET`（默认 `dev_admin_jwt_secret_change_me`）
- `ADMIN_ACCESS_TOKEN_TTL_SECONDS`（默认 `1800`）
- `ADMIN_REFRESH_TOKEN_TTL_SECONDS`（默认 `604800`）

> 首次启动请先调用 setup 初始化管理员与默认商户；上线前请显式覆盖 `ADMIN_JWT_SECRET`。

## 6. Web 控制台

目录：`web/admin`

- Next.js + React + Ant Design
- 页面：`/setup /setup/success /login /dashboard /merchants /customers /accounts /transactions /notify`
