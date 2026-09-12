// Package auth 封装与 HRPAuth 的 HTTP 交互。
//
// 设计模式对齐 WinnerProxy/internal/hrpauth：使用标准 net/http + 结构化错误，
// 通过 doXxx helpers 保持每端点方法精简。
package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client 是 HRPAuth 的轻量 HTTP 客户端。进程级单实例，并发安全。
type Client struct {
	baseURL        string
	publicClientID string
	http           *http.Client
}

// NewClient 构造 Client。baseURL 为 HRPAuth 根地址（如 http://localhost:8080）。
// publicClientID 为 OAuth2 public client_id（如 hrpauth-webui），用于 refresh_token 等第一方流程。
// 启动时校验 HRPAuth /status 可达性。
func NewClient(baseURL, publicClientID string) (*Client, error) {
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
		baseURL:        baseURL,
		publicClientID: publicClientID,
		http:           httpClient,
	}, nil
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

// tokenResponse 是 HRPAuth /oauth/token 和 /totp/verify 的通用响应。
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}
