package core

import (
	"regexp"
	"strconv"
	"strings"
)

// 开售时刻常见服务器错误页,永远不能当作命中
var ErrorKeywords = []string{
	"502", "503", "504", "bad gateway", "gateway timeout",
	"service unavailable", "too many requests", "access denied",
	"request blocked", "error code",
	// uutix 的 EdgeOne 拦截页与限流弹窗,重载后通常可以恢复
	"當前購票人數過多", "当前购票人数过多",
}

// 开售前等候室用语。等候室页面本身含有长文本(“活动尚未开始…等候室…准备中”),
// 不排除掉会触发长度兜底误判
var NotYetKeywords = []string{
	"活动尚未开始", "尚未开始", "准备中",
	"has not started", "hasn't started", "not started yet",
	// uutix 详情页开售前的按钮文案,永远不算命中
	"即將開售", "即将开售",
}

// 排队页特征(中英文)。只用具体短语:等候室本身叫 "Waiting Room",
// 泛词 queue / waiting room 会误报
var QueueKeywords = []string{
	"you are now in line", "你正在排队",
	"number of users ahead of you", "前方排队人数",
	"排队编号", "轮候编号", "號碼",
	"estimated wait", "预计等待",
	// uutix 点击「購買門票 / 選座購票」后同标签页跳到的选票页;
	// 排队弹窗也算命中,窗口会保留给用户继续操作
	"節目場次", "节目场次", "票價種類", "票价种类",
	"排隊人數太多", "排队人数太多",
}

// 人机验证页特征(PleaseQ 的 reCAPTCHA Enterprise 页面),需要用户手动点击复选框
var VerificationKeywords = []string{
	"human verification", "please click the checkbox",
	"我们需要验证你是真人", "我們需要驗證你是真人",
	"请点击以下方格", "請點擊以下方格",
}

// NeedsVerification 判断页面是否停留在人机验证页
func NeedsVerification(content string) bool {
	if content == "" {
		return false
	}
	return containsAny(strings.ToLower(content), VerificationKeywords)
}

// uutix 未登录时点击「購買門票」会跳到 passport 登录页。
// 登录只能由用户手动完成,工具不点击也不重载,但必须保留窗口
var LoginKeywords = []string{"登入uutix", "登录uutix"}

// NeedsLogin 判断页面是否停在 uutix 登录页
func NeedsLogin(content string) bool {
	if content == "" {
		return false
	}
	return containsAny(strings.ToLower(content), LoginKeywords)
}

// 入口按钮特征:开售前几秒 PleaseQ 会把等候室换成必须点击的按钮。
// 英文 Refresh/Reload 是开售前 5 秒出现的「重新整理」按钮,点击会立即重查排队状态,
// 开售瞬间需要连续点击(见 Monitor)。
// \b 防止英文单词被子串误伤(例如 "Entertainment" 里的 "enter")
// uutix 开售瞬间按钮由「即將開售」变成「購買門票 / 選座購票」(选座模式),
// 这几个文案必须能命中;「開搶」不算——详情页的倒计时文案里也有这两个字
var ButtonHintRE = regexp.MustCompile(`(?i)整理|進入|进入|加入|前往|購買門票|购买门票|選座購票|选座购票|特惠選座|立即購買|立即购买|\benter\b|\bjoin\b|\brefresh\b|\breload\b`)

// RefreshHintRE 判断点中的按钮是否「重新整理」类。这类按钮点一次只触发一次状态查询,
// 不是真正的入口;Monitor 会对它每轮重复点击,而不是像入口按钮那样只点一次
var RefreshHintRE = regexp.MustCompile(`(?i)重新整理|刷新|refresh|reload`)

var queueNumberPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Number of Users Ahead of You[:\s]*(\d+)`),
	regexp.MustCompile(`前方排队人数[：:\s]*(\d+)`),
	regexp.MustCompile(`(?i)(?:ahead|wait|排队).*?(\d+)`),
}

// 单页应用渲染前的过渡文案。此时页面还没准备好,不能主动重载,
// 否则每次加载到一半就被打断,页面永远渲染不出来(2026-10-28 uutix 实测)
var LoadingKeywords = []string{"正在加載", "正在加载", "loading"}

// IsLoadingPage 判断页面是否还在加载(空白也按加载中处理)
func IsLoadingPage(content string) bool {
	if content == "" {
		return true
	}
	return containsAny(strings.ToLower(content), LoadingKeywords)
}

// IsErrorPage 判断页面是否空白或是服务器错误页(需要恢复性重载)
func IsErrorPage(content string) bool {
	if content == "" {
		return true
	}
	lower := strings.ToLower(content)
	return containsAny(lower, ErrorKeywords)
}

// CheckHit 判断页面内容是否表示已经拿到排队位。
// 只认排队页明确特征:开售前的官网菜单页内容很长,任何长度兜底都会误报
func CheckHit(content string) bool {
	if content == "" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(content))
	if containsAny(lower, ErrorKeywords) {
		return false
	}
	if containsAny(lower, NotYetKeywords) {
		return false
	}
	return containsAny(lower, QueueKeywords)
}

// ExtractQueueNumber 从排队页内容提取前方人数,提取不到返回 false
func ExtractQueueNumber(content string) (int, bool) {
	for _, pattern := range queueNumberPatterns {
		m := pattern.FindStringSubmatch(content)
		if m != nil {
			n, err := strconv.Atoi(m[1])
			if err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func containsAny(lower string, keywords []string) bool {
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
