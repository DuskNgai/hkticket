package core

import (
	"fmt"
	"math"
	"time"

	"hkticket/internal/clock"
	"hkticket/internal/driver"
)

var ntpServers = []string{"ntp.aliyun.com", "ntp.tencent.com", "ntp.ntsc.ac.cn", "time.apple.com"}

// Run 编排一次完整任务:校时 → 打开各会话标签页 → 等待到点 → 监视 → 命中处理。
// ds 是每个会话一个的驱动实例(由 driver.NewN 创建),日志经 Logf 加时间戳,
// logf 由调用方提供(命令行写 stdout,图形控制台广播到页面);
// notify 转发人机验证状态给图形控制台,命令行传 nil
func Run(ds []driver.Driver, cfg Config, logf func(string), notify func(VerifyEvent), stop <-chan struct{}) error {
	// 命中排队位或出现需要人工处理的验证页时保留窗口;其余情况在结束时关闭
	keepOpen := false
	defer func() {
		if keepOpen {
			return
		}
		for i, d := range ds {
			if err := d.Close(); err != nil {
				Logf(logf, "[S%d] 关闭浏览器失败: %v", i+1, err)
			}
		}
	}()
	if err := checkStop(stop); err != nil {
		return err
	}
	site, err := ResolveSite(cfg.Site, cfg.URL)
	if err != nil {
		return err
	}
	offset, source, err := clock.Offset(ntpServers, 2*time.Second)
	if err != nil {
		return err
	}
	if err := checkStop(stop); err != nil {
		return err
	}
	absOffset := math.Abs(offset)
	if absOffset < 0.5 {
		Logf(logf, "时钟同步完成(%s),偏差 %.2f 秒", source, absOffset)
	} else {
		Logf(logf, "系统时钟偏差 %.2f 秒,已按 %s 偏移自动修正", absOffset, source)
	}
	// 浏览器使用系统时间,reCAPTCHA 令牌与时间戳相关;偏差过大时验证会反复失败
	if absOffset > 30 {
		Logf(logf, "警告: 系统时钟与真实时间相差 %.0f 秒。排队计时已自动修正,"+
			"但浏览器仍使用系统时间,人机验证可能反复失败;请开启系统自动时间同步", absOffset)
	}
	now := func() time.Time {
		return time.Now().Add(time.Duration(offset * float64(time.Second)))
	}

	target, err := ParseTargetTime(cfg.Time)
	if err != nil {
		return err
	}
	trigger := target.Add(-time.Duration(cfg.Lead) * time.Second)
	timeout := target.Add(time.Duration(cfg.TimeoutSec) * time.Second)

	if !now().Before(timeout) {
		return fmt.Errorf("目标时间已过(%s)", cfg.Time)
	}

	Logf(logf, "网站: %s | 目标时间: %s | 提前 %d 秒 | %d 个会话 | 间隔 %d 毫秒 | 超时 %d 秒",
		site.Name, cfg.Time, cfg.Lead, len(ds), cfg.IntervalMs, cfg.TimeoutSec)

	Logf(logf, "打开 %d 个会话的标签页...", len(ds))
	for i, d := range ds {
		if err := checkStop(stop); err != nil {
			return err
		}
		if err := d.OpenTabs(site.URL); err != nil {
			return fmt.Errorf("会话 %d: %w", i+1, err)
		}
	}

	// Safari 驱动的专属预检
	for i, d := range ds {
		if v, ok := d.(interface{ VerifyTabs() (int, error) }); ok {
			if n, err := v.VerifyTabs(); err != nil {
				Logf(logf, "[S%d] 无法确认标签页数量: %v", i+1, err)
			} else if n < 1 {
				Logf(logf, "[S%d] 预期 1 个标签页,实际只有 %d 个", i+1, n)
			} else {
				Logf(logf, "[S%d] 标签页已就绪", i+1)
			}
		}
		if c, ok := d.(interface{ CheckJSPermission() error }); ok {
			if err := c.CheckJSPermission(); err != nil {
				return fmt.Errorf("Safari 未开启「允许 Apple 事件中的 JavaScript」(%v)\n"+
					"请打开 Safari → 菜单栏「开发」→ 勾选「允许 Apple 事件中的 JavaScript」后重试\n"+
					"(如无「开发」菜单:Safari → 设置 → 高级 → 勾选「在菜单栏中显示开发菜单」)", err)
			}
		}
	}
	Logf(logf, "执行期间请勿关闭抢票窗口")

	if wait := trigger.Sub(now()); wait > 0 {
		Logf(logf, "等待 %d 秒后触发...", int(wait.Seconds()))
		if err := sleepUntil(trigger, now, stop); err != nil {
			return err
		}
	}

	Logf(logf, "触发!开始监视 %d 个会话(页面自行更新,仅出错才重载)...", len(ds))

	hits, manual, err := Monitor(ds, cfg, target, timeout, now, logf, notify, stop)
	if hits > 0 {
		keepOpen = true
	}
	if err != nil {
		return err
	}
	if hits == 0 {
		if manual {
			keepOpen = true
			Logf(logf, "未检测到排队页面,但出现过人机验证或登录页,浏览器已保留,请继续在浏览器里操作")
		}
		return fmt.Errorf("超时(%d 秒),未检测到排队页面", cfg.TimeoutSec)
	}
	if hits < len(ds) {
		Logf(logf, "超时,共 %d 个会话拿到排队位,%d 个未命中", hits, len(ds)-hits)
	} else {
		Logf(logf, "%d 个会话全部拿到排队位!浏览器标签页已保留。", hits)
	}
	return nil
}

func sleepUntil(t time.Time, now func() time.Time, stop <-chan struct{}) error {
	for {
		remaining := t.Sub(now())
		if remaining <= 0 {
			return nil
		}
		if remaining > 200*time.Millisecond {
			remaining = 200 * time.Millisecond
		}
		if err := sleepFor(remaining, stop); err != nil {
			return err
		}
	}
}
