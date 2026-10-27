package core

import (
	"strings"
	"testing"

	"rush/internal/driver"
)

func TestCheckHit(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"空内容", "", false},
		{"排队页英文", "You are now in line! Number of Users Ahead of You: 44", true},
		{"排队页中文", "你正在排队\n前方排队人数:53", true},
		{"排队编号", "您的排队编号是 8556", true},
		{"服务器错误 502", "502 Bad Gateway", false},
		{"服务器错误 503 含排队词", "503 Service Unavailable, you are now in line", false},
		{"等候室未开始", "活动尚未开始,请稍候。等候室准备中", false},
		{"等候室英文", "The event has not started yet", false},
		{"语言选择页(短内容)", "繁\n简\nEN", false},
		{"官网菜单页(长内容)", "HI01\n\nhins shop02\n\nHins auction03\n\nh.gallery04\n\nHINSCOGRAPHY\n\n" + strings.Repeat("票", 60), false},
	}
	for _, c := range cases {
		if got := CheckHit(c.content); got != c.want {
			t.Errorf("%s: CheckHit = %v, 期望 %v", c.name, got, c.want)
		}
	}
}

func TestNeedsVerification(t *testing.T) {
	hits := []string{
		"Human verification\nPlease click the checkbox below",
		"我們需要驗證你是真人\n請點擊以下方格",
		"我们需要验证你是真人\n请点击以下方格",
	}
	for _, s := range hits {
		if !NeedsVerification(s) {
			t.Errorf("%q 应判定为人机验证页", s)
		}
	}
	if NeedsVerification("活动尚未开始,请稍候") {
		t.Error("等候室内容不应判定为人机验证页")
	}
}

func TestValidateConfig(t *testing.T) {
	const validTime = "2026-12-31 20:00:00"
	ok := Config{Time: validTime, Sessions: 3, Lead: 10, IntervalMs: 500, TimeoutSec: 15}
	if err := ValidateConfig(ok); err != nil {
		t.Errorf("合法参数不应报错: %v", err)
	}
	// 提前 0 秒、间隔 100 毫秒、超时 1 秒是允许的边界
	edge := Config{Time: validTime, Sessions: 1, IntervalMs: 100, TimeoutSec: 1}
	if err := ValidateConfig(edge); err != nil {
		t.Errorf("边界参数不应报错: %v", err)
	}
	bad := []Config{
		{Time: "", Sessions: 1, IntervalMs: 500, TimeoutSec: 15},
		{Time: "10:00", Sessions: 1, IntervalMs: 500, TimeoutSec: 15},
		{Time: validTime, Sessions: 0, IntervalMs: 500, TimeoutSec: 15},
		{Time: validTime, Sessions: 11, IntervalMs: 500, TimeoutSec: 15},
		{Time: validTime, Sessions: 1, Lead: -1, IntervalMs: 500, TimeoutSec: 15},
		{Time: validTime, Sessions: 1, IntervalMs: 0, TimeoutSec: 15},
		{Time: validTime, Sessions: 1, IntervalMs: 99, TimeoutSec: 15},
		{Time: validTime, Sessions: 1, IntervalMs: 500, TimeoutSec: 0},
	}
	for _, c := range bad {
		if err := ValidateConfig(c); err == nil {
			t.Errorf("非法参数应报错: %+v", c)
		}
	}
}

func TestIsErrorPage(t *testing.T) {
	if !IsErrorPage("") {
		t.Error("空白页应判定为异常")
	}
	if !IsErrorPage("504 Gateway Timeout") {
		t.Error("504 页应判定为异常")
	}
	if IsErrorPage("You are now in line! Number of Users Ahead of You: 44") {
		t.Error("排队页不应判定为异常")
	}
}

func TestExtractQueueNumber(t *testing.T) {
	if n, ok := ExtractQueueNumber("Number of Users Ahead of You: 44"); !ok || n != 44 {
		t.Errorf("英文格式应提取 44,得到 %d, %v", n, ok)
	}
	if n, ok := ExtractQueueNumber("前方排队人数:53"); !ok || n != 53 {
		t.Errorf("中文格式应提取 53,得到 %d, %v", n, ok)
	}
	if _, ok := ExtractQueueNumber("活动尚未开始"); ok {
		t.Error("无数字内容不应提取成功")
	}
}

func TestButtonHintRE(t *testing.T) {
	hits := []string{"整理页面", "進入", "进入队列", "加入等候", "前往", "enter now", "Join the queue"}
	for _, s := range hits {
		if !ButtonHintRE.MatchString(s) {
			t.Errorf("%q 应命中入口按钮特征", s)
		}
	}
	misses := []string{"Entertainment", "some entertainment news", ""}
	for _, s := range misses {
		if ButtonHintRE.MatchString(s) {
			t.Errorf("%q 不应命中入口按钮特征", s)
		}
	}
}

func TestFindSite(t *testing.T) {
	s, err := FindSite("hcfc")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name == "" || s.URL == "" {
		t.Errorf("网站应有显示名和入口地址: %+v", s)
	}
	for _, site := range Sites {
		if _, err := FindSite(site.Key); err != nil {
			t.Errorf("注册表内网站 %q 应可查找: %v", site.Key, err)
		}
	}
	if _, err := FindSite("nope"); err == nil {
		t.Error("未知网站应报错")
	}
}

func TestResolveSite(t *testing.T) {
	if _, err := ResolveSite("hcfc", ""); err != nil {
		t.Errorf("注册网站不需要地址: %v", err)
	}
	s, err := ResolveSite("custom", " https://example.com/queue?a=1 ")
	if err != nil {
		t.Fatal(err)
	}
	if s.URL != "https://example.com/queue?a=1" {
		t.Errorf("自定义地址应去空白后原样使用,得到 %q", s.URL)
	}
	for _, bad := range []string{"", "example.com", "ftp://example.com", "https://"} {
		if _, err := ResolveSite("custom", bad); err == nil {
			t.Errorf("地址 %q 应报错", bad)
		}
	}
}

func TestParseTargetTime(t *testing.T) {
	target, err := ParseTargetTime("2026-09-12 10:00:00")
	if err != nil {
		t.Fatal(err)
	}
	if target.Hour() != 10 || target.Minute() != 0 {
		t.Errorf("解析结果应为 10:00,得到 %v", target)
	}
	if target.Location() != TargetLocation {
		t.Errorf("应按香港时区解析,得到 %v", target.Location())
	}
	if _, err := ParseTargetTime("10:00"); err == nil {
		t.Error("错误格式应报错")
	}
}

// CLICK_BUTTON_JS 会被嵌进 AppleScript 双引号字符串,反斜杠会被 AppleScript 吃掉
func TestClickButtonJSNoBackslash(t *testing.T) {
	if strings.Contains(driver.ClickButtonJS, `\`) {
		t.Error("ClickButtonJS 不允许包含反斜杠")
	}
}
