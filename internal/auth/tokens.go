package auth

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

// ServiceToken 定义服务凭据的配置结构。
type ServiceToken struct {
	Token  string   `yaml:"token"`
	UID    int64    `yaml:"uid"`
	Scopes []string `yaml:"scopes"`
}

// TokenStore 持有所有已配置的系统凭据。
type TokenStore struct {
	Tokens map[string]ServiceToken `yaml:"tokens"`
}

// LoadTokenStore 从指定路径加载 tokens.yaml。
// 如果文件不存在，则会自动创建一个包含随机全权限 Token 的默认文件。
func LoadTokenStore(path string) (*TokenStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return createDefaultTokenStore(path)
		}
		return nil, fmt.Errorf("读取 tokens 配置文件失败: %w", err)
	}

	var store TokenStore
	if err := yaml.Unmarshal(data, &store); err != nil {
		return nil, fmt.Errorf("解析 tokens 配置文件失败: %w", err)
	}

	if store.Tokens == nil {
		store.Tokens = make(map[string]ServiceToken)
	}

	return &store, nil
}

// createDefaultTokenStore 创建包含默认全权限 Token 的配置文件。
func createDefaultTokenStore(path string) (*TokenStore, error) {
	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建配置目录失败: %w", err)
	}

	store := &TokenStore{
		Tokens: map[string]ServiceToken{
			"super-admin": {
				Token:  generateRandomToken(32),
				UID:    -1, // 运维/全权限 UID
				Scopes: []string{"maintenance"},
			},
			"match-reporter": {
				Token:  generateRandomToken(32),
				UID:    -100,
				Scopes: []string{"match:report", "user:read"},
			},
		},
	}

	data, err := yaml.Marshal(store)
	if err != nil {
		return nil, fmt.Errorf("序列化默认 tokens 失败: %w", err)
	}

	content := "# MatchCoreArena 高权限服务凭据配置\n" +
		"# 此文件在初始化时自动生成，包含一个随机的全权限 Token。\n" +
		"# 请妥善保管 super-admin token。\n\n" +
		string(data)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("写入默认 tokens 配置文件失败: %w", err)
	}

	return store, nil
}

// FindByToken 根据原始 token 查找对应的服务凭据。
func (s *TokenStore) FindByToken(token string) (*ServiceToken, bool) {
	for _, st := range s.Tokens {
		if st.Token == token {
			return &st, true
		}
	}
	return nil, false
}

// generateRandomToken 生成指定字节长度的随机 16 进制字符串。
func generateRandomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}
