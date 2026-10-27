package driver

import (
	"fmt"
	"path/filepath"
	"runtime"

	"hkticket/internal/paths"
)

// DefaultName 返回当前平台的默认驱动名:macOS 用 Safari,其余平台用 CDP
func DefaultName() string {
	if runtime.GOOS == "darwin" {
		return "safari"
	}
	return "cdp"
}

// resolveDriverName 决定实际使用的驱动:auto 或空字符串时,macOS 单会话用 Safari,
// 其余情况(非 macOS,或 macOS 多会话)用 CDP。
// Safari 全系统共享同一份 Cookie,不支持多会话,显式指定 safari 且多会话时报错
func resolveDriverName(name string, count int) (string, error) {
	if count < 1 {
		return "", fmt.Errorf("会话数至少为 1")
	}
	if name == "" || name == "auto" {
		name = DefaultName()
		if count > 1 {
			name = "cdp"
		}
	}
	switch name {
	case "safari":
		if runtime.GOOS != "darwin" {
			return "", fmt.Errorf("Safari 驱动仅在 macOS 上可用")
		}
		if count > 1 {
			return "", fmt.Errorf("Safari 全系统共享同一份 Cookie,多会话拿到的仍是同一个排队号;" +
				"多会话请把浏览器驱动改为 cdp(Chrome / Edge)")
		}
		return "safari", nil
	case "cdp":
		return "cdp", nil
	default:
		return "", fmt.Errorf("未知驱动 %q(可选:auto / safari / cdp)", name)
	}
}

// NewN 创建 count 个相互独立的驱动;"auto" 或空字符串使用平台默认。
// 每个 CDP 实例使用独立会话目录(第 1 个沿用 browser-profile,
// 其余为 browser-profile-2、-3…),Cookie 互不共享,各自拿到独立排队号
func NewN(name string, count int) ([]Driver, error) {
	name, err := resolveDriverName(name, count)
	if err != nil {
		return nil, err
	}
	if name == "safari" {
		return []Driver{NewSafari()}, nil
	}
	dir, err := paths.AppDir()
	if err != nil {
		return nil, err
	}
	ds := make([]Driver, 0, count)
	for i := 0; i < count; i++ {
		profileDir := filepath.Join(dir, "browser-profile")
		if i > 0 {
			profileDir = filepath.Join(dir, fmt.Sprintf("browser-profile-%d", i+1))
		}
		d, err := NewCDP(profileDir)
		if err != nil {
			for _, created := range ds {
				created.Close()
			}
			return nil, fmt.Errorf("会话 %d: %w", i+1, err)
		}
		ds = append(ds, d)
	}
	return ds, nil
}
