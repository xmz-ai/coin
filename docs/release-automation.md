# Release 自动化（PR Label 驱动）

目标：PR 合并到 `main` 后，无需手动打 tag/发 release。

## 1. 你只需要做什么

提往 `main` 的 PR，选择 release label：

- `release:none`
- `release:core:patch`
- `release:core:minor`
- `release:core:major`
- `release:sdk-go:patch`
- `release:sdk-go:minor`
- `release:sdk-go:major`

支持同时选择 1 个 core label + 1 个 sdk-go label（同一次合并同时发两个版本）。

## 2. 自动流程

1. PR 阶段：`PR Release Label Check` 校验 label 合法性
2. 合并 `main` 后：`Release Bump On Merge`
   - 根据 label 自动 bump 版本文件
     - 核心服务：`VERSION`
     - Go SDK：`sdk/go/coin/VERSION`
   - 自动 commit 回 `main`
   - 自动创建并推送 tag
     - 核心：`vX.Y.Z`
     - SDK：`sdk/go/coin/vX.Y.Z`
3. tag 推送后自动进入发版流程
   - `Release Core`（`v*`）
   - `Release SDK Go`（`sdk/go/coin/v*`）

## 3. Workflow 文件

- `.github/workflows/pr-release-label-check.yml`
- `.github/workflows/release-bump-on-merge.yml`
- `.github/workflows/release-core.yml`
- `.github/workflows/release-sdk-go.yml`

## 4. 注意事项

- 需在仓库预先创建以下 labels（名称需完全一致）：
  - `release:none`
  - `release:core:patch` `release:core:minor` `release:core:major`
  - `release:sdk-go:patch` `release:sdk-go:minor` `release:sdk-go:major`
- 建议在 GitHub 开启 branch protection，要求 `PR Release Label Check` 必须通过后才可合并。
- `release:none` 不能和其他 `release:*` label 同时存在。
- 同一目标不能同时挂多个级别（例如 `release:core:patch` + `release:core:minor`）。
- 首次版本基线：
  - `VERSION = 0.1.0`
  - `sdk/go/coin/VERSION = 0.1.0`
