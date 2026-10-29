// Package driver 抽象浏览器控制。一个驱动实例对应一个浏览器窗口,
// 窗口内固定 1 个标签页;多会话靠多个驱动实例实现
package driver

// Driver 是浏览器控制接口。各方法把错误原样返回,由调用方决定中止或下一轮重试。
type Driver interface {
	// OpenTabs 新开一个窗口并打开目标 URL
	OpenTabs(url string) error
	// GetContent 返回页面 #root(或 body)的 innerText;
	// 页面正在导航等过渡状态下内容不可得,返回空字符串与 nil
	GetContent() (string, error)
	// ClickEntry 点击排队入口按钮,返回按钮文本;没有按钮时返回空字符串与 nil
	ClickEntry() (string, error)
	// Reload 重新导航到目标 URL(仅用于出错恢复)
	Reload() error
	// BringToFront 把浏览器窗口置前
	BringToFront() error
	// Close 关闭该驱动对应的浏览器窗口
	Close() error
}

// ClickButtonJS 在页面里查找并点击排队入口/购票按钮,返回按钮文本。
// 英文部分不用 \b:该脚本会被嵌进 AppleScript 双引号字符串,反斜杠会被吃掉,
// 因此用 ^(join|enter|proceed|continue|refresh|reload|buy|purchase)( |$) 做整词约束。
//
// 字符串一律用单引号,并且不能在单引号字符串里再出现单引号:
// 之前用 querySelectorAll('[class*='button']') 的写法会把字符串截断,
// 整段脚本抛 SyntaxError,点击被静默吞掉(2026-10-28 实测踩过)。
// 因此这里不用属性选择器,改为遍历元素、用 getAttribute('class') 自己判断,
// 同时兼容 uutix 的 div.button(它不是 <button>)。
// 外层容器和内层按钮可能同时命中同一段文字,只点没有命中子元素的那个:
// 外层 div 没有绑定 click,uutix 点它不会跳转
const ClickButtonJS = `(function(){var all=document.getElementsByTagName('*');var hits=[];var i,e,tag,cls;for(i=0;i<all.length;i++){e=all[i];tag=e.tagName.toLowerCase();cls=e.getAttribute('class')||'';var ok=tag==='button'||tag==='a'||(tag==='input'&&(e.type==='button'||e.type==='submit'))||e.getAttribute('role')==='button'||cls.indexOf('button')>=0||(' '+cls+' ').indexOf(' btn ')>=0;if(!ok)continue;t=(e.innerText||e.value||'').trim();if(t&&(/整理|進入|进入|加入|前往|購買門票|购买门票|選座購票|选座购票|特惠選座|立即購買|立即购买/i.test(t)||/^(join|enter|proceed|continue|refresh|reload|buy|purchase)( |$)/i.test(t))){hits.push(e);}}for(i=0;i<hits.length;i++){var d=!1;for(var k=0;k<hits.length;k++){if(k!==i&&hits[i].contains(hits[k])){d=!0;break;}}if(!d){hits[i].click();return (hits[i].innerText||hits[i].value||'').trim();}}if(hits.length){hits[0].click();return (hits[0].innerText||hits[0].value||'').trim();}return '';})()`

// GetContentJS 取 React 根节点(或 body)的可见文本
const GetContentJS = `document.getElementById('root') ? document.getElementById('root').innerText : document.body.innerText`
