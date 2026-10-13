package ctyun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
)

func (d Desktop) Specification() string {
	if d.CPUCore != nil && d.MemoryGB != nil && *d.CPUCore > 0 && *d.MemoryGB > 0 {
		return fmt.Sprintf("%g 核 / %g GB", *d.CPUCore, *d.MemoryGB)
	}
	return "未提供"
}

// Only the configuration fields used by the UI are exposed; unknown fields are discarded.
type DesktopConfiguration struct {
	CPUCore       *float64 `json:"cpuCore"`
	MemoryGB      *float64 `json:"memoryGB"`
	SystemDiskGB  *float64 `json:"systemDiskGB"`
	DataDiskGB    *float64 `json:"dataDiskGB"`
	ProductName   string   `json:"productName"`
	MirrorName    string   `json:"mirrorCategoryName"`
	MirrorVersion string   `json:"mirrorVersion"`
	MirrorType    *int     `json:"mirrorType"`
	OS            string   `json:"os"`
	CreateDate    *float64 `json:"createDate"`
	NowDate       *float64 `json:"nowDate"`
	ExpireDate    *float64 `json:"expireDate"`
}

func (cfg DesktopConfiguration) Specification() string {
	return (Desktop{CPUCore: cfg.CPUCore, MemoryGB: cfg.MemoryGB}).Specification()
}
func (cfg DesktopConfiguration) MirrorSource() string {
	if cfg.MirrorType == nil {
		return "未提供"
	}
	// Mapping verified against the official desktop client.
	if label, ok := map[int]string{10: "公共镜像", 15: "自定义镜像", 20: "toc镜像", 25: "共享镜像", 30: "用户自传镜像", 41: "场景镜像"}[*cfg.MirrorType]; ok {
		return label
	}
	return fmt.Sprintf("未知来源（%d）", *cfg.MirrorType)
}
func (c *Client) DesktopConfiguration(ctx context.Context, d Desktop) (DesktopConfiguration, error) {
	var empty DesktopConfiguration
	if d.ID() == "" {
		return empty, errors.New("云机编号为空")
	}
	if err := c.EnsureLogin(ctx); err != nil {
		return empty, err
	}
	h, err := c.signed(PCVersion, false)
	if err != nil {
		return empty, err
	}
	obj := d.ObjectID
	if obj == "" {
		obj = d.ID()
	}
	query := url.Values{"desktopId": {d.ID()}, "objId": {obj}, "objType": {fmt.Sprint(d.ObjectType)}, "getManage": {"1"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, PCOrigin+"/api/desktop/client/feature?"+query.Encode(), nil)
	if err != nil {
		return empty, err
	}
	req.Header = h
	response, err := c.HTTP.Do(req)
	if err != nil {
		return empty, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return empty, err
	}
	var envelope struct {
		Code      *int                  `json:"code"`
		Msg       string                `json:"msg"`
		Message   string                `json:"message"`
		Data      *DesktopConfiguration `json:"data"`
		Encrypted string                `json:"edata"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		return empty, fmt.Errorf("配置接口响应格式无效（HTTP %d）", response.StatusCode)
	}
	if envelope.Encrypted != "" {
		return empty, errors.New("配置接口返回加密数据，当前账号暂无法读取")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return empty, fmt.Errorf("配置接口 HTTP %d", response.StatusCode)
	}
	if envelope.Code == nil {
		return empty, errors.New("配置接口未返回业务状态码")
	}
	if *envelope.Code != 0 {
		message := envelope.Msg
		if message == "" {
			message = envelope.Message
		}
		return empty, APIError{Code: *envelope.Code, Message: message}
	}
	if envelope.Data == nil {
		return empty, errors.New("配置接口未返回配置对象")
	}
	cfg := *envelope.Data
	for _, v := range []*float64{cfg.CPUCore, cfg.MemoryGB, cfg.SystemDiskGB, cfg.DataDiskGB, cfg.CreateDate, cfg.NowDate, cfg.ExpireDate} {
		if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0) {
			return empty, errors.New("配置接口返回无效数值")
		}
	}
	return cfg, nil
}

func (d Desktop) WebURL() string {
	id := d.ObjectID
	if id == "" {
		id = d.ID()
	}
	return "https://pc.ctyun.cn/#/desktop?id=" + url.QueryEscape(base64.StdEncoding.EncodeToString([]byte(id)))
}

// Supplement the paged endpoint with the official list endpoint; no guessed paging parameters.
func (c *Client) ManagementDesktops(ctx context.Context) ([]Desktop, string, error) {
	primary, err := c.ListDesktops(ctx)
	if err != nil {
		return nil, "", err
	}
	h, err := c.signed(PCVersion, false)
	if err != nil {
		return nil, "", err
	}
	var extra struct{ DesktopList, DesktopPoolList, PreemptionDesktopList []Desktop }
	err = c.do(ctx, http.MethodGet, PCOrigin+"/api/desktop/client/list", nil, h, &extra)
	warning := ""
	if err != nil {
		if IsLoginExpired(err) {
			return nil, "", err
		}
		warning = "备用列表未读取成功；当前列表可能不完整"
	}
	for i := range extra.DesktopPoolList {
		if extra.DesktopPoolList[i].ObjectType == 0 {
			extra.DesktopPoolList[i].ObjectType = 1
		}
	}
	for i := range extra.PreemptionDesktopList {
		if extra.PreemptionDesktopList[i].ObjectType == 0 {
			extra.PreemptionDesktopList[i].ObjectType = 2
		}
	}
	values := append(primary, extra.DesktopList...)
	values = append(values, extra.DesktopPoolList...)
	values = append(values, extra.PreemptionDesktopList...)
	seen := map[string]bool{}
	result := make([]Desktop, 0, len(values))
	for _, d := range values {
		if d.ID() != "" && !seen[d.ID()] {
			seen[d.ID()] = true
			result = append(result, d)
		}
	}
	if len(primary) >= 50 {
		warning = "主列表达到接口查询上限；请核对云机数量，避免遗漏"
	}
	return result, warning, nil
}

func (c *Client) OperateDesktop(ctx context.Context, d Desktop, action string) error {
	codes := map[string]string{"poweron": "1", "shutdown": "2", "reboot": "3"}
	code, ok := codes[action]
	if !ok {
		return errors.New("不支持的电源操作")
	}
	if d.ID() == "" {
		return errors.New("云机编号为空")
	}
	h, err := c.signed(PCVersion, false)
	if err != nil {
		return err
	}
	h.Set("Content-Type", "application/x-www-form-urlencoded")
	form := url.Values{"desktopId": {d.ID()}, "operationType": {code}}
	// A timeout may occur after the platform accepted the operation: never retry here.
	return c.do(ctx, http.MethodPost, PCOrigin+"/api/desktop/client/operate", strings.NewReader(form.Encode()), h, nil)
}
