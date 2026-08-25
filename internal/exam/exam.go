package exam

import (
	"air_widget/internal/db"
	"air_widget/internal/domain"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/ikermy/air-common/pkg/com"
	"github.com/ikermy/air-common/pkg/rpc/proto"
)

type Req struct {
	UserId uint32
	Key    string
	RespId uint64
	Origin string
}

type WidgetCodeData struct {
	UserID       uint32
	ExamKey      string
	ExpiresAt    int64
	NeverExpires bool
	AllowedUrls  []string
	JTI          string
}

type Exam struct {
	ctx         context.Context
	cancel      context.CancelFunc
	db          *db.DB
	rpc         ORCClient
	userBalance sync.Map
	balanceTTL  sync.Map
	tokenCache  sync.Map
}

type Token struct {
	UserId uint32
	ReapId uint64
	Origin string
	JTI    string
}

// ORCClient интерфейс для получения MasterKey пользователя через Landing gRPC
type ORCClient interface {
	WidgetNewToken(ctx context.Context, userID uint32, respID uint64, expired time.Duration, origin string, jti string) (string, error)
	WidgetParseToken(ctx context.Context, tokenString string) (uint32, uint64, string, string, error)
	WidgetParseExpiredToken(ctx context.Context, tokenString string) (*proto.WidgetTokenData, error)
	WidgetNewCode(ctx context.Context, userId uint32, examKey string, expiresAt int64, neverExpires bool, allowedUrls []string, jti string) (string, error)
	WidgetParseCode(ctx context.Context, token string) (*proto.WidgetCodeData, error)
}

func New(parent context.Context, d *db.DB, b ORCClient) (*Exam, error) {
	ctx, cancel := context.WithCancel(parent)
	return &Exam{
		ctx:    ctx,
		cancel: cancel,
		db:     d,
		rpc:    b,
	}, nil
}

func (e *Exam) GenerateToken(userID uint32, responderId uint64, origin, jti string) (string, error) {
	signedToken, err := e.helperNewToken(userID, responderId, domain.AuthTokenTTL*time.Minute, origin, jti)
	if err != nil {
		return "", fmt.Errorf("ошибка подписи токена: %v", err)
	}

	return signedToken, nil
}

func (e *Exam) ParseExpiredToken(tokenString string) (*Token, error) {
	data, err := e.rpc.WidgetParseExpiredToken(e.ctx, tokenString)
	if err != nil {
		return &Token{}, fmt.Errorf("ошибка парсинга токена %v", err)
	}

	return &Token{
		UserId: data.UserId,
		ReapId: data.RespId,
		Origin: data.Origin,
		JTI:    data.Jti,
	}, nil
}

func (e *Exam) UpdateToken(t *Token) (string, error) {
	// Получаем токен с подписью от orc
	signedToken, err := e.helperNewToken(t.UserId, t.ReapId, domain.AuthTokenTTL*time.Minute, t.Origin, t.JTI)
	if err != nil {
		return "", err
	}
	return signedToken, nil
}

func (e *Exam) ParseToken(tokenString string) (*Token, error) {
	userID, respID, origin, jti, err := e.helperParseToken(tokenString)

	if err != nil {
		return nil, fmt.Errorf("ошибка парсинга токена: %v", err)
	}

	return &Token{
		UserId: userID,
		ReapId: respID,
		Origin: origin,
		JTI:    jti,
	}, nil
}

func (e *Exam) ExamUser(examData Req) (string, error) {
	// Проверяю что realUserId не равен 0
	if examData.UserId == 0 {
		return "", fmt.Errorf("получен пустой userId")
	}
	if examData.Origin == "" {
		return "", fmt.Errorf("получен пустой origin")
	}
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", fmt.Errorf("ошибка генерации jti: %w", err)
	}

	// Создаю токен jwt
	token, err := e.GenerateToken(examData.UserId, examData.RespId, examData.Origin, hex.EncodeToString(jtiBytes))
	if err != nil {
		return "", fmt.Errorf("error generating token: %v", err)
	}

	return token, nil
}

// CheckUserSubscription проверяет подписку пользователя
func (e *Exam) CheckUserSubscription(userId uint32) error {
	return com.CheckUserSubscription(e.db, userId)
}

func (e *Exam) WidgetNewCode(userID uint32, examKey string, expiresAt int64, neverExpires bool, allowedUrls []string, jti string) (string, error) {
	return e.rpc.WidgetNewCode(e.ctx, userID, examKey, expiresAt, neverExpires, allowedUrls, jti)
}

func (e *Exam) WidgetParseCode(tokenString string) (*WidgetCodeData, error) {
	data, err := e.rpc.WidgetParseCode(e.ctx, tokenString)
	if err != nil {
		return nil, err
	}
	return &WidgetCodeData{
		UserID: data.UserId, ExamKey: data.ExamKey, ExpiresAt: data.ExpiresAt,
		NeverExpires: data.NeverExpires, AllowedUrls: data.AllowedUrls, JTI: data.Jti,
	}, nil
}

func (e *Exam) helperNewToken(userID uint32, respID uint64, expired time.Duration, origin, jti string) (string, error) {
	token, err := e.rpc.WidgetNewToken(e.ctx, userID, respID, expired, origin, jti)
	if err != nil {
		return "", fmt.Errorf("ошибка создания токена: %v", err)
	}

	return token, nil
}

func (e *Exam) helperParseToken(tokenString string) (uint32, uint64, string, string, error) {
	userID, respID, origin, jti, err := e.rpc.WidgetParseToken(e.ctx, tokenString)
	if err != nil {
		return 0, 0, "", "", err
	}

	return userID, respID, origin, jti, nil
}
