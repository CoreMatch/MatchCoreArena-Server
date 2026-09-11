package auth

// UserResult 是 HRPAuth /user 端点的用户信息。
type UserResult struct {
	UID        int64  `json:"uid"`
	Email      string `json:"email"`
	Username   string `json:"username"`
	Avatar     string `json:"avatar"`
	Verified   bool   `json:"verified"`
	MojangUUID string `json:"mojang_uuid,omitempty"`
}

// LoginTicketResult 是 HRPAuth /oauth/login-ticket 的响应。
// 直接认证成功时含 AccessToken/RefreshToken；
// 需要 TOTP 时含 TotpRequired/LoginTicket。
type LoginTicketResult struct {
	TotpRequired bool   `json:"totp_required"`
	LoginTicket  string `json:"login_ticket,omitempty"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	Scope        string `json:"scope,omitempty"`
	UID          string `json:"uid,omitempty"`
}

// TokenPair 是 OAuth2 token 对，用于 /api/auth/totp-verify 和 /api/auth/refresh 响应。
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}
