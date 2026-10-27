package driver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDevToolsPort(t *testing.T) {
	dir := t.TempDir()
	if got := devToolsPort(dir); got != 0 {
		t.Errorf("没有 DevToolsActivePort 时应返回 0,得到 %d", got)
	}
	cases := []struct {
		content string
		want    int
	}{
		{"55022\n/devtools/browser/abc\n", 55022},
		{" 9222 \r\n", 9222},
		{"", 0},
		{"abc\n", 0},
		{"0\n", 0},
		{"70000\n", 0},
	}
	for _, c := range cases {
		if err := os.WriteFile(filepath.Join(dir, "DevToolsActivePort"), []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := devToolsPort(dir); got != c.want {
			t.Errorf("内容 %q 应解析为 %d,得到 %d", c.content, c.want, got)
		}
	}
}

// 调试端口文件不存在或已失效时,清理应该直接通过
func TestCleanupStaleBrowserWithoutPort(t *testing.T) {
	if err := cleanupStaleBrowser(t.TempDir()); err != nil {
		t.Errorf("没有调试端口时不应报错: %v", err)
	}
}
