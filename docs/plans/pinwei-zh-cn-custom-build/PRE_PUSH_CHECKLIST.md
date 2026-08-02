---
id: "pinwei-zh-cn-custom-build-pre-push"
title: "品蔚 Kandev v0.83 中文候选预推送清单"
status: "candidate-ready"
base_version: "v0.83.0"
branch: "pinwei/v0.83.0-docker-i18n-zh-CN"
---

# 品蔚 Kandev v0.83 中文候选预推送清单

## 结论边界

本清单的完成条件是 **可推送候选分支**：变更、自动化回归、隔离桌面包与空数据健康启动均有回执。
它不是生产切换批准，也不表示任务调度、Docker 运行时、当前工作区数据或真实 Agent 已在生产通过。

## 已验证回执

- 基线：上游 `v0.83.0`；候选分支：`pinwei/v0.83.0-docker-i18n-zh-CN`。
- 前端全量回归：`1001` 个测试文件通过；`7651` 通过、`4` 跳过。
- 前端生产构建：`pnpm --dir apps/web build` 通过。存在 Vite 大 chunk 警告，未阻断构建。
- 后端定向回归：调度失败恢复、Docker executor、Docker lifecycle、环境销毁和任务环境重置测试通过；`go build ./cmd/kandev` 通过。
- 桌面候选包：以隔离的 Rust/Tauri 工具链重新构建；副本仅在临时目录 ad-hoc 签名后启动。
- 空数据健康冒烟：候选包以全新临时 `KANDEV_HOME_DIR` 启动，`http://127.0.0.1:49231/health` 返回 `status=ok`，随后已停止该候选进程。

## 本分支包含的范围

- Docker executor / agentctl / 生命周期清理的 v0.83 兼容补丁，以及会话启动后失败时避免任务长期停在 `SCHEDULING` 的失败收束。
- 简体中文优先的展示层与 English 切换：覆盖主页、任务创建、任务工作台、会话恢复、Office、设置及其细分页、工作区管理、Agent/执行器、插件、系统维护、集成入口、看板四状态和常见空状态；旧页面通过动态 DOM 兼容层补齐。
- 语言偏好只保存于浏览器 `localStorage`；不改变数据库、任务状态枚举、任务层级或真实任务正文。

## 有意未包含的范围

- 不翻译用户自定义任务名、任务正文、提示词正文、仓库内容、日志、终端输出、模型/协议/CLI 专名和第三方业务数据；这些内容保持原样，避免改变业务语义。产品自带的界面标签、按钮、提示、下拉选项、输入框占位符和无障碍属性均纳入中文展示层。
- 未读写当前 Kandev 数据库、`~/.kandev`、现有任务、模型配置或凭据。
- 未做真实服务器、企微、SmartTable、Docker 拉镜像/容器、生产 Agent 调度验证。
- 候选 app 是 ad-hoc 本地签名，不是面向外部分发的 notarized release。

## 推送前最后检查

在真正 `git push` 前执行并记录：

1. `git status --short` 必须为空；`git diff --check v0.83.0..HEAD` 必须为零。
2. 确认提交只覆盖中文展示层、定向 Docker/调度恢复和本文档，无构建产物或本机数据。
3. 将上游贡献拆为独立提交：上游 PR 保持 English 默认，只提交通用 i18n 基础与可复用测试；中文默认、品蔚术语和本地部署补丁仅推送到个人 fork/定制分支。
4. 推送目标和远端仓库由所有者确认后再执行；本清单不授权创建 fork、推送或发起 PR。

## 与生产切换分离的后续门

另行批准后才可进行：备份当前 app、数据库与未推送工作；副本数据库迁移演练；候选 GUI 手工验证（中文默认、English 切换、Backlog/进行中/审核/完成、任务创建和会话恢复）；针对真实 Docker/Colima 的受控调度回归；以及按分发范围进行签名/notarization。任何一项未验证时，生产结论保持 `UNKNOWN`。
