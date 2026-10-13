package web

import (
	"github.com/vay1314/CtYun-Keeper/internal/storage"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestViewerLifecycleWaitsForLastWindowAndHandlesMissingClose(t *testing.T) {
	s, _, v := viewerFixture(t)
	now := time.Now()
	target := storage.PowerTarget{AccountID: 1, DesktopID: "desktop"}
	v.Login.Target = target
	v.LastSeen = now
	v.ClosedAt = now.Add(-20 * time.Second)
	s.viewers[v.ID] = v
	other := v
	other.ID = "other"
	other.ClosedAt = time.Time{}
	s.viewers[other.ID] = other
	if targets := s.collectViewerResumes(now); len(targets) != 0 {
		t.Fatal("another window still active", targets)
	}
	other.LastSeen = now.Add(-viewerHeartbeatGrace - time.Second)
	s.viewers[other.ID] = other
	if targets := s.collectViewerResumes(now); len(targets) != 1 || targets[0] != target {
		t.Fatal("missing close did not schedule resume", targets)
	}
}

func TestViewerRefreshRejectsOldWindowCloseAndLateHeartbeat(t *testing.T) {
	s, _, v := viewerFixture(t)
	pulse := func(instance string, closing bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://localhost/desktop-session/heartbeat", strings.NewReader(`{"id":"`+v.ID+`","key":"`+v.RelayKey+`","instance":"`+instance+`"}`))
		r.Header.Set("Origin", "http://localhost")
		w := httptest.NewRecorder()
		s.desktopViewerPulse(w, r, closing)
		return w
	}
	current := v
	current.Instance = "new-page"
	s.viewers[v.ID] = current
	if w := pulse(v.Instance, true); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if current, _ := s.getDesktopViewer(v.ID); !current.ClosedAt.IsZero() {
		t.Fatal("refresh affected by old window close")
	}
	if w := pulse("new-page", true); w.Code != 204 {
		t.Fatal(w.Code)
	}
	closed, _ := s.getDesktopViewer(v.ID)
	pulse("new-page", false)
	current, _ = s.getDesktopViewer(v.ID)
	if current.ClosedAt != closed.ClosedAt {
		t.Fatal("late heartbeat cancelled actual close")
	}
	if targets := s.collectViewerResumes(time.Now()); len(targets) != 0 {
		t.Fatal("close grace omitted")
	}
}
