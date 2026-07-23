# Windows 构建指南

## Windows 环境准备

### 1. 安装 Go 1.25+

从 [go.dev/dl](https://go.dev/dl/) 下载安装，安装后验证：

```powershell
go version
```

### 2. 安装 Node.js 18+

从 [nodejs.org](https://nodejs.org/) 下载安装 LTS 版本，安装后验证：

```powershell
node -v
npm -v
```

### 3. 安装 GCC（MSYS2）

SI 依赖 `mattn/go-sqlite3`，需要 CGO 编译，因此必须有一个 C 编译器。

1. 从 [msys2.org](https://www.msys2.org/) 下载安装 MSYS2
2. 打开 **MSYS2 MSYS** 终端，执行：

```bash
pacman -Syu
pacman -S mingw-w64-x86_64-gcc
```

3. 将 **包含 `gcc.exe` 的 MinGW-w64 `bin` 目录** 加入系统 `Path`。该路径取决于 MSYS2（或其他 MinGW-w64 工具链）的实际安装位置——默认 MSYS2 布局下通常是 `<MSYS2 根目录>\mingw64\bin`（若安装在 `C:\msys64`，则为 `C:\msys64\mingw64\bin`，仅作示例）。
   - 右键「此电脑」→ 属性 → 高级系统设置 → 环境变量
   - 在「系统变量」中找到 `Path`，编辑并新增上述 `bin` 目录
   - 重启终端使 PATH 生效

验证：

```powershell
where.exe gcc
gcc --version
```

### 4. 允许 Go 的 CGO 工具运行

Windows 构建依赖 CGO（`CGO_ENABLED=1` 与 `sqlite_fts5` build tag）。除可用的 `gcc` 外，还须保证 Go 安装目录中的 **`cgo` 工具可被执行**（通常位于 `GOROOT\pkg\tool\<GOOS>_<GOARCH>\cgo.exe`）。

在部分机器上，**Windows 应用程序控制**（包括智能应用控制 Smart App Control、WDAC、AppLocker 等）会拦截该二进制。此时 `go build` 会在编译项目代码之前失败，错误类似：

```text
go: error obtaining buildID for go tool cgo: fork/exec ...\cgo.exe: An Application Control policy has blocked this file.
```

本地构建要求当前策略环境允许运行 Go 的 `cgo` 工具。

## 构建

```powershell
# 克隆或拷贝源码到本地后进入项目目录
cd session-insight

# 构建前端
cd frontend
npm ci
npm run build
cd ..

# 将前端产物复制到 Go embed 目录
xcopy /E /Y frontend\dist internal\web\dist\

# 编译（CGO 必须开启）
set CGO_ENABLED=1
go build -tags sqlite_fts5 -o session-insight.exe .
```

构建完成后 `session-insight.exe` 即为完整可执行文件，前端已嵌入二进制中。

## 运行

```powershell
.\session-insight.exe
```

默认监听 `http://127.0.0.1:8080`，浏览器会自动打开。

可通过环境变量配置：

```powershell
$env:PORT = "9090"
$env:AGENT_DIRS = "C:\Users\YourName\.chrys\sessions;C:\Users\YourName\.claude\projects"
.\session-insight.exe
```

启动后会用实际监听地址打开浏览器（`http://127.0.0.1:<port>/`）。若默认 `PORT` 已被占用并回退到系统分配端口，打开的也是回退后的地址。

## 常见问题

**`gcc: not found` 或 `exec: "gcc": executable file not found`**

PATH 中没有 MinGW-w64 的 `gcc`。确认包含 `gcc.exe` 的 `bin` 目录已加入系统 `Path`，并重启终端。可用 `where.exe gcc` 查看实际解析到的路径。

**`sqlite3-binding.c: fatal error C1083: Cannot open include file: 'stdio.h'`**

说明使用了 MSVC 而非 mingw-w64 的 gcc。确保 PATH 中 MinGW-w64 的 `bin` 路径排在 MSVC 之前，或在 MSYS2 MinGW 64-bit 终端中执行构建命令。

**`An Application Control policy has blocked this file`（常见于 `cgo.exe`）**

Windows 应用程序控制阻止了 Go 的 `cgo` 工具运行。见 [允许 Go 的 CGO 工具运行](#4-允许-go-的-cgo-工具运行)。这是主机策略限制，不是项目缺少依赖。

**`npm run build` 失败**

尝试删除 `frontend/node_modules` 后重新 `npm ci`。
