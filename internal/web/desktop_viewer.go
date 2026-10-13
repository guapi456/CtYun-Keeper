package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/security"
	"github.com/vay1314/CtYun-Keeper/internal/service"
)

type desktopViewerSession struct {
	ID       string
	RelayKey string
	EntryKey string
	Login    service.DesktopBrowserLogin
	Expires  time.Time
	LastSeen time.Time
	ClosedAt time.Time
	Instance string
}

func (s *Server) secureDesktopViewerRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.secure && r.Header.Get("X-Forwarded-Proto") == "https" {
		return true
	}
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func (s *Server) createDesktopViewer(login service.DesktopBrowserLogin) (string, error) {
	u, err := url.Parse(login.URL)
	if err != nil || u.Scheme != "https" || u.Host != "pc.ctyun.cn" || !strings.HasPrefix(u.Fragment, "/desktop?") {
		return "", errors.New("官方桌面入口无效")
	}
	s.viewerMu.Lock()
	defer s.viewerMu.Unlock()
	if s.viewers == nil {
		s.viewers = map[string]desktopViewerSession{}
	}
	for id, v := range s.viewers {
		if time.Now().After(v.Expires) {
			s.queueViewerResumeLocked(v.Login.Target)
			delete(s.viewers, id)
		}
	}
	if len(s.viewers) >= 16 {
		return "", errors.New("已打开过多桌面会话，请等待旧会话过期")
	}
	id := security.RandomToken(18)
	v := desktopViewerSession{ID: id, RelayKey: security.RandomToken(32), EntryKey: security.RandomToken(32), Login: login, Expires: time.Now().Add(time.Hour), LastSeen: time.Now(), Instance: security.RandomToken(16)}
	s.viewers[id] = v
	// The entry ID alone grants no authority; page access requires management login.
	return "/desktop-view/" + id, nil
}
func (s *Server) getDesktopViewer(id string) (desktopViewerSession, bool) {
	s.viewerMu.Lock()
	defer s.viewerMu.Unlock()
	v, ok := s.viewers[id]
	if ok && time.Now().After(v.Expires) {
		return desktopViewerSession{}, false
	}
	return v, ok
}
func viewerHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
func (s *Server) viewerClient() *http.Client {
	if s.viewerHTTP != nil {
		return s.viewerHTTP
	}
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var htmlAssetAttribute = regexp.MustCompile(`(?i)(src|href)\s*=\s*["']([^"']*)["']`)
var htmlExternalScript = regexp.MustCompile(`(?is)<script\b[^>]*src\s*=\s*["']https?://[^"']+["'][^>]*>\s*</script>`)
var htmlScriptBlock = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
var regexpViewerHost = regexp.MustCompile(`^[a-zA-Z0-9.\-\[\]:]+$`)

func rewriteDesktopHTML(raw string) string {
	raw = htmlExternalScript.ReplaceAllStringFunc(raw, func(tag string) string {
		match := htmlAssetAttribute.FindStringSubmatch(tag)
		if len(match) != 3 {
			return ""
		}
		u, err := url.Parse(match[2])
		if err != nil || (u.Hostname() != "pc.ctyun.cn" && u.Hostname() != "deskcdn.ctyun.cn") {
			return ""
		}
		return tag
	})
	// Never interpret JavaScript strings (such as document.write) as HTML attributes.
	var out strings.Builder
	last := 0
	for _, span := range htmlScriptBlock.FindAllStringIndex(raw, -1) {
		out.WriteString(rewriteDesktopAttributes(raw[last:span[0]]))
		block := raw[span[0]:span[1]]
		end := strings.Index(block, ">") + 1
		body := block[end:]
		// Service workers cannot run in the isolated desktop window.
		if !strings.Contains(body, "navigator.serviceWorker.register") {
			body = strings.ReplaceAll(body, "https://deskcdn.ctyun.cn/pccdnstatic/", "/desktop-assets/cdn/pccdnstatic/")
			body = strings.ReplaceAll(body, "var base=useLocal ? localBase() : cdnBase;", `var base=useLocal ? "/desktop-assets/pc/" : cdnBase;`)
			out.WriteString(rewriteDesktopAttributes(block[:end]))
			out.WriteString(body)
		}
		last = span[1]
	}
	out.WriteString(rewriteDesktopAttributes(raw[last:]))
	return out.String()
}

func rewriteDesktopAttributes(raw string) string {
	return htmlAssetAttribute.ReplaceAllStringFunc(raw, func(attr string) string {
		match := htmlAssetAttribute.FindStringSubmatch(attr)
		if len(match) != 3 {
			return attr
		}
		value := match[2]
		if strings.HasPrefix(value, "data:") || strings.HasPrefix(value, "#") {
			return attr
		}
		u, err := url.Parse(value)
		if err != nil {
			return attr
		}
		origin := "pc"
		if u.IsAbs() {
			switch u.Hostname() {
			case "pc.ctyun.cn":
			case "deskcdn.ctyun.cn":
				origin = "cdn"
			default:
				return attr
			}
		}
		return match[1] + `="/desktop-assets/` + origin + "/" + strings.TrimPrefix(path.Clean("/"+u.Path), "/") + `"`
	})
}

func (s *Server) desktopViewerPage(w http.ResponseWriter, r *http.Request) {
	viewerHeaders(w)
	if !regexpViewerHost.MatchString(r.Host) {
		http.Error(w, "Invalid host", 400)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if !s.secureDesktopViewerRequest(r) {
		http.Error(w, "自动登录桌面支持局域网 HTTP 访问，公网访问请使用 HTTPS", 400)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/desktop-view/")
	v, ok := s.getDesktopViewer(id)
	if !ok {
		http.Error(w, "桌面会话已过期，请重新打开", 410)
		return
	}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 2048)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "桌面入口验证失败，请从云机管理重新打开", 403)
			return
		}
		s.viewerMu.Lock()
		current := s.viewers[id]
		valid := current.EntryKey != "" && subtle.ConstantTimeCompare([]byte(current.EntryKey), []byte(r.PostForm.Get("entry_key"))) == 1
		if valid {
			current.EntryKey = ""
			s.viewers[id] = current
		}
		s.viewerMu.Unlock()
		if !valid && !s.authed(r) {
			http.Error(w, "桌面入口已使用或无效，请从云机管理重新打开", 403)
			return
		}
	} else if !s.authed(r) {
		http.Error(w, "请从已登录的云机管理页面重新打开桌面", 401)
		return
	}
	s.viewerOpMu.Lock()
	s.viewerMu.Lock()
	current, exists := s.viewers[id]
	if exists {
		current.LastSeen = time.Now()
		current.ClosedAt = time.Time{}
		current.Instance = security.RandomToken(16)
		s.viewers[id] = current
		v = current
	}
	s.viewerMu.Unlock()
	if exists && s.manager != nil && v.Login.Target.AccountID > 0 {
		_ = s.manager.RenewDesktopBrowser(v.Login.Target)
	}
	s.viewerOpMu.Unlock()
	if !exists {
		http.Error(w, "桌面会话已结束，请重新打开", 410)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://pc.ctyun.cn/", nil)
	if err != nil {
		http.Error(w, "页面请求失败", 502)
		return
	}
	req.Header.Set("User-Agent", r.UserAgent())
	req.Header.Set("Referer", "https://pc.ctyun.cn/")
	response, err := s.viewerClient().Do(req)
	if err != nil && r.Context().Err() == nil {
		response, err = s.viewerClient().Do(req.Clone(r.Context()))
	}
	if err != nil {
		message := "无法读取官方桌面页面：网络连接失败，请检查运行程序的设备能否访问 pc.ctyun.cn:443"
		var dnsError *net.DNSError
		var networkError net.Error
		if errors.As(err, &dnsError) {
			message = "无法读取官方桌面页面：DNS 解析失败，请检查运行设备的 DNS 配置"
		} else if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &networkError) && networkError.Timeout()) {
			message = "无法读取官方桌面页面：连接超时，请检查运行设备到 pc.ctyun.cn:443 的网络"
		}
		http.Error(w, message, 502)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		http.Error(w, "官方桌面页面暂不可用", 502)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		http.Error(w, "页面读取失败", 502)
		return
	}
	p := v.Login.Profile
	bootstrap := map[string]any{"id": v.ID, "relayKey": v.RelayKey, "expires": v.Expires.UnixMilli(), "deviceCode": v.Login.DeviceCode, "hash": strings.TrimPrefix(strings.SplitN(v.Login.URL, "#", 2)[1], "#"), "auth": map[string]any{"userId": p.UserID, "userEid": p.UserEID, "tenantId": p.TenantID, "userName": p.UserName, "userAccount": v.Login.UserAccount, "mobilephone": p.MobilePhone, "secretKey": p.SecretKey, "bondedDevice": p.BondedDevice, "commonLoginReqHeader": p.CommonLoginReqHeader, "deviceType": "60", "timestamp": time.Now().UnixMilli(), "logined": true}}
	scheme := "http"
	if r.TLS != nil || (s.secure && r.Header.Get("X-Forwarded-Proto") == "https") {
		scheme = "https"
	}
	bootstrap["origin"] = scheme + "://" + r.Host
	payload, _ := json.Marshal(bootstrap)
	inject := `<script data-desktop-bootstrap>window.__DESKTOP_BOOTSTRAP__=` + string(payload) + `;</script><script data-desktop-storage>window.__DESKTOP_STORAGE_SNAPSHOT__=[];</script><script src="/static/desktop-viewer.js?v=ui61"></script>`
	html := rewriteDesktopHTML(string(raw))
	index := strings.Index(strings.ToLower(html), "<head>")
	if index < 0 {
		http.Error(w, "官方页面结构变化，无法带入登录态", 502)
		return
	}
	html = html[:index+6] + inject + html[index+6:]
	// The outer page retains management authentication; only vendor code gets an opaque origin.
	sources := "http://" + r.Host + " https://" + r.Host
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src "+sources+" 'unsafe-inline' 'unsafe-eval' 'wasm-unsafe-eval'; style-src "+sources+" 'unsafe-inline'; img-src "+sources+" data: blob: https://*.ctyun.cn https://*.ctyun.cn:8999; font-src "+sources+" data: https://deskcdn.ctyun.cn https://at.alicdn.com http://at.alicdn.com; connect-src "+sources+" wss://*.ctyun.cn wss://*.ctyun.cn:9011 https://*.ctyun.cn:9011 blob:; worker-src "+sources+" blob:; media-src "+sources+" blob:; frame-src 'self' about: https://desk.ctyun.cn/selforder/points.html; manifest-src "+sources+"; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	shell, _ := json.Marshal(map[string]any{"id": v.ID, "key": v.RelayKey, "instance": v.Instance, "html": html})
	io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><title>云电脑桌面</title><style>html,body,iframe{width:100%;height:100%;margin:0;border:0;overflow:hidden}</style></head><body><iframe id="desktop-frame" title="云电脑桌面" sandbox="allow-scripts allow-forms allow-popups allow-modals allow-pointer-lock allow-downloads" allow="fullscreen; clipboard-read; clipboard-write"></iframe><script id="desktop-shell-data" type="application/json">`+string(shell)+`</script><script src="/static/desktop-view-shell.js?v=ui61"></script></body></html>`)
}

func (s *Server) closeDesktopViewer(w http.ResponseWriter, r *http.Request) {
	s.desktopViewerPulse(w, r, true)
}
func (s *Server) heartbeatDesktopViewer(w http.ResponseWriter, r *http.Request) {
	s.desktopViewerPulse(w, r, false)
}
func (s *Server) desktopViewerPulse(w http.ResponseWriter, r *http.Request, closing bool) {
	viewerHeaders(w)
	if r.Method != "POST" || !s.viewerPulseOrigin(r) {
		http.Error(w, "Forbidden", 403)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "null")
	var input struct {
		ID       string `json:"id"`
		Key      string `json:"key"`
		Instance string `json:"instance"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&input); err != nil {
		http.Error(w, "Invalid request", 400)
		return
	}
	s.viewerOpMu.Lock()
	defer s.viewerOpMu.Unlock()
	s.viewerMu.Lock()
	v, ok := s.viewers[input.ID]
	if !ok || time.Now().After(v.Expires) || subtle.ConstantTimeCompare([]byte(v.RelayKey), []byte(input.Key)) != 1 {
		s.viewerMu.Unlock()
		http.Error(w, "Invalid session", 403)
		return
	}
	if subtle.ConstantTimeCompare([]byte(v.Instance), []byte(input.Instance)) != 1 {
		s.viewerMu.Unlock()
		if closing {
			w.WriteHeader(204)
		} else {
			http.Error(w, "桌面入口已在其他窗口重新打开", 409)
		}
		return
	}
	if !closing && !v.ClosedAt.IsZero() {
		s.viewerMu.Unlock()
		w.WriteHeader(204)
		return
	}
	if closing {
		v.ClosedAt = time.Now()
	} else {
		v.LastSeen = time.Now()
		v.ClosedAt = time.Time{}
	}
	s.viewers[input.ID] = v
	s.viewerMu.Unlock()
	if !closing && s.manager != nil && v.Login.Target.AccountID > 0 {
		if err := s.manager.RenewDesktopBrowser(v.Login.Target); err != nil {
			http.Error(w, "无法续期桌面暂停状态", 503)
			return
		}
	}
	w.WriteHeader(204)
}

func (s *Server) desktopViewerAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/desktop-assets/"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	host := "https://pc.ctyun.cn/"
	if parts[0] == "cdn" {
		host = "https://deskcdn.ctyun.cn/"
	} else if parts[0] == "fonts" {
		host = "https://at.alicdn.com/"
	} else if parts[0] == "points" {
		host = "https://desk.ctyun.cn/"
	} else if parts[0] != "pc" {
		http.NotFound(w, r)
		return
	}
	relative := path.Clean("/" + parts[1])
	if parts[0] == "points" && !strings.HasPrefix(relative, "/selforder/static/") {
		http.Error(w, "Forbidden", 403)
		return
	}
	if strings.HasPrefix(relative, "/api/") || strings.Contains(parts[1], "..") {
		http.Error(w, "Forbidden", 403)
		return
	}
	ext := strings.ToLower(path.Ext(relative))
	if parts[0] == "fonts" && ext != ".woff" && ext != ".woff2" && ext != ".ttf" {
		http.Error(w, "Forbidden", 403)
		return
	}
	allowed := map[string]bool{".js": true, ".css": true, ".wasm": true, ".json": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".svg": true, ".ico": true, ".woff": true, ".woff2": true, ".ttf": true, ".mp3": true, ".mp4": true}
	if !allowed[ext] {
		http.Error(w, "Forbidden", 403)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, host+strings.TrimPrefix(relative, "/"), nil)
	if err != nil {
		http.Error(w, "Invalid asset", 400)
		return
	}
	req.Header.Set("User-Agent", r.UserAgent())
	req.Header.Set("Referer", "https://pc.ctyun.cn/")
	response, err := s.viewerClient().Do(req)
	if err != nil {
		http.Error(w, "Asset unavailable", 502)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		http.Error(w, "Asset unavailable", 502)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
	if ext == ".js" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}
	if ext == ".wasm" {
		w.Header().Set("Content-Type", "application/wasm")
	}
	if parts[0] == "points" && relative == "/selforder/static/js/desktop-jsbridge.min.js" {
		bridge, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if err != nil || !strings.Contains(string(bridge), "n.top.postMessage") {
			http.Error(w, "官方积分桥接脚本结构变化", 502)
			return
		}
		// The official desktop is the immediate parent, while the management shell is top.
		io.WriteString(w, rewritePointsBridge(string(bridge)))
		return
	}
	if ext == ".css" {
		css, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		if err != nil {
			http.Error(w, "Stylesheet unavailable", 502)
			return
		}
		styles := rewriteDesktopCSS(string(css))
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.WriteHeader(200)
		io.WriteString(w, styles)
		return
	}
	w.WriteHeader(200)
	io.Copy(w, io.LimitReader(response.Body, 64<<20))
}

func rewritePointsBridge(script string) string {
	return strings.ReplaceAll(script, "n.top.postMessage", "n.parent.postMessage")
}

func rewriteDesktopCSS(css string) string {
	return strings.NewReplacer(
		"https://deskcdn.ctyun.cn/", "/desktop-assets/cdn/",
		"https://pc.ctyun.cn/", "/desktop-assets/pc/",
		"http://at.alicdn.com/", "/desktop-assets/fonts/",
		"https://at.alicdn.com/", "/desktop-assets/fonts/",
	).Replace(css)
}

var regionDesktopHost = regexp.MustCompile(`^[a-z0-9-]+-deskmgr\.ctyun\.cn$`)

func allowedDesktopRelayTarget(raw, method string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("Invalid target")
	}
	host := strings.ToLower(u.Hostname())
	if host != "desk.ctyun.cn" && !regionDesktopHost.MatchString(host) {
		return nil, errors.New("Invalid host")
	}
	pointsRequest := strings.HasPrefix(u.Path, "/selforder/api/")
	if port := u.Port(); port != "8810" && port != "8816" && !(pointsRequest && (port == "" || port == "443")) {
		return nil, errors.New("Invalid port")
	}
	if strings.Contains(u.Path, "..") || (!strings.HasPrefix(u.Path, "/api/") && !pointsRequest && u.Path != "/cloudDesktopServer/ctyundesk/operation/banner") {
		return nil, errors.New("Invalid path")
	}
	pointsQueries := map[string]bool{
		"/selforder/api/marketing/userPoints/getUserPoints":           true,
		"/selforder/api/marketing/userPoints/getTaskList":             true,
		"/selforder/api/marketing/userPoints/getPointDetailList":      true,
		"/selforder/api/auth/client/getUserInfo":                      true,
		"/selforder/api/auth/client/syncUserInfo":                     true,
		"/selforder/api/selforder/prod/get":                           true,
		"/selforder/api/desktop-admin/order/mgr/pageTobListOrderInst": true,
	}
	if method == "GET" && pointsQueries[u.Path] {
		return u, nil
	}
	groupQueries := map[string]bool{
		"/api/auth/businessGroup/queryBusinessGroup":   true,
		"/api/auth/businessGroup/queryCombineDataList": true,
		"/api/auth/businessGroup/queryRedDotNum":       true,
	}
	virtualAppQueries := map[string]bool{
		"/api/appvirtual/client/category/list":   true,
		"/api/appvirtual/client/pageList":        true,
		"/api/appvirtual/client/genConnectToken": true,
	}
	if method == "GET" && (strings.HasPrefix(u.Path, "/api/desktop/client/") || strings.HasPrefix(u.Path, "/api/cdserv/client/") || strings.HasPrefix(u.Path, "/api/auth/client/get") || u.Path == "/api/auth/client/listUserProperties" || groupQueries[u.Path] || virtualAppQueries[u.Path]) {
		return u, nil
	}
	// POST does not necessarily mutate a desktop: the official client uses it for list/query APIs.
	posts := map[string]bool{
		"/selforder/api/desktop-admin/order/mgr/listOrderInstStatisticsV2": true,
		"/cloudDesktopServer/ctyundesk/operation/banner":                   true,
		"/api/cdserv/client/msgcenter/page":                                true,
		"/api/cdserv/client/getSoftwareIcon":                               true,
		"/api/auth/businessGroup/checkIfObjsCanJoinGroup":                  true,
		"/api/desktop/client/pageDesktop":                                  true,
		"/api/desktop/client/listDesktopByIds":                             true,
		"/api/cdserv/client/pickRegionConnectUrl":                          true,
		"/api/desktop/client/connect":                                      true,
		"/api/desktop/client/connectMaster":                                true,
		"/api/desktop/client/queryConnectData":                             true,
		"/api/desktop/client/getConnectDataByToken":                        true,
		"/api/desktop/client/quitConnect":                                  true,
		"/api/desktop/client/state":                                        true,
		"/api/desktop/client/lineUp":                                       true,
		"/api/desktop/client/cancelLineUp":                                 true,
		"/api/desktop/client/checkDesktopRebuild":                          true,
		"/api/auth/client/negotiationEncKey":                               true,
	}
	if method == "POST" && posts[u.Path] {
		return u, nil
	}
	return nil, errors.New("Unsupported desktop request")
}

func desktopUpstreamHeader(key string) bool {
	key = strings.ToLower(key)
	if strings.HasPrefix(key, "ctg-") || strings.HasPrefix(key, "yl-") {
		return true
	}
	switch key {
	case "content-type", "x-auth-token", "x-client-trace-id", "x-product-id", "x-random", "from", "x-lang", "x-error", "cache-control", "if-modified-since", "pragma":
		return true
	}
	return false
}
func (s *Server) desktopViewerRelay(w http.ResponseWriter, r *http.Request) {
	viewerHeaders(w)
	if r.Header.Get("Origin") != "null" {
		http.Error(w, "Forbidden origin", 403)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "null")
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Expose-Headers", "X-Desktop-Relay-Error, CTG-Rspdata-Etype, CTG-Captcha-Key, CTG-Sms-Key")
	if r.Method == "OPTIONS" {
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST")
		headers := []string{"Content-Type", "X-Desktop-Session", "X-Desktop-Key"}
		for _, key := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			key = strings.TrimSpace(key)
			if key == "" {
				continue
			}
			if !desktopUpstreamHeader(key) && !strings.EqualFold(key, "X-Desktop-Session") && !strings.EqualFold(key, "X-Desktop-Key") {
				http.Error(w, "Unsupported request header", 403)
				return
			}
			headers = append(headers, key)
		}
		w.Header().Set("Access-Control-Allow-Headers", strings.Join(headers, ", "))
		w.WriteHeader(204)
		return
	}
	v, ok := s.getDesktopViewer(r.Header.Get("X-Desktop-Session"))
	if !ok || subtle.ConstantTimeCompare([]byte(v.RelayKey), []byte(r.Header.Get("X-Desktop-Key"))) != 1 {
		w.Header().Set("X-Desktop-Relay-Error", "invalid-session")
		http.Error(w, "Desktop session expired", 403)
		return
	}
	target, err := allowedDesktopRelayTarget(r.URL.Query().Get("target"), r.Method)
	if err != nil {
		w.Header().Set("X-Desktop-Relay-Error", "unsupported-request")
		http.Error(w, "Forbidden target", 403)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), http.MaxBytesReader(w, r.Body, 4<<20))
	if err != nil {
		http.Error(w, "Invalid request", 400)
		return
	}
	for k, vs := range r.Header {
		if desktopUpstreamHeader(k) {
			for _, value := range vs {
				req.Header.Add(k, value)
			}
		}
	}
	req.Header.Set("Origin", "https://pc.ctyun.cn")
	req.Header.Set("Referer", "https://pc.ctyun.cn/")
	if strings.HasPrefix(target.Path, "/selforder/api/") {
		req.Header.Set("Origin", "https://desk.ctyun.cn")
		req.Header.Set("Referer", "https://desk.ctyun.cn/selforder/points.html")
	}
	req.Header.Set("User-Agent", r.UserAgent())
	response, err := s.viewerClient().Do(req)
	if err != nil {
		http.Error(w, "Desktop request failed", 502)
		return
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 && response.StatusCode < 400 {
		http.Error(w, "Desktop redirect blocked", 502)
		return
	}
	for _, key := range []string{"Content-Type", "ctg-rspdata-etype", "ctg-captcha-key", "ctg-sms-key"} {
		if value := response.Header.Get(key); value != "" {
			w.Header().Set(key, value)
		}
	}
	w.WriteHeader(response.StatusCode)
	io.Copy(w, io.LimitReader(response.Body, 8<<20))
}
