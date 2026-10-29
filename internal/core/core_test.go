package core

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hkticket/internal/driver"
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
		{"uutix 开售前详情页", "澳門倫敦人榮譽呈獻：張敬軒 x 澳門中樂團\n10/28 15:00 (UTC+8) 開搶\n剩 00 天 00 時 15 分 15 秒\n即將開售", false},
		{"uutix 选票页", "節目場次\n票價種類\n選擇座位\n數量\n合計\n購買", true},
		{"uutix 排队弹窗", "當前排隊人數太多啦，請在倒計時結束後繼續", true},
		{"uutix EdgeOne 拦截页", "當前購票人數過多\n請稍後重試\n刷 新", false},
	}
	for _, c := range cases {
		if got := CheckHit(c.content); got != c.want {
			t.Errorf("%s: CheckHit = %v, 期望 %v", c.name, got, c.want)
		}
	}
}

func TestIsLoadingPage(t *testing.T) {
	for _, s := range []string{"", "繁 | EN 正在加載...", "正在加载中", "Loading, please wait"} {
		if !IsLoadingPage(s) {
			t.Errorf("%q 应判定为加载中", s)
		}
	}
	if IsLoadingPage("You are now in line") {
		t.Error("排队页不应判定为加载中")
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

func TestNeedsLogin(t *testing.T) {
	hits := []string{
		"登入uutix\nuutix已採用電郵方式登入\n電郵地址\n密碼",
		"登录uutix\n请使用电话号码登录",
	}
	for _, s := range hits {
		if !NeedsLogin(s) {
			t.Errorf("%q 应判定为登录页", s)
		}
	}
	if NeedsLogin("You are now in line") {
		t.Error("排队页不应判定为登录页")
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
	hits := []string{"整理页面", "進入", "进入队列", "加入等候", "前往", "enter now", "Join the queue",
		"重新整理", "Refresh", "please refresh at that time", "Reload",
		"購買門票", "選座購票", "特惠選座", "立即購買"}
	for _, s := range hits {
		if !ButtonHintRE.MatchString(s) {
			t.Errorf("%q 应命中入口按钮特征", s)
		}
	}
	// 开售前的按钮和倒计时文案不能被当成入口按钮
	misses := []string{"Entertainment", "some entertainment news", "",
		"即將開售", "即將開售，請耐心等待", "10/28 15:00 (UTC+8) 開搶"}
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

// CLICK_BUTTON_JS 会被嵌进 AppleScript 双引号字符串,反斜杠会被 AppleScript 吃掉,
// 双引号会提前结束字符串
func TestClickButtonJSNoBackslash(t *testing.T) {
	if strings.Contains(driver.ClickButtonJS, `\`) {
		t.Error("ClickButtonJS 不允许包含反斜杠")
	}
	if strings.Contains(driver.ClickButtonJS, `"`) {
		t.Error("ClickButtonJS 不允许包含双引号")
	}
}

// ClickButtonJS 必须能被 JS 引擎解析。单引号字符串里再嵌单引号会把字符串截断,
// 整段脚本抛 SyntaxError,而驱动会把该错误当过渡态吞掉,表现为「点击从未发生」。
// 有 node 时直接做语法检查(没有则跳过,不影响其他平台)
func TestClickButtonJSSyntax(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("未找到 node,跳过 JS 语法检查")
	}
	path := filepath.Join(t.TempDir(), "click.js")
	if err := os.WriteFile(path, []byte(driver.ClickButtonJS), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, "--check", path).CombinedOutput(); err != nil {
		t.Fatalf("ClickButtonJS 不是合法 JS: %v\n%s", err, out)
	}
}

func TestRefreshHintRE(t *testing.T) {
	for _, s := range []string{"重新整理", "Refresh", "刷新", "Reload"} {
		if !RefreshHintRE.MatchString(s) {
			t.Errorf("%q 应判定为重新整理按钮", s)
		}
	}
	for _, s := range []string{"进入网站", "進入網站", "Enter", "Queue Again", "重新排队"} {
		if RefreshHintRE.MatchString(s) {
			t.Errorf("%q 不应判定为重新整理按钮", s)
		}
	}
}

// fakeDriver 用固定内容序列驱动 Monitor,并记录按钮点击次数
type fakeDriver struct {
	contents  []string
	idx       int
	clickText string
	clicks    int
	reloads   int
	stop      chan struct{}
	stopAfter int
	gets      int
}

func (f *fakeDriver) OpenTabs(string) error { return nil }

func (f *fakeDriver) GetContent() (string, error) {
	c := f.contents[f.idx]
	if f.idx < len(f.contents)-1 {
		f.idx++
	}
	f.gets++
	if f.stop != nil && f.stopAfter > 0 && f.gets == f.stopAfter {
		close(f.stop)
	}
	return c, nil
}

func (f *fakeDriver) ClickEntry() (string, error) { f.clicks++; return f.clickText, nil }
func (f *fakeDriver) Reload() error               { f.reloads++; return nil }
func (f *fakeDriver) BringToFront() error         { return nil }
func (f *fakeDriver) Close() error                { return nil }

func newTestMonitor(f *fakeDriver, notify func(VerifyEvent)) (int, bool, error) {
	target := time.Now()
	return Monitor([]driver.Driver{f}, Config{IntervalMs: 100, TimeoutSec: 60, Sessions: 1},
		target, target.Add(time.Minute), time.Now, func(string) {}, notify, f.stop)
}

// 等候室的「重新整理」点一次只重查一次状态,必须每轮都点
func TestMonitorRepeatsRefreshClick(t *testing.T) {
	stop := make(chan struct{})
	d := &fakeDriver{
		contents:  []string{"Coming soon... Countdown 重新整理"},
		clickText: "重新整理",
		stop:      stop,
		stopAfter: 4,
	}
	_, _, err := newTestMonitor(d, nil)
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("应因停止信号结束,得到 %v", err)
	}
	if d.clicks < 3 {
		t.Errorf("「重新整理」应每轮重复点击,实际只点了 %d 次", d.clicks)
	}
}

// 真正的入口按钮只点一次,之后不再重复点击
func TestMonitorClicksEntryOnce(t *testing.T) {
	stop := make(chan struct{})
	d := &fakeDriver{
		contents:  []string{"进入网站"},
		clickText: "进入网站",
		stop:      stop,
		stopAfter: 4,
	}
	_, _, err := newTestMonitor(d, nil)
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("应因停止信号结束,得到 %v", err)
	}
	if d.clicks != 1 {
		t.Errorf("入口按钮应只点一次,实际点了 %d 次", d.clicks)
	}
}

// 连续点击「重新整理」-> 页面切换成排队页 -> 命中
func TestMonitorRefreshThenHit(t *testing.T) {
	d := &fakeDriver{
		contents: []string{
			"重新整理",
			"重新整理",
			"You are now in line! Number of Users Ahead of You: 5",
		},
		clickText: "重新整理",
		stop:      make(chan struct{}),
	}
	hits, _, err := newTestMonitor(d, nil)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if hits != 1 {
		t.Fatalf("应命中 1 个会话,得到 %d", hits)
	}
	if d.clicks != 2 {
		t.Errorf("命中前应点过 2 次「重新整理」,实际 %d 次", d.clicks)
	}
}

// 普通网站(没有「重新整理」按钮)到点后不会自己更新,应主动重载
func TestMonitorReloadsAfterTarget(t *testing.T) {
	stop := make(chan struct{})
	d := &fakeDriver{
		contents:  []string{"Coming soon... 活动尚未开始"},
		stop:      stop,
		stopAfter: 6,
	}
	target := time.Now()
	clock := target.Add(-2 * time.Second)
	now := func() time.Time {
		clock = clock.Add(300 * time.Millisecond)
		return clock
	}
	_, _, err := Monitor([]driver.Driver{d}, Config{IntervalMs: 100, TimeoutSec: 60, Sessions: 1},
		target, target.Add(time.Minute), now, func(string) {}, nil, stop)
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("应因停止信号结束,得到 %v", err)
	}
	if d.reloads < 1 {
		t.Errorf("到点后应主动重载页面,实际重载 %d 次", d.reloads)
	}
}

// 出现过「重新整理」按钮的 PleaseQ 页面不能主动重载,否则会打断排队
func TestMonitorNoReloadAfterRefreshButton(t *testing.T) {
	stop := make(chan struct{})
	d := &fakeDriver{
		contents:  []string{"Coming soon... 重新整理", "Loading..."},
		clickText: "重新整理",
		stop:      stop,
		stopAfter: 6,
	}
	target := time.Now()
	clock := target.Add(-2 * time.Second)
	now := func() time.Time {
		clock = clock.Add(300 * time.Millisecond)
		return clock
	}
	_, _, err := Monitor([]driver.Driver{d}, Config{IntervalMs: 100, TimeoutSec: 60, Sessions: 1},
		target, target.Add(time.Minute), now, func(string) {}, nil, stop)
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("应因停止信号结束,得到 %v", err)
	}
	if d.reloads != 0 {
		t.Errorf("PleaseQ 页面不应主动重载,实际重载 %d 次", d.reloads)
	}
}

// 人机验证出现和消失都要通知界面,供控制台显示和撤掉提醒
func TestMonitorNotifiesVerifyState(t *testing.T) {
	const verifyPage = "Human verification, please click the checkbox"
	var events []VerifyEvent
	d := &fakeDriver{contents: []string{
		verifyPage,
		verifyPage,
		"You are now in line! Number of Users Ahead of You: 5",
	}}
	target := time.Now()
	hits, manual, err := Monitor([]driver.Driver{d},
		Config{IntervalMs: 100, TimeoutSec: 60, Sessions: 1},
		target, target.Add(time.Minute), time.Now, func(string) {},
		func(ev VerifyEvent) { events = append(events, ev) }, make(chan struct{}))
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if hits != 1 || !manual {
		t.Fatalf("应命中 1 个会话并标记出现过人机验证,得到 hits=%d manual=%v", hits, manual)
	}
	want := []VerifyEvent{{Session: 1, Active: true}, {Session: 1, Active: false}}
	if len(events) != len(want) {
		t.Fatalf("验证状态事件应为 %v,得到 %v", want, events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("第 %d 个事件应为 %v,得到 %v", i+1, want[i], events[i])
		}
	}
}

// 单页应用重载后需要几秒渲染,「正在加載」期间不能主动重载,
// 否则每秒一次的重载会把加载不断打断,页面永远出不来(2026-10-28 实测)
func TestMonitorDoesNotReloadWhileLoading(t *testing.T) {
	stop := make(chan struct{})
	d := &fakeDriver{
		contents: []string{
			"正在加載...",
			"正在加載...",
			"購買門票",
			"節目場次\n票價種類\n數量\n合計\n購買",
		},
		clickText: "購買門票",
		stop:      stop,
	}
	target := time.Now()
	clock := target.Add(-time.Second)
	now := func() time.Time {
		clock = clock.Add(300 * time.Millisecond)
		return clock
	}
	hits, _, err := Monitor([]driver.Driver{d}, Config{IntervalMs: 100, TimeoutSec: 60, Sessions: 1},
		target, target.Add(time.Minute), now, func(string) {}, nil, stop)
	if err != nil {
		t.Fatalf("页面加载完并命中后应正常结束,得到 %v", err)
	}
	if hits != 1 {
		t.Errorf("页面加载完成后应命中,得到 %d", hits)
	}
	if d.clicks != 1 {
		t.Errorf("应点击 1 次购票按钮,实际 %d 次", d.clicks)
	}
	if d.reloads != 0 {
		t.Errorf("加载中不应重载,实际重载 %d 次", d.reloads)
	}
}

// 未登录时点击入口会跳到 uutix 登录页:不点击、不重载,
// 但必须标记人工处理,让 Run 保留窗口等用户登录
func TestMonitorLoginKeepsWindow(t *testing.T) {
	const loginPage = "登入uutix\nuutix已採用電郵方式登入\n電郵地址\n密碼\n登入"
	stop := make(chan struct{})
	d := &fakeDriver{
		contents:  []string{loginPage},
		clickText: "購買門票",
		stop:      stop,
		stopAfter: 3,
	}
	hits, manual, err := newTestMonitor(d, nil)
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("应因停止信号结束,得到 %v", err)
	}
	if hits != 0 || !manual {
		t.Errorf("登录页不应命中,但应标记需要人工处理,得到 hits=%d manual=%v", hits, manual)
	}
	if d.clicks != 0 {
		t.Errorf("登录页不应点击按钮,实际点击 %d 次", d.clicks)
	}
	if d.reloads != 0 {
		t.Errorf("登录页不应重载,实际重载 %d 次", d.reloads)
	}
}

// 验证页持续存在时只通知一次,避免界面反复刷新
func TestMonitorVerifyNotifyOnce(t *testing.T) {
	stop := make(chan struct{})
	d := &fakeDriver{
		contents:  []string{"Human verification, please click the checkbox"},
		stop:      stop,
		stopAfter: 4,
	}
	var events []VerifyEvent
	target := time.Now()
	_, manual, err := Monitor([]driver.Driver{d},
		Config{IntervalMs: 100, TimeoutSec: 60, Sessions: 1},
		target, target.Add(time.Minute), time.Now, func(string) {},
		func(ev VerifyEvent) { events = append(events, ev) }, stop)
	if !errors.Is(err, ErrStopped) {
		t.Fatalf("应因停止信号结束,得到 %v", err)
	}
	if !manual {
		t.Error("应标记出现过人机验证")
	}
	if len(events) != 1 || events[0] != (VerifyEvent{Session: 1, Active: true}) {
		t.Errorf("验证页持续存在时只应通知一次,得到 %v", events)
	}
}
