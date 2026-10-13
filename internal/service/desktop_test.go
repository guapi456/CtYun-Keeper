package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/ctyun"
	"github.com/vay1314/CtYun-Keeper/internal/security"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
)

type desktopTransport func(*http.Request) (*http.Response, error)

func (f desktopTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func desktopManagerFixture(t *testing.T) (*Manager, int64) {
	t.Helper()
	dir := t.TempDir()
	s, err := storage.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	key := []byte(strings.Repeat("k", 32))
	m := New(s, key, dir, "")
	t.Cleanup(func() { m.Close(); s.Close() })
	id, err := s.SaveAccount(storage.Account{Name: "test", Username: "user", DeviceCode: "device", Enabled: true, KeepaliveMode: storage.KeepaliveOff}, "password", key, security.EncryptFernet)
	if err != nil {
		t.Fatal(err)
	}
	m.saveProfile(id, ctyun.Profile{UserID: 1, TenantID: 2, SecretKey: "secret", BondedDevice: true})
	return m, id
}
func TestPowerBatchChecksMembershipAndDoesNotRetryTimeout(t *testing.T) {
	m, id := desktopManagerFixture(t)
	var submitted atomic.Int32
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = desktopTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/api/desktop/client/operate" {
			submitted.Add(1)
			r.ParseForm()
			if r.Form.Get("desktopId") != "123" {
				t.Error("foreign desktop operated")
			}
			return nil, errors.New("simulated transport timeout")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"desktopList":[{"desktopId":"123","desktopName":"Test 4C8G"}]}}`))}, nil
	})
	jobID, err := m.StartPowerBatch("shutdown", []storage.PowerTarget{{AccountID: id, DesktopID: "123"}, {AccountID: id, DesktopID: "123"}, {AccountID: id, DesktopID: "foreign"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.StartPowerBatch("reboot", []storage.PowerTarget{{AccountID: id, DesktopID: "123"}}); err == nil {
		t.Fatal("overlapping batch accepted")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := m.store.PowerJob(jobID)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == "finished" {
			if len(j.Items) != 2 || j.Items[0].Status != "unknown" || j.Items[1].Status != "failed" || submitted.Load() != 1 {
				t.Fatal(j, submitted.Load())
			}
			if !m.desktopHeld(id, "123") {
				t.Fatal("uncertain shutdown lost hold")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("batch did not finish")
}
func TestPowerBatchRejectsInvalidInputsAndUpdateConflict(t *testing.T) {
	m, id := desktopManagerFixture(t)
	for _, input := range []struct {
		action string
		ts     []storage.PowerTarget
	}{{"invalid", []storage.PowerTarget{{AccountID: id, DesktopID: "1"}}}, {"reboot", nil}, {"reboot", []storage.PowerTarget{{AccountID: id, DesktopID: ""}}}} {
		if _, err := m.StartPowerBatch(input.action, input.ts); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	m.mu.Lock()
	m.powerActive = 99
	m.mu.Unlock()
	if err := m.PrepareForUpdate(context.Background()); err == nil {
		t.Fatal("update accepted during power batch")
	}
	m.mu.Lock()
	m.powerActive = 0
	m.mu.Unlock()
}
func TestPowerPlatformFailurePreservesPreviousHold(t *testing.T) {
	m, id := desktopManagerFixture(t)
	target := storage.PowerTarget{AccountID: id, DesktopID: "123"}
	m.store.SetDesktopHold(target, 0, "existing pause")
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = desktopTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":{"desktopList":[{"desktopId":"123"}]}}`
		if r.URL.Path == "/api/desktop/client/operate" {
			body = `{"code":50001,"msg":"rejected"}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	jid, err := m.StartPowerBatch("poweron", []storage.PowerTarget{target})
	if err != nil {
		t.Fatal(err)
	}
	for start := time.Now(); time.Since(start) < 5*time.Second; {
		j, _ := m.store.PowerJob(jid)
		if j.Status == "finished" {
			hold, _ := m.store.DesktopHold(target)
			if hold != "existing pause" || j.Items[0].Status != "failed" {
				t.Fatal(hold, j)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("batch did not finish")
}

func TestBrowserDesktopChecksOwnershipAndReturnsOnlyOfficialURL(t *testing.T) {
	m, id := desktopManagerFixture(t)
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = desktopTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"desktopList":[{"desktopId":"123","objId":"123"}]}}`))}, nil
	})
	if _, err := m.DesktopBrowser(context.Background(), storage.PowerTarget{AccountID: id, DesktopID: "foreign"}); err == nil {
		t.Fatal("foreign desktop opened")
	}
	target := storage.PowerTarget{AccountID: id, DesktopID: "123"}
	url, err := m.DesktopBrowser(context.Background(), target)
	if err != nil || !strings.HasPrefix(url, "https://pc.ctyun.cn/#/desktop?id=") || strings.Contains(url, "secret") || strings.Contains(url, "password") {
		t.Fatal(url, err)
	}
	if !m.desktopHeld(id, "123") {
		t.Fatal("browser did not pause keepalive")
	}
	if err = m.ResumeDesktop(context.Background(), target); err != nil || m.desktopHeld(id, "123") {
		t.Fatal("resume failed", err)
	}
}

func TestAutomaticBrowserResumePreservesManualPowerHold(t *testing.T) {
	m, id := desktopManagerFixture(t)
	target := storage.PowerTarget{AccountID: id, DesktopID: "123"}
	if err := m.store.SetDesktopHold(target, time.Now().Add(time.Minute).Unix(), desktopBrowserHoldReason); err != nil {
		t.Fatal(err)
	}
	if err := m.RenewDesktopBrowser(target); err != nil {
		t.Fatal(err)
	}
	until, _, _ := m.store.DesktopHoldSnapshot(target)
	if until < time.Now().Add(140*time.Second).Unix() {
		t.Fatal("browser lease not renewed")
	}
	m.mu.Lock()
	m.powerActive = 1
	m.mu.Unlock()
	if err := m.ResumeDesktopBrowser(target); err == nil {
		t.Fatal("resume during power operation")
	}
	m.mu.Lock()
	m.powerActive = 0
	m.mu.Unlock()
	if err := m.ResumeDesktopBrowser(target); err != nil {
		t.Fatal(err)
	}
	if hold, _ := m.store.DesktopHold(target); hold != "" {
		t.Fatal("browser hold retained", hold)
	}
	m.store.SetDesktopHold(target, 0, "手动关机，自动连接已暂停")
	m.RenewDesktopBrowser(target)
	if err := m.ResumeDesktopBrowser(target); err != nil {
		t.Fatal(err)
	}
	until, reason, _ := m.store.DesktopHoldSnapshot(target)
	if until != 0 || reason != "手动关机，自动连接已暂停" {
		t.Fatal("manual shutdown changed", until, reason)
	}
}

func TestDesktopDetailsChecksOwnershipAndExposesAllowlistedFields(t *testing.T) {
	m, id := desktopManagerFixture(t)
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	var calls atomic.Int32
	http.DefaultTransport = desktopTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":{"desktopList":[{"desktopId":"123","desktopCode":"D123","desktopName":"test"}]}}`
		if r.URL.Path == "/api/desktop/client/feature" {
			calls.Add(1)
			body = `{"code":0,"data":{"cpuCore":8,"memoryGB":16,"mirrorType":10,"secretKey":"sensitive-field","expireDate":null}}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	if _, err := m.DesktopDetails(context.Background(), storage.PowerTarget{AccountID: id, DesktopID: "foreign"}); err == nil || calls.Load() != 0 {
		t.Fatal("unowned configuration queried")
	}
	details, err := m.DesktopDetails(context.Background(), storage.PowerTarget{AccountID: id, DesktopID: "123"})
	if err != nil || details.Code != "D123" || details.Specification != "8 核 / 16 GB" || details.MirrorSource != "公共镜像" {
		t.Fatal(details, err)
	}
	raw, err := json.Marshal(details)
	if err != nil || strings.Contains(string(raw), "sensitive-field") || strings.Contains(string(raw), "secretKey") {
		t.Fatal("unexpected sensitive field", err)
	}
}

func TestBrowserLoginDoesNotSkipAdditionalVerification(t *testing.T) {
	m, id := desktopManagerFixture(t)
	m.saveProfile(id, ctyun.Profile{UserID: 1, SecretKey: "secret", BondedDevice: true, NeedSmsValidate: true})
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	http.DefaultTransport = desktopTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"desktopList":[{"desktopId":"123"}]}}`))}, nil
	})
	if _, err := m.DesktopBrowserLogin(context.Background(), storage.PowerTarget{AccountID: id, DesktopID: "123"}); err == nil {
		t.Fatal("additional verification bypassed")
	}
	if m.desktopHeld(id, "123") {
		t.Fatal("rejected login paused desktop")
	}
}
