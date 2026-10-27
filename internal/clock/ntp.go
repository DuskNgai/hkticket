package clock

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// 1900-01-01 与 1970-01-01 之间的秒数(NTP 时间戳起点是 1900 年)
const ntpEpochOffset = 2208988800

// httpTimeServers 是 NTP 全部失败时的兜底时间源(取响应 Date 头)。
// 精度约 1 秒,不如 NTP,但足以覆盖抢号场景;UDP 123 被网络或防火墙拦截时仍可用
var httpTimeServers = []string{
	"https://www.baidu.com",
	"https://www.qq.com",
	"https://www.jd.com",
}

// Offset 依次尝试 NTP 服务器,全部失败后改用 HTTPS Date 头,
// 返回本地时钟的偏移秒数(本地时间 + 偏移 = 真实时间)与校时来源。
// 全部失败时返回错误,调用方应立即退出:抢号对时间敏感,
// 用未校准的时钟继续运行没有价值。
func Offset(servers []string, timeout time.Duration) (float64, string, error) {
	var ntpErrs []string
	for _, server := range servers {
		offset, err := query(server, timeout)
		if err == nil {
			return offset, "NTP " + server, nil
		}
		ntpErrs = append(ntpErrs, fmt.Sprintf("%s: %v", server, err))
	}

	var httpErrs []string
	for _, server := range httpTimeServers {
		offset, err := queryHTTP(server, timeout)
		if err == nil {
			return offset, "HTTPS " + server, nil
		}
		httpErrs = append(httpErrs, fmt.Sprintf("%s: %v", server, err))
	}

	return 0, "", fmt.Errorf("NTP 校时失败,请先校准系统时钟(NTP 与 HTTPS 均不可用,可检查网络是否拦截 UDP 123): NTP[%s]; HTTPS[%s]",
		strings.Join(ntpErrs, "; "), strings.Join(httpErrs, "; "))
}

func query(server string, timeout time.Duration) (float64, error) {
	return queryAddr(server+":123", timeout)
}

func queryAddr(addr string, timeout time.Duration) (float64, error) {
	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}

	req := make([]byte, 48)
	req[0] = 0x1B // LI=0, Version=3, Mode=3(客户端)

	t1 := time.Now()
	if _, err := conn.Write(req); err != nil {
		return 0, err
	}
	resp := make([]byte, 48)
	n, err := conn.Read(resp)
	if err != nil {
		return 0, err
	}
	t4 := time.Now()

	// 短包或异常响应会把时间算成天量偏移,必须校验后再使用
	if n < 48 {
		return 0, fmt.Errorf("NTP 响应过短(%d 字节)", n)
	}
	if mode := resp[0] & 0x7; mode != 4 {
		return 0, fmt.Errorf("NTP 响应模式异常(%d)", mode)
	}
	if stratum := resp[1]; stratum == 0 || stratum > 15 {
		return 0, fmt.Errorf("NTP 服务器未同步(stratum=%d)", stratum)
	}

	secs := binary.BigEndian.Uint32(resp[40:44])
	if secs == 0 {
		return 0, fmt.Errorf("NTP 响应缺少发送时间")
	}
	frac := binary.BigEndian.Uint32(resp[44:48])
	t3 := time.Unix(int64(secs)-ntpEpochOffset, int64(float64(frac)/4294967296.0*1e9))

	// 服务器发送时刻与本地往返中点之差,即本地时钟偏移
	midpoint := t1.Add(t4.Sub(t1) / 2)
	return t3.Sub(midpoint).Seconds(), nil
}

// queryHTTP 用响应的 Date 头估算时钟偏移。Date 头只精确到秒,
// 因此结果约有 1 秒误差;往返中点用于抵消网络时延
func queryHTTP(rawURL string, timeout time.Duration) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}

	t1 := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	t4 := time.Now()

	// Date 头与状态码无关(例如 QQ 对 HEAD 返回 501 但仍带 Date),有 Date 就能用
	date := resp.Header.Get("Date")
	if date == "" {
		return 0, fmt.Errorf("响应缺少 Date 头")
	}
	t3, err := http.ParseTime(date)
	if err != nil {
		return 0, fmt.Errorf("解析 Date 头失败: %w", err)
	}
	// Date 头向下截断到秒,加上半秒取该秒中点,把量化误差从 [-1, 0] 缩小到 ±0.5 秒
	t3 = t3.Add(500 * time.Millisecond)

	midpoint := t1.Add(t4.Sub(t1) / 2)
	return t3.Sub(midpoint).Seconds(), nil
}
