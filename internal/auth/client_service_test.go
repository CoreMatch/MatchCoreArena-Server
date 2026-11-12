package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// newServiceFake 返回一个假 HRPAuth：/status 通过启动校验，
// /oauth/token 按 client_credentials 签发令牌（计数签发次数），
// /services/users/{uuid} 按预设返回。
type serviceFake struct {
	tokenIssues atomic.Int32
	tokenStatus int    // /oauth/token 的状态码，默认 200
	expiresIn   string // expires_in 字面量
	lookupCode  int    // /services/users/* 的状态码，默认 200
	lookupBody  string
	lastBearer  atomic.Value
}

func newServiceFakeServer(t *testing.T, f *serviceFake) *httptest.Server {
	t.Helper()
	if f.expiresIn == "" {
		f.expiresIn = "3600"
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/status":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/oauth/token":
			if r.FormValue("grant_type") != "client_credentials" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			if f.tokenStatus != 0 {
				w.WriteHeader(f.tokenStatus)
				return
			}
			f.tokenIssues.Add(1)
			w.WriteHeader(http.StatusOK)
			body := `{"success":true,"data":{"access_token":"svc-token","expires_in":` +
				f.expiresIn + `},"access_token":"svc-token","expires_in":` + f.expiresIn + `}`
			_, _ = w.Write([]byte(body))
		case r.URL.Path == "/services/users/uuid-1":
			f.lastBearer.Store(r.Header.Get("Authorization"))
			if f.lookupCode != 0 {
				w.WriteHeader(f.lookupCode)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(f.lookupBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newServiceTestClient(t *testing.T, srvURL string) *Client {
	t.Helper()
	cli, err := NewClient(srvURL, "public-client", "svc-id", "svc-secret")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return cli
}

// TestLookupUserByUUID_Success 覆盖正常解析：服务令牌随请求携带，
// 信封 data.uid 被正确取出。
func TestLookupUserByUUID_Success(t *testing.T) {
	f := &serviceFake{lookupBody: `{"success":true,"data":{"uid":7,"uuid":"uuid-1","email":"a@b.c","username":"alice"}}`}
	srv := newServiceFakeServer(t, f)
	cli := newServiceTestClient(t, srv.URL)

	uid, err := cli.LookupUserByUUID(context.Background(), "uuid-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uid != 7 {
		t.Fatalf("uid = %d, want 7", uid)
	}
	if got := f.lastBearer.Load(); got != "Bearer svc-token" {
		t.Errorf("Authorization = %v, want Bearer svc-token", got)
	}
}

// TestLookupUserByUUID_NotFound 保持对外语义：404 → ErrUserNotFound。
func TestLookupUserByUUID_NotFound(t *testing.T) {
	f := &serviceFake{lookupCode: http.StatusNotFound}
	srv := newServiceFakeServer(t, f)
	cli := newServiceTestClient(t, srv.URL)

	_, err := cli.LookupUserByUUID(context.Background(), "uuid-1")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("err = %v, want ErrUserNotFound", err)
	}
}

// TestLookupUserByUUID_TokenCached 覆盖令牌缓存：两次解析只签发一次。
func TestLookupUserByUUID_TokenCached(t *testing.T) {
	f := &serviceFake{lookupBody: `{"success":true,"data":{"uid":7}}`}
	srv := newServiceFakeServer(t, f)
	cli := newServiceTestClient(t, srv.URL)

	for i := 0; i < 2; i++ {
		if _, err := cli.LookupUserByUUID(context.Background(), "uuid-1"); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if n := f.tokenIssues.Load(); n != 1 {
		t.Fatalf("token issued %d times, want 1 (cached)", n)
	}
}

// TestLookupUserByUUID_TokenRefreshOnExpiry 覆盖过期前刷新：
// expires_in=0 使缓存立即失效，每次调用都重新签发。
func TestLookupUserByUUID_TokenRefreshOnExpiry(t *testing.T) {
	f := &serviceFake{expiresIn: "0", lookupBody: `{"success":true,"data":{"uid":7}}`}
	srv := newServiceFakeServer(t, f)
	cli := newServiceTestClient(t, srv.URL)

	for i := 0; i < 2; i++ {
		if _, err := cli.LookupUserByUUID(context.Background(), "uuid-1"); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if n := f.tokenIssues.Load(); n != 2 {
		t.Fatalf("token issued %d times, want 2 (refreshed)", n)
	}
}

// TestLookupUserByUUID_TokenEndpointFailure 覆盖换取令牌失败：
// 类型化 ErrServiceAuth，不静默。
func TestLookupUserByUUID_TokenEndpointFailure(t *testing.T) {
	f := &serviceFake{tokenStatus: http.StatusUnauthorized}
	srv := newServiceFakeServer(t, f)
	cli := newServiceTestClient(t, srv.URL)

	_, err := cli.LookupUserByUUID(context.Background(), "uuid-1")
	if !errors.Is(err, ErrServiceAuth) {
		t.Fatalf("err = %v, want ErrServiceAuth", err)
	}
}

// TestLookupUserByUUID_CredentialsNotConfigured 覆盖凭据未配置：
// 类型化 ErrServiceAuth。
func TestLookupUserByUUID_CredentialsNotConfigured(t *testing.T) {
	f := &serviceFake{}
	srv := newServiceFakeServer(t, f)
	cli, err := NewClient(srv.URL, "public-client", "", "")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = cli.LookupUserByUUID(context.Background(), "uuid-1")
	if !errors.Is(err, ErrServiceAuth) {
		t.Fatalf("err = %v, want ErrServiceAuth", err)
	}
	if n := f.tokenIssues.Load(); n != 0 {
		t.Errorf("no token should be requested without credentials, got %d", n)
	}
}

// TestLookupUserByUUID_Rejected 覆盖服务令牌被拒（401/403）：
// 类型化 ErrServiceAuth。
func TestLookupUserByUUID_Rejected(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		f := &serviceFake{lookupCode: code}
		srv := newServiceFakeServer(t, f)
		cli := newServiceTestClient(t, srv.URL)

		_, err := cli.LookupUserByUUID(context.Background(), "uuid-1")
		if !errors.Is(err, ErrServiceAuth) {
			t.Fatalf("code %d: err = %v, want ErrServiceAuth", code, err)
		}
	}
}

// TestLookupUserByUUID_UpstreamError 覆盖上游 5xx：ErrUpstream。
func TestLookupUserByUUID_UpstreamError(t *testing.T) {
	f := &serviceFake{lookupCode: http.StatusInternalServerError}
	srv := newServiceFakeServer(t, f)
	cli := newServiceTestClient(t, srv.URL)

	_, err := cli.LookupUserByUUID(context.Background(), "uuid-1")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
}
