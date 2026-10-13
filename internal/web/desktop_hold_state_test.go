package web

import (
	"encoding/json"
	"github.com/vay1314/CtYun-Keeper/internal/security"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDesktopHoldStateUsesLocalStateAndRequiresLogin(t *testing.T) {
	s, _, _ := viewerFixture(t)
	id, err := s.store.SaveAccount(storage.Account{Name: "test", Username: "test", DeviceCode: "test"}, "password", []byte(strings.Repeat("k", 32)), security.EncryptFernet)
	if err != nil {
		t.Fatal(err)
	}
	s.store.SetDesktopHold(storage.PowerTarget{AccountID: id, DesktopID: "web"}, time.Now().Add(time.Minute).Unix(), "web hold")
	s.store.SetDesktopHold(storage.PowerTarget{AccountID: id, DesktopID: "manual"}, 0, "manual hold")
	s.store.SetDesktopHold(storage.PowerTarget{AccountID: id, DesktopID: "expired"}, time.Now().Add(-time.Minute).Unix(), "old hold")
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		s.desktopsAPI(w, httptest.NewRequest("GET", "/api/desktops/holds", nil))
		return w
	}
	w := request()
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var rows []struct {
		ID   string `json:"desktop_id"`
		Hold string `json:"hold"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		if row.ID == "expired" {
			t.Fatal("expired pause visible")
		}
	}
	s.store.ClearDesktopHold(storage.PowerTarget{AccountID: id, DesktopID: "web"})
	w = request()
	if strings.Contains(w.Body.String(), "web hold") {
		t.Fatal("released pause still visible")
	}
	s.store.SetSetting("admin_auth_enabled", "true")
	if w = request(); w.Code != 401 {
		t.Fatal("anonymous hold state accepted", w.Code)
	}
}
