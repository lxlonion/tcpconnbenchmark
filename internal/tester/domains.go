package tester

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

// BuiltinDomains is a curated set of 84 commonly used websites in mainland
// China. They are only used as TCP connection targets; no HTTP request or TLS
// handshake is ever sent to them.
var BuiltinDomains = []string{
	"baidu.com",
	"qq.com",
	"taobao.com",
	"jd.com",
	"tmall.com",
	"sina.com.cn",
	"weibo.com",
	"sohu.com",
	"163.com",
	"bilibili.com",
	"zhihu.com",
	"douban.com",
	"iqiyi.com",
	"youku.com",
	"tencent.com",
	"aliyun.com",
	"alibaba.com",
	"1688.com",
	"toutiao.com",
	"csdn.net",
	"cnblogs.com",
	"oschina.net",
	"gitee.com",
	"51cto.com",
	"segmentfault.com",
	"v2ex.com",
	"kuaishou.com",
	"douyin.com",
	"xiaohongshu.com",
	"pinduoduo.com",
	"suning.com",
	"vip.com",
	"dangdang.com",
	"meituan.com",
	"dianping.com",
	"ele.me",
	"ctrip.com",
	"qunar.com",
	"12306.cn",
	"didiglobal.com",
	"amap.com",
	"so.com",
	"sogou.com",
	"people.com.cn",
	"xinhuanet.com",
	"cctv.com",
	"ifeng.com",
	"thepaper.cn",
	"caixin.com",
	"36kr.com",
	"huxiu.com",
	"tmtpost.com",
	"leiphone.com",
	"ithome.com",
	"pconline.com.cn",
	"zol.com.cn",
	"yesky.com",
	"chinaz.com",
	"alipay.com",
	"xiaomi.com",
	"huawei.com",
	"oppo.com",
	"vivo.com",
	"lenovo.com.cn",
	"meizu.com",
	"honor.com",
	"oneplus.com",
	"bytedance.com",
	"netease.com",
	"17173.com",
	"3dmgame.com",
	"gamersky.com",
	"huya.com",
	"douyu.com",
	"yy.com",
	"mgtv.com",
	"xunlei.com",
	"360.cn",
	"2345.com",
	"wps.cn",
	"kingsoft.com",
	"yonyou.com",
	"kingdee.com",
	"58.com",
}

// NormalizeDomain accepts a domain, an IP literal, or a full URL and returns
// a clean lowercase ASCII host name. Internationalized domain names are
// converted to their ASCII (punycode) form.
func NormalizeDomain(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("域名为空")
	}

	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" {
			return "", fmt.Errorf("无法解析网址")
		}
		s = u.Hostname()
	} else {
		s = strings.TrimPrefix(s, "//")
		if i := strings.IndexAny(s, "/?#"); i >= 0 {
			s = s[:i]
		}
		if host, _, err := net.SplitHostPort(s); err == nil {
			s = host
		} else if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			s = s[1 : len(s)-1]
		} else if strings.Count(s, ":") == 1 {
			// Treat example.com:443 as host plus port.
			host, port, _ := strings.Cut(s, ":")
			if allDigits(port) {
				s = host
			}
		}
	}

	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if s == "" {
		return "", fmt.Errorf("域名为空")
	}

	if ip, err := netip.ParseAddr(s); err == nil {
		return ip.Unmap().String(), nil
	}

	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil {
		return "", fmt.Errorf("域名格式无效")
	}
	ascii = strings.ToLower(ascii)
	if len(ascii) > 253 {
		return "", fmt.Errorf("域名过长")
	}

	labels := strings.Split(ascii, ".")
	for _, label := range labels {
		if len(label) == 0 {
			return "", fmt.Errorf("域名包含空标签")
		}
		if len(label) > 63 {
			return "", fmt.Errorf("域名标签过长")
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("域名标签不能以连字符开头或结尾")
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return "", fmt.Errorf("域名包含无效字符")
		}
	}
	return ascii, nil
}

// NormalizeDomains splits pasted text into domains, removes comments and
// duplicates, and reports the entries that could not be used. Order is kept.
func NormalizeDomains(inputs []string) (valid []string, invalid []string) {
	seen := make(map[string]struct{})
	for _, input := range inputs {
		for _, raw := range splitDomainInput(input) {
			domain, err := NormalizeDomain(raw)
			if err != nil {
				invalid = append(invalid, fmt.Sprintf("%q（%v）", strings.TrimSpace(raw), err))
				continue
			}
			if _, ok := seen[domain]; ok {
				continue
			}
			seen[domain] = struct{}{}
			valid = append(valid, domain)
		}
	}
	return valid, invalid
}

func splitDomainInput(input string) []string {
	var fields []string
	for _, line := range strings.Split(input, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields = append(fields, strings.FieldsFunc(line, func(r rune) bool {
			return unicode.IsSpace(r) || r == ',' || r == ';' || r == '，' || r == '；'
		})...)
	}
	return fields
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
