// Package paths 提供程序自身所在目录,用户数据全部保存在这里,
// 整个文件夹拷到别的机器上即可带走全部状态
package paths

import (
	"os"
	"path/filepath"
)

// AppDir 返回可执行文件所在目录
func AppDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}
