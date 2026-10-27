# HKTicket 架构设计

## 总体结构

HKTicket 由四层组成，全部编译为单个静态二进制(Go，无 CGO):

- `main.go` — 入口分流:无参数 → 图形控制台；带参数 → 命令行
- `internal/core` — 平台无关的任务编排与页面判定
- `internal/driver` — 浏览器控制接口与两个实现
- `internal/webui` — 图形控制台(标准库 HTTP 服务 + embed 内嵌页面)

辅助模块:`internal/clock`(NTP 校时)、`internal/settings`(参数记忆)、`internal/paths`(程序所在目录定位)。

支持的网站固定在 `core.Sites` 注册表(标识、显示名、排队入口地址)，新增网站只需在注册表加一行。另有 `custom` 选项:用户粘贴任意入口地址，经 `core.ResolveSite` 校验 http/https 后使用。用户选择网站并提供开售时间。时间固定按香港时区解析，与运行机器的系统时区无关。

## 用户数据位置

全部保存在可执行文件所在目录(`paths.AppDir()`)，不写入用户主目录:

- `settings.json` — 上次输入的参数，启动时自动作为默认值
- `browser-profile/` — CDP 驱动的浏览器会话目录

整个文件夹拷到别的机器即可带走全部状态。

## 浏览器驱动

`internal/driver/driver.go` 定义接口。一个驱动实例对应一个浏览器窗口，窗口内固定 1 个标签页；多会话靠多个驱动实例实现:

```
OpenTabs(url)    新开一个窗口并打开目标地址
GetContent()     返回 #root(或 body)的 innerText；导航过渡态返回空串
ClickEntry()     点击入口按钮，返回按钮文本
Reload()         恢复性重载
BringToFront()   置前窗口并激活标签页
```

### Safari 驱动(safari_darwin.go，仅 macOS 编译)

通过 `osascript` 执行 AppleScript 控制系统 Safari。要点:

- 「允许 Apple 事件中的 JavaScript」必须开启，启动时用 `do JavaScript "1"` 预检；新版 macOS 返回 `"1.0"`，按数值比较
- JS 代码嵌在 AppleScript 双引号字符串里，反斜杠会被 AppleScript 吃掉，所以 CLICK_BUTTON_JS 不用 `\b`，英文整词约束写成 `^(join|enter|proceed|continue)( |$)`
- URL 先去掉 shell 转义(`\?` `\&` `\=`)，再按 AppleScript 规则转义

### CDP 驱动(cdp.go，全平台)

`go-rod/rod` 走 Chrome DevTools Protocol 驱动系统浏览器:

- 内置候选表按 Chrome → Chromium → Edge 的固定顺序探测(Chrome 优先)，均未命中时回退 `launcher.LookPath()`
- `--user-data-dir` 指向持久化会话目录(第 1 个会话沿用 `browser-profile/`，其余为 `browser-profile-2/`、`-3/`…)，人机验证 Cookie 跨运行保留；各目录 Cookie 互不可见，排队系统把每个实例当作独立身份
- 启动参数去掉 go-rod 默认的 `--enable-automation`，并加 `--disable-blink-features=AutomationControlled`，避免 `navigator.webdriver` 为 true 让 reCAPTCHA Enterprise 反复要求验证
- 每个会话目录同一时刻只允许一个浏览器实例使用:启动后在目录里记录浏览器 PID，下次启动先检查该进程是否存活，占用时立即报错提示先关闭已有窗口；进程被强杀、PID 文件缺失时，再读目录里的 `DevToolsActivePort` 找到残留浏览器并通过 CDP 关掉，避免新版 `msedge` 把启动命令转交给旧窗口后自己退出；macOS/Linux 另外检查 Chrome 的 SingletonLock。启动过程带 30 秒超时，避免 DevTools 端口迟迟不出现时无限等待
- 不拦截页面资源，图片、字体、音视频与脚本正常加载，保证排队后进入购票页面时元素完整显示
- 拿到排队位或出现过人机验证时保留浏览器窗口，由用户继续操作；停止、启动阶段出错、超时且无人机验证时调用 `Close()` 关闭窗口，释放会话目录；即使 CDP 连接已经断开(用户手动关了窗口)，也会等待并强制结束进程
- 因此关闭 go-rod 的 leakless(同时避免 Windows Defender 误报 `leakless.exe` 为病毒)

驱动选择:`--driver auto` 时 macOS → safari，其余平台 → cdp；自动模式下会话数大于 1 直接改用 cdp(Safari 全系统共享 Cookie，多会话拿到的仍是同一个排队号，显式指定 safari 才报错)。`driver.NewN(name， count)` 一次创建 count 个相互独立的驱动。

## 监视策略(core.Monitor)

来自 2026-09-12 实测的行为模型:PleaseQ 是自动更新的单页应用，等候室会自行切换，持续刷新只会中断应用启动，还会抹掉入口按钮。因此:

1. 每轮依次轮询各会话的标签页内容(每会话固定 1 个标签页，日志前缀 `[S<会话号>]`)
2. BUTTON_HINT_RE 命中即点击按钮:
   - 「重新整理」类按钮(RefreshHintRE，文案为重新整理 / Refresh / Reload):每轮重复点击，不记入 entered。PleaseQ 前端在距开售不超过 5 秒时显示该按钮，点一次只触发一次 servingCounter 查询；页面自身的轮询间隔是 serving_number_rotate_rate(通常长于检查间隔)，所以开售瞬间必须连续点击，只点一次会错过
   - 真正的入口按钮(进入网站 / 進入網站 / Enter 等):点击一次，记入 entered，之后该会话不再重载
3. 命中人机验证页特征(NeedsVerification)→ 日志提示并把该会话窗口置前，等待用户手动完成，不重载、不点击
4. CheckHit 命中 → 提取排队编号、置前窗口、标记该会话命中，继续监视其余会话；全部命中才提前结束
5. 页面空白或含 5xx 关键词(IsErrorPage)→ 恢复性重载，每个会话 5 秒退避
6. 开售后兜底:没有出现过「重新整理」按钮的会话(非 PleaseQ 页面)到点后第一个轮询仍无入口或命中，就主动重载页面，之后每秒重试；出现过刷新按钮的 PleaseQ 页面不重载，避免打断排队
7. 1 号会话内容变化时打印前 60 字符，供诊断

超时后只要至少一个会话命中即算任务成功，日志汇报命中/未命中会话数。

判定优先级(core/detect.go):错误页关键词 → 等候室关键词 → 排队页关键词。CheckHit 只认排队页的明确特征；开售前的官网菜单页内容很长，任何长度兜底都会误报，2026-10-23 实测确认后移除了兜底规则。

## 定时(core.Run + internal/clock)

- 手写 NTP 客户端(UDP 48 字节报文)依次查询 `ntp.aliyun.com`、`ntp.tencent.com`、`ntp.ntsc.ac.cn`、`time.apple.com`，取服务器发送时刻与本地往返中点之差作为本地时钟偏移
- NTP 全部失败时(常见于 UDP 123 被网络/防火墙拦截)，用百度/腾讯/京东的 HTTPS `Date` 响应头兜底，精度约 1 秒；两条路都失败才报错退出，并在错误里汇总每台服务器的失败原因
- 校时失败立即报错退出:抢号对时间敏感，用未校准的时钟继续运行没有价值
- 偏移应用到全部计时；目标时间固定按香港时区解析(`time.ParseInLocation` + `core.TargetLocation`，时区数据库由 `time/tzdata` 内嵌，Windows 等无系统时区库的平台也可用)
- 等待阶段以 200 毫秒为步长睡眠，随时响应停止信号

## 图形控制台(internal/webui)

- `embed.FS` 内嵌 `index.html`，标准库 `net/http` 监听 `127.0.0.1` 随机端口，启动后按平台打开浏览器；Windows 优先用 Chrome(与抢票窗口同一套探测逻辑)，找不到才回退 `rundll32` 打开系统默认浏览器(通常是 Edge)；macOS/Linux 用 `open` / `xdg-open`
- `/api/start` 校验参数后保存参数记忆，并在后台 goroutine 运行 `core.Run`；`/api/stop` 关闭停止通道并等待任务真正退出后返回，紧跟其后的 `/api/start` 会等待正在停止的旧任务结束再启动；用户主动停止属于正常结束，日志输出「任务已停止」而不是错误；`/api/state` 返回运行/停止中状态，页面刷新后据此恢复按钮；`/api/logs` 用 SSE 推送日志；`/api/settings` 返回保存的参数供表单预填；`/api/sites` 返回 `core.Sites` 网站列表供下拉框渲染(网站名单只维护一份)
- 日志经订阅通道广播，订阅者读取过慢时丢弃，不阻塞任务
- 每个会话固定 1 个标签页(同一会话内多开标签页共享 Cookie，没有额外队列位)；会话数 1–10 可配，多会话靠多个独立浏览器实例实现

## 构建

`build.sh` 交叉编译四个目标(darwin/arm64、darwin/amd64、windows/amd64、linux/amd64)，`-ldflags="-s -w"` 去掉符号表，产物各约 11MB。Safari 驱动文件带 `//go:build darwin` 约束，Windows/Linux 产物自动排除。
