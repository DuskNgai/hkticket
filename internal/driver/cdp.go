package driver

import (
	"context"
	"errors"
	"fmt"
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
	// 上一次运行留下的浏览器仍在运行时,新的 chrome.exe 会把命令转交给已有进程
	// 然后退出,DevTools 端口永远不会出现。用 PID 文件在各平台统一提前判断
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
	if err != nil {
		return nil, fmt.Errorf("启动浏览器失败: %w", err)
	}
	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		browser.Close()
		return nil, fmt.Errorf("连接浏览器失败: %w", err)
	}
	if err := saveBrowserPID(profileDir, pid); err != nil {
		browser.Close()
		return nil, err
	}
	return &CDPDriver{browser: browser, profileDir: profileDir, pid: pid}, nil
}

// Close 关闭浏览器进程,等进程真正退出后清掉 PID 文件,
// 避免紧接着的新任务与仍在退出的旧进程争用同一份会话目录
func (d *CDPDriver) Close() error {
	if err := d.browser.Close(); err != nil {
		return err
	}
	for i := 0; i < 30 && processAlive(d.pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if processAlive(d.pid) {
		// Browser.close 没有让进程退出(例如后台继续运行设置),直接结束它
		p, err := os.FindProcess(d.pid)
		if err != nil {
			return fmt.Errorf("结束浏览器进程失败: %w", err)
		}
		if err := p.Kill(); err != nil {
			return fmt.Errorf("结束浏览器进程失败: %w", err)
		}
		for i := 0; i < 20 && processAlive(d.pid); i++ {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if processAlive(d.pid) {
		return fmt.Errorf("浏览器进程(PID %d)未能退出", d.pid)
	}
	if err := os.Remove(pidFilePath(d.profileDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除浏览器 PID 文件失败: %w", err)
	}
	return nil
}

func pidFilePath(profileDir string) string {
	return filepath.Join(profileDir, ".rush-browser.pid")
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
