# Docker 部署与初始化指南

本文档提供一套最小可用的 Docker 部署流程，覆盖：

- PostgreSQL + Redis + API + Admin Web 启动
- 数据库 migration 执行
- 首次管理员/默认商户初始化

## 1. 文件说明

- Compose: `deploy/docker/docker-compose.yml`
- 一键脚本: `deploy/docker/deploy.sh`
- API 镜像: `deploy/docker/Dockerfile.api`
- Admin Web 镜像: `deploy/docker/Dockerfile.admin`
- 环境变量模板: `deploy/docker/.env.example`

## 2. 前置条件

- Docker 24+（含 `docker compose`）
- 本机可用端口：
  - `8080`（API）
  - `3000`（Admin Web）
  - `5432`（PostgreSQL）
  - `6379`（Redis）

## 3. 配置环境变量

```bash
cp deploy/docker/.env.example deploy/docker/.env
```

必须修改以下字段：

- `LOCAL_KMS_KEY_V1`
- `ADMIN_JWT_SECRET`

可选自动初始化字段：

- `ADMIN_SETUP_USERNAME`
- `ADMIN_SETUP_PASSWORD`
- `ADMIN_SETUP_MERCHANT_NAME`

如果不填自动初始化字段，部署后可手动调用 setup 接口初始化。

## 4. 一键部署

```bash
./deploy/docker/deploy.sh up
```

脚本会按顺序执行：

1. 启动 `db` 和 `redis`
2. 执行全部 `migrations/*.up.sql`
3. 启动 `api` 和 `admin-web`
4. 等待健康检查
5. 若已配置 `ADMIN_SETUP_USERNAME/ADMIN_SETUP_PASSWORD`，自动执行 setup 初始化

部署完成后默认访问：

- API: `http://127.0.0.1:8080`
- Admin Web: `http://127.0.0.1:3000/login`

## 5. 手动初始化（可选）

如果未使用自动初始化，可手动调用：

```bash
curl -sS -X POST http://127.0.0.1:8080/admin/api/v1/setup/initialize \
  -H 'Content-Type: application/json' \
  -d '{
    "admin_username":"admin",
    "admin_password":"ChangeMe123!",
    "merchant_name":"Default Merchant"
  }'
```

初始化状态查询：

```bash
curl -sS http://127.0.0.1:8080/admin/api/v1/setup/status
```

## 6. 常用运维命令

查看状态：

```bash
./deploy/docker/deploy.sh status
```

查看日志：

```bash
./deploy/docker/deploy.sh logs
./deploy/docker/deploy.sh logs api
./deploy/docker/deploy.sh logs admin-web
```

停止服务（保留数据卷）：

```bash
./deploy/docker/deploy.sh down
```

销毁服务并清空数据卷：

```bash
./deploy/docker/deploy.sh destroy
```

## 7. 初始化结果保存

若使用自动初始化，脚本会把 setup 响应保存到：

`deploy/docker/admin_setup_result.json`

该文件可能包含 `merchant_secret`，请按安全要求妥善处理（避免提交到代码仓库）。
