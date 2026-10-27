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
	{"hcfc", "HCFC 歌迷会", "https://vwr.hinscheung.com/?page_id=hins-1698981622&redirect_to=https://hinscheung.com/"},
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
