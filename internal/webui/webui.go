// Package webui 提供内嵌的图形控制台:本机 HTTP 服务 + 内嵌 HTML 页面
package webui

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"rush/internal/core"
	"rush/internal/driver"
	"rush/internal/settings"
)

//go:embed index.html
var indexHTML string

type hub struct {
	mu       sync.Mutex
	running  bool
	stopping bool
	stop     chan struct{}
	done     chan struct{}
	subs     map[chan string]struct{}
}

func newHub() *hub {
	return &hub{subs: map[chan string]struct{}{}}
}

func (h *hub) broadcast(s string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- s:
		default: // 订阅者读取太慢时丢弃,不阻塞任务
		}
	}
}

func (h *hub) subscribe() chan string {
	ch := make(chan string, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan string) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 先占位再启动浏览器:创建 Chrome 需要数秒,期间重复点击「开始」
	// 不能并发启动第二套任务。正在停止的任务要等它退出再开始,
	// 否则会与旧任务争用同一份浏览器配置目录
	h.mu.Lock()
	for h.running && h.stopping {
		done := h.done
		if done == nil {
			break
		}
		h.mu.Unlock()
		core.Logf(h.broadcast, "上一个任务正在停止,等待它退出后再启动...")
		<-done
		h.mu.Lock()
	}
	if h.running {
		h.mu.Unlock()
		http.Error(w, "任务正在运行", http.StatusConflict)
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	h.running = true
	h.stopping = false
	h.stop = stop
	h.done = done
	h.mu.Unlock()
	started := false
	defer func() {
		if started {
			return
		}
		h.mu.Lock()
		h.running = false
		h.stopping = false
		h.stop = nil
		h.done = nil
		h.mu.Unlock()
		close(done)
	}()

	// 前端用 FormData 提交(multipart);ParseForm 不解析 multipart 正文,
	// 必须先走 ParseMultipartForm(其内部已包含 ParseForm)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, err := parseConfig(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	core.Logf(h.broadcast, "正在启动 %d 个浏览器...", cfg.Sessions)
	ds, err := driver.NewN(r.FormValue("driver"), cfg.Sessions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	core.Logf(h.broadcast, "浏览器已启动")

	// 记下本次输入,下次启动自动填好
	if err := settings.Save(settings.Settings{
		Site: cfg.Site, URL: cfg.URL, Time: cfg.Time, Lead: cfg.Lead, Sessions: cfg.Sessions,
		Interval: cfg.IntervalMs, Timeout: cfg.TimeoutSec, Driver: r.FormValue("driver"),
	}); err != nil {
		for i, d := range ds {
			if cerr := d.Close(); cerr != nil {
				core.Logf(h.broadcast, "[S%d] 关闭浏览器失败: %v", i+1, cerr)
			}
		}
		http.Error(w, "保存参数记忆失败: "+err.Error(), http.StatusInternalServerError)
		return
	}

	started = true
	go func() {
		err := core.Run(ds, cfg, h.broadcast, stop)
		if errors.Is(err, core.ErrStopped) {
			core.Logf(h.broadcast, "任务已停止")
		} else if err != nil {
			for _, line := range strings.Split(err.Error(), "\n") {
				core.Logf(h.broadcast, "错误: %s", line)
			}
		}
		// 先把服务端状态改回空闲,再通知页面,页面收到「任务结束」后可以直接开始新任务
		h.mu.Lock()
		h.running = false
		h.stopping = false
		h.stop = nil
		h.done = nil
		h.mu.Unlock()
		close(done)
		core.Logf(h.broadcast, "任务结束")
	}()
	fmt.Fprintln(w, "started")
}

func (h *hub) handleStop(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if !h.running {
		h.mu.Unlock()
		fmt.Fprintln(w, "stopped")
		return
	}
	h.stopping = true
	if h.stop != nil {
		close(h.stop)
		h.stop = nil
	}
	done := h.done
	h.mu.Unlock()
	// 等到任务真正退出,避免紧接着的「开始」读到旧的运行状态
	if done != nil {
		<-done
	}
	fmt.Fprintln(w, "stopped")
}

// handleState 返回当前任务状态,页面刷新后据此恢复按钮状态
func (h *hub) handleState(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	state := struct {
		Running  bool `json:"running"`
		Stopping bool `json:"stopping"`
	}{h.running, h.stopping}
	h.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	data, err := json.Marshal(state)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, string(data))
}

func (h *hub) handleLogs(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := h.subscribe()
	defer h.unsubscribe(ch)
	for {
		select {
		case <-r.Context().Done():
			return
		case s := <-ch:
			// SSE 的 data 行不允许包含原始换行,逐行发送
			for _, line := range strings.Split(s, "\n") {
				fmt.Fprintf(w, "data: %s\n", line)
			}
			fmt.Fprint(w, "\n")
			flusher.Flush()
		}
	}
}

// handleSites 返回支持的网站列表(标识 + 显示名 + 入口地址),供前端渲染下拉框
func handleSites(w http.ResponseWriter, r *http.Request) {
	type option struct {
		Key  string `json:"key"`
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	list := make([]option, len(core.Sites))
	for i, s := range core.Sites {
		list[i] = option{s.Key, s.Name, s.URL}
	}
	// 自定义选项固定在最后,地址由用户粘贴
	list = append(list, option{core.CustomSiteKey, "自定义(粘贴地址)", ""})
	w.Header().Set("Content-Type", "application/json")
	data, err := json.Marshal(list)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, string(data))
}

func (h *hub) handleSettings(w http.ResponseWriter, r *http.Request) {
	s, err := settings.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	data, err := json.Marshal(s)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	fmt.Fprintln(w, string(data))
}

func parseConfig(r *http.Request) (core.Config, error) {
	cfg := core.Config{
		Site: r.FormValue("site"),
		URL:  r.FormValue("url"),
		Time: r.FormValue("time"),
	}
	if _, err := core.ResolveSite(cfg.Site, cfg.URL); err != nil {
		return cfg, err
	}
	if cfg.Time == "" {
		return cfg, fmt.Errorf("目标时间必填")
	}
	ints := []struct {
		name string
		def  int
		dst  *int
	}{
		{"lead", core.DefaultLeadSec, &cfg.Lead},
		{"sessions", core.DefaultSessions, &cfg.Sessions},
		{"interval", core.DefaultIntervalMs, &cfg.IntervalMs},
		{"timeout", core.DefaultTimeoutSec, &cfg.TimeoutSec},
	}
	for _, it := range ints {
		v := r.FormValue(it.name)
		if v == "" {
			*it.dst = it.def
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return cfg, fmt.Errorf("参数 %s 应为非负整数: %q", it.name, v)
		}
		*it.dst = n
	}
	if err := core.ValidateConfig(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Run 启动控制台服务并打开系统默认浏览器,阻塞直到进程结束
func Run() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	h := newHub()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, indexHTML)
	})
	mux.HandleFunc("/api/start", h.handleStart)
	mux.HandleFunc("/api/stop", h.handleStop)
	mux.HandleFunc("/api/state", h.handleState)
	mux.HandleFunc("/api/logs", h.handleLogs)
	mux.HandleFunc("/api/settings", h.handleSettings)
	mux.HandleFunc("/api/sites", handleSites)

	addr := "http://" + listener.Addr().String()
	fmt.Println("Rush 控制台:", addr)
	fmt.Println("关闭此窗口即可退出程序")
	openBrowser(addr)
	return http.Serve(listener, mux)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		// Windows 默认浏览器往往是 Edge;控制台优先用 Chrome,找不到再走系统默认
		if bin, ok := driver.BrowserPath(); ok {
			cmd = exec.Command(bin, url)
		} else {
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		}
	default:
		cmd = exec.Command("xdg-open", url)
	}
	// 必须异步启动:直接启动 chrome.exe 时 cmd.Run 会等浏览器退出才返回,
	// 导致后面的 http.Serve 迟迟不执行,控制台页面一直转圈
	if err := cmd.Start(); err != nil {
		fmt.Println("无法自动打开浏览器,请手动访问上面的地址:", err)
		return
	}
	_ = cmd.Process.Release()
}
