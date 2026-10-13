package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPointsPageProxiesModulesAndKeepsIsolation(t *testing.T) {
	s, _, _ := viewerFixture(t)
	s.viewerHTTP = &http.Client{Transport: viewerRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://desk.ctyun.cn/selforder/points.html" || r.Header.Get("Cookie") != "" {
			t.Fatal(r.URL, r.Header)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`<html><head><script type="module" crossorigin src="./static/js/points.js"></script><link href="./static/css/points.css" rel="stylesheet"><script>import.meta.url;</script></head><body></body></html>`))}, nil
	})}
	w := httptest.NewRecorder()
	s.securityHeaders(http.HandlerFunc(s.desktopPointsPage)).ServeHTTP(w, httptest.NewRequest("GET", "http://localhost/desktop-points?deviceType=60", nil))
	if w.Code != 200 || w.Header().Get("X-Frame-Options") != "" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox allow-scripts") || strings.Contains(w.Header().Get("Content-Security-Policy"), "allow-same-origin") {
		t.Fatal(w.Code, w.Header())
	}
	for _, value := range []string{"/desktop-assets/points/selforder/static/js/points.js", "/desktop-assets/points/selforder/static/css/points.css", "/static/desktop-points.js", "import.meta.url;"} {
		if !strings.Contains(w.Body.String(), value) {
			t.Fatal(value, w.Body.String())
		}
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("points page cached")
	}
}

func TestPointsStaticAssetsHaveCORSWithoutAPIProxy(t *testing.T) {
	s, _, _ := viewerFixture(t)
	calls := 0
	s.viewerHTTP = &http.Client{Transport: viewerRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://desk.ctyun.cn/selforder/static/js/points.js" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("export const value=1;"))}, nil
	})}
	w := httptest.NewRecorder()
	s.desktopViewerAsset(w, httptest.NewRequest("GET", "/desktop-assets/points/selforder/static/js/points.js", nil))
	if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal(w.Code, w.Header())
	}
	for _, path := range []string{"/desktop-assets/points/selforder/api/marketing/list.js", "/desktop-assets/points/elsewhere/points.js"} {
		w = httptest.NewRecorder()
		s.desktopViewerAsset(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 403 {
			t.Fatal(path, w.Code)
		}
	}
	if calls != 1 {
		t.Fatal("invalid asset requested upstream", calls)
	}
}

func TestPointsRelayAllowsOnlyVerifiedQueries(t *testing.T) {
	base := "https://desk.ctyun.cn/selforder/api/"
	if _, err := allowedDesktopRelayTarget(base+"desktop-admin/order/mgr/listOrderInstStatisticsV2", "POST"); err != nil {
		t.Fatal("official instance statistics query rejected", err)
	}
	for _, path := range []string{"marketing/userPoints/getUserPoints", "marketing/userPoints/getTaskList", "marketing/userPoints/getPointDetailList", "auth/client/getUserInfo"} {
		if _, err := allowedDesktopRelayTarget(base+path, "GET"); err != nil {
			t.Fatal(path, err)
		}
	}
	for _, path := range []string{"selforder/paas/placeOrder", "auth/client/resetPassword", "marketing/unknown"} {
		for _, method := range []string{"GET", "POST"} {
			if _, err := allowedDesktopRelayTarget(base+path, method); err == nil {
				t.Fatal("unapproved points operation", path, method)
			}
		}
	}
}

func TestPointsBridgeTargetsDesktopParent(t *testing.T) {
	got := rewritePointsBridge(`n.top.postMessage({eventType:r,fid:e,params:o},"*");var nested=n.top!==n;`)
	if !strings.Contains(got, `n.parent.postMessage({eventType:r,fid:e,params:o},"*");`) || !strings.Contains(got, "nested=n.top!==n") {
		t.Fatal(got)
	}
	if !strings.Contains(rewritePointsHTML(`<script src="./static/js/desktop-jsbridge.min.js?v=2.8"></script>`), "desktop-jsbridge.min.js?v=ui61") {
		t.Fatal("modified bridge can be hidden by cached vendor asset")
	}
}

func TestPointsPreflightMatchesForwardedHeaders(t *testing.T) {
	s, _, _ := viewerFixture(t)
	requested := "from, x-lang, cache-control, pragma, if-modified-since, ctg-device-manu, ctg-authenticate, ctg-signaturestr, x-desktop-session, x-desktop-key"
	r := httptest.NewRequest("OPTIONS", "/desktop-relay", nil)
	r.Header.Set("Origin", "null")
	r.Header.Set("Access-Control-Request-Headers", requested)
	w := httptest.NewRecorder()
	s.desktopViewerRelay(w, r)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	allowed := strings.ToLower(w.Header().Get("Access-Control-Allow-Headers"))
	for _, key := range strings.Split(requested, ", ") {
		if !strings.Contains(allowed, key) {
			t.Fatal("header missing", key)
		}
	}
	for _, key := range []string{"Cookie", "Authorization", "X-Desktop-Key"} {
		if desktopUpstreamHeader(key) {
			t.Fatal("management credential forwarded", key)
		}
	}
	r.Header.Set("Access-Control-Request-Headers", "authorization")
	w = httptest.NewRecorder()
	s.desktopViewerRelay(w, r)
	if w.Code != 403 {
		t.Fatal("unapproved header accepted", w.Code)
	}
}
