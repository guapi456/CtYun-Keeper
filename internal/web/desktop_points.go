package web

import (
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

func rewritePointsAttributes(raw string) string {
	return htmlAssetAttribute.ReplaceAllStringFunc(raw, func(attr string) string {
		match := htmlAssetAttribute.FindStringSubmatch(attr)
		u, err := url.Parse(match[2])
		if err != nil || (u.IsAbs() && u.Host != "desk.ctyun.cn") {
			return attr
		}
		base, _ := url.Parse("https://desk.ctyun.cn/selforder/points.html")
		u = base.ResolveReference(u)
		if !strings.HasPrefix(u.Path, "/selforder/static/") {
			return attr
		}
		query := u.RawQuery
		if u.Path == "/selforder/static/js/desktop-jsbridge.min.js" {
			query = "v=ui61"
		}
		if query != "" {
			query = "?" + query
		}
		return match[1] + `="/desktop-assets/points` + path.Clean(u.Path) + query + `"`
	})
}

func rewritePointsHTML(raw string) string {
	var out strings.Builder
	last := 0
	for _, span := range htmlScriptBlock.FindAllStringIndex(raw, -1) {
		out.WriteString(rewritePointsAttributes(raw[last:span[0]]))
		block := raw[span[0]:span[1]]
		end := strings.Index(block, ">") + 1
		out.WriteString(rewritePointsAttributes(block[:end]))
		out.WriteString(block[end:])
		last = span[1]
	}
	out.WriteString(rewritePointsAttributes(raw[last:]))
	return out.String()
}

func (s *Server) desktopPointsPage(w http.ResponseWriter, r *http.Request) {
	viewerHeaders(w)
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if !regexpViewerHost.MatchString(r.Host) {
		http.Error(w, "Invalid host", 400)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", "https://desk.ctyun.cn/selforder/points.html", nil)
	if err != nil {
		http.Error(w, "页面请求失败", 502)
		return
	}
	req.Header.Set("User-Agent", r.UserAgent())
	response, err := s.viewerClient().Do(req)
	if err != nil {
		http.Error(w, "无法读取官方积分中心", 502)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		http.Error(w, "官方积分中心暂不可用", 502)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		http.Error(w, "积分中心读取失败", 502)
		return
	}
	html := rewritePointsHTML(string(raw))
	index := strings.Index(strings.ToLower(html), "<head>")
	if index < 0 {
		http.Error(w, "积分中心页面结构变化", 502)
		return
	}
	html = html[:index+6] + `<script src="/static/desktop-points.js?v=ui61"></script>` + html[index+6:]
	sources := "http://" + r.Host + " https://" + r.Host
	// Public vendor HTML contains no account credentials. Access to APIs requires the parent's private relay key.
	w.Header().Del("X-Frame-Options")
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-popups; default-src 'none'; script-src "+sources+" 'unsafe-inline' 'unsafe-eval'; style-src "+sources+" 'unsafe-inline'; connect-src "+sources+"; img-src "+sources+" data: blob: https://*.ctyun.cn https://*.ctyun.cn:8999; font-src "+sources+" data: https://at.alicdn.com http://at.alicdn.com; frame-src 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, html)
}
