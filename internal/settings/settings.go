// Package settings 把用户输入过的参数保存到程序所在目录的 settings.json,
// 下次启动自动填好
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"rush/internal/paths"
)

type Settings struct {
	Site     string `json:"site"`
	URL      string `json:"url"`
	Time     string `json:"time"`
	Lead     int    `json:"lead"`
	Sessions int    `json:"sessions"`
	Interval int    `json:"interval"`
	Timeout  int    `json:"timeout"`
	Driver   string `json:"driver"`
}

func path() (string, error) {
	dir, err := paths.AppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// Load 读取保存的参数;文件不存在时返回零值,不视为错误
func Load() (Settings, error) {
	var s Settings
	p, err := path()
	if err != nil {
		return s, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return s, err
	}
	return s, nil
}

func Save(s Settings) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// 先写临时文件再改名,避免写到一半断电留下损坏的 settings.json
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
