// Package auth 封装与 HRPAuth 的 HTTP 交互。
//
// 设计模式对齐 WinnerProxy/internal/hrpauth：使用标准 net/http + 结构化错误，
// 通过 doXxx helpers 保持每端点方法精简。
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// serviceTokenRefreshMargin 服务令牌剩余有效期低于该值时提前刷新，
// 避免请求途中令牌过期。
const serviceTokenRefreshMargin = 60 * time.Second

// Client 是 HRPAuth 的轻量 HTTP 客户端。进程级单实例，并发安全。
type Client struct {
	baseURL        string
	publicClientID string
	http           *http.Client

	// 服务身份（client_credentials）：用于 ops 级服务间接点。
	serviceClientID     string
	serviceClientSecret string
	svcMu               sync.Mutex
	svcToken            string
	svcTokenExpiry      time.Time
}

// NewClient 构造 Client。baseURL 为 HRPAuth 根地址（如 http://localhost:8080）。
// publicClientID 为 OAuth2 public client_id（如 hrpauth-webui），用于 refresh_token 等第一方流程。
// serviceClientID/serviceClientSecret 为 ops 级服务凭据（client_credentials），
// 用于 GET /services/users/{uuid} 等服务间接点；留空时相关调用返回 ErrServiceAuth。
// 启动时校验 HRPAuth /status 可达性。
func NewClient(baseURL, publicClientID, serviceClientID, serviceClientSecret string) (*Client, error) {
	baseURL = strings.TrimRight(baseURL, "/")
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// 启动时校验 HRPAuth 可达
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/status", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("hrpauth 不可达: %w", err)
	}
	resp.Body.Close()

	return &Client{
		baseURL:             baseURL,
		publicClientID:      publicClientID,
		http:                httpClient,
		serviceClientID:     strings.TrimSpace(serviceClientID),
		serviceClientSecret: strings.TrimSpace(serviceClientSecret),
	}, nil
}

// serviceToken 返回缓存的 ops 级服务令牌，过期前自动用
// client_credentials 刷新。凭据未配置、换取失败或被拒绝时返回
// ErrServiceAuth（类型化，不静默）。
func (c *Client) serviceToken(ctx context.Context) (string, error) {
	if c.serviceClientID == "" || c.serviceClientSecret == "" {
		return "", fmt.Errorf("服务凭据未配置 (auth.hrpauth.service_client_id/secret): %w", ErrServiceAuth)
	}

	c.svcMu.Lock()
	defer c.svcMu.Unlock()

	// 缓存命中且距过期仍有刷新余量 → 直接复用。
	if c.svcToken != "" && time.Until(c.svcTokenExpiry) > serviceTokenRefreshMargin {
		return c.svcToken, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.serviceClientID},
		"client_secret": {c.serviceClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("构造服务令牌请求失败: %w", ErrServiceAuth)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("换取服务令牌失败: %w", ErrServiceAuth)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", fmt.Errorf("换取服务令牌被拒绝 (HTTP %d): %w", resp.StatusCode, ErrServiceAuth)
	}

	// HRPAuth 的统一信封会把 data 字段同时扁平化到顶层，
	// 因此顶层即可解出 access_token / expires_in。
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil || tr.AccessToken == "" {
		return "", fmt.Errorf("服务令牌响应无效: %w", ErrServiceAuth)
	}

	c.svcToken = tr.AccessToken
	c.svcTokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return c.svcToken, nil
}

// ─── Endpoint methods ────────────────────────────────────────────────────────

// GetUser 通过 Bearer token 调用 POST /user 获取用户信息。
// token 参数为玩家的 access_token（而非 service token），HRPAuth 侧由 token 本身鉴权。
//
//	200 → *UserResult, nil
//	401 → nil, ErrTokenExpired（或 ErrTokenInvalid）
//	其他 → nil, ErrUpstream
func (c *Client) GetUser(token string) (*UserResult, error) {
	body, err := c.doPostAuthed("/user", token, nil)
	if err != nil {
		return nil, err
	}

	// POST /user 成功时返回含 uid 字段的用户数据
	var resp struct {
		UID        int64  `json:"uid"`
		Email      string `json:"email"`
		Username   string `json:"username"`
		Avatar     string `json:"avatar"`
		Verified   bool   `json:"verified"`
		MojangUUID string `json:"mojang_uuid,omitempty"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("解析 /user 响应失败: %w", err)
	}
	if resp.UID == 0 {
		return nil, ErrTokenInvalid
	}

	return &UserResult{
		UID:        resp.UID,
		Email:      resp.Email,
		Username:   resp.Username,
		Avatar:     resp.Avatar,
		Verified:   resp.Verified,
		MojangUUID: resp.MojangUUID,
	}, nil
}

// GetLoginTicket 调用 POST /oauth/login-ticket 验证邮箱密码。
//
//	200 → *LoginTicketResult, nil
//	401 → nil, ErrCredentialsInvalid
//	其他 → nil, ErrUpstream
func (c *Client) GetLoginTicket(email, password string) (*LoginTicketResult, error) {
	resp, err := c.doPost("/oauth/login-ticket", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return nil, ErrUpstream
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		// 成功：可能是直接返回 token，或返回 totp_required
		var result LoginTicketResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("解析 login-ticket 响应失败: %w", err)
		}
		return &result, nil
	case 401:
		return nil, ErrCredentialsInvalid
	case 400:
		return nil, ErrCredentialsInvalid
	case 429:
		return nil, ErrRateLimited
	default:
		return nil, ErrUpstream
	}
}

// VerifyTotp 调用 POST /totp/verify 提交 TOTP passcode。
//
//	200 → *TokenPair, nil
//	401 → nil, ErrTotpInvalid
//	其他 → nil, ErrUpstream
func (c *Client) VerifyTotp(loginTicket, passcode string) (*TokenPair, error) {
	resp, err := c.doPost("/totp/verify", map[string]string{
		"login_ticket": loginTicket,
		"passcode":     passcode,
	})
	if err != nil {
		return nil, ErrUpstream
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		var tokenResp tokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			return nil, fmt.Errorf("解析 totp/verify 响应失败: %w", err)
		}
		return &TokenPair{
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
			ExpiresIn:    tokenResp.ExpiresIn,
			TokenType:    tokenResp.TokenType,
		}, nil
	case 400, 401:
		return nil, ErrTotpInvalid
	default:
		return nil, ErrUpstream
	}
}

// RefreshToken 调用 POST /oauth/token（grant_type=refresh_token）刷新 token。
//
//	200 → *TokenPair, nil
//	400/401 → nil, ErrTokenRefreshFailed
//	其他 → nil, ErrUpstream
func (c *Client) RefreshToken(refreshToken string) (*TokenPair, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.publicClientID},
	}

	// 先构造完整请求，再执行
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建 refresh 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, ErrUpstream
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		var tokenResp tokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			return nil, fmt.Errorf("解析 token 响应失败: %w", err)
		}
		return &TokenPair{
			AccessToken:  tokenResp.AccessToken,
			RefreshToken: tokenResp.RefreshToken,
			ExpiresIn:    tokenResp.ExpiresIn,
			TokenType:    tokenResp.TokenType,
		}, nil
	case 400, 401, 403:
		return nil, ErrTokenRefreshFailed
	default:
		return nil, ErrUpstream
	}
}

// RevokeToken 调用 POST /oauth/revoke 吊销 access_token。
//
//	200 → nil
//	其他 → nil, ErrUpstream
func (c *Client) RevokeToken(token string) error {
	form := url.Values{"token": {token}}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/oauth/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("创建 revoke 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUpstream
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ErrUpstream
	}
	return nil
}

// ─── HTTP helpers ────────────────────────────────────────────────────────────

// doGet 执行 GET 请求。调用方负责关闭 resp.Body。
func (c *Client) doGet(path, query string) (*http.Response, error) {
	u := c.baseURL + path
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return c.http.Do(req)
}

// doPost 执行 POST JSON 请求。调用方负责关闭 resp.Body。
func (c *Client) doPost(path string, body interface{}) (*http.Response, error) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return c.http.Do(req)
}

// doPostAuthed 执行带 Bearer token 的 POST 请求，返回解码后的 body。
func (c *Client) doPostAuthed(path, token string, body interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, ErrUpstream
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("读取响应失败: %w", err)
		}
		return b, nil
	case 401:
		return nil, ErrTokenExpired
	case 404:
		return nil, ErrTokenInvalid
	default:
		return nil, ErrUpstream
	}
}

// LookupUserByUsername 通过用户名查找 HRPAuth 用户。
// 调用 POST /user/lookup（公开端点，无需鉴权）。
//
//	200 → (uid, username, nil)
//	404 → (0, "", ErrUserNotFound)
//	其他 → (0, "", ErrUpstream)
func (c *Client) LookupUserByUsername(username string) (uid int64, uname string, err error) {
	resp, err := c.doPost("/user/lookup", map[string]string{
		"username": username,
	})
	if err != nil {
		return 0, "", ErrUpstream
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		var result struct {
			UID      int64  `json:"uid"`
			Username string `json:"username"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return 0, "", fmt.Errorf("解析 lookup 响应失败: %w", err)
		}
		if result.UID == 0 {
			return 0, "", ErrUserNotFound
		}
		return result.UID, result.Username, nil
	case 404:
		return 0, "", ErrUserNotFound
	default:
		return 0, "", ErrUpstream
	}
}

// LookupUserByUUID 通过 UUID 查找 HRPAuth 用户并获取 UID。
// 调用已缔约的 ops 级服务间接点 GET /services/users/{uuid}
// （HA-Contract hrpauth-business.yaml），携带 client_credentials
// 换取的服务令牌。此前的 POST /user/lookup-uuid 从未在 HRPAuth
// 实现过（HA-Contract knowledge/0006），已于方案 C 切换。
//
//	200        → (uid, nil)
//	404        → (0, ErrUserNotFound)
//	401 / 403  → (0, ErrServiceAuth)（服务凭据/级别问题，重试无意义）
//	凭据不可用 → (0, ErrServiceAuth)
//	其他       → (0, ErrUpstream)
func (c *Client) LookupUserByUUID(ctx context.Context, uuid string) (int64, error) {
	token, err := c.serviceToken(ctx)
	if err != nil {
		return 0, err
	}

	u := c.baseURL + "/services/users/" + url.PathEscape(uuid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("构造 services/users 请求失败: %w", ErrUpstream)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, ErrUpstream
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		// 统一信封：uid 在 data 内（顶层亦有扁平化副本，取 data 为准）。
		var result struct {
			Data struct {
				UID int64 `json:"uid"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return 0, fmt.Errorf("解析 services/users 响应失败: %w", ErrUpstream)
		}
		if result.Data.UID == 0 {
			return 0, ErrUserNotFound
		}
		return result.Data.UID, nil
	case 404:
		return 0, ErrUserNotFound
	case 401, 403:
		return 0, ErrServiceAuth
	default:
		return 0, ErrUpstream
	}
}

// tokenResponse 是 HRPAuth /oauth/token 和 /totp/verify 的通用响应。
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}
