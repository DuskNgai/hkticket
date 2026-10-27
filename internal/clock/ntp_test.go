package clock

import (
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func dateServer(t *testing.T, serverTime time.Time) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", serverTime.UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// startNTPServer 在随机端口上回一个固定的 NTP 响应包
func startNTPServer(t *testing.T, packet []byte) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	go func() {
		buf := make([]byte, 48)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil || n < 48 {
			return
		}
		_, _ = pc.WriteTo(packet, addr)
	}()
	return pc.LocalAddr().String()
}

// ntpPacket 构造标准 NTP 响应:模式 4(服务端)、stratum 2、发送时刻为 serverTime
func ntpPacket(serverTime time.Time) []byte {
	resp := make([]byte, 48)
	resp[0] = 0x1C // LI=0, Version=3, Mode=4(服务端)
	resp[1] = 2    // stratum
	binary.BigEndian.PutUint32(resp[40:44], uint32(serverTime.Unix()+ntpEpochOffset))
	return resp
}

func TestQueryNTP(t *testing.T) {
	serverTime := time.Now().Add(2 * time.Second).Truncate(time.Second)
	addr := startNTPServer(t, ntpPacket(serverTime))

	offset, err := queryAddr(addr, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if offset < 0.5 || offset > 2.5 {
		t.Errorf("偏移应约为 2 秒,得到 %.2f", offset)
	}
}

func TestQueryNTPRejectsBadPackets(t *testing.T) {
	short := make([]byte, 10)
	shortAddr := startNTPServer(t, short)
	if _, err := queryAddr(shortAddr, time.Second); err == nil {
		t.Error("过短的响应应报错")
	}

	kiss := ntpPacket(time.Now())
	kiss[1] = 0 // stratum 0 = Kiss-o'-Death,服务器未同步
	kissAddr := startNTPServer(t, kiss)
	if _, err := queryAddr(kissAddr, time.Second); err == nil {
		t.Error("stratum=0 的响应应报错")
	}

	noTime := ntpPacket(time.Now())
	copy(noTime[40:48], []byte{0, 0, 0, 0, 0, 0, 0, 0})
	noTimeAddr := startNTPServer(t, noTime)
	if _, err := queryAddr(noTimeAddr, time.Second); err == nil {
		t.Error("缺少发送时间的响应应报错")
	}
}

func TestQueryHTTP(t *testing.T) {
	serverTime := time.Now().Add(3 * time.Second).Truncate(time.Second)
	srv := dateServer(t, serverTime)

	offset, err := queryHTTP(srv.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Date 头只精确到秒,允许 1 秒量化误差
	if offset < 1.5 || offset > 3.5 {
		t.Errorf("偏移应约为 3 秒,得到 %.2f", offset)
	}
}

func TestOffsetFallsBackToHTTP(t *testing.T) {
	serverTime := time.Now().Add(2 * time.Second).Truncate(time.Second)
	srv := dateServer(t, serverTime)

	old := httpTimeServers
	httpTimeServers = []string{srv.URL}
	t.Cleanup(func() { httpTimeServers = old })

	// 127.0.0.1 的 UDP 123 无人应答,应超时后回退到 HTTPS
	offset, source, err := Offset([]string{"127.0.0.1"}, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(source, "HTTPS ") {
		t.Errorf("应使用 HTTPS 兜底,实际来源 %q", source)
	}
	if offset < 0.5 || offset > 2.5 {
		t.Errorf("偏移应约为 2 秒,得到 %.2f", offset)
	}
}
