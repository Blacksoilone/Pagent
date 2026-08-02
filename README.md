# Pagent — 多 Agent 对话工作空间

本地优先的多 Agent 对话工作空间。每条 Agent session 是一条由对话节点组成的持续工作链；节点图是不可变的追加记录——修正通过 fork，删除通过隐藏。

设计文档：[docs/agent-workspace-product-draft.md](docs/agent-workspace-product-draft.md)

## 功能

- **多链并行**：多条持久 Agent 链，每条链独立对话、独立上下文
- **会话运行时**：Agentic Loop（LLM 调用 → 工具循环 → 节点落盘），流式输出 + 中断恢复
- **工具系统**：read_file / list_dir / write_file / edit_file，经权限模型控制（T0-T3 风险分级 + 挂载目录边界）
- **节点文件事务（10.4）**：写工具进入暂存区，节点完成时原子提交
- **横线机制（10.1）**：文件状态锚点，虚横线/实横线，跨链消费通知
- **不可变节点图（2.1）**：节点不可删除/修改，修正通过 fork
- **Web 界面**：Vue 3 + 纵向链图渲染（节点/虚节点/横线）

## 快速开始

### 1. 初始化

```bash
export PAGENT_API_KEY=<你的 OpenAI 兼容 API Key>
go build -o pagent ./cmd/pagent
./pagent init --dir ~/.pagent          # 初始化工作区
./pagent chains                        # 查看链
```

### 2. CLI 对话

```bash
./pagent chat "你好"                    # 第一条链对话
./pagent chat --chain <链ID> "消息"     # 指定链
```

### 3. Web 界面

```bash
# 终端 1：启动后端（默认 :8080）
./pagent serve --dir ~/.pagent --base-url https://opencode.ai/zen/go/v1 --model deepseek-v4-flash

# 终端 2：启动前端（默认 :5173）
cd frontend && npm install && npm run dev
```

浏览器打开 http://localhost:5173

## 项目结构

```
cmd/pagent/           # CLI 入口（init/chains/chat/serve）
internal/
  agent/              # 会话共享层（权限/工具/文件事务/横线回调）
  context/            # 上下文组装器（L1 目录 + 注入消息）
  db/                 # SQLite 持久化（WAL）
  model/              # 领域模型（node/part/chain/stateline）
  permission/         # 权限模型（T0-T3 + 挂载目录边界）
  provider/           # OpenAI-compatible 模型层（SSE 流式）
  runtime/            # Agentic Loop 引擎
  server/             # HTTP/SSE Web 层
  tools/              # 文件工具（write/edit + 节点文件事务）
frontend/             # Vue 3 + TS 前端
docs/                 # 设计文档
```

## 权限模型

工具权限按 T0-T3 风险分级（读文件 → 写文件 → 命令 → 危险操作），
由"工具 + 参数"共同决定，而非工具名。路径必须位于项目挂载目录内
（授权不越界，禁止可越界）。CLI 默认项目内 T1（读/写文件）自动允许。

## 状态

里程碑 1（本地 Web 模式）进行中。后端/前端核心链路已通，
浏览器视觉验证与 UI 打磨待进行。
