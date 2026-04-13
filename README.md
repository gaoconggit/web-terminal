# web-terminal (Go 版)

一个浏览器可访问的多标签 AI 终端，目标是复刻 `E:\prj\mycc\.claude\skills\web-terminal` 的核心能力：

- Token 登录
- 多标签页（Claude / Codex / pwsh 模板）
- 懒加载 PTY
- WebSocket 实时交互
- 断线重连后保留会话与回放 scrollback
- 运行时新建 / 复制 / 重命名 / 删除标签页
- `runtime-tabs.json` 持久化
- 文件上传、拖拽上传、剪贴板图片上传
- 移动端快捷键（Ctrl+C / Enter / Tab / Esc / 方向键）
- PWA manifest

## 目录

```text
.
├─ main.go
├─ internal
│  ├─ terminal
│  │  ├─ terminal.go
│  │  ├─ pty_windows.go
│  │  └─ pty_unix.go
│  └─ webterm
│     ├─ config.go
│     ├─ server.go
│     └─ web
│        ├─ login.html
│        └─ terminal.html
├─ go.mod
└─ go.sum
```

## 启动

```powershell
$env:WEB_TERMINAL_TOKEN = 'mycc'
$env:WEB_TERMINAL_PORT = '7681'
$env:WEB_TERMINAL_CWD = 'E:\prj\mycc'
go run .
```

启动后会打印：

- 本地地址：`http://127.0.0.1:7681`
- 免手输 token 地址：`http://127.0.0.1:7681/t/<token>`

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `WEB_TERMINAL_BIND` | `127.0.0.1` | 监听地址 |
| `WEB_TERMINAL_PORT` | `7681` | 监听端口 |
| `WEB_TERMINAL_TOKEN` | 随机 | 登录 token |
| `WEB_TERMINAL_CWD` | 当前目录 | PTY 工作目录、上传目录根 |
| `WEB_TERMINAL_TABS` | 内置默认值 | 自定义默认标签页 JSON |

### 自定义标签页

```powershell
$env:WEB_TERMINAL_TABS='[
  {"id":"claude","label":"Claude Code","cmd":"claude","args":["--continue","--dangerously-skip-permissions"]},
  {"id":"codex","label":"Codex","cmd":"codex","args":["resume","--last","--yolo"]},
  {"id":"pwsh","label":"pwsh","cmd":"pwsh","args":[]}
]'
go run .
```

## 持久化

默认沿用原 skill 的兼容路径：

```text
<WEB_TERMINAL_CWD>/.claude/skills/web-terminal/runtime-tabs.json
```

上传文件默认落到：

```text
<WEB_TERMINAL_CWD>/.claude/uploads/
```

## 与原 Node skill 的对应关系

### 已覆盖

- 登录页 + cookie 认证
- `/t/<token>` 兼容入口
- `/health`、`/manifest.json`
- 多标签终端 UI
- 运行时创建 / 删除 / 重命名标签页
- 懒加载终端会话
- scrollback 缓冲和重连回放
- 状态指示（Connected / Disconnected）
- 移动端快捷键栏
- 文件上传 / 拖拽 / 剪贴板图片
- Windows 上用 PowerShell + ConPTY 包装非 pwsh 命令，兼容 `claude` / `codex` 这类 shim 命令

### 当前未完全 1:1 复刻的细节

- 原版里更细的移动端 IME / iOS 组合输入桥接
- 原版的 reader mode、部分手势与滚动增强
- 更复杂的移动端长按重命名、缩放与重绘策略

这些不是架构缺失，而是前端交互层尚未做完全等价移植。

## 验证

已在当前环境完成：

- `go build ./...`
- `go test ./...`
- 本地 smoke test：`/health`、`/login`、`/auth`

## 远程访问

和原 skill 一样，这个 Go 服务本身只负责本地 Web 终端；需要外网访问时可单独配合 cloudflared：

```powershell
cloudflared tunnel --url http://127.0.0.1:7681
```
