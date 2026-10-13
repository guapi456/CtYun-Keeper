package web

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vay1314/CtYun-Keeper/internal/security"
	"github.com/vay1314/CtYun-Keeper/internal/service"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
)

func TestDesktopPowerRequiresAuthenticationAndCSRF(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := service.New(store, []byte(strings.Repeat("k", 32)), dir, "")
	defer manager.Close()
	s := &Server{store: store, manager: manager, sessionKey: []byte("session")}
	r := httptest.NewRequest("POST", "/api/desktops/power", strings.NewReader(`{"action":"reboot","targets":[]}`))
	w := httptest.NewRecorder()
	s.desktopsAPI(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	store.SetSetting("admin_password_hash", "configured")
	store.SetSetting("admin_auth_enabled", "false")
	r = httptest.NewRequest("POST", "/api/desktops/power", strings.NewReader(`{"action":"reboot","targets":[]}`))
	w = httptest.NewRecorder()
	s.desktopsAPI(w, r)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
	r = httptest.NewRequest("POST", "/api/desktops/power", strings.NewReader(`{"action":"invalid","targets":[{"account_id":1,"desktop_id":"1"}]}`))
	r.AddCookie(&http.Cookie{Name: "ctyun_csrf", Value: security.SignCookie(s.sessionKey, "token")})
	r.Header.Set("X-CSRF-Token", "token")
	w = httptest.NewRecorder()
	s.desktopsAPI(w, r)
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
}
