package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleStopWaitsForTaskExit(t *testing.T) {
	h := newHub()
	stop := make(chan struct{})
	done := make(chan struct{})
	h.running = true
	h.stop = stop
	h.done = done

	returned := make(chan struct{})
	go func() {
		h.handleStop(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/stop", nil))
		close(returned)
	}()

	<-stop
	select {
	case <-returned:
		t.Fatal("任务尚未退出时,停止请求不应返回")
	case <-time.After(50 * time.Millisecond):
	}
	h.mu.Lock()
	stopping := h.stopping
	h.mu.Unlock()
	if !stopping {
		t.Error("停止过程中应标记为 stopping")
	}

	close(done)
	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("任务退出后,停止请求应返回")
	}
}

func TestHandleStartConflictWhileRunning(t *testing.T) {
	h := newHub()
	h.running = true
	h.stopping = false

	rr := httptest.NewRecorder()
	h.handleStart(rr, httptest.NewRequest(http.MethodPost, "/api/start", nil))
	if rr.Code != http.StatusConflict {
		t.Errorf("运行中再次开始应返回 409, 得到 %d", rr.Code)
	}
}

func TestHandleStartWaitsForStoppingTask(t *testing.T) {
	h := newHub()
	done := make(chan struct{})
	h.running = true
	h.stopping = true
	h.stop = make(chan struct{})
	h.done = done

	returned := make(chan int)
	go func() {
		rr := httptest.NewRecorder()
		h.handleStart(rr, httptest.NewRequest(http.MethodPost, "/api/start", nil))
		returned <- rr.Code
	}()

	select {
	case <-returned:
		t.Fatal("旧任务退出前,新的开始请求不应返回")
	case <-time.After(50 * time.Millisecond):
	}

	h.mu.Lock()
	h.running = false
	h.stopping = false
	h.mu.Unlock()
	close(done)

	select {
	case code := <-returned:
		// 旧任务退出后继续处理;空请求体解析失败,返回 400
		if code != http.StatusBadRequest {
			t.Errorf("空请求体应返回 400, 得到 %d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("旧任务退出后,新的开始请求应继续处理")
	}
}

func TestHandleState(t *testing.T) {
	h := newHub()
	h.running = true
	h.stopping = true

	rr := httptest.NewRecorder()
	h.handleState(rr, httptest.NewRequest(http.MethodGet, "/api/state", nil))

	if got, want := strings.TrimSpace(rr.Body.String()), `{"running":true,"stopping":true}`; got != want {
		t.Errorf("状态 JSON = %s, 期望 %s", got, want)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}
}
