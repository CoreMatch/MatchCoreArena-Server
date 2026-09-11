// Package auth 封装与 HRPAuth 的 OAuth2/OIDC 交互。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"MatchCoreArena-Server/config"
)

// TokenSet OAuth2 token 响应。
type TokenSet struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
}

// OAuthClient 封装与 HRPAuth 的 OAuth2 交互。
type OAuthClient struct {
	cfg        *config.HRPAuthConfig
	httpClient *http.Client
	verifier   *JWTVerifier
}

// NewOAuthClient 创建 OAuthClient，启动时做 OIDC discovery 验证可达性。
func NewOAuthClient(ctx context.Context, cfg *config.HRPAuthConfig) (*OAuthClient, error) {
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// 启动时校验 issuer 可达：尝试 GET issuer，非2xx 不 fail-fast（有些 IdP 不响应 GET /）
	// 但至少保证 token_endpoint 可达
	if cfg.TokenEndpoint != "" {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, cfg.TokenEndpoint, nil)
		resp, err := httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("hrpauth token_endpoint 不可达: %w", err)
		}
		resp.Body.Close()
	}

	verifier := NewJWTVerifier(cfg.Issuer)

	return &OAuthClient{
		cfg:        cfg,
		httpClient: httpClient,
		verifier:   verifier,
	}, nil
}

// AuthCodeURL 构建授权 URL，由前端跳转。
// 返回值: authorization_url, state, code_verifier (前端需保存 state 和 code_verifier 用于 callback)
func (c *OAuthClient) BuildLoginParams() (authURL string, state string, codeVerifier string, err error) {
	state = generateRandomBase64(32)
	codeVerifier = generateRandomBase64(32)
	codeChallenge := sha256Base64URL(codeVerifier)

	params := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.cfg.ClientID},
		"redirect_uri":          {c.cfg.RedirectURI},
		"scope":                 {strings.Join(c.cfg.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}

	authURL = c.cfg.AuthorizationEndpoint
	if strings.Contains(authURL, "?") {
		authURL += "&" + params.Encode()
	} else {
		authURL += "?" + params.Encode()
	}

	return authURL, state, codeVerifier, nil
}

// ExchangeCode 用 authorization_code + code_verifier 换取 token。
func (c *OAuthClient) ExchangeCode(ctx context.Context, code, codeVerifier string) (*TokenSet, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.cfg.RedirectURI},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
		"code_verifier": {codeVerifier},
	}

	return c.doTokenRequest(ctx, data)
}

// RefreshToken 用 refresh_token 换取新的 token。
func (c *OAuthClient) RefreshToken(ctx context.Context, refreshToken string) (*TokenSet, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	}

	return c.doTokenRequest(ctx, data)
}

// RevokeToken 吊销 token。
func (c *OAuthClient) RevokeToken(ctx context.Context, token string) error {
	if c.cfg.RevocationEndpoint == "" {
		return nil // 不支持吊销
	}

	data := url.Values{
		"token":         {token},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.RevocationEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("创建吊销请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("吊销请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("吊销失败，状态码: %d", resp.StatusCode)
	}
	return nil
}

// FetchUserInfo 获取用户信息（含 sub）。
func (c *OAuthClient) FetchUserInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	if c.cfg.UserInfoEndpoint == "" {
		return nil, fmt.Errorf("userinfo_endpoint 未配置")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.UserInfoEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 userinfo 请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("userinfo 请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("userinfo 失败，状态码: %d, body: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("解析 userinfo 响应失败: %w", err)
	}

	return result, nil
}

// Verifier 返回 JWT 验签器。
func (c *OAuthClient) Verifier() *JWTVerifier {
	return c.verifier
}

// Config 返回配置（用于 handler 层读取 client_id 等）。
func (c *OAuthClient) Config() *config.HRPAuthConfig {
	return c.cfg
}

func (c *OAuthClient) doTokenRequest(ctx context.Context, data url.Values) (*TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.TokenEndpoint, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("创建 token 请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token 请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取 token 响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token 请求失败，状态码: %d, body: %s", resp.StatusCode, string(body))
	}

	var tokenSet TokenSet
	if err := json.Unmarshal(body, &tokenSet); err != nil {
		return nil, fmt.Errorf("解析 token 响应失败: %w", err)
	}

	return &tokenSet, nil
}

func generateRandomBase64(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sha256Base64URL(s string) string {
	h := sha256.Sum256([]byte(s))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
