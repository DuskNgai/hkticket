package driver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

// CDPDriver 通过 Chrome DevTools Protocol 控制系统 Chrome / Edge / Chromium。
// 使用持久化用户数据目录,人机验证 Cookie 跨运行保留;
// 不同会话目录的实例 Cookie 互不共享,在排队系统眼里是独立身份
type CDPDriver struct {
	browser    *rod.Browser
	tab        *rod.Page
	url        string
	profileDir string
	pid        int
}

func NewCDP(profileDir string) (*CDPDriver, error) {
	bin, found := lookBrowser()
	if !found {
		return nil, fmt.Errorf("未找到系统浏览器,请安装 Chrome 或 Edge")
	}
	// 上一次任务异常结束时浏览器进程可能还在,新启动的 msedge 会把命令转交给它
	// 并立刻退出,DevTools 端口永远不会出现。先按配置目录里的调试端口找到并关掉它,
	// 用户不必自己去关残留的浏览器窗口;关不掉的由 cleanupStaleBrowser 报错
	if err := cleanupStaleBrowser(profileDir); err != nil {
		return nil, err
	}
	// 调试端口确认关闭后,再用 PID 文件核对一次:进程已退出就清掉残留记录;
	// 仍然存活说明它不是本工具记录的那个浏览器(例如 PID 被复用),宁可报错也不误杀
	if err := checkProfileOwner(profileDir); err != nil {
		return nil, err
	}
	// macOS/Linux 上再检查 Chrome 自己的单实例锁,覆盖不是本工具启动的浏览器
	if runtime.GOOS != "windows" {
		switch err := clearStaleLock(profileDir); {
		case errors.Is(err, errNoLockFile):
		case err != nil:
			return nil, err
		}
	}
	launch := func() (string, int, error) {
		// 启动过程也必须有超时:配置目录被占用时 Launch 会一直等不到 DevTools 端口
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		l := launcher.New().Context(ctx).Bin(bin).Leakless(false).
			Delete("enable-automation").
			Set("disable-blink-features", "AutomationControlled").
			Set("user-data-dir", profileDir).Headless(false)
		u, err := l.Launch()
		if errors.Is(err, context.DeadlineExceeded) {
			return "", 0, fmt.Errorf("浏览器在 30 秒内没有启动,请确认没有仍然打开着同一会话目录的 Chrome 窗口")
		}
		return u, l.PID(), err
	}
	controlURL, pid, err := launch()
	if err != nil && runtime.GOOS != "windows" {
		// 单实例锁残留(进程已退出)则清掉重试一次;仍在用则明确告知
		switch cerr := clearStaleLock(profileDir); {
		case errors.Is(cerr, errNoLockFile):
		case cerr != nil:
			return nil, cerr
		default:
			controlURL, pid, err = launch()
		}
	}
	// 上一次的浏览器窗口还没退干净时,新启动的 msedge 会把命令转交给它,
	// 然后自己立刻退出,缓冲区里什么都没有。再清一次残留进程后重试一次;
	// 30 秒超时说明端口始终没出现,重试只会再等 30 秒,直接报错
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		if cerr := cleanupStaleBrowser(profileDir); cerr == nil {
			time.Sleep(500 * time.Millisecond)
			controlURL, pid, err = launch()
		}
	}
	if err != nil {
		if profileInUse(err) {
			return nil, fmt.Errorf("启动浏览器失败: 会话目录 %s 仍被浏览器窗口占用,请关闭残留的浏览器窗口后重试", profileDir)
		}
		return nil, fmt.Errorf("启动浏览器失败: %w\n"+
			"如果浏览器进程立刻退出,通常是占用会话目录 %s 的残留窗口还没关闭,请关闭后重试", err, profileDir)
	}
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		browser.Close()
		if kerr := terminate(pid); kerr != nil {
			return nil, fmt.Errorf("连接浏览器失败: %w;清理浏览器进程失败: %v", err, kerr)
		}
		return nil, fmt.Errorf("连接浏览器失败: %w", err)
	}
	if err := saveBrowserPID(profileDir, pid); err != nil {
		browser.Close()
		_ = terminate(pid)
		return nil, err
	}
	return &CDPDriver{browser: browser, profileDir: profileDir, pid: pid}, nil
}

// Close 关闭浏览器进程,等进程真正退出后清掉 PID 文件,
// 避免紧接着的新任务与仍在退出的旧进程争用同一份会话目录
func (d *CDPDriver) Close() error {
	// 用户手动关掉窗口后 CDP 连接已断开,browser.Close 会报错,
	// 但进程仍必须回收,否则会话目录会被残留进程一直占用
	_ = d.browser.Close()
	if err := terminate(d.pid); err != nil {
		return err
	}
	if err := os.Remove(pidFilePath(d.profileDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除浏览器 PID 文件失败: %w", err)
	}
	return nil
}

// terminate 等待浏览器进程自行退出,超时后强制结束。
// 用于 CDP 关闭失败(例如用户已经关掉窗口、连接已断开)后的兜底,
// 确保会话目录不再被残留进程占用
func terminate(pid int) error {
	for i := 0; i < 30 && processAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !processAlive(pid) {
		return nil
	}
	// Browser.close 没有让进程退出(例如后台继续运行设置),直接结束它
	p, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("结束浏览器进程失败: %w", err)
	}
	if err := p.Kill(); err != nil {
		return fmt.Errorf("结束浏览器进程失败: %w", err)
	}
	for i := 0; i < 20 && processAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(pid) {
		return fmt.Errorf("浏览器进程(PID %d)未能退出", pid)
	}
	return nil
}

func pidFilePath(profileDir string) string {
	return filepath.Join(profileDir, ".hkticket-browser.pid")
}

// checkProfileOwner 用 PID 文件判断上一次启动的浏览器是否仍在运行。
// 进程已退出时清掉残留文件;文件内容损坏则报错,避免静默放过异常的配置目录
func checkProfileOwner(profileDir string) error {
	path := pidFilePath(profileDir)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取浏览器 PID 文件失败: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("浏览器 PID 文件内容无效: %q", strings.TrimSpace(string(data)))
	}
	if processAlive(pid) {
		return fmt.Errorf("已有使用同一会话目录的浏览器进程在运行(PID %d),请先关闭它的窗口", pid)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("清理残留的浏览器 PID 文件失败: %w", err)
	}
	return nil
}

func saveBrowserPID(profileDir string, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("浏览器进程号无效: %d", pid)
	}
	if err := os.WriteFile(pidFilePath(profileDir), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("记录浏览器进程号失败: %w", err)
	}
	return nil
}

// profileInUse 判断启动失败是否因为会话目录已被另一个浏览器窗口占用。
// 中文版 msedge 的提示是 GBK 编码,直接显示会乱码,这里识别后换成可读文案
func profileInUse(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "\xd5\xfd\xd4\xda\xcf\xd6\xd3\xd0\xe4\xaf\xc0\xc0\xc6\xf7\xbb\xe1\xbb\xb0\xd6\xd0\xb4\xf2\xbf\xaa\xa1\xa3") ||
		strings.Contains(msg, "Opening in existing browser session")
}

// devToolsPort 读取配置目录里 DevToolsActivePort 的第一行(调试端口)。
// 文件不存在或内容异常时返回 0
func devToolsPort(profileDir string) int {
	data, err := os.ReadFile(filepath.Join(profileDir, "DevToolsActivePort"))
	if err != nil {
		return 0
	}
	line, _, _ := strings.Cut(string(data), "\n")
	port, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}

// devToolsWS 询问调试端口对应的 WebSocket 地址;
// 浏览器已经退出或端口不可用时返回 false
func devToolsWS(port int) (string, bool) {
	client := http.Client{Timeout: 2 * time.Second}
	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/version", port))
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", false
	}
	var info struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil || info.WS == "" {
		return "", false
	}
	return info.WS, true
}

// cleanupStaleBrowser 关闭仍占用会话目录的浏览器。
// 上次任务异常结束(控制台被关闭、进程被强杀)时浏览器窗口会留在后台,
// 之后新启动的 msedge 会把命令转交给它并立即退出,DevTools 端口永远不会出现。
// 只使用配置目录里记录的调试端口,不会误关用户自己的其他浏览器窗口
func cleanupStaleBrowser(profileDir string) error {
	port := devToolsPort(profileDir)
	if port == 0 {
		return nil
	}
	ws, ok := devToolsWS(port)
	if !ok {
		return nil // DevToolsActivePort 只是上次运行留下的残留文件
	}
	b := rod.New().ControlURL(ws)
	if err := b.Connect(); err != nil {
		return nil
	}
	_ = b.Close()
	for i := 0; i < 50; i++ {
		if _, ok := devToolsWS(port); !ok {
			// 调试端口先关闭、进程稍后才退出。等 PID 文件里的进程一并退出,
			// 避免紧接着的 checkProfileOwner 把正在退出的浏览器当成仍在运行
			waitProfileProcessGone(profileDir)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("会话目录 %s 仍被浏览器占用(调试端口 %d),请手动关闭残留的浏览器窗口后重试", profileDir, port)
}

// waitProfileProcessGone 等 PID 文件记录的浏览器进程退出,最多等 3 秒。
// 进程早已退出或没有 PID 文件时立即返回
func waitProfileProcessGone(profileDir string) {
	data, err := os.ReadFile(pidFilePath(profileDir))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return
	}
	for i := 0; i < 30 && processAlive(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
}

var errNoLockFile = errors.New("无残留锁文件")

// clearStaleLock 检查配置目录的单实例锁(macOS/Linux 为形如 "主机名-PID" 的符号链接):
// 锁指向的进程已退出则删除锁文件,返回 nil 表示可以重试启动;
// 进程仍在运行则返回错误,提示用户先关闭那个窗口
func clearStaleLock(profileDir string) error {
	target, err := os.Readlink(filepath.Join(profileDir, "SingletonLock"))
	if errors.Is(err, os.ErrNotExist) {
		return errNoLockFile
	}
	if err != nil {
		return fmt.Errorf("读取单实例锁失败: %w", err)
	}
	dash := strings.LastIndex(target, "-")
	if dash < 0 {
		return fmt.Errorf("无法解析的单实例锁: %s", target)
	}
	pid, err := strconv.Atoi(target[dash+1:])
	if err != nil {
		return fmt.Errorf("无法解析的单实例锁: %s", target)
	}
	if processAlive(pid) {
		return fmt.Errorf("已有使用同一会话目录的浏览器进程在运行(PID %d),请先关闭它的窗口", pid)
	}
	for _, f := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if err := os.Remove(filepath.Join(profileDir, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("清理残留锁文件失败: %w", err)
		}
	}
	return nil
}

func (d *CDPDriver) OpenTabs(url string) error {
	d.url = url
	page, err := d.browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return fmt.Errorf("创建标签页失败: %w", err)
	}
	if err := page.Timeout(60 * time.Second).Navigate(url); err != nil {
		return fmt.Errorf("打开目标页面失败: %w", err)
	}
	d.tab = page
	return nil
}

// tab 返回本实例的标签页;OpenTabs 之前调用属于用法错误,就地报错
func (d *CDPDriver) tabPage() (*rod.Page, error) {
	if d.tab == nil {
		return nil, fmt.Errorf("标签页尚未打开")
	}
	return d.tab, nil
}

func (d *CDPDriver) GetContent() (string, error) {
	page, err := d.tabPage()
	if err != nil {
		return "", err
	}
	res, err := page.Timeout(10 * time.Second).Eval(`() => (` + GetContentJS + `)`)
	if err != nil {
		// 页面正在导航时 Eval 会失败,属于过渡状态,按空白页处理
		return "", nil
	}
	return res.Value.Str(), nil
}

func (d *CDPDriver) ClickEntry() (string, error) {
	page, err := d.tabPage()
	if err != nil {
		return "", err
	}
	res, err := page.Timeout(10 * time.Second).Eval(`() => (` + ClickButtonJS + `)`)
	if err != nil {
		return "", nil
	}
	return res.Value.Str(), nil
}

func (d *CDPDriver) Reload() error {
	page, err := d.tabPage()
	if err != nil {
		return err
	}
	return page.Timeout(10 * time.Second).Navigate(d.url)
}

func (d *CDPDriver) BringToFront() error {
	page, err := d.tabPage()
	if err != nil {
		return err
	}
	_, err = page.Timeout(10 * time.Second).Activate()
	return err
}
