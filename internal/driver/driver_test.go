package driver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// macOS 单会话默认 Safari,多会话默认 CDP(Chrome)
func TestResolveDriverName(t *testing.T) {
	platformDefault, safariWant, safariErr := "cdp", "", true
	if runtime.GOOS == "darwin" {
		platformDefault, safariWant, safariErr = "safari", "safari", false
	}
	tests := []struct {
		name    string
		count   int
		want    string
		wantErr bool
	}{
		{"auto", 1, platformDefault, false},
		{"", 1, platformDefault, false},
		{"auto", 3, "cdp", false},
		{"", 3, "cdp", false},
		{"cdp", 1, "cdp", false},
		{"safari", 1, safariWant, safariErr},
		{"safari", 3, "", true},
		{"bogus", 1, "", true},
		{"auto", 0, "", true},
	}
	for _, tt := range tests {
		got, err := resolveDriverName(tt.name, tt.count)
		if (err != nil) != tt.wantErr {
			t.Errorf("resolveDriverName(%q, %d) error = %v, wantErr %v", tt.name, tt.count, err, tt.wantErr)
			continue
		}
		if got != tt.want {
			t.Errorf("resolveDriverName(%q, %d) = %q, want %q", tt.name, tt.count, got, tt.want)
		}
	}
}

func TestProcessAlive(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Error("当前进程应判定为存活")
	}
	dead := os.Getpid() + 100000
	for processAlive(dead) {
		dead += 100000
	}
	if processAlive(dead) {
		t.Error("不存在的进程不应判定为存活")
	}
}

func TestCheckProfileOwner(t *testing.T) {
	dir := lockDir(t)

	if err := checkProfileOwner(dir); err != nil {
		t.Fatalf("没有 PID 文件时应通过: %v", err)
	}

	if err := os.WriteFile(pidFilePath(dir), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkProfileOwner(dir); err == nil {
		t.Error("浏览器进程仍然存活时应报错")
	}

	dead := os.Getpid() + 100000
	for processAlive(dead) {
		dead += 100000
	}
	if err := os.WriteFile(pidFilePath(dir), []byte(strconv.Itoa(dead)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkProfileOwner(dir); err != nil {
		t.Errorf("残留的 PID 文件应被清理: %v", err)
	}
	if _, err := os.Lstat(pidFilePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Error("进程已退出时 PID 文件应被删除")
	}

	if err := os.WriteFile(pidFilePath(dir), []byte("bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkProfileOwner(dir); err == nil {
		t.Error("损坏的 PID 文件应报错")
	}
}

// Chrome 必须排在 Edge 之前(默认优先 Chrome)
func TestBrowserCandidatesChromeFirst(t *testing.T) {
	candidates := browserCandidates()
	index := func(substr string) int {
		for i, c := range candidates {
			if strings.Contains(strings.ToLower(c), substr) {
				return i
			}
		}
		return -1
	}
	chrome, edge := index("chrome"), index("edge")
	if chrome < 0 || edge < 0 {
		t.Skipf("当前平台候选中没有同时包含 Chrome 与 Edge: %v", candidates)
	}
	if chrome > edge {
		t.Errorf("Chrome 应排在 Edge 之前: %v", candidates)
	}
}

func lockDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("..", "..", ".scratch", "test-lock")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestClearStaleLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不写 SingletonLock")
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("无锁文件", func(t *testing.T) {
		if err := clearStaleLock(lockDir(t)); !errors.Is(err, errNoLockFile) {
			t.Errorf("应返回 errNoLockFile,得到 %v", err)
		}
	})

	t.Run("残留锁(进程已退出)", func(t *testing.T) {
		dir := lockDir(t)
		// 找一个确定不存在的 PID:从当前 PID 起探测
		dead := os.Getpid() + 100000
		for processAlive(dead) {
			dead += 100000
		}
		for _, f := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
			p := filepath.Join(dir, f)
			if f == "SingletonLock" {
				if err := os.Symlink(fmt.Sprintf("%s-%d", host, dead), p); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := clearStaleLock(dir); err != nil {
			t.Fatalf("残留锁应清理成功,得到 %v", err)
		}
		for _, f := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
			if _, err := os.Lstat(filepath.Join(dir, f)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("%s 应已被删除", f)
			}
		}
	})

	t.Run("存活锁(进程仍在运行)", func(t *testing.T) {
		dir := lockDir(t)
		if err := os.Symlink(fmt.Sprintf("%s-%d", host, os.Getpid()), filepath.Join(dir, "SingletonLock")); err != nil {
			t.Fatal(err)
		}
		if err := clearStaleLock(dir); err == nil {
			t.Error("进程仍在运行时应报错")
		}
		if _, err := os.Lstat(filepath.Join(dir, "SingletonLock")); err != nil {
			t.Error("进程仍在运行时不应删除锁文件")
		}
	})
}
