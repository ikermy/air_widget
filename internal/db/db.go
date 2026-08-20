package db

import (
	"air_widget/internal/domain"
	"air_widget/internal/repository"
	"air_widget/internal/repository/mysql"
	"context"
	"encoding/json"

	"github.com/ikermy/air_common/pkg/comdb"
	"github.com/ikermy/air_logger/v2/pkg/logger"

	_ "github.com/go-sql-driver/mysql"
)

// DB обёртка соединения с базой данных и репозиториями
type DB struct {
	*comdb.DB
	repo repository.Repository
}

func (d *DB) GetWidgetBotUser(userID uint32) (domain.WidgetBotData, error) {
	return d.repo.Internal.GetWidgetBotUser(userID)
}

func (d *DB) GetWidgetBotUsers() ([]domain.WidgetBotData, error) {
	return d.repo.Internal.GetWidgetBotUsers()
}

func (d *DB) ReadResponderName(respId uint64) (json.RawMessage, error) {
	return d.repo.Internal.ReadResponderName(respId)
}

// New создаёт подключение к БД и инициализирует репозитории
func New(parent context.Context) (*DB, error) {
	base, err := comdb.New(parent)
	if err != nil {
		return nil, err
	}
	repo, err := mysql.New(base)
	if err != nil {
		return nil, err
	}
	return &DB{
		DB:   base,
		repo: repo,
	}, nil
}

// Repo возвращает набор репозиториев
func (d *DB) Repo() repository.Repository {
	return d.repo
}

func (d *DB) HandlerClose() {
	go func() {
		// Получаю сигнал о завершении работы от главного контекста приложения
		<-d.MainCTX().Done()
		logger.Info("DB: контекст отменен, ожидаю завершения всех операций...")
		// Ожидаем сигнал о завершении от компонентов работающих с ДБ
		<-domain.UsersDB
		logger.Info("DB: все модули работающие с БД завершили работу, продолжаю остановку...")

		if err := d.Close(); err != nil {
			logger.Error("DB: ошибка при закрытии: %v", err)
		}

		close(domain.Exit)
	}()
}
