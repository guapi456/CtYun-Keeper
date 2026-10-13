package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/storage"
)

func (s *Server) desktopsPage(w http.ResponseWriter, r *http.Request) {
	if !s.guard(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", 405)
		return
	}
	accounts, err := s.store.Accounts()
	if err != nil {
		http.Error(w, "读取账号失败", 500)
		return
	}
	var options strings.Builder
	for _, a := range accounts {
		fmt.Fprintf(&options, `<option value="%d">%s</option>`, a.ID, esc(a.Name))
	}
	body := `<header class=page-head><div><p class=eyebrow>设备</p><h1>云机管理</h1><p class=page-subtitle>查看配置规格，批量操作电源并打开官方网页桌面。</p></div></header><article class="panel cloud-panel" data-cloud-manager><div class=cloud-toolbar><label>账号<select data-cloud-account><option value="">全部账号</option>` + options.String() + `</select></label><div class=cloud-search-group><label>搜索<input data-cloud-search placeholder="云机名称、编号或账号"></label><button class="icon-button cloud-refresh-button" type=button data-cloud-refresh title="刷新云机" aria-label="刷新云机"><svg width="26" height="26" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 11a8 8 0 0 0-15-3M4 4v5h5M4 13a8 8 0 0 0 15 3M20 20v-5h-5"/></svg></button></div></div><p class=muted data-cloud-notice role=status>正在读取云机列表…</p><div class=cloud-errors data-cloud-errors></div><div class=cloud-actions><label class=cloud-select-all><input type=checkbox data-cloud-select-all>选择当前列表</label><span data-cloud-selection>已选 0 台</span><select data-cloud-action aria-label=电源操作><option value=poweron>开机</option><option value=reboot>重启</option><option value=shutdown>关机</option></select><button type=button class=primary data-cloud-power>执行电源操作</button></div><div class=cloud-machine-grid data-cloud-cards></div><div class=cloud-pagination><button type=button class=secondary data-cloud-prev>上一页</button><span data-cloud-page></span><button type=button class=secondary data-cloud-next>下一页</button></div></article><article class="panel cloud-panel"><div class=panel-head><h2>电源任务记录</h2></div><div data-cloud-jobs></div></article>`
	s.page(w, r, "云机管理", body, true)
}

func cloudJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func cloudError(w http.ResponseWriter, status int, err string) {
	cloudJSON(w, status, map[string]string{"error": err})
}
func (s *Server) desktopsAPI(w http.ResponseWriter, r *http.Request) {
	if !s.authed(r) {
		cloudError(w, 401, "请先登录")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/desktops/")
	if r.Method == http.MethodGet && path == "holds" {
		rows, err := s.store.DB.QueryContext(r.Context(), "SELECT account_id,desktop_id,reason FROM desktop_holds WHERE until_unix=0 OR until_unix>?", time.Now().Unix())
		if err != nil {
			cloudError(w, 500, "读取暂停状态失败")
			return
		}
		defer rows.Close()
		type holdState struct {
			AccountID int64  `json:"account_id"`
			DesktopID string `json:"desktop_id"`
			Hold      string `json:"hold"`
		}
		values := []holdState{}
		for rows.Next() {
			var value holdState
			if err = rows.Scan(&value.AccountID, &value.DesktopID, &value.Hold); err != nil {
				cloudError(w, 500, "读取暂停状态失败")
				return
			}
			values = append(values, value)
		}
		if rows.Err() != nil {
			cloudError(w, 500, "读取暂停状态失败")
			return
		}
		cloudJSON(w, 200, values)
		return
	}
	if r.Method == http.MethodGet && path == "details" {
		id, err := strconv.ParseInt(r.URL.Query().Get("account_id"), 10, 64)
		desktop := r.URL.Query().Get("desktop_id")
		if err != nil || id < 1 || desktop == "" || len(desktop) > 256 {
			cloudError(w, 400, "云机参数无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		result, err := s.manager.DesktopDetails(ctx, storage.PowerTarget{AccountID: id, DesktopID: desktop})
		if err != nil {
			cloudError(w, 502, err.Error())
			return
		}
		cloudJSON(w, 200, result)
		return
	}
	if r.Method == http.MethodGet && path == "list" {
		id, err := strconv.ParseInt(r.URL.Query().Get("account_id"), 10, 64)
		if err != nil || id < 1 {
			cloudError(w, 400, "账号无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		values, warning, err := s.manager.ManagedDesktops(ctx, id)
		if err != nil {
			cloudError(w, 502, err.Error())
			return
		}
		cloudJSON(w, 200, map[string]any{"desktops": values, "warning": warning})
		return
	}
	if r.Method == http.MethodGet && path == "jobs" {
		rows, err := s.store.DB.Query("SELECT id FROM power_jobs ORDER BY id DESC LIMIT 10")
		if err != nil {
			cloudError(w, 500, err.Error())
			return
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err = rows.Scan(&id); err != nil {
				break
			}
			ids = append(ids, id)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			cloudError(w, 500, err.Error())
			return
		}
		jobs := []storage.PowerJob{}
		for _, id := range ids {
			job, err := s.store.PowerJob(id)
			if err != nil {
				cloudError(w, 500, err.Error())
				return
			}
			jobs = append(jobs, job)
		}
		cloudJSON(w, 200, jobs)
		return
	}
	if r.Method != http.MethodPost {
		cloudError(w, 405, "不支持的请求")
		return
	}
	if !s.checkCSRF(r) {
		cloudError(w, 403, "请求验证失败，请刷新页面")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if path == "power" {
		var input struct {
			Action  string                `json:"action"`
			Targets []storage.PowerTarget `json:"targets"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil {
			cloudError(w, 400, "请求格式错误")
			return
		}
		id, err := s.manager.StartPowerBatch(input.Action, input.Targets)
		if err != nil {
			cloudError(w, 409, err.Error())
			return
		}
		cloudJSON(w, 202, map[string]int64{"job_id": id})
		return
	}
	if path == "open" || path == "resume" {
		var input storage.PowerTarget
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&input); err != nil || input.AccountID < 1 || input.DesktopID == "" {
			cloudError(w, 400, "云机参数无效")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		if path == "resume" {
			if err := s.manager.ResumeDesktop(ctx, input); err != nil {
				cloudError(w, 409, err.Error())
				return
			}
			cloudJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		if !s.secureDesktopViewerRequest(r) {
			cloudError(w, 400, "自动登录桌面支持局域网 HTTP 访问，公网访问请使用 HTTPS")
			return
		}
		s.viewerOpMu.Lock()
		defer s.viewerOpMu.Unlock()
		login, err := s.manager.DesktopBrowserLogin(ctx, input)
		if err != nil {
			cloudError(w, 409, err.Error())
			return
		}
		entry, err := s.createDesktopViewer(login)
		if err != nil {
			cloudError(w, 409, err.Error())
			return
		}
		viewer, _ := s.getDesktopViewer(strings.TrimPrefix(entry, "/desktop-view/"))
		cloudJSON(w, 200, map[string]string{"url": entry, "entry_key": viewer.EntryKey})
		return
	}
	cloudError(w, 404, "未找到接口")
}
