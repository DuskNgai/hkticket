package driver

import (
	"errors"
	"testing"
)

func TestProfileInUse(t *testing.T) {
	gbk := errors.New("failed: \xd5\xfd\xd4\xda\xcf\xd6\xd3\xd0\xe4\xaf\xc0\xc0\xc6\xf7\xbb\xe1\xbb\xb0\xd6\xd0\xb4\xf2\xbf\xaa\xa1\xa3")
	if !profileInUse(gbk) {
		t.Error("中文版 msedge 的 GBK 提示应被识别为会话目录被占用")
	}
	if !profileInUse(errors.New("Opening in existing browser session.")) {
		t.Error("英文提示应被识别为会话目录被占用")
	}
	if profileInUse(errors.New("[launcher] Failed to get the debug url: ")) {
		t.Error("空缓冲区不应判定为会话目录被占用")
	}
}
