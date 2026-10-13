package ctyun

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestDesktopSpecificationDoesNotGuessPackages(t *testing.T) {
	cpu, memory := 8.0, 16.0
	for _, tc := range []struct {
		d    Desktop
		want string
	}{
		{Desktop{CPUCore: &cpu, MemoryGB: &memory}, "8 核 / 16 GB"},
		{Desktop{FlavorName: "4C8G"}, "未提供"},
		{Desktop{DesktopName: "桌面 8核/16GB"}, "未提供"},
		{Desktop{ProductGroup: "4vCPU 8GiB"}, "未提供"},
		{Desktop{DesktopName: "天翼云电脑精英版"}, "未提供"},
	} {
		if got := tc.d.Specification(); got != tc.want {
			t.Fatalf("%#v: %s", tc.d, got)
		}
	}
}

func TestDesktopConfigurationUsesVerifiedFields(t *testing.T) {
	c := NewClient("u", "p", "d", "")
	c.Profile = &Profile{UserID: 1, SecretKey: "s"}
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		q := r.URL.Query()
		if r.Method != "GET" || r.URL.Path != "/api/desktop/client/feature" || q.Get("desktopId") != "123" || q.Get("objId") != "pool" || q.Get("objType") != "1" || q.Get("getManage") != "1" {
			t.Fatal(r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"code":0,"data":{"cpuCore":8,"memoryGB":16,"systemDiskGB":120,"dataDiskGB":8,"productName":"公众版-精英型","mirrorCategoryName":"Windows 11","mirrorVersion":"v1","mirrorType":30,"os":"Windows","createDate":1788396307000,"nowDate":1791899215371,"expireDate":null,"secretKey":"must-not-expose"}}`))}, nil
	})
	cfg, err := c.DesktopConfiguration(context.Background(), Desktop{DesktopID: "123", ObjectID: "pool", ObjectType: 1})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Specification() != "8 核 / 16 GB" || cfg.ExpireDate != nil || *cfg.DataDiskGB != 8 || cfg.MirrorSource() != "用户自传镜像" {
		t.Fatal(cfg)
	}
}
func TestDesktopConfigurationRejectsUnusableResponses(t *testing.T) {
	for _, body := range []string{`{"edata":"encrypted"}`, `{"data":{}}`, `{"code":0,"data":null}`, `{"code":50001,"msg":"failed"}`, `{"code":0,"data":{"cpuCore":-1}}`} {
		c := NewClient("u", "p", "d", "")
		c.Profile = &Profile{UserID: 1, SecretKey: "s"}
		c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})
		if _, err := c.DesktopConfiguration(context.Background(), Desktop{DesktopID: "123"}); err == nil {
			t.Fatalf("bad configuration accepted: %s", body)
		}
	}
}

func TestDesktopConfigurationMirrorTypeRepresentations(t *testing.T) {
	for _, value := range []string{`30`, `"30"`, `" 30 "`} {
		var cfg DesktopConfiguration
		if err := json.Unmarshal([]byte(`{"cpuCore":8,"mirrorType":`+value+`}`), &cfg); err != nil {
			t.Fatal(value, err)
		}
		if cfg.MirrorSource() != "用户自传镜像" || cfg.CPUCore == nil || *cfg.CPUCore != 8 {
			t.Fatal(value, cfg)
		}
	}
	for _, value := range []string{`null`, `""`} {
		var cfg DesktopConfiguration
		if err := json.Unmarshal([]byte(`{"mirrorType":`+value+`}`), &cfg); err != nil || cfg.MirrorType != nil {
			t.Fatal(value, cfg, err)
		}
	}
	for _, value := range []string{`"public"`, `true`, `{}`, `30.5`, `-1`, `"-1"`} {
		var cfg DesktopConfiguration
		if err := json.Unmarshal([]byte(`{"mirrorType":`+value+`}`), &cfg); err == nil {
			t.Fatal("invalid mirror type accepted", value)
		}
	}
}
func TestDesktopWebURLUsesOfficialOriginAndObjectID(t *testing.T) {
	d := Desktop{DesktopID: "123", ObjectID: "pool-456"}
	u, err := url.Parse(d.WebURL())
	if err != nil || u.Host != "pc.ctyun.cn" || u.Scheme != "https" {
		t.Fatal(u, err)
	}
	q, _ := url.ParseQuery(strings.SplitN(u.Fragment, "?", 2)[1])
	raw, err := base64.StdEncoding.DecodeString(q.Get("id"))
	if err != nil || string(raw) != "pool-456" {
		t.Fatal(string(raw), err)
	}
}
func TestDesktopOperationsUseExplicitActionCodes(t *testing.T) {
	for action, code := range map[string]string{"poweron": "1", "shutdown": "2", "reboot": "3"} {
		t.Run(action, func(t *testing.T) {
			c := NewClient("u", "p", "d", "")
			c.Profile = &Profile{UserID: 1, SecretKey: "s"}
			calls := 0
			c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/api/desktop/client/operate" || r.Method != "POST" {
					t.Fatal(r.URL)
				}
				r.ParseForm()
				if r.Form.Get("desktopId") != "7" || r.Form.Get("operationType") != code {
					t.Fatal(r.Form)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":0}`)), Header: make(http.Header)}, nil
			})
			if err := c.OperateDesktop(context.Background(), Desktop{DesktopID: "7"}, action); err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
			if err := c.OperateDesktop(context.Background(), Desktop{DesktopID: "7"}, "invalid"); err == nil || calls != 1 {
				t.Fatal("invalid action sent")
			}
		})
	}
}
func TestManagementDesktopsMergesAndDeduplicates(t *testing.T) {
	c := NewClient("u", "p", "d", "")
	c.Profile = &Profile{UserID: 1, SecretKey: "s"}
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"code":0,"data":{"desktopList":[{"desktopId":"1","flavorName":"4C8G"}]}}`
		if r.URL.Path == "/api/desktop/client/list" {
			body = `{"code":0,"data":{"desktopList":[{"desktopId":"1"},{"desktopId":"2"}],"desktopPoolList":[{"poolId":"3"}]}}`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	ds, warning, err := c.ManagementDesktops(context.Background())
	if err != nil || warning != "" || len(ds) != 3 || ds[0].FlavorName != "4C8G" || ds[2].ObjectType != 1 {
		t.Fatal(ds, warning, err)
	}
}
