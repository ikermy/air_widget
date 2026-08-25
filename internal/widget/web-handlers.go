package widget

import (
	"air_widget/internal/domain"
	"air_widget/internal/exam"
	"air_widget/internal/metrics"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ikermy/air-logger/v2/pkg/logger"
)

// widgetToken извлекает токен из Authorization: Bearer <token>.
// Query-параметр оставлен для обратной совместимости.
func widgetToken(c *gin.Context) string {
	authorization := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(authorization) >= 7 && strings.EqualFold(authorization[:7], "Bearer ") {
		return strings.TrimSpace(authorization[7:])
	}

	if token := strings.TrimSpace(c.Query("token")); token != "" {
		return token
	}
	return strings.TrimSpace(c.Query("ticket"))
}

func (u *User) generateWidgetCode(userID uint32, config domain.WidgetConfig) (string, error) {
	examKeyBytes := make([]byte, 32)
	if _, err := rand.Read(examKeyBytes); err != nil {
		return "", fmt.Errorf("generate exam key: %w", err)
	}
	examKey := base64.RawURLEncoding.EncodeToString(examKeyBytes)

	var jtiBytes [16]byte
	if _, err := rand.Read(jtiBytes[:]); err != nil {
		return "", err
	}
	var expiresAt int64
	if config.ExpiresAt != nil {
		expiresAt = config.ExpiresAt.Unix()
	}
	return u.web.exam.WidgetNewCode(userID, examKey, expiresAt, config.NeverExpires, config.AllowedUrls, hex.EncodeToString(jtiBytes[:]))
}

func originAllowed(origin string, allowed []string) bool {
	if origin == "" {
		return false
	}
	normalized, err := domain.NormalizeOrigin(origin)
	if err != nil {
		return false
	}
	for _, candidate := range allowed {
		if normalized == candidate {
			return true
		}
	}
	return false
}

func tokenOriginAllowed(c *gin.Context, token *exam.Token) bool {
	requestOrigin := requestOriginFromHeaders(c)
	logger.Debug(
		"origin check: requestOrigin=%q tokenOrigin=%q host=%q referer=%q",
		requestOrigin,
		token.Origin,
		c.Request.Host,
		c.GetHeader("Referer"),
	)

	normalizedOrigin, err := domain.NormalizeOrigin(requestOrigin)
	return err == nil && normalizedOrigin == token.Origin
}

func (u *User) botIsRunning(userID uint32) bool {
	_, exists := u.bot.Load(userID)
	return exists
}

func requestOriginFromHeaders(c *gin.Context) string {
	if origin := strings.TrimSpace(c.GetHeader("Origin")); origin != "" {
		return origin
	}

	referer := strings.TrimSpace(c.GetHeader("Referer"))
	if referer == "" {
		return ""
	}

	parsed, err := url.Parse(referer)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}

	return parsed.Scheme + "://" + parsed.Host
}

// handleAvailable обрабатывает проверку доступности канала
func (u *User) handleAvailable(c *gin.Context) {
	c.Status(http.StatusOK)
}

func (u *User) handleWidgetCode(c *gin.Context) {
	uid, ok := c.Get("uid")
	if !ok {
		logger.Error("handleWidgetCode: uid отсутствует в контексте для %s", c.Request.URL.RequestURI())
		c.JSON(http.StatusUnauthorized, gin.H{"error": "uid context is missing"})
		return
	}
	userID, ok := uid.(uint32)
	if !ok {
		logger.Error("handleWidgetCode: uid имеет неожиданный тип %T", uid)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid uid context"})
		return
	}
	logger.Debug("userID", userID)
	var requestData struct {
		AllowedUrls  []string   `json:"allowedUrls"`
		ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
		NeverExpires bool       `json:"neverExpires"`
	}
	if err := c.ShouldBindJSON(&requestData); err != nil {
		metrics.WidgetCodeRequests.WithLabelValues("bad_request").Inc()
		logger.Error("ошибка парсинга параметров widget code: %v", err, userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	config := domain.WidgetConfig{
		AllowedUrls:  requestData.AllowedUrls,
		ExpiresAt:    requestData.ExpiresAt,
		NeverExpires: requestData.NeverExpires,
	}
	logger.Debug("handleWidgetCode: получен запрос на генерацию widget code с параметрами: %+v", config, userID)
	if _, err := domain.ParseWidgetConfigForGeneration(config, time.Now()); err != nil {
		metrics.WidgetCodeRequests.WithLabelValues("invalid_config").Inc()
		logger.Error("ошибка при проверке параметров widget code: %v", err, userID)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Получаю реальный код виджета
	widgetCode, err := u.generateWidgetCode(userID, config)
	if err != nil {
		metrics.WidgetCodeRequests.WithLabelValues("error").Inc()
		logger.Error("ошибка при получении кода виджета: %v", err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get widget_code code"})
		return
	}
	logger.Debug("handleWidgetCode: сгенерирован widget code: %s", widgetCode, userID)
	// Возвращаю widgetCode
	c.JSON(http.StatusOK, gin.H{"widgetCode": widgetCode})
	metrics.WidgetCodeRequests.WithLabelValues("success").Inc()
}

// handleExam обрабатывает проверку ключа и ид пользователя
func (u *User) handleExam(c *gin.Context) {
	var requestData struct {
		WidgetCode string `json:"widgetCode"`
		RespId     uint64 `json:"respId"`
	}
	if err := c.ShouldBindJSON(&requestData); err != nil && err != io.EOF {
		logger.Error("Ошибка парсинга JSON: %e", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	logger.Debug("handleExam: получен запрос widget code и RespId %d", requestData.RespId)
	claims, err := u.web.exam.WidgetParseCode(requestData.WidgetCode)
	if err != nil {
		metrics.WidgetTokenRequests.WithLabelValues("validate", "invalid").Inc()
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid widget code"})
		return
	}
	requestOrigin, originErr := domain.NormalizeOrigin(c.GetHeader("Origin"))
	if originErr != nil || !originAllowed(requestOrigin, claims.AllowedUrls) {
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}

	// Декодирую из WidgetData userID и key
	userID, key := claims.UserID, claims.ExamKey

	// Состояние в БД является источником истины для widget code.
	userData, err := u.db.GetWidgetBotUser(userID)
	if err != nil || !userData.WaUserBotEnabled {
		logger.Error("Бот не найден или отключён", userID)
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found or disabled"})
		return
	}
	if !u.botIsRunning(userID) {
		logger.Warn("Бот не запущен, запрос /exam отклонён", userID)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Bot is not running"})
		return
	}

	// Проверяю подписку пользователя
	err = u.web.exam.CheckUserSubscription(userID)
	if err != nil {
		logger.Error("Ошибка при проверке подписки пользователя: %v", err, userID)
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "Low balance or no subscription"})
		return
	}

	req := exam.Req{
		UserId: userID,
		Key:    key,
		RespId: requestData.RespId,
		Origin: requestOrigin,
	}

	// Возвращаю токен ошибку
	token, err := u.web.exam.ExamUser(req)
	if err != nil {
		logger.Error("Ошибка при проверке респондента: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Возвращаем JSON-ответ клиенту
	c.JSON(http.StatusOK, gin.H{"token": token})
}

// handleTokenValidate обрабатывает проверку токена
func (u *User) handleTokenValidate(c *gin.Context) {
	token := widgetToken(c)
	logger.Debug("handleTokenValidate: получен запрос на проверку токена %v", token)
	// Верифицирую токен и получаю userId
	parsedToken, err := u.web.exam.ParseToken(token)
	if err != nil {
		logger.Error("validate: ошибка при парсинге токена подтверждения: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	if !tokenOriginAllowed(c, parsedToken) {
		metrics.WidgetOriginDenied.WithLabelValues("validate").Inc()
		logger.Warn(
			"validate: origin запрещён, requestOrigin=%q tokenOrigin=%q",
			requestOriginFromHeaders(c),
			parsedToken.Origin,
		)
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{})
	metrics.WidgetTokenRequests.WithLabelValues("validate", "success").Inc()
}

// handleTokenRefresh обрабатывает обновление токена
func (u *User) handleTokenRefresh(c *gin.Context) {
	var requestData struct {
		OldToken string `json:"oldToken"`
	}
	if err := c.ShouldBindJSON(&requestData); err != nil && err != io.EOF {
		logger.Error("Ошибка парсинга JSON: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	oldToken := widgetToken(c)
	if oldToken == "" {
		oldToken = requestData.OldToken
	}
	logger.Debug("handleTokenRefresh: получен запрос на обновление токена %v", oldToken)

	// Парсинг просроченного токена
	t, err := u.web.exam.ParseExpiredToken(oldToken)
	if err != nil {
		metrics.WidgetTokenRequests.WithLabelValues("refresh", "invalid").Inc()
		logger.Error("Ошибка при парсинге старого токена: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	if !tokenOriginAllowed(c, t) {
		metrics.WidgetOriginDenied.WithLabelValues("refresh").Inc()
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}

	// Обновление токена
	token, err := u.web.exam.UpdateToken(t)
	if err != nil {
		metrics.WidgetTokenRequests.WithLabelValues("refresh", "error").Inc()
		logger.Error("Ошибка при обновлении токена: %v", err)
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "Low balance"})
		return
	}

	// Создаю JSON с токеном
	dataToSend := struct {
		Token string `json:"token"`
	}{
		Token: token,
	}

	c.JSON(http.StatusOK, dataToSend)
	metrics.WidgetTokenRequests.WithLabelValues("refresh", "success").Inc()
}

// handleGetUsername обрабатывает получение имени пользователя
func (u *User) handleGetUsername(c *gin.Context) {
	tokenData := widgetToken(c)
	logger.Debug("handleGetUsername: получен запрос на получение имени пользователя с токеном %v", tokenData)
	// Парсинг токена
	token, err := u.web.exam.ParseToken(tokenData)
	if err != nil {
		logger.Error("ошибка при проверке токена: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	if !tokenOriginAllowed(c, token) {
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}

	respData, err := u.db.ReadResponderName(token.ReapId)
	if err != nil {
		logger.Error("ошибка при получении respData: %v", err, token.UserId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !tokenOriginAllowed(c, token) {
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}

	var RespData struct {
		Name   string
		RespId uint64
	}

	err = json.Unmarshal(respData, &RespData)
	if err != nil {
		logger.Error("failed to unmarshal json: %v", err, token.UserId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if RespData.Name == "" {
		c.JSON(http.StatusOK, gin.H{"message": "No user name"})
		return
	}

	// Создаю JSON с именем пользователя
	dataToSend := struct {
		Name string `json:"name"`
	}{
		Name: RespData.Name,
	}

	c.JSON(http.StatusOK, dataToSend)
}

// handleReadDialog обрабатывает чтение диалога и создание нового респондера
func (u *User) handleReadDialog(c *gin.Context) {
	tokenData := widgetToken(c)
	userName := c.Query("name")

	token, err := u.web.exam.ParseToken(tokenData)
	if err != nil {
		logger.Error("ошибка при парсинге токена: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}

	// Получаем бот пользователя
	value, exists := u.bot.Load(token.UserId)
	if !exists {
		logger.Debug("Бот не найден", token.UserId)
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	bot := value.(*Bot)

	// Инициализируем каналы пользователя
	dialogId, err := bot.initializeUserChannels(token.ReapId, userName)
	if err != nil {
		logger.Error("ошибка при инициализации каналов пользователя: %v", err, token.UserId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	data, err := u.db.ReadDialog(dialogId, 50)
	if err != nil {
		logger.Error("ошибка при чтении диалога: %v", err, token.UserId)
		metrics.DialogRequests.WithLabelValues("error").Inc()
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	decryptedDialog, err := u.decryptData(token.UserId, string(data))
	if err != nil {
		logger.Error("ошибка расшифровки диалога: %v", err, token.UserId)
		metrics.DialogRequests.WithLabelValues("decrypt_error").Inc()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decrypt dialog data"})
		return
	}
	data = json.RawMessage(decryptedDialog)

	logger.Debug("data %s", string(data), token.UserId)
	normalizedData, err := normalizeDialogResponse(data)
	if err != nil {
		logger.Error("ошибка нормализации данных диалога: %v", err, token.UserId)
		metrics.DialogRequests.WithLabelValues("invalid_data").Inc()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid dialog data"})
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", normalizedData)
	metrics.DialogRequests.WithLabelValues("success").Inc()
}

// normalizeDialogResponse keeps the legacy frontend contract: Data is a JSON
// string which the client parses separately.
func normalizeDialogResponse(raw json.RawMessage) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	data, ok := object["Data"]
	if !ok {
		data, ok = object["data"]
	}
	if !ok {
		return raw, nil
	}
	var textValue string
	if json.Unmarshal(data, &textValue) != nil {
		encoded, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		textValue = string(encoded)
	}
	encodedText, err := json.Marshal(textValue)
	if err != nil {
		return nil, err
	}
	if object["Data"] != nil {
		object["Data"] = encodedText
	} else {
		object["data"] = encodedText
	}
	return json.Marshal(object)
}

// handleData обрабатывает получение данных (сообщений от пользователей)
func (u *User) handleData(c *gin.Context) {
	var requestData struct {
		Token   string `json:"token"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}

	if err := c.ShouldBindJSON(&requestData); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Парсинг токена
	tokenData := requestData.Token
	if tokenData == "" {
		tokenData = widgetToken(c)
	}
	token, err := u.web.exam.ParseToken(tokenData)
	if err != nil {
		logger.Error("Ошибка при парсинге токена: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	if !tokenOriginAllowed(c, token) {
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}

	// Получаем бот пользователя
	value, exists := u.bot.Load(token.UserId)
	if !exists {
		logger.Error("Бот не найден", token.UserId)
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	bot := value.(*Bot)
	logger.Debug("Name %s", requestData.Name)
	// Используем централизованную функцию handleMessage
	err = bot.handleMessage(token.ReapId, requestData.Name, requestData.Content)
	if err != nil {
		logger.Error("Ошибка обработки сообщения: %v", err, token.UserId)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	logger.Debug("WidgetHandler Сообщение успешно обработано: %v", requestData.Content)
	c.JSON(http.StatusOK, gin.H{})
}

// handleEventsTicket validates the bearer token and issues a separate token
// for the EventSource URL, since EventSource cannot send Authorization headers.
func (u *User) handleEventsTicket(c *gin.Context) {
	accessToken := strings.TrimSpace(c.GetHeader("Authorization"))
	if len(accessToken) >= 7 && strings.EqualFold(accessToken[:7], "Bearer ") {
		accessToken = strings.TrimSpace(accessToken[7:])
	}
	if accessToken == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization token"})
		return
	}

	parsed, err := u.web.exam.ParseToken(accessToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	if !tokenOriginAllowed(c, parsed) {
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}
	if !u.botIsRunning(parsed.UserId) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Bot is not running"})
		return
	}

	ticket, err := u.web.exam.UpdateToken(parsed)
	if err != nil {
		logger.Error("ошибка создания SSE ticket: %v", err, parsed.UserId)
		c.JSON(http.StatusPaymentRequired, gin.H{"error": "Low balance"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ticket": ticket})
}

// handleEvents обрабатывает отправку данных с использованием Server-Sent Events
func (u *User) handleEvents(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	tokenData := widgetToken(c)
	token, err := u.web.exam.ParseToken(tokenData)
	if err != nil {
		logger.Error("error parsing token: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
		return
	}
	if !tokenOriginAllowed(c, token) {
		c.JSON(http.StatusForbidden, gin.H{"error": "origin not allowed"})
		return
	}

	// Получаем бот пользователя
	value, exists := u.bot.Load(token.UserId)
	if !exists {
		logger.Error("бот не найден", token.UserId)
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	bot := value.(*Bot)

	// Проверяем общее количество активных соединений к боту
	bot.connectionsMutex.Lock()
	if bot.activeConnections >= bot.maxConnections {
		bot.connectionsMutex.Unlock()
		logger.Error("превышен лимит соединений к боту (%d/%d)", bot.activeConnections, bot.maxConnections, token.UserId)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Too many active connections to bot"})
		return
	}
	bot.activeConnections++
	currentConnections := bot.activeConnections
	bot.connectionsMutex.Unlock()
	metrics.SSEConnections.WithLabelValues(metrics.BotLabel(token.UserId)).Inc()
	metrics.SSERequests.WithLabelValues(metrics.BotLabel(token.UserId), "accepted").Inc()
	metrics.SetActiveSessions(token.UserId, "sse", int(currentConnections))

	logger.Debug("Новое соединение к боту (%d/%d)", currentConnections, bot.maxConnections, token.UserId)

	usrCh, err := u.mod.GetCh(token.ReapId)
	if err != nil {
		logger.Warn("канал не найден для respId %d, повторная инициализация: %v", token.ReapId, err)
		metrics.ObserveReconnect(token.UserId, "attempt")
		_, initErr := bot.initializeUserChannels(token.ReapId, "")
		if initErr != nil {
			metrics.ObserveReconnect(token.UserId, "error")
			logger.Error("ошибка повторной инициализации каналов: %v", initErr, token.UserId)
			bot.decrementConnection()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize user channel"})
			return
		}
		metrics.ObserveReconnect(token.UserId, "success")

		usrCh, err = u.mod.GetCh(token.ReapId)
		if err != nil {
			logger.Error("канал не найден после повторной инициализации: %v", err, token.ReapId)
			bot.decrementConnection()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "channel not found"})
			return
		}
	}

	connectionStartTime := time.Now()

	// Heartbeat должен быть короче idle timeout reverse proxy (обычно 30 с).
	pingTicker := time.NewTicker(10 * time.Second)
	defer pingTicker.Stop()

	// Timeout ticker для принудительного закрытия соединения
	timeoutTicker := time.NewTicker(domain.LongPollingTimeout)
	defer timeoutTicker.Stop()

	defer func() {
		metrics.SSEConnections.WithLabelValues(metrics.BotLabel(token.UserId)).Dec()
		// Уменьшаем счетчик соединений при завершении
		bot.decrementConnection()
		bot.connectionsMutex.RLock()
		metrics.SetActiveSessions(token.UserId, "sse", int(bot.activeConnections))
		bot.connectionsMutex.RUnlock()

		bot.connectionsMutex.RLock()
		remainingConnections := bot.activeConnections
		bot.connectionsMutex.RUnlock()

		connectionDuration := time.Since(connectionStartTime)
		logger.Debug("Соединение к боту закрыто после %v (осталось %d/%d)",
			connectionDuration.Round(time.Second), remainingConnections, bot.maxConnections, token.UserId)

		// Завершаем диалог только если это последнее соединение
		if remainingConnections == 0 {
			const reconnectGracePeriod = 10 * time.Second
			logger.Debug("Все SSE-соединения закрыты, ожидаю reconnect %v", reconnectGracePeriod, token.UserId)
			time.AfterFunc(reconnectGracePeriod, func() {
				bot.connectionsMutex.RLock()
				stillDisconnected := bot.activeConnections == 0
				bot.connectionsMutex.RUnlock()
				if !stillDisconnected {
					logger.Debug("Reconnect выполнен, диалог оставлен активным", token.UserId)
					return
				}

				select {
				case domain.EndDialog <- usrCh.DialogID:
				default:
					logger.Warn("Ошибка при отправке данных в EndDialog")
				}
				u.mod.CleanDialogData(usrCh.DialogID)
				logger.Debug("Все соединения к боту закрыты, диалог завершен", token.UserId)
			})
		}
	}()

	// Немедленно отправляем SSE handshake. Без первого байта reverse proxy
	// может считать соединение неустановленным из-за ResponseHeaderTimeout.
	if _, err := fmt.Fprint(c.Writer, ": connected\n\n"); err != nil {
		logger.Error("ошибка отправки SSE handshake: %v", err, token.UserId)
		return
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	} else {
		logger.Error("SSE writer не поддерживает Flush", token.UserId)
		return
	}

	for {
		select {
		case msg, ok := <-usrCh.TxCh:
			if !ok {
				logger.Warn("Канал TxCh закрыт!")
				return
			}
			logger.Debug("Отправка сообщения клиенту: %v", msg)

			// Отправляем ответ ассистента в CRM
			if msg.Type == "assist" && bot.crm != nil {
				// Собираем имена файлов
				fileNames := make([]string, 0, len(msg.Content.Action.SendFiles))
				for _, file := range msg.Content.Action.SendFiles {
					fileNames = append(fileNames, file.FileName)
				}

				// Создаем и отправляем сообщение в CRM (используем usrCh.RespName как идентификатор)
				csg := bot.crm.MSG("assist", usrCh.RespName, msg.Content.Message).
					WithAltContact(usrCh.RespName).
					WithFiles(fileNames...).
					SetMeta(msg.Content.Meta)

				if err := bot.crm.SendMessage(csg); err != nil {
					logger.Error("Ошибка отправки ответа ассистента в CRM: %v", err, token.UserId)
				}
			}

			jsonData, err := json.Marshal(msg)
			if err != nil {
				logger.Error("Ошибка при маршалинге сообщения: %v", err)
				return
			}

			if _, err = fmt.Fprintf(c.Writer, "data: %s\n\n", jsonData); err != nil {
				metrics.ObservewidgetSend(token.UserId, "error")
				logger.Error("Ошибка при отправке данных клиенту: %v", err)
				return
			}
			metrics.ObservewidgetSend(token.UserId, "success")

			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			} else {
				logger.Error("Ошибка: невозможен сброс данных")
				return
			}

		case <-bot.shutdownCh:
			// Graceful shutdown: уведомляем клиента о завершении
			logger.Debug("User shutdown signal received for resp %d", token.ReapId)
			if _, err := fmt.Fprintf(c.Writer, "event: shutdown\ndata: {\"type\":\"shutdown\",\"message\":\"User is shutting down\"}\n\n"); err == nil {
				if flusher, ok := c.Writer.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			return

		case <-pingTicker.C:
			// Отправляем ping для проверки соединения
			if _, err := fmt.Fprintf(c.Writer, ": ping\n\n"); err != nil {
				logger.Error("Соединение разорвано (ping failed) для слушателя %d", token.ReapId)
				return
			}
			if flusher, ok := c.Writer.(http.Flusher); ok {
				flusher.Flush()
			}

		case <-timeoutTicker.C:
			// Принудительное закрытие соединения по timeout
			logger.Error("Long-polling timeout (%v) для слушателя %d", domain.LongPollingTimeout, token.ReapId)
			if _, err := fmt.Fprintf(c.Writer, "event: timeout\ndata: {\"type\":\"timeout\",\"message\":\"Connection timeout\"}\n\n"); err == nil {
				if flusher, ok := c.Writer.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			return

		case <-c.Request.Context().Done():
			logger.Debug("Клиент разорвал соединение %d", token.ReapId)
			return
		}
	}
}

// enableBot включает/запускает бота для конкретного пользователя
func (u *User) enableBot(c *gin.Context) {
	uid, ok := c.Get("uid")
	if !ok {
		return
	}
	userID := uid.(uint32)

	err := u.StartBot(userID)
	if err != nil {
		logger.Error("'enableBot' Ошибка при запуске бота: %v", err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to start bot"})
		return
	}

	logger.Info("'enableBot' Бот успешно запущен", userID)
	c.JSON(http.StatusOK, gin.H{"status": "bot started successfully"})
}

// restartBot перезапускает бота для конкретного пользователя (перезагружает данные из БД)
func (u *User) restartBot(c *gin.Context) {
	uid, ok := c.Get("uid")
	if !ok {
		return
	}
	userID := uid.(uint32)

	err := u.RestartBot(userID)
	if err != nil {
		logger.Error("'restartBot' Ошибка при перезапуске бота: %v", err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restart bot"})
		return
	}

	logger.Info("'restartBot' Бот успешно перезапущен", userID)
	c.JSON(http.StatusOK, gin.H{"status": "bot restarted successfully"})
}

// disableBot останавливает бот для конкретного пользователя
func (u *User) disableBot(c *gin.Context) {
	uid, ok := c.Get("uid")
	if !ok {
		return
	}
	userID := uid.(uint32)

	err := u.StopBot(userID)
	if err != nil {
		logger.Error("'disableBot' Ошибка при остановке бота: %v", err, userID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stop bot"})
		return
	}

	logger.Info("'disableBot' Бот успешно остановлен", userID)
	c.JSON(http.StatusOK, gin.H{"status": "bot stopped successfully"})
}
