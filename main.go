package main

import (
	"flag"
	"fmt"
	"os"

	"hkticket/internal/core"
	"hkticket/internal/driver"
	"hkticket/internal/settings"
	"hkticket/internal/webui"
)

func main() {
	// 无参数启动:打开图形控制台
	if len(os.Args) == 1 {
		if err := webui.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "错误:", err)
			os.Exit(1)
		}
		return
	}

	// 上次使用过的参数作为本次默认值
	saved, err := settings.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误: 读取参数记忆失败:", err)
		os.Exit(1)
	}
	defLead, defSessions, defInterval, defTimeout, defDriver :=
		core.DefaultLeadSec, core.DefaultSessions, core.DefaultIntervalMs, core.DefaultTimeoutSec, "auto"
	defSite := core.Sites[0].Key
	if saved.Site != "" {
		defSite = saved.Site
	}
	if saved.Lead > 0 {
		defLead = saved.Lead
	}
	if saved.Sessions > 0 {
		defSessions = saved.Sessions
	}
	if saved.Interval > 0 {
		defInterval = saved.Interval
	}
	if saved.Timeout > 0 {
		defTimeout = saved.Timeout
	}
	if saved.Driver != "" {
		defDriver = saved.Driver
	}

	fs := flag.NewFlagSet("hkticket", flag.ExitOnError)
	site := fs.String("site", defSite, "目标网站:hcfc(HCFC 歌迷会)/ custom(自定义,配合 --url)")
	urlStr := fs.String("url", saved.URL, "自定义入口地址(仅 --site custom 时使用;上次输入会自动填入)")
	timeStr := fs.String("time", saved.Time, "目标时间 YYYY-MM-DD HH:MM:SS(香港时区;上次输入会自动填入)")
	lead := fs.Int("lead", defLead, "提前开始监视的秒数")
	sessions := fs.Int("sessions", defSessions, "独立会话数量(各自排一个号;多会话需 cdp 驱动)")
	interval := fs.Int("interval", defInterval, "页面检查间隔(毫秒,不小于 100)")
	timeout := fs.Int("timeout", defTimeout, "开售后多少秒仍未命中则放弃")
	driverName := fs.String("driver", defDriver, "浏览器驱动:auto / safari / cdp(默认 macOS 用 Safari,其余平台用系统 Chrome/Edge)")
	fs.Parse(os.Args[1:])

	if *timeStr == "" {
		fs.Usage()
		os.Exit(2)
	}
	cfg := core.Config{
		Site:       *site,
		URL:        *urlStr,
		Time:       *timeStr,
		Lead:       *lead,
		Sessions:   *sessions,
		IntervalMs: *interval,
		TimeoutSec: *timeout,
	}
	if _, err := core.ResolveSite(cfg.Site, cfg.URL); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(2)
	}
	if err := core.ValidateConfig(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(2)
	}

	ds, err := driver.NewN(*driverName, *sessions)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	if err := settings.Save(settings.Settings{
		Site: cfg.Site, URL: cfg.URL, Time: cfg.Time, Lead: cfg.Lead, Sessions: cfg.Sessions,
		Interval: cfg.IntervalMs, Timeout: cfg.TimeoutSec, Driver: *driverName,
	}); err != nil {
		for _, d := range ds {
			if cerr := d.Close(); cerr != nil {
				fmt.Fprintln(os.Stderr, "关闭浏览器失败:", cerr)
			}
		}
		fmt.Fprintln(os.Stderr, "错误: 保存参数记忆失败:", err)
		os.Exit(1)
	}

	if err := core.Run(ds, cfg, func(s string) { fmt.Println(s) }, nil); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
