package core

import (
	"fmt"
	"net/url"
	"strings"
)

// Site 一个支持的购票网站
type Site struct {
	Key  string // 稳定标识,用于参数记忆与命令行参数
	Name string // 界面显示名
	URL  string // 排队入口地址
}

// Sites 支持的网站,顺序即界面选项顺序
var Sites = []Site{
	{"hcfc", "HCFC 歌迷会", "https://vwr.hinscheung.com/?page_id=hins-1698981622&redirect_to=https%3A%2F%2Fhinscheung.com%2F"},
	// uutix 没有单独的排队入口页:开售前就是演出详情页,按钮由「即將開售」在开售时刻
	// 自动切换成「購買門票 / 選座購票」,点击后在同一标签页进入 /ticket 选票页。
	// 换一场演出时把 pId 换成对应详情页的编号,或用「自定义」粘贴详情页地址。
	{"uutix", "uutix(澳門演唱會)", "https://www.uutix.com/detail?pId=4007"},
}

// FindSite 按标识查找网站
func FindSite(key string) (Site, error) {
	for _, s := range Sites {
		if s.Key == key {
			return s, nil
		}
	}
	keys := make([]string, len(Sites))
	for i, s := range Sites {
		keys[i] = s.Key
	}
	return Site{}, fmt.Errorf("未知网站 %q(可选: %s)", key, strings.Join(keys, " / "))
}

// CustomSiteKey 自定义地址选项的标识:地址由用户粘贴,不在注册表内
const CustomSiteKey = "custom"

// ResolveSite 把网站标识解析成入口地址;custom 时校验并采用用户粘贴的地址
func ResolveSite(key, rawURL string) (Site, error) {
	if key != CustomSiteKey {
		return FindSite(key)
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return Site{}, fmt.Errorf("自定义地址应为完整的 http/https 地址: %q", rawURL)
	}
	return Site{Key: CustomSiteKey, Name: "自定义网站", URL: u.String()}, nil
}
