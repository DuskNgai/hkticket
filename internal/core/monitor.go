package core

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"hkticket/internal/driver"
)

// ErrStopped 表示任务被停止信号中断,调用方应把它当作正常停止处理
var ErrStopped = errors.New("任务已被停止")

// VerifyEvent 是某个会话人机验证状态的变化,供图形控制台显示提醒。
// Session 从 1 开始编号;Active 为 true 表示页面出现人机验证,
// false 表示验证页已消失(通常是用户已完成验证或已进入排队页)
type VerifyEvent struct {
	Session int
	Active  bool
}

// 默认参数,命令行与图形控制台共用
const (
	DefaultLeadSec    = 10
	DefaultIntervalMs = 500
	DefaultTimeoutSec = 10
	DefaultSessions   = 3
	// MaxSessions 会话数上限;每个会话是一个完整浏览器进程,过多会拖垮机器
	MaxSessions = 10
)

// Config 是一次抢号任务的全部参数
type Config struct {
	Site       string // 网站标识,见 Sites;custom 表示自定义地址
	URL        string // 自定义入口地址,仅 Site 为 custom 时使用
	Time       string // "YYYY-MM-DD HH:MM:SS",按香港时区
	Lead       int    // 提前开始监视的秒数
	Sessions   int    // 独立会话数量;每个会话一个浏览器实例、一份 Cookie,各排一个号
	IntervalMs int    // 页面检查间隔毫秒数
	TimeoutSec int    // 开售后多少秒仍未命中则放弃
}

// ValidateSessions 校验会话数范围
func ValidateSessions(n int) error {
	if n < 1 || n > MaxSessions {
		return fmt.Errorf("会话数应在 1 到 %d 之间: %d", MaxSessions, n)
	}
	return nil
}

// ValidateConfig 校验任务参数。间隔过小会造成忙循环反复读取页面,
// 超时为 0 或负数会让监视一开始就结束,均在启动前拦下
func ValidateConfig(cfg Config) error {
	if _, err := ParseTargetTime(cfg.Time); err != nil {
		return err
	}
	if err := ValidateSessions(cfg.Sessions); err != nil {
		return err
	}
	if cfg.Lead < 0 {
		return fmt.Errorf("提前秒数不能为负: %d", cfg.Lead)
	}
	if cfg.IntervalMs < 100 {
		return fmt.Errorf("检查间隔应不小于 100 毫秒: %d", cfg.IntervalMs)
	}
	if cfg.TimeoutSec < 1 {
		return fmt.Errorf("超时时间应不小于 1 秒: %d", cfg.TimeoutSec)
	}
	return nil
}

const (
	reloadBackoff          = 5 * time.Second
	proactiveReloadBackoff = time.Second
)

// Monitor 监视各会话的标签页直到出现排队页或超时,返回命中的会话数,
// 以及是否出现过需要人工处理的人机验证页。
// notify 在会话的人机验证状态变化时调用(可为 nil),供界面显示或撤掉提醒。
// 每个会话一个独立浏览器实例(标签页固定 1 个),命中后继续监视其余会话,
// 全部命中或超时才结束。
//
// 策略(来自 2026-09-12 实测):PleaseQ 页面是自动更新的单页应用,
// 等候室会自行切换成入口按钮,持续刷新只会中断应用启动,还会抹掉入口按钮。
// 因此:轮询内容、按钮出现即点击,只有页面出错才恢复性重载。
func Monitor(ds []driver.Driver, cfg Config, target, timeout time.Time, now func() time.Time, logf func(string), notify func(VerifyEvent), stop <-chan struct{}) (hits int, manual bool, err error) {
	attempt := 0
	var lastS1 string
	lastS1Set := false
	entered := map[int]bool{}         // 已点击过入口按钮的会话,之后不再重载
	hit := map[int]bool{}             // 已拿到排队位的会话,之后不再轮询
	lastReload := map[int]time.Time{} // 每个会话上次恢复性重载的时刻
	verifyNotified := map[int]bool{}  // 已提示过人机验证的会话
	verifyCount := map[int]int{}      // 每个会话出现人机验证的次数
	loginLogged := map[int]bool{}     // 已提示过「需要登录」的会话
	refreshLogged := map[int]bool{}   // 已提示过「重新整理」按钮的会话
	refreshSeen := map[int]bool{}     // 出现过「重新整理」按钮的会话,属于 PleaseQ 类页面
	manual = false                    // 是否出现过需要人工处理的验证页

	notifyVerify := func(ev VerifyEvent) {
		if notify != nil {
			notify(ev)
		}
	}

	for now().Before(timeout) {
		if err := checkStop(stop); err != nil {
			return len(hit), manual, err
		}
		attempt++
		loopStart := time.Now()
		Logf(logf, "第 %d 轮", attempt)

		for s, d := range ds {
			if hit[s] {
				continue
			}
			if !now().Before(timeout) {
				return len(hit), manual, nil
			}
			if err := checkStop(stop); err != nil {
				return len(hit), manual, err
			}

			content, err := d.GetContent()
			if err != nil {
				Logf(logf, "[S%d] 读取页面失败: %v", s+1, err)
				continue
			}

			// 诊断:1 号会话内容变化时打印前 60 个字符
			if s == 0 && (!lastS1Set || content != lastS1) {
				snippet := []rune(content)
				if len(snippet) > 60 {
					snippet = snippet[:60]
				}
				Logf(logf, "[S1] 页面内容: %q", string(snippet))
				lastS1 = content
				lastS1Set = true
			}

			// 人机验证需要用户在浏览器里手动完成,工具只负责把窗口置前并提示
			if NeedsVerification(content) {
				manual = true
				if !verifyNotified[s] {
					verifyNotified[s] = true
					verifyCount[s]++
					notifyVerify(VerifyEvent{Session: s + 1, Active: true})
					Logf(logf, "[S%d] 页面要求人机验证,请在对应浏览器窗口点击「我不是机器人」", s+1)
					if err := d.BringToFront(); err != nil {
						Logf(logf, "[S%d] 置前窗口失败: %v", s+1, err)
					}
					if verifyCount[s] == 3 {
						Logf(logf, "[S%d] 人机验证反复出现,通常是系统时间偏差过大或 recaptcha.net 网络不通", s+1)
					}
				}
				continue
			}
			// 验证页消失后重置,下次再出现时重新提示,并通知界面撤掉该会话的提醒
			if verifyNotified[s] {
				verifyNotified[s] = false
				notifyVerify(VerifyEvent{Session: s + 1, Active: false})
			}

			// uutix 未登录时点「購買門票」会跳到登录页。登录只能人工完成,
			// 这里不点击不重载,但要标记出现过人工处理,让 Run 保留窗口
			if NeedsLogin(content) {
				manual = true
				if !loginLogged[s] {
					loginLogged[s] = true
					Logf(logf, "[S%d] 页面要求登入 uutix,请在浏览器里完成登录(登录后会回到选票页)", s+1)
					if err := d.BringToFront(); err != nil {
						Logf(logf, "[S%d] 置前窗口失败: %v", s+1, err)
					}
				}
				continue
			}

			// 入口按钮一出现就点击;本轮内容是在点击前读取的,跳过命中检查
			if !entered[s] && ButtonHintRE.MatchString(content) {
				clicked, err := d.ClickEntry()
				if err != nil {
					Logf(logf, "[S%d] 点击入口按钮失败: %v", s+1, err)
				} else if clicked != "" {
					// 「重新整理」在开售前 5 秒出现,点一次只重查一次排队状态。
					// 记为已进入会导致之后只点一次、错过开售瞬间,所以每轮都点,
					// 直到页面切出排队页或出现真正的入口按钮
					if RefreshHintRE.MatchString(clicked) {
						refreshSeen[s] = true
						if !refreshLogged[s] {
							refreshLogged[s] = true
							Logf(logf, "[S%d] 检测到「%s」按钮,开始连续点击", s+1, clicked)
						}
					} else {
						entered[s] = true
						Logf(logf, "[S%d] 检测到入口按钮,已自动点击: %q", s+1, clicked)
					}
					continue
				}
			}

			if CheckHit(content) {
				delta := now().Sub(target).Seconds()
				Logf(logf, "[S%d] 命中!距开售 %+.3f 秒", s+1, delta)
				if n, ok := ExtractQueueNumber(content); ok {
					Logf(logf, "[S%d] 排队编号:前面还有 %d 人", s+1, n)
				}
				if err := d.BringToFront(); err != nil {
					Logf(logf, "[S%d] 置前窗口失败: %v", s+1, err)
				}
				hit[s] = true
				if len(hit) == len(ds) {
					return len(hit), manual, nil
				}
				continue
			}

			// 开售后兜底:没有出现「重新整理」按钮的页面(非 PleaseQ)通常不会自己更新,
			// 到点后必须主动重载才能刷出购票入口,第一个到点的轮询就重载,之后每秒重试;
			// PleaseQ 由上面的连续点击推进,重载会打断它,所以用 refreshSeen 排除。
			// 空白或「正在加載」都是重载后的过渡态:单页应用要几秒才能渲染,
			// 每秒一次的重载会把加载不断打断、页面永远出不来(uutix 实测),
			// 因此加载中一律跳过,交给下面错误页重载的 5 秒退避兜底
			if !entered[s] && !refreshSeen[s] && !IsLoadingPage(content) && !now().Before(target) &&
				time.Since(lastReload[s]) >= proactiveReloadBackoff {
				lastReload[s] = time.Now()
				if err := d.Reload(); err != nil {
					Logf(logf, "[S%d] 到点重载失败: %v", s+1, err)
				} else {
					Logf(logf, "[S%d] 到点未见更新,已重载页面", s+1)
				}
			}

			// 恢复性重载:页面空白或服务器错误,且距上次重载超过退避时间
			if !entered[s] && IsErrorPage(content) {
				if time.Since(lastReload[s]) >= reloadBackoff {
					// 失败也要记时间,否则每轮都会重试,退避形同虚设
					lastReload[s] = time.Now()
					if err := d.Reload(); err != nil {
						Logf(logf, "[S%d] 重载失败: %v", s+1, err)
					} else {
						Logf(logf, "[S%d] 页面异常,已重载", s+1)
					}
				}
			}
		}

		// 间隔扣除本轮耗时,加 ±20% 抖动
		remaining := time.Duration(cfg.IntervalMs)*time.Millisecond - time.Since(loopStart)
		if remaining > 0 {
			// 注意先把比例换算成具体时长再转 Duration:time.Duration(0.1) 会截断成 0
			jitter := time.Duration((rand.Float64()*0.4 - 0.2) * float64(remaining))
			if err := sleepFor(remaining+jitter, stop); err != nil {
				return len(hit), manual, err
			}
		}
	}
	return len(hit), manual, nil
}

func checkStop(stop <-chan struct{}) error {
	select {
	case <-stop:
		return ErrStopped
	default:
		return nil
	}
}

func sleepFor(d time.Duration, stop <-chan struct{}) error {
	if d <= 0 {
		return checkStop(stop)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-stop:
		return ErrStopped
	case <-timer.C:
		return nil
	}
}
