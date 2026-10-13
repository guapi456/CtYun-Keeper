package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/ctyun"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
)

type ManagedDesktop struct {
	AccountID         int64  `json:"account_id"`
	AccountName       string `json:"account_name"`
	ID                string `json:"id"`
	Name              string `json:"name"`
	Status            string `json:"status"`
	Specification     string `json:"specification"`
	Flavor            string `json:"flavor"`
	Image             string `json:"image"`
	Hold              string `json:"hold"`
	Forbidden         bool   `json:"forbidden"`
	ConfigurationRead bool   `json:"configuration_read"`
}

func (m *Manager) desktopClient(ctx context.Context, id int64) (*ctyun.Client, []ctyun.Desktop, string, error) {
	select {
	case m.desktopReadSlots <- struct{}{}:
		defer func() { <-m.desktopReadSlots }()
	case <-ctx.Done():
		return nil, nil, "", ctx.Err()
	}
	a, err := m.store.Account(id)
	if err != nil {
		return nil, nil, "", err
	}
	c, err := m.newClient(a)
	if err != nil {
		return nil, nil, "", err
	}
	values, warning, err := c.ManagementDesktops(ctx)
	if ctyun.IsLoginExpired(err) {
		c.Profile = nil
		m.store.ClearAuthCache(id)
		values, warning, err = c.ManagementDesktops(ctx)
	}
	if err == nil && c.Profile != nil {
		m.saveProfile(id, *c.Profile)
	}
	return c, values, warning, err
}
func (m *Manager) ManagedDesktops(ctx context.Context, id int64) ([]ManagedDesktop, string, error) {
	a, err := m.store.Account(id)
	if err != nil {
		return nil, "", err
	}
	_, values, warning, err := m.desktopClient(ctx, id)
	if err != nil {
		return nil, "", err
	}
	result := make([]ManagedDesktop, 0, len(values))
	for _, d := range values {
		hold, e := m.store.DesktopHold(storage.PowerTarget{AccountID: id, DesktopID: d.ID()})
		if e != nil {
			return nil, "", e
		}
		result = append(result, ManagedDesktop{AccountID: id, AccountName: a.Name, ID: d.ID(), Name: d.Name(), Status: d.StatusText(), Specification: d.Specification(), Flavor: strings.TrimSpace(d.FlavorName + " " + d.ProductGroup), Image: d.ImageName, Hold: hold, Forbidden: d.Forbidden})
	}
	return result, warning, nil
}
func (m *Manager) desktopHeld(id int64, desktop string) bool {
	reason, err := m.store.DesktopHold(storage.PowerTarget{AccountID: id, DesktopID: desktop})
	return err != nil || reason != ""
}

type DesktopDetails struct {
	Name          string                     `json:"name"`
	Code          string                     `json:"code"`
	ID            string                     `json:"id"`
	Configuration ctyun.DesktopConfiguration `json:"configuration"`
	Specification string                     `json:"specification"`
	MirrorSource  string                     `json:"mirror_source"`
}

func (m *Manager) DesktopDetails(ctx context.Context, t storage.PowerTarget) (DesktopDetails, error) {
	for attempt := 0; attempt < 2; attempt++ {
		c, values, _, err := m.desktopClient(ctx, t.AccountID)
		if err != nil {
			return DesktopDetails{}, err
		}
		var target *ctyun.Desktop
		for i := range values {
			if values[i].ID() == t.DesktopID {
				target = &values[i]
				break
			}
		}
		if target == nil {
			return DesktopDetails{}, errors.New("云机不存在或不属于该账号")
		}
		select {
		case m.desktopReadSlots <- struct{}{}:
		case <-ctx.Done():
			return DesktopDetails{}, ctx.Err()
		}
		cfg, err := c.DesktopConfiguration(ctx, *target)
		<-m.desktopReadSlots
		if ctyun.IsLoginExpired(err) && attempt == 0 {
			m.store.ClearAuthCache(t.AccountID)
			continue
		}
		if err != nil {
			return DesktopDetails{}, err
		}
		code := target.DesktopCode
		if code == "" {
			code = target.ID()
		}
		return DesktopDetails{Name: target.Name(), Code: code, ID: target.ID(), Configuration: cfg, Specification: cfg.Specification(), MirrorSource: cfg.MirrorSource()}, nil
	}
	return DesktopDetails{}, errors.New("配置读取失败")
}

// Rebuild only this account's sessions. Other accounts are unaffected.
func (m *Manager) resetDesktopSessions(id int64) {
	m.mu.Lock()
	if s := m.clients[id]; s != nil && s.cancel != nil {
		s.cancel()
	}
	delete(m.clients, id)
	for _, run := range m.active {
		if run.accountID == id && (strings.HasSuffix(run.typ, ":pc") || strings.HasSuffix(run.typ, ":login")) {
			run.cancel()
		}
	}
	m.mu.Unlock()
	if a, err := m.store.Account(id); err == nil {
		m.reconcileAccountKeepalive(a, time.Now())
	}
}
func (m *Manager) desktopHoldScheduler() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.mu.RLock()
			busy := m.powerActive != 0 || m.maintenance
			m.mu.RUnlock()
			if busy {
				continue
			}
			ids, err := m.store.ExpireDesktopHolds()
			if err == nil {
				for _, id := range ids {
					m.resetDesktopSessions(id)
				}
			}
		}
	}
}

type DesktopBrowserLogin struct {
	Target      storage.PowerTarget
	URL         string
	Profile     ctyun.Profile
	DeviceCode  string
	UserAccount string
}

const desktopBrowserHoldReason = "网页桌面使用中，关闭页面后自动恢复"

func (m *Manager) RenewDesktopBrowser(t storage.PowerTarget) error {
	m.desktopControlMu.Lock()
	defer m.desktopControlMu.Unlock()
	_, err := m.store.DB.Exec("UPDATE desktop_holds SET until_unix=MAX(until_unix,?) WHERE account_id=? AND desktop_id=? AND reason=?", time.Now().Add(150*time.Second).Unix(), t.AccountID, t.DesktopID, desktopBrowserHoldReason)
	return err
}

func (m *Manager) ResumeDesktopBrowser(t storage.PowerTarget) error {
	m.desktopControlMu.Lock()
	defer m.desktopControlMu.Unlock()
	m.mu.RLock()
	busy := m.powerActive != 0 || m.maintenance
	m.mu.RUnlock()
	if busy {
		return errors.New("电源操作或系统更新正在执行")
	}
	result, err := m.store.DB.Exec("DELETE FROM desktop_holds WHERE account_id=? AND desktop_id=? AND reason=?", t.AccountID, t.DesktopID, desktopBrowserHoldReason)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed > 0 {
		m.resetDesktopSessions(t.AccountID)
	}
	return err
}

func (m *Manager) DesktopBrowser(ctx context.Context, t storage.PowerTarget) (string, error) {
	login, err := m.DesktopBrowserLogin(ctx, t)
	return login.URL, err
}
func (m *Manager) DesktopBrowserLogin(ctx context.Context, t storage.PowerTarget) (DesktopBrowserLogin, error) {
	m.desktopControlMu.Lock()
	defer m.desktopControlMu.Unlock()
	m.mu.RLock()
	busy := m.powerActive != 0 || m.maintenance
	m.mu.RUnlock()
	if busy {
		return DesktopBrowserLogin{}, errors.New("电源操作或系统更新正在执行，请稍后打开桌面")
	}
	c, values, _, err := m.desktopClient(ctx, t.AccountID)
	if err != nil {
		return DesktopBrowserLogin{}, err
	}
	for _, d := range values {
		if d.ID() == t.DesktopID {
			if c.Profile == nil || !c.Profile.BondedDevice {
				return DesktopBrowserLogin{}, errors.New("请先完成该账号的设备验证")
			}
			if c.Profile.NeedSmsValidate || c.Profile.NeedBindVirtualMfa || c.Profile.NeedUpdatePassword || c.Profile.TwoFaValidateType == 5 {
				return DesktopBrowserLogin{}, errors.New("平台要求额外登录验证，请先在官方客户端完成验证")
			}
			if d.Forbidden {
				return DesktopBrowserLogin{}, errors.New("平台禁止连接此云机")
			}
			if err = m.store.SetDesktopHold(t, time.Now().Add(3*time.Minute).Unix(), desktopBrowserHoldReason); err != nil {
				return DesktopBrowserLogin{}, err
			}
			m.resetDesktopSessions(t.AccountID)
			return DesktopBrowserLogin{Target: t, URL: d.WebURL(), Profile: *c.Profile, DeviceCode: c.DeviceCode, UserAccount: c.Username}, nil
		}
	}
	return DesktopBrowserLogin{}, errors.New("云机不存在或不属于该账号")
}
func (m *Manager) ResumeDesktop(ctx context.Context, t storage.PowerTarget) error {
	m.desktopControlMu.Lock()
	defer m.desktopControlMu.Unlock()
	m.mu.RLock()
	busy := m.powerActive != 0 || m.maintenance
	m.mu.RUnlock()
	if busy {
		return errors.New("电源操作或系统更新正在执行，请稍后恢复连接")
	}
	_, values, _, err := m.desktopClient(ctx, t.AccountID)
	if err != nil {
		return err
	}
	for _, d := range values {
		if d.ID() == t.DesktopID {
			if err = m.store.ClearDesktopHold(t); err == nil {
				m.resetDesktopSessions(t.AccountID)
			}
			return err
		}
	}
	return errors.New("云机不存在或不属于该账号")
}

func (m *Manager) StartPowerBatch(action string, targets []storage.PowerTarget) (int64, error) {
	m.desktopControlMu.Lock()
	defer m.desktopControlMu.Unlock()
	switch action {
	case "poweron", "shutdown", "reboot":
	default:
		return 0, errors.New("不支持的电源操作")
	}
	if len(targets) == 0 || len(targets) > 2000 {
		return 0, errors.New("请选择 1 到 2000 台云机")
	}
	unique := make([]storage.PowerTarget, 0, len(targets))
	seen := map[storage.PowerTarget]bool{}
	for _, t := range targets {
		if t.AccountID < 1 || strings.TrimSpace(t.DesktopID) == "" || len(t.DesktopID) > 256 {
			return 0, errors.New("云机参数无效")
		}
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	m.launchMu.Lock()
	defer m.launchMu.Unlock()
	if m.closing {
		return 0, errors.New("服务正在关闭")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.maintenance || m.powerActive != 0 {
		return 0, errors.New("已有电源操作或系统更新正在执行")
	}
	id, err := m.store.AddPowerJob(action, unique)
	if err != nil {
		return 0, err
	}
	m.powerActive = id
	m.powerAccounts = map[int64]bool{}
	for _, t := range unique {
		m.powerAccounts[t.AccountID] = true
	}
	m.wg.Add(1)
	go func() { defer m.wg.Done(); m.runPowerBatch(id, action, unique) }()
	return id, nil
}
func (m *Manager) runPowerBatch(id int64, action string, targets []storage.PowerTarget) {
	changed := map[int64]bool{}
	defer func() {
		m.mu.Lock()
		m.powerActive = 0
		m.powerAccounts = nil
		m.mu.Unlock()
		for account := range changed {
			m.resetDesktopSessions(account)
		}
	}()
	done := false
	defer func() {
		if !done {
			_, _ = m.store.DB.Exec("UPDATE power_items SET status='cancelled',message='任务停止，未提交' WHERE job_id=? AND status IN ('queued','validating')", id)
			_, _ = m.store.DB.Exec("UPDATE power_items SET status='unknown',message='任务中断，请核对平台状态' WHERE job_id=? AND status='submitting'", id)
			_ = m.store.FinishPowerJob(id, "interrupted")
		}
	}()
	// One worker, one submission per second. Refresh membership once per account.
	clients := map[int64]*ctyun.Client{}
	desktops := map[int64]map[string]ctyun.Desktop{}
	accountErrors := map[int64]error{}
	for pos, t := range targets {
		if m.ctx.Err() != nil {
			return
		}
		if err := m.store.SetPowerItem(id, pos, "", "validating", "正在校验账号和云机归属"); err != nil {
			m.logf("电源任务 #%d 数据记录失败：%v", id, err)
			return
		}
		if _, ok := desktops[t.AccountID]; !ok && accountErrors[t.AccountID] == nil {
			ctx, cancel := context.WithTimeout(m.ctx, 90*time.Second)
			c, values, _, err := m.desktopClient(ctx, t.AccountID)
			cancel()
			if err != nil {
				accountErrors[t.AccountID] = err
			} else {
				clients[t.AccountID] = c
				desktops[t.AccountID] = map[string]ctyun.Desktop{}
				for _, d := range values {
					desktops[t.AccountID][d.ID()] = d
				}
			}
		}
		d, ok := desktops[t.AccountID][t.DesktopID]
		err := accountErrors[t.AccountID]
		if err == nil && !ok {
			err = errors.New("云机不存在或不属于该账号")
		}
		if err == nil && d.Forbidden {
			err = errors.New("平台禁止连接/操作该云机")
		}
		if err != nil {
			_ = m.store.SetPowerItem(id, pos, d.Name(), "failed", err.Error())
			continue
		}
		// Preserve manual shutdown across restarts; reboot/web holds expire automatically.
		previousUntil, previousReason, holdErr := m.store.DesktopHoldSnapshot(t)
		if holdErr != nil {
			_ = m.store.SetPowerItem(id, pos, d.Name(), "failed", holdErr.Error())
			continue
		}
		if action == "shutdown" {
			err = m.store.SetDesktopHold(t, 0, "手动关机，自动连接已暂停")
		} else if action == "reboot" {
			err = m.store.SetDesktopHold(t, time.Now().Add(5*time.Minute).Unix(), "重启中，自动连接暂停 5 分钟")
		} else {
			err = m.store.ClearDesktopHold(t)
		}
		if err != nil {
			_ = m.store.SetPowerItem(id, pos, d.Name(), "failed", err.Error())
			continue
		}
		changed[t.AccountID] = true
		// Stop existing account sessions and desktop-related tasks before power submission.
		m.mu.Lock()
		if s := m.clients[t.AccountID]; s != nil && s.cancel != nil {
			s.cancel()
		}
		delete(m.clients, t.AccountID)
		for _, r := range m.active {
			if r.accountID == t.AccountID && (strings.HasSuffix(r.typ, ":pc") || strings.HasSuffix(r.typ, ":login")) {
				r.cancel()
			}
		}
		m.mu.Unlock()
		if err = m.store.SetPowerItem(id, pos, d.Name(), "submitting", "正在提交电源指令"); err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(m.ctx, 30*time.Second)
		err = clients[t.AccountID].OperateDesktop(ctx, d, action)
		cancel()
		status, message := "accepted", "平台已接受指令，请刷新云机状态确认执行结果"
		if err != nil {
			status = "unknown"
			message = "请求结果不确定，请核对状态后再操作：" + err.Error()
			if ctyun.IsPlatformError(err) {
				status = "failed"
				message = err.Error()
				if previousReason == "" {
					err = m.store.ClearDesktopHold(t)
				} else {
					err = m.store.SetDesktopHold(t, previousUntil, previousReason)
				}
				if err != nil {
					message += "；恢复连接策略失败，请手动核对：" + err.Error()
				}
			} else if action == "poweron" {
				_ = m.store.SetDesktopHold(t, time.Now().Add(5*time.Minute).Unix(), "电源指令结果待核对，自动连接暂停 5 分钟")
			}
		}
		if err = m.store.SetPowerItem(id, pos, d.Name(), status, message); err != nil {
			return
		}
		m.logf("[电源任务 #%d/%s] %s：%s", id, d.Name(), action, message)
		select {
		case <-m.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	if err := m.store.FinishPowerJob(id, "finished"); err == nil {
		done = true
	} else {
		m.logf("保存电源任务结果失败：%v", err)
	}
}
