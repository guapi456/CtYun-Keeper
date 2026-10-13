package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/ctyun"
	"github.com/vay1314/CtYun-Keeper/internal/service"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
)

type viewerRoundTrip func(*http.Request) (*http.Response, error)

func (f viewerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func viewerFixture(t *testing.T) (*Server, string, desktopViewerSession) {
	t.Helper()
	store, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	store.SetSetting("admin_password_hash", "configured")
	store.SetSetting("admin_auth_enabled", "false")
	s := &Server{store: store, viewers: map[string]desktopViewerSession{}}
	entry, err := s.createDesktopViewer(service.DesktopBrowserLogin{URL: "https://pc.ctyun.cn/#/desktop?id=MTIz", Profile: ctyun.Profile{UserID: 1, TenantID: 2, UserName: "</script><script>bad</script>", SecretKey: "platform-test-key", BondedDevice: true}, DeviceCode: "test-device", UserAccount: "test-account"})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimPrefix(entry, "/desktop-view/")
	v, _ := s.getDesktopViewer(id)
	return s, entry, v
}
func TestViewerEntryRequiresManagementLoginAndUsesOpaqueSandbox(t *testing.T) {
	s, entry, v := viewerFixture(t)
	if strings.Contains(entry, v.RelayKey) || strings.Contains(entry, v.Login.Profile.SecretKey) {
		t.Fatal("credential in entry URL")
	}
	s.viewerHTTP = &http.Client{Transport: viewerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://pc.ctyun.cn/" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<html><head><script src="js/main.js"></script></head><body></body></html>`))}, nil
	})}
	s.store.SetSetting("admin_auth_enabled", "true")
	r := httptest.NewRequest("GET", "http://localhost"+entry, nil)
	w := httptest.NewRecorder()
	s.desktopViewerPage(w, r)
	if w.Code != 401 {
		t.Fatal("anonymous viewer entry accepted")
	}
	s.store.SetSetting("admin_auth_enabled", "false")
	r = httptest.NewRequest("GET", "http://192.168.2.251:9845"+entry, nil)
	w = httptest.NewRecorder()
	s.desktopViewerPage(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `sandbox="allow-scripts`) || strings.Contains(w.Body.String(), "allow-same-origin") || strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(w.Code, w.Header())
	}
	for _, source := range []string{"wss://*.ctyun.cn:9011", "https://*.ctyun.cn:9011", "https://*.ctyun.cn:8999", "manifest-src", "frame-src 'self' about: https://desk.ctyun.cn/selforder/points.html;"} {
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), source) {
			t.Fatal("official connection/resource port blocked", source)
		}
	}
	if !strings.Contains(w.Body.String(), "platform-test-key") || strings.Contains(w.Body.String(), "</script><script>bad</script>") || !strings.Contains(w.Body.String(), "/desktop-assets/pc/js/main.js") {
		t.Fatal("invalid login bootstrap/asset rewriting")
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("private viewer cached or referrer enabled")
	}
	s.viewerMu.Lock()
	old := s.viewers[v.ID]
	old.Expires = time.Now().Add(-time.Second)
	s.viewers[v.ID] = old
	s.viewerMu.Unlock()
	w = httptest.NewRecorder()
	s.desktopViewerPage(w, r)
	if w.Code != 410 {
		t.Fatal("expired session accepted")
	}
}
func TestViewerRelayRejectsUnsafeTargetsAndDoesNotForwardManagementCredentials(t *testing.T) {
	s, _, v := viewerFixture(t)
	calls := 0
	s.viewerHTTP = &http.Client{Transport: viewerRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Cookie") != "" || r.Header.Get("X-Desktop-Key") != "" || r.Header.Get("Authorization") != "" {
			t.Fatal("management credential forwarded")
		}
		if r.Header.Get("CTG-Signaturestr") != "platform-signature" {
			t.Fatal("platform signature lost")
		}
		if r.Header.Get("YL-Signature") != "banner-signature" {
			t.Fatal("banner signature lost")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0}`))}, nil
	})}
	request := func(target, origin, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/desktop-relay?target="+url.QueryEscape(target), nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Desktop-Session", v.ID)
		r.Header.Set("X-Desktop-Key", key)
		r.Header.Set("Cookie", "ctyun_session=management")
		r.Header.Set("Authorization", "management-token")
		r.Header.Set("CTG-Signaturestr", "platform-signature")
		r.Header.Set("YL-Signature", "banner-signature")
		w := httptest.NewRecorder()
		s.desktopViewerRelay(w, r)
		return w
	}
	valid := "https://desk.ctyun.cn:8810/api/desktop/client/status?desktopId=123"
	if w := request(valid, "https://evil.test", v.RelayKey); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request(valid, "null", "wrong"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	for _, target := range []string{"http://desk.ctyun.cn:8810/api/desktop/client/status", "https://127.0.0.1:8810/api/desktop/client/status", "https://evil.ctyun.cn:8810/api/desktop/client/status", "https://desk.ctyun.cn:443/api/desktop/client/status", "https://desk.ctyun.cn:8810/private", "https://user:pass@desk.ctyun.cn:8810/api/desktop/client/status"} {
		if w := request(target, "null", v.RelayKey); w.Code != 403 {
			t.Fatal(target, w.Code)
		}
	}
	if calls != 0 {
		t.Fatal("invalid target requested")
	}
	if w := request(valid, "null", v.RelayKey); w.Code != 200 || calls != 1 || w.Header().Get("Access-Control-Allow-Origin") != "null" {
		t.Fatal(w.Code, calls)
	}
	if _, err := allowedDesktopRelayTarget("https://desk.ctyun.cn:8810/api/desktop/client/operate", "POST"); err == nil {
		t.Fatal("destructive power proxy allowed")
	}
	if _, err := allowedDesktopRelayTarget("https://desk.ctyun.cn:8810/api/auth/client/login", "POST"); err == nil {
		t.Fatal("password login proxy allowed")
	}
}

func TestViewerRelayAllowsOfficialPOSTQueries(t *testing.T) {
	s, _, v := viewerFixture(t)
	calls := 0
	s.viewerHTTP = &http.Client{Transport: viewerRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/desktop/client/pageDesktop" {
			t.Fatal(r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"desktopList":[]}}`))}, nil
	})}
	r := httptest.NewRequest("POST", "/desktop-relay?target="+url.QueryEscape("https://desk.ctyun.cn:8810/api/desktop/client/pageDesktop"), strings.NewReader(`{"getCnt":50}`))
	r.Header.Set("Origin", "null")
	r.Header.Set("X-Desktop-Session", v.ID)
	r.Header.Set("X-Desktop-Key", v.RelayKey)
	w := httptest.NewRecorder()
	s.desktopViewerRelay(w, r)
	if w.Code != 200 || calls != 1 || w.Header().Get("X-Desktop-Relay-Error") != "" {
		t.Fatal(w.Code, calls, w.Header())
	}
	for _, path := range []string{"listDesktopByIds", "queryConnectData", "getConnectDataByToken", "quitConnect", "cancelLineUp", "checkDesktopRebuild"} {
		if _, err := allowedDesktopRelayTarget("https://desk.ctyun.cn:8810/api/desktop/client/"+path, "POST"); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{"operate", "deleteDesktop", "rebuildDesktop"} {
		if _, err := allowedDesktopRelayTarget("https://desk.ctyun.cn:8810/api/desktop/client/"+path, "POST"); err == nil {
			t.Fatal("unapproved mutation allowed", path)
		}
	}
}

func TestViewerAllowsReadOnlyUserProperties(t *testing.T) {
	endpoint := "https://desk.ctyun.cn:8810/api/auth/client/listUserProperties"
	if _, err := allowedDesktopRelayTarget(endpoint, "GET"); err != nil {
		t.Fatal(err)
	}
	if _, err := allowedDesktopRelayTarget(endpoint, "POST"); err == nil {
		t.Fatal("unsupported user properties mutation allowed")
	}
	for _, endpoint := range []string{"saveConfSetting", "resetPassword", "login", "bindPhone/confirm"} {
		if _, err := allowedDesktopRelayTarget("https://desk.ctyun.cn:8810/api/auth/client/"+endpoint, "POST"); err == nil {
			t.Fatal("unapproved account operation allowed", endpoint)
		}
	}
}

func TestViewerAllowsIconLookupButNotShortcutChanges(t *testing.T) {
	base := "https://desk.ctyun.cn:8810/api/cdserv/client/"
	if _, err := allowedDesktopRelayTarget(base+"getSoftwareIcon", "POST"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"addDesktopShortcuts", "deleteSoftwareShortcut"} {
		if _, err := allowedDesktopRelayTarget(base+path, "POST"); err == nil {
			t.Fatal("shortcut mutation accepted", path)
		}
	}
}

func TestViewerAllowsMessageQueryWithoutSavingThirdPartyData(t *testing.T) {
	base := "https://desk.ctyun.cn:8810/api/cdserv/client/msgcenter/"
	if _, err := allowedDesktopRelayTarget(base+"page", "POST"); err != nil {
		t.Fatal(err)
	}
	if _, err := allowedDesktopRelayTarget(base+"thirdPartyData/saveTemporary", "POST"); err == nil {
		t.Fatal("unapproved third party data write accepted")
	}
}

func TestViewerAllowsOnlyVerifiedBannerRequest(t *testing.T) {
	base := "https://desk.ctyun.cn:8810/cloudDesktopServer/ctyundesk/operation/"
	if _, err := allowedDesktopRelayTarget(base+"banner", "POST"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"placeOrder", "banner/other", "../banner"} {
		if _, err := allowedDesktopRelayTarget(base+path, "POST"); err == nil {
			t.Fatal("unverified operation allowed", path)
		}
	}
}

func TestViewerAllowsVerifiedVirtualAppInitialization(t *testing.T) {
	base := "https://desk.ctyun.cn:8810/api/appvirtual/client/"
	for _, path := range []string{"category/list", "pageList", "genConnectToken"} {
		if _, err := allowedDesktopRelayTarget(base+path, "GET"); err != nil {
			t.Fatal(path, err)
		}
		if _, err := allowedDesktopRelayTarget(base+path, "POST"); err == nil {
			t.Fatal("unverified method allowed", path)
		}
	}
	if _, err := allowedDesktopRelayTarget(base+"other", "GET"); err == nil {
		t.Fatal("unverified virtual app API allowed")
	}
}

func TestViewerAllowsOnlyVerifiedBusinessGroupQueries(t *testing.T) {
	base := "https://desk.ctyun.cn:8810/api/auth/businessGroup/"
	if _, err := allowedDesktopRelayTarget(base+"checkIfObjsCanJoinGroup", "POST"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"queryBusinessGroup", "queryCombineDataList", "queryRedDotNum"} {
		if _, err := allowedDesktopRelayTarget(base+path, "GET"); err != nil {
			t.Fatal(path, err)
		}
		if _, err := allowedDesktopRelayTarget(base+path, "POST"); err == nil {
			t.Fatal("wrong method accepted", path)
		}
	}
	for _, path := range []string{"joinGroup", "invite", "deleteGroup", "saveBusinessGroup"} {
		if _, err := allowedDesktopRelayTarget(base+path, "POST"); err == nil {
			t.Fatal("group mutation accepted", path)
		}
	}
}
func TestViewerStaticAssetCannotProxyAPIs(t *testing.T) {
	s, _, _ := viewerFixture(t)
	for _, path := range []string{"/desktop-assets/pc/api/desktop/client/status.js", "/desktop-assets/evil/file.js", "/desktop-assets/pc/../../api/file.js", "/desktop-assets/pc/index.html"} {
		r := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		s.desktopViewerAsset(w, r)
		if w.Code == 200 {
			t.Fatal(path)
		}
	}
}

func TestDesktopHTMLPreservesDynamicLoaderJavaScript(t *testing.T) {
	loader := `var cdnBase="https://deskcdn.ctyun.cn/pccdnstatic/";var base=useLocal ? localBase() : cdnBase;document.write('<script defer src="' + join(base, asset) + '"><\/script>');document.write('<link href="' + join(base, asset) + '" rel="stylesheet">');`
	html := rewriteDesktopHTML(`<html><head><script>` + loader + `</script><script src="static/main.js"></script></head><body><script>if(navigator.serviceWorker){navigator.serviceWorker.register('./sw.js');}</script></body></html>`)
	if !strings.Contains(html, `document.write('<script defer src="' + join(base, asset) + '"><\/script>');`) || !strings.Contains(html, `document.write('<link href="' + join(base, asset) + '" rel="stylesheet">');`) {
		t.Fatal("dynamic loader strings corrupted")
	}
	if strings.Contains(html, "https://deskcdn.ctyun.cn/") || strings.Contains(html, "navigator.serviceWorker") || !strings.Contains(html, `/desktop-assets/cdn/pccdnstatic/`) || !strings.Contains(html, `/desktop-assets/pc/static/main.js`) {
		t.Fatal("desktop assets not isolated correctly")
	}
}

func TestDesktopCSSProxiesKnownFonts(t *testing.T) {
	css := rewriteDesktopCSS(`@font-face{src:url("https://deskcdn.ctyun.cn/pccdnstatic/css/icon.woff2"),url("http://at.alicdn.com/t/c/font.woff?t=1")}`)
	if strings.Contains(css, "ctyun.cn") || strings.Contains(css, "alicdn.com") || !strings.Contains(css, "/desktop-assets/cdn/pccdnstatic/css/icon.woff2") || !strings.Contains(css, "/desktop-assets/fonts/t/c/font.woff?t=1") {
		t.Fatal(css)
	}
}

func TestViewerEntryKeyWorksWithoutManagementCookieOnlyOnce(t *testing.T) {
	s, entry, v := viewerFixture(t)
	s.store.SetSetting("admin_auth_enabled", "true")
	s.viewerHTTP = &http.Client{Transport: viewerRoundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<html><head></head><body></body></html>`))}, nil
	})}
	post := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost"+entry, strings.NewReader(url.Values{"entry_key": {key}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		s.desktopViewerPage(w, r)
		return w
	}
	if w := post("wrong"); w.Code != 403 {
		t.Fatal("wrong key accepted", w.Code)
	}
	if w := post(v.EntryKey); w.Code != 200 || strings.Contains(w.Body.String(), v.EntryKey) {
		t.Fatal("entry handoff failed or key disclosed", w.Code)
	}
	if w := post(v.EntryKey); w.Code != 403 {
		t.Fatal("entry key replay accepted", w.Code)
	}
}

func TestViewerAllowsLANHTTPAndClosesWithPrivateKey(t *testing.T) {
	s, _, v := viewerFixture(t)
	for _, address := range []string{"192.168.2.251:9845", "10.0.0.2:9845", "172.16.0.2:9845", "127.0.0.1:9845", "[fd00::1]:9845", "[fe80::1]:9845"} {
		if !s.secureDesktopViewerRequest(httptest.NewRequest("GET", "http://"+address+"/desktop-view/test", nil)) {
			t.Fatal("LAN or loopback rejected", address)
		}
	}
	for _, address := range []string{"8.8.8.8:9845", "172.32.0.2:9845", "[2001:4860:4860::8888]:9845"} {
		if s.secureDesktopViewerRequest(httptest.NewRequest("GET", "http://"+address+"/desktop-view/test", nil)) {
			t.Fatal("public HTTP accepted", address)
		}
	}
	r := httptest.NewRequest("GET", "http://example.com/desktop-view/test", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if s.secureDesktopViewerRequest(r) {
		t.Fatal("unconfigured forwarded HTTPS accepted")
	}
	s.secure = true
	if !s.secureDesktopViewerRequest(r) {
		t.Fatal("configured HTTPS rejected")
	}
	closeRequest := func(key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/desktop-session/close", strings.NewReader(`{"id":"`+v.ID+`","key":"`+key+`","instance":"`+v.Instance+`"}`))
		r.Header.Set("Origin", "null")
		w := httptest.NewRecorder()
		s.closeDesktopViewer(w, r)
		return w
	}
	if w := closeRequest("wrong"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := closeRequest(v.RelayKey); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if current, ok := s.getDesktopViewer(v.ID); !ok || current.ClosedAt.IsZero() {
		t.Fatal("close grace missing")
	}
	s.collectViewerResumes(time.Now().Add(viewerCloseGrace + time.Second))
	if _, ok := s.getDesktopViewer(v.ID); ok {
		t.Fatal("closed viewer retained after grace")
	}
}
