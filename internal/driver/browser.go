package driver

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/go-rod/rod/lib/launcher"
)

// BrowserPath 返回优先使用的系统浏览器路径(Chrome → Chromium → Edge)。
// 供打开控制台等场景复用与抢票窗口相同的浏览器优先级。
func BrowserPath() (string, bool) {
	return lookBrowser()
}

// lookBrowser 按固定优先级查找系统浏览器:Chrome → Chromium → Edge。
// 不直接用 launcher.LookPath(),因为它的候选顺序随版本/平台变化,这里显式固定 Chrome 优先。
func lookBrowser() (string, bool) {
	for _, name := range browserCandidates() {
		if path, err := exec.LookPath(name); err == nil {
			return path, true
		}
	}
	// 兜底: go-rod 的候选表(覆盖 OpenBSD 等未列出的平台)
	return launcher.LookPath()
}

// browserCandidates 返回当前平台的浏览器候选路径,Chrome 系排在 Edge 之前
func browserCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Google Chrome Canary.app/Contents/MacOS/Google Chrome Canary",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"google-chrome-stable",
			"google-chrome",
			"chrome",
			"chromium",
			"chromium-browser",
			"microsoft-edge",
		}
	case "windows":
		var paths []string
		for _, rel := range []string{
			`Google\Chrome\Application\chrome.exe`,
			`Chromium\Application\chrome.exe`,
			`Microsoft\Edge\Application\msedge.exe`,
		} {
			for _, root := range []string{
				os.Getenv("ProgramFiles"),
				os.Getenv("ProgramFiles(x86)"),
				os.Getenv("LocalAppData"),
			} {
				if root != "" {
					paths = append(paths, filepath.Join(root, rel))
				}
			}
		}
		return append(paths, "chrome.exe", "msedge.exe")
	default:
		return []string{
			"google-chrome-stable",
			"google-chrome",
			"chrome",
			"chromium",
			"chromium-browser",
			"microsoft-edge",
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/microsoft-edge",
			"/snap/bin/chromium",
			"/data/data/com.termux/files/usr/bin/chromium-browser",
		}
	}
}
