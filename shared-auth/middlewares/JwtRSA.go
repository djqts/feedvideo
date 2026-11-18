package middlewares

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// TokenPair 是返回给客户端的令牌对
type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

type RefreshTokenStore interface {
	Save(userID string, tokenDigest string, expiresAt int64) error
	Validate(userID string, tokenDigest string) error
	Delete(userID string, tokenDigest string) error
	Rotate(userID string, oldDigest string, newDigest string, expiresAt int64) error
}

// JWTManager 负责 JWT 的生成和验证
type JWTManager struct {
	privateKey        *rsa.PrivateKey
	publicKey         *rsa.PublicKey
	accessTokenTTL    time.Duration
	refreshTokenTTL   time.Duration
	refreshTokenStore RefreshTokenStore
	clock             func() time.Time
}

// generateRSAKeys 生成 RSA 密钥对并写入文件，仅用于工具使用
func generateRSAKeys() error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("generate RSA key: %w", err)
	}

	privateFile, err := os.Create("private.pem")
	if err != nil {
		return fmt.Errorf("create private.pem: %w", err)
	}
	defer privateFile.Close()

	if err := pem.Encode(privateFile, &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	}); err != nil {
		return fmt.Errorf("encode private key: %w", err)
	}

	publicFile, err := os.Create("public.pem")
	if err != nil {
		return fmt.Errorf("create public.pem: %w", err)
	}
	defer publicFile.Close()

	pubBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return fmt.Errorf("marshal public key: %w", err)
	}
	if err := pem.Encode(publicFile, &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	}); err != nil {
		return fmt.Errorf("encode public key: %w", err)
	}
	return nil
}

// LoadPrivateKey 从 PEM 文件加载 RSA 私钥，支持 PKCS#1 和 PKCS#8 格式
func LoadPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	// 尝试 PKCS#1
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	// 尝试 PKCS#8
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rsaKey, ok := key.(*rsa.PrivateKey); ok {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("key is not RSA")
	}
	return nil, fmt.Errorf("unsupported private key format")
}

// LoadPublicKey 从 PEM 文件加载 RSA 公钥
func LoadPublicKey(path string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key file: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA public key")
	}
	return rsaPub, nil
}

func NewJWTManager(privateKey *rsa.PrivateKey, publicKey *rsa.PublicKey, accessTTL, refreshTTL time.Duration, store RefreshTokenStore) (*JWTManager, error) {
	if privateKey == nil || publicKey == nil || store == nil {
		return nil, fmt.Errorf("privateKey, publicKey and store must not be nil")
	}
	return &JWTManager{
		privateKey:        privateKey,
		publicKey:         publicKey,
		accessTokenTTL:    accessTTL,
		refreshTokenTTL:   refreshTTL,
		refreshTokenStore: store,
		clock:             time.Now,
	}, nil
}

// generateJwt 生成并签名 JWT
func (j *JWTManager) generateJwt(claims jwt.MapClaims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(j.privateKey)
}

func (j *JWTManager) parseJwt(tokenString string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		if token.Method != jwt.SigningMethodRS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.publicKey, nil
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(), jwt.WithLeeway(30*time.Second), jwt.WithTimeFunc(j.clock))
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid JWT")
	}
	// 固定 issuer/audience/type/sub 校验（防跨站代签与类型混淆）
	if claims["iss"] != "feedvideo" {
		return nil, fmt.Errorf("invalid JWT issuer")
	}
	if aud, ok := claims["aud"].(string); !ok || aud != "feedvideo-web" {
		return nil, fmt.Errorf("invalid JWT audience")
	}
	if claims["type"] != "access" {
		return nil, fmt.Errorf("invalid JWT type")
	}
	sub, ok := claims["sub"].(string)
	userID, subjectErr := strconv.ParseInt(sub, 10, 64)
	if !ok || subjectErr != nil || userID <= 0 || strconv.FormatInt(userID, 10) != sub {
		return nil, fmt.Errorf("invalid JWT subject")
	}
	issuedAt, issuedErr := claims.GetIssuedAt()
	expiresAt, expiresErr := claims.GetExpirationTime()
	if issuedErr != nil || expiresErr != nil || issuedAt == nil || expiresAt == nil || !expiresAt.After(issuedAt.Time) {
		return nil, fmt.Errorf("invalid JWT lifetime")
	}
	return claims, nil
}

func generateRefreshToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// sha256Digest 计算输入字符串的 SHA-256 摘要，用于存储
func sha256Digest(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (j *JWTManager) GenerateTokenPair(userID string) (*TokenPair, error) {
	now := j.clock()

	// Refresh token
	refreshToken, err := generateRefreshToken()
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}
	refreshExpireAt := now.Add(j.refreshTokenTTL).Unix()
	digest := sha256Digest(refreshToken)
	if err := j.refreshTokenStore.Save(userID, digest, refreshExpireAt); err != nil {
		return nil, fmt.Errorf("save refresh token: %w", err)
	}

	return j.newPairWith(userID, refreshToken)
}

// newPairWith 用给定的 refresh token 组装 TokenPair（refresh 已由调用方持久化）。
func (j *JWTManager) newPairWith(userID, refreshToken string) (*TokenPair, error) {
	now := j.clock()
	accessExpireAt := now.Add(j.accessTokenTTL)
	accessClaims := jwt.MapClaims{
		"sub":  userID,
		"iat":  now.Unix(),
		"exp":  accessExpireAt.Unix(),
		"type": "access",
		"iss":  "feedvideo",
		"aud":  "feedvideo-web",
	}
	accessToken, err := j.generateJwt(accessClaims)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}
	return &TokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(j.accessTokenTTL.Seconds()),
		TokenType:    "Bearer",
	}, nil
}

func (j *JWTManager) RefreshAccessToken(userID string, refreshToken string) (*TokenPair, error) {
	newRefresh, err := j.RotateRefreshToken(userID, refreshToken)
	if err != nil {
		return nil, fmt.Errorf("rotate refresh token: %w", err)
	}
	return j.newPairWith(userID, newRefresh)
}

// RotateRefreshToken 原子轮换 refresh token：旧 token 有效则签发新 token 并删除旧值；
// 旧 token 已不存在（重用/过期）则返回 ErrTokenReused（调用方可据此刻画账号风险）。
func (j *JWTManager) RotateRefreshToken(userID string, oldToken string) (string, error) {
	oldDigest := sha256Digest(oldToken)
	newToken, err := generateRefreshToken()
	if err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}
	newDigest := sha256Digest(newToken)
	expiresAt := j.clock().Add(j.refreshTokenTTL).Unix()
	if err := j.refreshTokenStore.Rotate(userID, oldDigest, newDigest, expiresAt); err != nil {
		return "", err
	}
	return newToken, nil
}

func (j *JWTManager) DeleteRefreshToken(userID, refreshToken string) error {
	digest := sha256Digest(refreshToken)
	return j.refreshTokenStore.Delete(userID, digest)
}

func AuthMiddleware(jwtManager *JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		accessToken, err := c.Cookie("access_token")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing access token"})
			return
		}
		claims, err := jwtManager.parseJwt(accessToken)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid access token"})
			return
		}
		if !matchExpectedActor(c, claims["sub"].(string)) {
			return
		}
		c.Set("userID", claims["sub"])
		c.Next()
	}
}

// OptionalAuthMiddleware permits anonymous reads unless the page expects an
// account. A supplied actor must match the verified access-cookie subject.
func OptionalAuthMiddleware(jwtManager *JWTManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		accessToken, err := c.Cookie("access_token")
		if err != nil {
			if matchExpectedActor(c, "") {
				c.Next()
			}
			return
		}
		claims, err := jwtManager.parseJwt(accessToken)
		if err != nil {
			if matchExpectedActor(c, "") {
				c.Next()
			}
			return
		}
		if !matchExpectedActor(c, claims["sub"].(string)) {
			return
		}
		c.Set("userID", claims["sub"])
		c.Next()
	}
}
