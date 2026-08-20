package widget

import (
	"air_widget/internal/db"
	"air_widget/internal/domain"
	"air_widget/internal/exam"
	"air_widget/internal/metrics"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air_common/pkg/com"
	"github.com/ikermy/air_common/pkg/comdb"
	"github.com/ikermy/air_common/pkg/crm"
	"github.com/ikermy/air_common/pkg/crypto"
	"github.com/ikermy/air_common/pkg/endpoint"
	"github.com/ikermy/air_common/pkg/model"
	"github.com/ikermy/air_common/pkg/operator"
	"github.com/ikermy/air_common/pkg/rpc"
	"github.com/ikermy/air_logger/v2/pkg/logger"
	"github.com/redis/go-redis/v9"
)

type Inter interface {
	//GetBotUsername(userID uint32) string
	//StopUserBot(userID uint32) error
	//RestartUserBot(userID uint32) error
	//StartUserBot(userID uint32) error
}

type Model = model.Inter
type Endpoint = endpoint.Inter
type Operator = operator.Inter
type CRM = crm.Inter

type Exam interface {
	ExamUser(examData exam.Req) (string, error)
	ParseToken(tokenString string) (*exam.Token, error)
	UpdateToken(t *exam.Token) (string, error)
	ParseExpiredToken(tokenString string) (*exam.Token, error)
	CheckUserSubscription(userId uint32) error
	WidgetNewCode(userID uint32, examKey string, expiresAt int64, neverExpires bool, allowedUrls []string, jti string) (string, error)
	WidgetParseCode(tokenString string) (*exam.WidgetCodeData, error)
}

// ORCClient интерфейс для получения MasterKey пользователя через Landing gRPC
type ORCClient interface {
	GetUserMasterKey(ctx context.Context, userId uint32) ([32]byte, error)
}

var StartCh = make(chan model.StartCh, 1) // Канал для запуска горутины слушателя

type Web struct {
	exam Exam
	Gin  *gin.Engine
}

type Bot struct {
	userID uint32
	ctx    context.Context
	cancel context.CancelFunc
	// Модель ассистента
	assist model.Assistant
	// Карта для хранения ID пользователей которые уже взаимодействовали с ботом
	knownUsers      sync.Map // Используем sync.Map для потокобезопасного хранения известных пользователей
	messageReceived chan struct{}
	shutdownCh      chan struct{} // Канал для уведомления о завершении
	end             Endpoint
	db              *db.DB
	mod             Model
	crm             *crm.User
	firstCache      CacheMethods // при первоначальной загрузке тащит первые контакты из redis
	// ОБЩИЙ ЛИМИТ: общее количество соединений к боту
	activeConnections uint8 // Общий счетчик активных соединений к боту
	connectionsMutex  sync.RWMutex
	maxConnections    uint8 // Максимум соединений к боту
	// Добавляю User для доступа к методам
	user *User
}

// User представляет все пользовательские Bot боты
type User struct {
	ctx    context.Context
	cancel context.CancelFunc
	web    *Web
	end    Endpoint
	crm    CRM
	db     *db.DB
	rpc    ORCClient
	mod    Model
	bot    sync.Map // key: userID uint32
	// Режим оператора по диалогам
	operatorModeByDialog sync.Map // key: dialogId (uint64), value: bool
	op                   Operator
	firstCache           CacheMethods
}

// New создает новый экземпляр аутентификатора для Bot ботов
func New(parent context.Context, d *db.DB, m Model, e Endpoint, c CRM, rpc *rpc.Client, redisClient redis.UniversalClient) *User {
	ctx, cancel := context.WithCancel(parent)
	gin.SetMode(gin.ReleaseMode)
	gin.DefaultWriter = io.Discard
	g := gin.Default()
	x, err := exam.New(ctx, d, rpc)
	if err != nil {
		logger.Fatal(err)
	}
	return &User{
		web: &Web{
			exam: x,
			Gin:  g,
		},
		ctx:        ctx,
		cancel:     cancel,
		end:        e,
		db:         d,
		mod:        m,
		crm:        c,
		rpc:        rpc,
		bot:        sync.Map{}, // key: userID uint32
		firstCache: newRedisFirstInteractionCache(redisClient),
	}
}

func (u *User) SetOperator(op Operator) { u.op = op }

func (u *User) maxConnect(token string) (uint8, error) {
	maxConnect := uint8(0) // Максимальное число коннектов к боту в зависимости от разрешённых урлов х2
	if botData, err := domain.ParseWidgetConfig(token, time.Now()); err != nil {
		return maxConnect, fmt.Errorf("ошибка парсинга конфигурации Widget")
	} else {
		maxConnect = uint8(len(botData.AllowedUrls) * 2)
	}
	return maxConnect, nil
}

// StartBots запускает Bot User
func (u *User) StartBots() error {
	logger.Info("Запуск Widget ботов...")

	// Получаем список пользователей с включенными ботами
	userDetails, err := u.db.GetWidgetBotUsers()
	if err != nil {
		return fmt.Errorf("ошибка получения пользователей: %w", err)
	}

	// Запускаем ботов для каждого пользователя
	for _, user := range userDetails {
		// Пропускаем отключенных пользователей или пользователей без данных
		if !user.WaUserBotEnabled || user.Data == "" {
			metrics.ObserveBotLifecycle(user.UserId, "start", "skipped")
			logger.Warn("Пропуск пользователя: бот отключен или не настроен", user.UserId)
			continue
		}

		if err := u.createBot(user.UserId); err != nil {
			metrics.ObserveBotLifecycle(user.UserId, "start", "error")
			logger.Error("Ошибка создания бота: %v", err, user.UserId)
			continue
		}

		logger.Info("Бот успешно запущен", user.UserId)
		metrics.ObserveBotLifecycle(user.UserId, "start", "success")
	}

	logger.Info("Все боты запущены")
	return nil
}

func (u *User) decryptData(userID uint32, encryptData string) (string, error) {
	// Если поле зашифровано MasterKey — расшифровываем
	if crypto.IsEncryptedWithMasterKey(encryptData) {
		if u.rpc == nil {
			metrics.ObserveDecryptError(userID, "missing_orc_client")
			logger.Error("orcClient не инициализирован, невозможно расшифровать токен", userID)
			return "", fmt.Errorf("orcClient не инициализирован")
		}

		mk, err := u.rpc.GetUserMasterKey(u.ctx, userID)
		if err != nil {
			metrics.ObserveDecryptError(userID, "master_key")
			logger.Error("Ошибка получения MasterKey: %v (требуется вход на Landing)", err, userID)

			notifyMsg := com.CarpCh{
				Event:  "reauth-userkey",
				UserID: userID,
			}
			if err := u.end.SendNotification(notifyMsg); err != nil {
				logger.Error("Ошибка отправки уведомления о повторной аутентификации %v", err, userID)
			}

			return "", fmt.Errorf("ошибка получения MasterKey для пользователя %d: %w", userID, err)
		}

		decrypted, err := crypto.DecryptFieldWithMasterKey(mk, encryptData)
		if err != nil {
			metrics.ObserveDecryptError(userID, "decrypt")
			logger.Error("Ошибка расшифровывания токена: %v", err, userID)
			return "", fmt.Errorf("ошибка расшифровывания токена: %w", err)
		}
		encryptData = decrypted
	}

	return encryptData, nil
}

func (u *User) StopBots() {
	logger.Info("Bot: получен сигнал завершения, остановка ботов...")

	done := make(chan struct{})
	var wg sync.WaitGroup

	go func() {
		u.bot.Range(func(key, value interface{}) bool {
			userId := key.(uint32)
			wg.Add(1)
			go func(id uint32) {
				defer wg.Done()
				u.StopUserBot(id)
			}(userId)
			return true
		})

		wg.Wait()
		close(done)

		// Сохраняем данные при завершении
		if u.mod != nil {
			u.mod.SaveAllContextDuringExit()
			logger.Debug("Bot: все данные сохранены")
		}
	}()

	select {
	case <-done:
		logger.Debug("Bot: Все пользовательские боты успешно остановлены")
	case <-time.After(5 * time.Second):
		logger.Debug("Bot: Тайм-аут при остановке ботов, принудительное завершение")
	}
}

// StopBot останавливает бота для конкретного пользователя
func (u *User) StopBot(userID uint32) error {
	logger.Info("Остановка бота", userID)

	// Проверяем, существует ли бот
	_, exists := u.bot.Load(userID)
	if !exists {
		logger.Warn("Бот не найден или уже остановлен", userID)
		return fmt.Errorf("бот не найден для пользователя %d", userID)
	}

	// Останавливаем бота
	u.StopUserBot(userID)

	logger.Info("Бот успешно остановлен", userID)
	return nil
}

// StartBot запускает бота для конкретного пользователя
func (u *User) StartBot(userID uint32) error {
	logger.Info("Запуск бота", userID)

	err := u.createBot(userID)
	if err != nil {
		return fmt.Errorf("ошибка запуска бота: %w", err)
	}

	logger.Info("Бот успешно запущен", userID)
	return nil
}

func (u *User) createBot(userID uint32) error {
	// Получаем данные пользователя из БД
	userData, err := u.db.GetWidgetBotUser(userID)
	if err != nil {
		return fmt.Errorf("ошибка получения данных пользователя: %w", err)
	}

	// Проверяем, что бот включен и есть данные
	if !userData.WaUserBotEnabled || userData.Data == "" {
		return fmt.Errorf("бот отключен или не настроен для пользователя %d", userID)
	}

	token, err := u.decryptData(userID, userData.Data)
	if err != nil {
		return err
	}

	if _, err = domain.ParseWidgetConfig(token, time.Now()); err != nil {
		return fmt.Errorf("некорректная конфигурация Widget: %w", err)
	}

	maxConnect, err := u.maxConnect(token)
	if err != nil {
		return fmt.Errorf("ошибка при определении максимального числа подключений: %w", err)
	}

	// Проверяем подписку
	if err = com.CheckUserSubscription(u.db, userID); err != nil {
		var commonErr *com.SubscriptionError
		ok := errors.As(err, &commonErr)
		if ok {
			errorCode := fmt.Sprintf("%d", commonErr.Code)
			msg := com.CarpCh{
				Event:      "subscription",
				UserName:   "",
				AssistName: "",
				Target:     errorCode,
				UserID:     userID,
			}
			if notifErr := u.end.SendNotification(msg); notifErr != nil {
				logger.Error("Ошибка отправки уведомления о подписке: %v", notifErr, userID)
			}

			// Выключаем все каналы пользователя
			if dbErr := u.db.DisableAllUserChannel(userID); dbErr != nil {
				logger.Error("Ошибка при выключении каналов пользователя: %v", dbErr, userID)
			}
		}
		return fmt.Errorf("ошибка проверки подписки: %w", err)
	}
	// Создаем модель ассистента
	assistModel := u.createAssistantModel(userData)

	// Создаем бота
	bot, err := u.createOrUpdateBot(userID, maxConnect, assistModel)
	if err != nil {
		return fmt.Errorf("ошибка создания бота: %w", err)
	}

	// Сохраняем бота в карте
	u.bot.Store(userID, bot)

	return nil
}

// RestartBot перезапускает бота для конкретного пользователя (перезагружает данные из БД и пересоздает бота)
func (u *User) RestartBot(userID uint32) error {
	logger.Info("Перезапуск бота", userID)

	err := u.createBot(userID)
	if err != nil {
		return fmt.Errorf("ошибка перезапуска бота: %w", err)
	}

	logger.Info("Бот успешно перезапущен", userID)
	return nil
}

// StopUserBot останавливает бота для конкретного пользователя и удаляет его из карты
func (u *User) StopUserBot(userId uint32) {
	// Атомарно получаем и удаляем бота из карты
	// Это гарантирует, что только один поток сможет остановить бота
	value, exists := u.bot.LoadAndDelete(userId)
	if !exists {
		metrics.ObserveBotLifecycle(userId, "stop", "not_found")
		logger.Error("Бот не найден или уже остановлен", userId)
		return
	}
	bot := value.(*Bot)

	logger.Info("Остановка бота...", userId)

	// Уведомляем все SSE соединения о завершении
	close(bot.shutdownCh)

	// Ждем завершения активных соединений
	timeout := time.After(5 * time.Second)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		bot.connectionsMutex.RLock()
		activeConns := bot.activeConnections
		bot.connectionsMutex.RUnlock()

		if activeConns == 0 {
			break
		}

		select {
		case <-u.ctx.Done():
			logger.Error("Контекст пользователя отменен при остановке бота (%d активных)", activeConns, userId)
			goto cleanup
		case <-timeout:
			logger.Error("Timeout при ожидании закрытия соединений (%d активных)", activeConns, userId)
			goto cleanup
		case <-ticker.C:
			continue
		}
	}

cleanup:
	// Отменяем контекст
	if bot.cancel != nil {
		bot.cancel()
		logger.Debug("Контекст отменен", userId)
	}

	logger.Info("Бот остановлен", userId)
	metrics.ObserveBotLifecycle(userId, "stop", "success")
}

// Проверяет подписку пользователя
func (b *Bot) checkUserSubscription() error {
	err := com.CheckUserSubscription(b.db, b.userID)
	if err != nil {
		var commonErr *com.SubscriptionError
		ok := errors.As(err, &commonErr)
		if ok {
			// Форматируем сообщение, включив код ошибки
			errorCode := fmt.Sprintf("%d", commonErr.Code)
			msg := com.CarpCh{
				Event:      "subscription",
				UserName:   "",
				AssistName: "",
				Target:     errorCode,
				UserID:     b.userID,
			}
			err := b.end.SendNotification(msg)
			if err != nil {
				logger.Error("Ошибка отправки уведомления о подписке: %v", err, b.userID)
			}
			//common.SendEvent(w.UserId, "subscription", "", "", errorCode)

			// Выключаю все каналы пользователя
			if dbErr := b.db.DisableAllUserChannel(b.userID); dbErr != nil {
				logger.Error("Ошибка при выключении каналов: %v", dbErr, b.userID)
			}
		} else {
			logger.Error("Неизвестная ошибка проверки подписки: %v", err)
		}
		logger.Error("Ошибка проверки подписки: %v", err, b.userID)
		return err
	}
	return nil
}

// Создает модель ассистента
func (u *User) createAssistantModel(user domain.WidgetBotData) model.Assistant {
	return model.Assistant{
		UserID:     user.UserId,
		Provider:   user.Provider,
		AssistName: user.AssistName,
		AssistId:   user.AssistantId,
		Espero:     user.Espero,
		Limit:      user.AskLimit,
		Ignore:     user.Ignore,
		Events: model.Notifications{
			Start:  user.Events.Start,
			End:    user.Events.End,
			Target: user.Events.Target,
		},
		Metas: model.Target{
			MetaAction: user.MetaAction,
			Triggers:   user.Triggers,
		},
	}
}

// Создает нового бота или обновляет существующего
func (u *User) createOrUpdateBot(userID uint32, maxConnect uint8, assist model.Assistant) (*Bot, error) {
	// Проверяем, существует ли уже такой бот
	value, exists := u.bot.Load(userID)

	// Если модель существует, всегда останавливаем её
	if exists {
		existingBot := value.(*Bot)
		if existingBot.cancel != nil {
			existingBot.cancel()
		}
	}

	return u.initializeBot(userID, maxConnect, assist)
}

// Инициализирует нового бота с заданными параметрами
func (u *User) initializeBot(userID uint32, maxConnect uint8, assist model.Assistant) (*Bot, error) {
	// Создаем контекст с возможностью отмены
	ctx, cancel := context.WithCancel(u.ctx)

	// Создаем нового бота
	bot := &Bot{
		userID:            userID,
		ctx:               ctx,
		cancel:            cancel,
		assist:            assist,
		messageReceived:   make(chan struct{}),
		shutdownCh:        make(chan struct{}),
		end:               u.end,
		db:                u.db,
		mod:               u.mod,
		activeConnections: 0,          // Инициализируем счетчик активных соединений
		maxConnections:    maxConnect, // Максимум соединений к боту - НУЖНО БООООЛЬШЕ ТЕСТИТЬ!
		user:              u,          // Ссылка на User для доступа к методам
		firstCache:        u.firstCache,
	}

	// Загружаю из редис контакты у которых было первое взаимодействие с этим ботом
	go bot.preloadFirstInteraction()

	// Инициализируем CRM для этого пользователя
	crmUser, debug, err := u.crm.Init(userID)
	if err != nil {
		// Может быть не ошибка, просто не настроена или отключена CRM
		logger.Warn("Ошибка инициализации CRM: %v", err, userID)
	}

	if debug != "" {
		logger.Debug("User инициализирован с настройками CRM: %s", debug, userID)
	}

	bot.crm = crmUser

	return bot, nil
}

func (b *Bot) preloadFirstInteraction() {
	if b.firstCache == nil {
		return
	}

	senderIDs, err := b.firstCache.LoadUser(b.ctx, b.userID)
	if err != nil {
		logger.Warn("Redis: не удалось прогреть firstInteraction: %v", err, b.userID)
		return
	}

	for _, senderID := range senderIDs {
		b.knownUsers.Store(uint64(senderID), false) // ключ uint64, как в handleMessage
	}

	if len(senderIDs) > 0 {
		logger.Debug("Redis: прогрето firstInteraction=%d", len(senderIDs), b.userID)
	}
}

func (u *User) connectionLimitMiddleware() gin.HandlerFunc {
	var totalConnections int64
	const maxTotalConnections = 1000

	return func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/events") {
			current := atomic.LoadInt64(&totalConnections)
			if current >= maxTotalConnections {
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Server overloaded"})
				c.Abort()
				return
			}
			atomic.AddInt64(&totalConnections, 1)
			defer atomic.AddInt64(&totalConnections, -1)
		}
		c.Next()
	}
}

// initializeUserChannels создаёт каналы связи для пользователя
func (b *Bot) initializeUserChannels(senderID uint64, senderName string) (uint64, error) {
	startedAt := time.Now()
	dialogId, err := b.db.GetOrSetTreadAndResponder(b.userID, senderID, senderName, comdb.Widget)
	if err != nil {
		metrics.ObserveUserChannelInit(b.userID, "error", startedAt)
		return 0, fmt.Errorf("ошибка ID диалога: %w", err)
	}

	usrMod, err := b.mod.GetOrSetRespGPT(b.assist, dialogId, senderID, senderName)
	if err != nil {
		// Проверяем, является ли это сообщением о пустых данных
		if strings.Contains(err.Error(), "получены пустые данные") {
			// Это ожидаемое состояние - просто продолжаем
			logger.Debug("Создание новой модели для диалога %d с пользователем %s", dialogId, senderName)
		} else {
			metrics.ObserveUserChannelInit(b.userID, "error", startedAt)
			return 0, fmt.Errorf("ошибка модели пользователя: %w", err)
		}
	}

	usrCh, err := b.mod.GetCh(senderID)
	if err != nil {
		//return fmt.Errorf("ошибка канала пользователя: %w", err)
		if strings.Contains(err.Error(), "получены пустые данные") {
			logger.Debug("Инициализация каналов для нового пользователя %s", senderName)
		} else {
			metrics.ObserveUserChannelInit(b.userID, "error", startedAt)
			return 0, fmt.Errorf("ошибка канала пользователя: %w", err)
		}
	}

	// Отправляем данные в канал запуска
	startCh := model.StartCh{
		Ctx:     b.ctx,
		Model:   usrMod,
		Chanel:  usrCh,
		TreadId: dialogId,
		RespId:  senderID,
	}

	select {
	case StartCh <- startCh:
		//log.Printf("Данные отправлены в StartCh для пользователя %d", senderID)
	default:
		metrics.ObserveUserChannelInit(b.userID, "error", startedAt)
		return 0, errors.New("ошибка отправки данных в StartCh")
	}
	metrics.ObserveUserChannelInit(b.userID, "success", startedAt)

	return dialogId, nil
}

// handleMessage обрабатывает входящее сообщение от пользователя
func (b *Bot) handleMessage(senderID uint64, senderName string, messageContent string) error {
	startedAt := time.Now()
	metrics.ObserveMessageReceived(b.userID, "user")
	defer metrics.ObserveMessageProcessingStage(b.userID, "total", startedAt)
	// Проверяем, является ли пользователь новым.
	// knownUsers прогревается из Redis в preloadFirstInteraction, которая гарантированно ?!
	// завершается раньше, чем бот становится доступным (до u.crm.Init в initializeBot).
	_, isKnown := b.knownUsers.Load(senderID)
	first := !isKnown

	// Если это новый пользователь
	if first {
		// Сначала фиксируем контакт (Redis + in-memory), затем уведомляем.
		// Такой порядок гарантирует: даже если уведомление не дойдёт,
		// повторного «первого» взаимодействия после рестарта не будет.
		b.knownUsers.Store(senderID, false)
		if b.firstCache != nil {
			if err := b.firstCache.Set(b.ctx, b.userID, int64(senderID)); err != nil {
				logger.Warn("Redis: не удалось сохранить firstInteraction senderID=%d: %v",
					senderID, err, b.userID)
			}
		}

		// Отправляем уведомление о начале диалога, если включено
		if b.assist.Events.Start {
			msgNotif := com.CarpCh{
				Event:      "start",
				UserName:   senderName,
				AssistName: b.assist.AssistName,
				Target:     "",
				UserID:     b.userID,
			}
			if err := b.end.SendNotification(msgNotif); err != nil {
				logger.Error("Ошибка отправки уведомления о первом взаимодействии: %v", err, b.userID)
			}
		}
	}

	// Получаем канал пользователя для отправки сообщения
	usrCh, err := b.mod.GetCh(senderID)
	if err != nil {
		metrics.ObserveMessageIgnored(b.userID, "channel_not_found")
		metrics.ObserveMessageProcessed(b.userID, "error")
		return fmt.Errorf("ошибка получения канала пользователя: %w", err)
	}

	// Отправляем в CRM (используем senderName как идентификатор респондента)
	b.sendToCRM(senderName, senderName, messageContent, "user", first, []model.FileUpload{})

	// Создаем и отправляем сообщение пользователя
	userMessage := b.createUserMessage(messageContent, senderName)

	// Используем безопасный метод отправки
	if err = usrCh.SendToRx(userMessage); err != nil {
		metrics.ObserveMessageProcessed(b.userID, "error")
		logger.Error("Ошибка отправки сообщения в канал: %v", senderID, err, b.userID)
		return fmt.Errorf("не удалось отправить сообщение: %w", err)
	}
	metrics.ObserveMessageProcessed(b.userID, "success")

	return nil
}

// sendToCRM отправляет сообщение в CRM
func (b *Bot) sendToCRM(respIdentifier, senderName, message, msgType string, first bool, files []model.FileUpload) {
	if b.crm == nil {
		metrics.ObserveCRMRequest(b.userID, "outgoing", "disabled")
		return
	}
	startedAt := time.Now()

	isVoice := msgType == "user_voice"
	fileNames := make([]string, 0, len(files))
	for _, file := range files {
		fileNames = append(fileNames, file.Name)
	}

	csg := b.crm.MSG("user", senderName, message).
		WithAltContact(respIdentifier).
		NewDialog(first).
		WithVoice(isVoice).
		WithFiles(fileNames...)

	if err := b.crm.SendMessage(csg); err != nil {
		metrics.ObserveCRMRequest(b.userID, "outgoing", "error")
		metrics.ObserveCRMRequestDuration(b.userID, "outgoing", startedAt)
		logger.Error("Ошибка отправки сообщения в CRM: %v", err, b.userID)
		return
	}
	metrics.ObserveCRMRequest(b.userID, "outgoing", "success")
	metrics.ObserveCRMRequestDuration(b.userID, "outgoing", startedAt)
}

// createUserMessage создает объект сообщения пользователя
func (b *Bot) createUserMessage(content, senderName string) model.Message {
	return model.Message{
		Type: "user",
		//Content:   content,
		Content: model.AssistResponse{
			Message: content,
			//Action: TODO,
			//Meta: TODO,
		},
		Name:      senderName,
		Timestamp: time.Now(),
	}
}

// decrementConnection уменьшает счетчик активных соединений для пользователя
func (b *Bot) decrementConnection() {
	b.connectionsMutex.Lock()
	defer b.connectionsMutex.Unlock()

	if b.activeConnections > 0 {
		b.activeConnections--
	}
}
