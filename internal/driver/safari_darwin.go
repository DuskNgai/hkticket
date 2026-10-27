//go:build darwin

package driver

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SafariDriver 通过 AppleScript 控制系统 Safari。
// 需要用户在 Safari 开启「开发 → 允许 Apple 事件中的 JavaScript」。
type SafariDriver struct {
	windowID int
	url      string
}

func NewSafari() Driver {
	return &SafariDriver{}
}

// cleanURL 去掉 shell 转义(\? \& \=),并按 AppleScript 双引号字符串规则转义
func cleanURL(url string) string {
	re := regexp.MustCompile(`\\([?&=#])`)
	url = re.ReplaceAllString(url, "$1")
	url = strings.ReplaceAll(url, `\`, `\\`)
	url = strings.ReplaceAll(url, `"`, `\"`)
	return url
}

func runAppleScript(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Errorf("osascript 执行超时")
	}
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("osascript 失败: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (d *SafariDriver) OpenTabs(url string) error {
	d.url = cleanURL(url)
	out, err := runAppleScript(fmt.Sprintf(`
tell application "Safari"
	activate
	make new document with properties {URL:"%s"}
	return id of front window
end tell`, d.url))
	if err != nil {
		return fmt.Errorf("无法打开 Safari 窗口: %w", err)
	}
	id, err := strconv.Atoi(out)
	if err != nil {
		return fmt.Errorf("无法解析 Safari 窗口编号 %q", out)
	}
	d.windowID = id
	return nil
}

// VerifyTabs 返回窗口实际标签页数量
func (d *SafariDriver) VerifyTabs() (int, error) {
	out, err := runAppleScript(fmt.Sprintf(`
tell application "Safari"
	return count of tabs of window id %d
end tell`, d.windowID))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(out)
}

// CheckJSPermission 验证「允许 Apple 事件中的 JavaScript」已开启
func (d *SafariDriver) CheckJSPermission() error {
	out, err := runAppleScript(fmt.Sprintf(`
tell application "Safari"
	do JavaScript "1" in tab 1 of window id %d
end tell`, d.windowID))
	if err != nil {
		return err
	}
	// 新版 macOS 的 Safari 把 JS 数字返回为 "1.0",旧版返回 "1"
	f, err := strconv.ParseFloat(out, 64)
	if err != nil || f != 1.0 {
		return fmt.Errorf("返回值异常: %q", out)
	}
	return nil
}

func (d *SafariDriver) GetContent() (string, error) {
	out, err := runAppleScript(fmt.Sprintf(`
tell application "Safari"
	set pageText to do JavaScript "%s" in tab 1 of window id %d
	return pageText
end tell`, GetContentJS, d.windowID))
	if err != nil {
		// 页面正在导航时 do JavaScript 会报错,属于过渡状态,按空白页处理
		return "", nil
	}
	if out == "missing value" {
		return "", nil
	}
	return out, nil
}

func (d *SafariDriver) ClickEntry() (string, error) {
	out, err := runAppleScript(fmt.Sprintf(
		`tell application "Safari" to do JavaScript "%s" in tab 1 of window id %d`,
		ClickButtonJS, d.windowID))
	if err != nil {
		return "", nil
	}
	if out == "missing value" {
		return "", nil
	}
	return out, nil
}

func (d *SafariDriver) Reload() error {
	_, err := runAppleScript(fmt.Sprintf(`
tell application "Safari"
	set URL of tab 1 of window id %d to "%s"
end tell`, d.windowID, d.url))
	return err
}

// Close 关闭本驱动创建的 Safari 窗口
func (d *SafariDriver) Close() error {
	if d.windowID == 0 {
		return nil
	}
	_, err := runAppleScript(fmt.Sprintf(`
tell application "Safari"
	close window id %d
end tell`, d.windowID))
	return err
}

func (d *SafariDriver) BringToFront() error {
	_, err := runAppleScript(fmt.Sprintf(`
tell application "Safari"
	activate
	tell window id %d
		set current tab to tab 1
		set index to 1
	end tell
end tell`, d.windowID))
	return err
}
