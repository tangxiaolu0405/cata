# Cata Desktop — 桌面工作空间浏览器

`cata-desktop` 是 cata 的**桌面侧边应用**：左侧工作空间树 + 右侧「多文件查看器 / 多内嵌终端」，
像 VSCode 一样浏览文件、内嵌终端、打开编辑器查看内容。它**不侵入 cata 核心**——不参与 LLM
对话、不写脑子，只读注册表与文件系统；真正的 `cata`（TUI 对话）运行在内嵌终端里，两者各司其职。

> 定位：`cata` 是终端优先的 agent，`cata-desktop` 是给它配的「工作台」。
> 交互闭环：桌面端看文件/开终端 → 终端里跑 `cata`（或任意 shell 命令）→ LLM 产出来到文件系统 → 桌面端查看。

## 技术栈与边界

- **纯 Go + Fyne**：无 WebView、无 npm、无 build tags；跨平台（macOS / Windows / Linux）。
- **不修改 cata 核心**：工作空间来自 `~/.cata/link.json`（注册的常驻工作区）+ `~/.cata/desktop.json`
  （桌面端自己手动添加的目录）；只读注册表，不写 `link.json`，不碰 `internal/cata/{server,client,evolve,brain}`。
  **边界如实声明**：读取 `link.json` 经 `link.LoadConfig()`，其内部会调用 `brain.EnsureCataLayout()`
  （mkdir CATA_HOME 目录 + seed guidance + 迁移），即桌面端**不主动写**注册表与脑子内容，但会触发
  核心布局/迁移的幂等初始化副作用——这是核心读接口的既有行为，桌面端不做额外写操作。
- **终端是自带的**：内嵌终端用 `cmd/cata-desktop/terminal`（PTY + 完整 VT/ANSI 渲染，独立子包），
  运行系统 shell（macOS/Linux 用 `$SHELL`，Windows 用 PowerShell）。在终端里输入任意命令、启动 `cata` 均可。

## 架构总览

```
┌───────────────────────────── cata-desktop ─────────────────────────────┐
│ 左：工作空间树 (widget.Tree)              右：多窗口区                   │
│   ├─ ws:<root> 工作空间（顶级文件夹）         ├─ Tab 模式（默认）          │
│   │     └─ 文件/目录（懒加载）               │   标签栏(可拖动重排) + 激活 │
│   └─ 点击文件 → 打开查看器窗口               │   窗口内容占满 + 底部路径栏  │
│                                              └─ Split 模式（⌘\）         │
│                                                多窗口并排平铺 + 自绘滚动条  │
└─────────────────────────────────────────────────────────────────────────┘
           │ 读 link.json / desktop.json（工作空间） │ 读文件系统（目录/文件）
           ▼                                        ▼
   ~/.cata（CATA_HOME）                         磁盘上的项目目录
```

- **工作空间**：树的最顶级是工作空间（顶级文件夹），点开才是里面的文件和目录。
- **窗口（panel）**：文件查看器窗口或内嵌终端窗口；同一份内容在 Tab / Split 两种呈现间移动，
  同一时刻只挂在一棵对象树上（切换模式时先摘再挂，避免 Fyne 重复挂载）。
- **数据流**：`workspaces.go` 汇总工作空间 → 树懒加载目录（`ListDir`，缓存 + `.git` 跳过）→
  点击文件 `ReadFile`（512KB 截断 + 二进制检测）→ `TextGrid` 预览。

## 界面与交互

### 左侧：工作空间树

- 顶级节点 = 工作空间（两行：名称加粗 + 路径灰色斜体）；点击**展开/收起**并设为当前工作空间。
- 目录节点单击展开/收起，文件节点单击**打开预览窗口**（点击一个加载一个）。
- **右键菜单**：
  - 工作空间节点：在终端中打开 / 在文件管理器中显示 / 复制路径 /（手动添加的）从列表移除。
  - 文件/目录节点：打开 / 在终端中打开 / 在文件管理器中显示 / 复制路径 / **删除**（二次确认，不可恢复）。

### 右侧：Tab 模式（默认，参考 VSCode）

- **标签栏**：所有窗口标题连成一行，激活标签底部有主色条；标签宽度定长区间
  `[140, 240]`（短标题按内容，长标题省略号截断）。
- **拖动重排**：按住标签横向拖动，被拖标签**实时跟随光标**浮在最上层，其余标签在目标槽位让出等宽空隙，
  松手落定（VSCode 式「跟手」拖拽，边缘判定换位）。
- 点击标签切换激活；点标签上的 **✕** 关闭窗口。
- 激活窗口的内容占满整个右侧，**底部状态栏**显示完整路径（标题只放文件名 + 大小）。
- 标签过多时标签行横向滑动（滚动条在标签行正下方）。

### 右侧：Split 模式（⌘/Ctrl+\）

- 多窗口**并排平铺**：每个窗口 = 定长标题栏（200px，标题 + 固定 ✕）+ 内容 + 底部路径栏 + 右分隔线。
- 窗口总宽 ≤ 可用宽度时**等宽铺满**；超出时每个窗口保持最小定宽，靠横向滚动查看后面的窗口；
  关闭任意一个，其余**自动重排占满**。
- **滚动条**：Fyne 内置滚动条固定在视口底部无法移动，这里用 `hiddenScrollTheme` 隐藏它，
  由 `hscrollBar` 自绘在**标题栏正下方**（细轨道 + 滑块，可拖动、可滚轮，与外层 HScroll 双向同步）。

### 系统菜单与快捷键

无顶部工具栏；功能入口在**系统菜单 + 快捷键**。

| 功能 | macOS | Windows / Linux |
|------|-------|-----------------|
| 添加目录… | ⌘O | Ctrl+O |
| 复制路径 | ⌘⇧C | Ctrl+⇧C |
| 在文件管理器中显示 | ⌘⇧R | Ctrl+⇧R |
| 系统打开 | ⌘⇧O | Ctrl+⇧O |
| 重新打开上次文件 | ⌘1 | Ctrl+1 |
| 新建终端 | ⌘2 | Ctrl+2 |
| 分割窗口 | ⌘\ | Ctrl+\ |
| 合并窗口 | ⌘⇧\ | Ctrl+⇧\ |
| 关闭所有窗口 | ⌘⇧W | Ctrl+⇧W |
| 在终端中打开…（系统终端） | ⌘⇧T | Ctrl+⇧T |
| 退出 | ⌘Q | Ctrl+Q |

## 功能细节

- **文件查看器**：`TextGrid` 只读预览，带行号、双向滚动；二进制文件提示不支持预览；
  超过 512KB 截断并标记。长文件在固定窗口内滚动，不会撑大窗口。
- **内嵌终端**：每个终端窗口一个独立 shell（PTY），启动于工作空间根目录；关闭窗口/退出应用时
  发送 `^D` 结束 shell，避免遗留孤儿进程。**可开多个终端窗口**。
- **去重**：同一文件重复点击不重复开窗口，改为激活已有窗口；「重新打开上次文件」同理。
- **同文件去重 + 标题**：窗口标题 = 文件名（+ 大小），完整路径在底部状态栏单独展示。
- **中文字体**：捆绑 Noto Sans CJK SC 作为界面普通字体（跨平台不依赖系统字体）；
  等宽字体保持默认（终端与代码预览需要真正的等宽度量，CJK 不参与对齐）。
- **原生能力**：目录选择用系统原生对话框（macOS `osascript choose folder`，Win/Linux 对应实现）；
  「系统打开 / 文件管理器显示」调用系统默认应用。

## 构建与安装

```sh
# 编译并安装到 ~/.local/bin/cata-desktop（macOS 自动 ad-hoc 签名）
sh ./cmd/cata-desktop/build.sh

# 或仅编译
cd /Users/lucas/work/cata
go build ./cmd/cata-desktop
```

启动：

```sh
cata-desktop
```

首次使用：`文件 → 添加目录…` 选择项目根；或先用 `cata link add` 注册工作区（会自动出现在左侧树，
排在最前）。启动默认打开当前第一个工作空间的**内嵌终端**。

## 代码布局（`cmd/cata-desktop/`）

| 文件 | 角色 |
|------|------|
| `main.go` | 入口：`desktop.NewApp().Run()` |
| `desktop/app.go` | App 状态与主逻辑：UI 组装、菜单/快捷键、Tab/Split 切换、窗口增删、拖动重排、树与文件加载 |
| `desktop/panel.go` | `panel` 统一窗口（文件/终端）；Split 模式外壳（定长标题栏 + 底部路径栏 + 右分隔线） |
| `desktop/tabbar.go` | Tab 模式标签栏：`tabRowLayout` 挨着排列 + VSCode 式拖动（浮层跟随光标、边缘判定换位） |
| `desktop/workspaces.go` | 工作空间来源（link.json + desktop.json）、`ListDir` / `ReadFile` / 二进制检测 |
| `desktop/layouts.go` | `fixedSplitLayout`（左右分栏）、`tileLayout`（多窗口并排/滚动）、`hbarOverlayLayout` |
| `desktop/hscrollbar.go` | 自绘水平滚动条 + `hiddenScrollTheme`（隐藏 Fyne 内置滚动条） |
| `desktop/context_menu.go` | 树节点右键菜单（打开/终端/文件管理器/复制路径/删除/移除工作空间） |
| `desktop/node_cell.go` | 树节点单元格（工作空间两行式、单击/右键） |
| `desktop/terminal_panel.go` | 内嵌终端生命周期（start/stop，每窗口一个 shell） |
| `desktop/open_terminal.go` | 在系统终端打开目录（macOS iTerm/Terminal、Windows cmd、Linux 常见模拟器） |
| `desktop/theme.go` | 捆绑中文字体主题（等宽样式保持默认字体） |
| `desktop/native_dialog_*.go` | 平台原生目录选择器（darwin/linux/windows） |
| `terminal/` | 独立子包：PTY + VT/ANSI 终端渲染（`Terminal` 控件，含选择/复制粘贴/快捷键） |

## 设计约束

1. **不侵入核心**：不 import `internal/cata/{server,client,evolve,brain}` 的运行时逻辑；
   只复用 `internal/cata/config`（CATA_HOME 路径）与 `internal/cata/link`（工作空间注册读取）。
2. **不写注册表**：手动添加的目录只进 `~/.cata/desktop.json`（`extras` 列表），不进 `link.json`。
3. **状态全在 App 内**：无数据库；目录/文件缓存只活在内存（`dirCache` 等），工作空间变更时清空。
4. **内容单挂载**：同一 `content` 在 Tab / Split 间移动时必须先摘再挂，防止 Fyne 对象重复挂树。
5. **`cmd/cata-desktop/` 已被 `.gitignore` 忽略**：改动不体现在 `git status`（`go.mod/go.sum` 除外，勿动）。

## 版本历史

| 版本 | 要点 |
|------|------|
| 0.8.0 | Tab 标签加宽（140–240）+ VSCode 式实时跟随拖拽（边缘判定、浮层 + 空隙让位） |
| 0.7.x | Tab/Split 双呈现、定长标题栏、自绘滚动条、多窗口自动占满、右键菜单、去重、底部完整路径 |
