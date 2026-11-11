package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	AppVersion       = "1.1.0"
	AppDeveloper     = "Jhonatan Garcia (jhona1234)"
	DefaultPort      = "8099"
	SettingsFilePath = "/data/iabot_settings.json"
	LegacyFilePath   = "/data/settings.json"
)

// Telegram structs
type TelegramUser struct {
	ChatID    string    `json:"chat_id"`
	Name      string    `json:"name"`
	Username  string    `json:"username"`
	Status    string    `json:"status"` // "admin", "allowed", "pending", "blocked"
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"` // "INFO", "WARN", "MSG", "IA"
	Message   string `json:"message"`
}

type IABoTSettings struct {
	TelegramBotToken string                   `json:"telegram_bot_token"`
	AdminChatID      string                   `json:"admin_chat_id"`
	BotName          string                   `json:"bot_name"`
	EnableAI         bool                     `json:"enable_ai"`
	GeminiAPIKey     string                   `json:"gemini_api_key"`
	GeminiModel      string                   `json:"gemini_model"`
	ModelsPriority   string                   `json:"models_priority"`
	LastModelUsed    string                   `json:"last_model_used"`
	SystemPrompt     string                   `json:"system_prompt"`
	TelegramUsers    map[string]*TelegramUser `json:"telegram_users"`
	MessagesSent     int64                    `json:"messages_sent"`
	MessagesReceived int64                    `json:"messages_received"`
	AITokensUsed     int64                    `json:"ai_tokens_used"`
}

var (
	settingsMu sync.RWMutex
	settings   IABoTSettings
	logsMu     sync.RWMutex
	recentLogs []LogEntry

	// User chat history for Gemini memory
	chatHistoryMu sync.RWMutex
	chatHistory   = make(map[string][]GeminiContent)

	httpClient = &http.Client{Timeout: 35 * time.Second}
)

func addLog(logType, msg string) {
	log.Printf("[%s] %s", logType, msg)
	logsMu.Lock()
	defer logsMu.Unlock()
	entry := LogEntry{
		Timestamp: time.Now().Format("15:04:05"),
		Type:      logType,
		Message:   msg,
	}
	recentLogs = append([]LogEntry{entry}, recentLogs...)
	if len(recentLogs) > 100 {
		recentLogs = recentLogs[:100]
	}
}

func loadSettings() {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	settings = IABoTSettings{
		BotName:        "Herbie",
		EnableAI:       true,
		GeminiModel:    "gemini-3.6-flash",
		ModelsPriority: "gemini-3.6-flash, gemini-3.5-flash-lite, gemini-3.1-flash-lite",
		SystemPrompt:   "Você é o Herbie, o assistente inteligente da casa de Jhonatan e Claudia. Você é amigável, direto, prestativo e fala português do Brasil com naturalidade e emojis moderados.",
		TelegramUsers:  make(map[string]*TelegramUser),
	}

	data, err := os.ReadFile(SettingsFilePath)
	if err != nil {
		data, err = os.ReadFile(LegacyFilePath)
	}

	if err == nil {
		_ = json.Unmarshal(data, &settings)
	}

	if settings.TelegramUsers == nil {
		settings.TelegramUsers = make(map[string]*TelegramUser)
	}
	if settings.BotName == "" {
		settings.BotName = "Herbie"
	}
		if strings.Contains(settings.GeminiModel, "1.5") || strings.Contains(settings.GeminiModel, "2.5") || settings.GeminiModel == "" {
		settings.GeminiModel = "gemini-3.6-flash"
	}
	if strings.Contains(settings.ModelsPriority, "1.5") || strings.Contains(settings.ModelsPriority, "2.5") || settings.ModelsPriority == "" {
		settings.ModelsPriority = "gemini-3.6-flash, gemini-3.5-flash-lite, gemini-3.1-flash-lite"
	}
}

func saveSettings() error {
	settingsMu.RLock()
	data, err := json.MarshalIndent(settings, "", "  ")
	settingsMu.RUnlock()
	if err != nil {
		return err
	}
	return os.WriteFile(SettingsFilePath, data, 0644)
}

// Home Assistant Supervisor integration
func callSupervisorService(domain, service string, data map[string]interface{}) {
	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		return
	}
	url := fmt.Sprintf("http://supervisor/core/api/services/%s/%s", domain, service)
	payload, _ := json.Marshal(data)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

func updateSupervisorSensor() {
	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		return
	}

	settingsMu.RLock()
	activeCount := 0
	pendingCount := 0
	for _, u := range settings.TelegramUsers {
		if u.Status == "admin" || u.Status == "allowed" {
			activeCount++
		} else if u.Status == "pending" {
			pendingCount++
		}
	}
	botName := settings.BotName
	model := settings.GeminiModel
	sent := settings.MessagesSent
	recv := settings.MessagesReceived
	aiEnabled := settings.EnableAI
	settingsMu.RUnlock()

	body := map[string]interface{}{
		"state": "Online",
		"attributes": map[string]interface{}{
			"friendly_name":     "IABoT Status",
			"icon":              "mdi:robot-happy",
			"bot_name":          botName,
			"active_users":      activeCount,
			"pending_users":     pendingCount,
			"messages_sent":     sent,
			"messages_received": recv,
			"ai_model":          model,
			"ai_enabled":        aiEnabled,
			"version":           AppVersion,
		},
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", "http://supervisor/core/api/states/sensor.iabot_status", bytes.NewBuffer(payload))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// Telegram API Helpers
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

func sendTelegramMessage(token, chatID, text string, markup *InlineKeyboardMarkup) error {
	if token == "" || chatID == "" {
		return fmt.Errorf("token ou chat_id vazio")
	}

	body := map[string]interface{}{
		"chat_id":    chatID,
		"text":       text,
		"parse_mode": "HTML",
	}
	if markup != nil {
		body["reply_markup"] = markup
	}

	payload, _ := json.Marshal(body)
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram status %d: %s", resp.StatusCode, string(respBytes))
	}

	settingsMu.Lock()
	settings.MessagesSent++
	settingsMu.Unlock()
	return nil
}

func answerCallbackQuery(token, callbackID, text string) {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/answerCallbackQuery", token)
	payload, _ := json.Marshal(map[string]string{
		"callback_query_id": callbackID,
		"text":              text,
	})
	resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
	if err == nil {
		_ = resp.Body.Close()
	}
}

// Gemini AI Integration
type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiContent struct {
	Role  string       `json:"role"`
	Parts []GeminiPart `json:"parts"`
}

type GeminiRequest struct {
	Contents          []GeminiContent `json:"contents"`
	SystemInstruction *GeminiContent  `json:"systemInstruction,omitempty"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		TotalTokenCount int64 `json:"totalTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

var defaultModelsPriority = []string{
	"gemini-3.6-flash",
	"gemini-3.5-flash-lite",
	"gemini-3.1-flash-lite",
}

func getModelsToTry(selectedModel, priorityStr string) []string {
	var models []string
	if priorityStr != "" {
		for _, m := range strings.Split(priorityStr, ",") {
			m = strings.TrimSpace(m)
			if m != "" && !strings.Contains(m, "1.5") && !strings.Contains(m, "2.5") {
				models = append(models, m)
			}
		}
	}
	if len(models) == 0 {
		models = defaultModelsPriority
	}
	if selectedModel != "" {
		filtered := []string{selectedModel}
		for _, m := range models {
			if m != selectedModel {
				filtered = append(filtered, m)
			}
		}
		return filtered
	}
	return models
}

func callGeminiAI(userChatID, userPrompt string) (string, error) {
	settingsMu.RLock()
	apiKey := settings.GeminiAPIKey
	selectedModel := settings.GeminiModel
	priorityStr := settings.ModelsPriority
	sysPrompt := settings.SystemPrompt
	settingsMu.RUnlock()

	if apiKey == "" {
		return "🤖 A IA está desativada ou a chave do Gemini ainda não foi configurada no painel do IABoT.", nil
	}

	chatHistoryMu.Lock()
	history := chatHistory[userChatID]
	history = append(history, GeminiContent{
		Role:  "user",
		Parts: []GeminiPart{{Text: userPrompt}},
	})
	if len(history) > 12 {
		history = history[len(history)-12:]
	}
	chatHistory[userChatID] = history
	chatHistoryMu.Unlock()

	modelsToTry := getModelsToTry(selectedModel, priorityStr)

	reqBody := GeminiRequest{
		Contents: history,
	}
	if sysPrompt != "" {
		reqBody.SystemInstruction = &GeminiContent{
			Role:  "system",
			Parts: []GeminiPart{{Text: sysPrompt}},
		}
	}
	payload, _ := json.Marshal(reqBody)

	var lastErr error
	for _, modelName := range modelsToTry {
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)
		resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
		if err != nil {
			lastErr = err
			addLog("WARN", fmt.Sprintf("Modelo %s erro de rede: %v. Tentando próximo...", modelName, err))
			continue
		}

		var geminiResp GeminiResponse
		err = json.NewDecoder(resp.Body).Decode(&geminiResp)
		resp.Body.Close()

		if err != nil {
			lastErr = err
			addLog("WARN", fmt.Sprintf("Modelo %s falha de json: %v. Tentando próximo...", modelName, err))
			continue
		}

		if geminiResp.Error != nil {
			lastErr = fmt.Errorf("%s", geminiResp.Error.Message)
			addLog("WARN", fmt.Sprintf("Modelo %s sem cota ou erro: %s. Tentando próximo...", modelName, geminiResp.Error.Message))
			continue
		}

		if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
			replyText := geminiResp.Candidates[0].Content.Parts[0].Text

			chatHistoryMu.Lock()
			chatHistory[userChatID] = append(chatHistory[userChatID], GeminiContent{
				Role:  "model",
				Parts: []GeminiPart{{Text: replyText}},
			})
			chatHistoryMu.Unlock()

			settingsMu.Lock()
			settings.AITokensUsed += geminiResp.UsageMetadata.TotalTokenCount
			settings.LastModelUsed = modelName
			settingsMu.Unlock()

			addLog("IA", fmt.Sprintf("Sucesso com modelo [%s] (%d tokens)", modelName, geminiResp.UsageMetadata.TotalTokenCount))
			return replyText, nil
		}
	}

	addLog("WARN", fmt.Sprintf("Todos os modelos da fila falharam: %v", lastErr))
	return fmt.Sprintf("⚠️ Todos os modelos da fila (%s) estão sem cota no momento. Aguarde alguns minutos!", strings.Join(modelsToTry, ", ")), nil
}

// Telegram Polling Engine
type TelegramUpdate struct {
	UpdateID int `json:"update_id"`
	Message  *struct {
		MessageID int `json:"message_id"`
		From      struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Chat struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		Text string `json:"text"`
		Date int64  `json:"date"`
	} `json:"message"`
	CallbackQuery *struct {
		ID   string `json:"id"`
		From struct {
			ID        int64  `json:"id"`
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"from"`
		Data string `json:"data"`
	} `json:"callback_query"`
}

func startTelegramPolling(ctx context.Context) {
	offset := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		settingsMu.RLock()
		token := settings.TelegramBotToken
		settingsMu.RUnlock()

		if token == "" {
			time.Sleep(3 * time.Second)
			continue
		}

		url := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=25", token, offset)
		resp, err := httpClient.Get(url)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}

		var result struct {
			OK     bool             `json:"ok"`
			Result []TelegramUpdate `json:"result"`
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		resp.Body.Close()

		if err != nil || !result.OK {
			time.Sleep(3 * time.Second)
			continue
		}

		for _, up := range result.Result {
			if up.UpdateID >= offset {
				offset = up.UpdateID + 1
			}

			// Handle Callback Queries (Buttons)
			if up.CallbackQuery != nil {
				handleCallbackQuery(token, up.CallbackQuery)
				continue
			}

			// Handle Incoming Messages
			if up.Message != nil && up.Message.Text != "" {
				handleIncomingMessage(token, up.Message)
			}
		}
	}
}

func handleCallbackQuery(token string, cb *struct {
	ID   string `json:"id"`
	From struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		Username  string `json:"username"`
	} `json:"from"`
	Data string `json:"data"`
}) {
	callerID := fmt.Sprintf("%d", cb.From.ID)
	settingsMu.RLock()
	adminID := settings.AdminChatID
	settingsMu.RUnlock()

	if callerID != adminID {
		answerCallbackQuery(token, cb.ID, "Apenas o Administrador pode executar esta ação.")
		return
	}

	parts := strings.Split(cb.Data, ":")
	if len(parts) == 2 {
		action := parts[0]
		targetCID := parts[1]

		settingsMu.Lock()
		u, exists := settings.TelegramUsers[targetCID]
		if exists {
			if action == "auth_allow" {
				u.Status = "allowed"
				settingsMu.Unlock()
				_ = saveSettings()
				answerCallbackQuery(token, cb.ID, fmt.Sprintf("✅ %s foi autorizado!", u.Name))
				sendTelegramMessage(token, adminID, fmt.Sprintf("✅ <b>Usuário Autorizado com Sucesso:</b> %s (<code>%s</code>)", html.EscapeString(u.Name), targetCID), nil)
				sendTelegramMessage(token, targetCID, "🎉 <b>Acesso Concedido!</b>\n\nO Administrador aprovou seu acesso. Agora você pode conversar com o assistente da casa!", nil)
				addLog("INFO", fmt.Sprintf("Usuário %s (%s) aprovado pelo Admin", u.Name, targetCID))
				updateSupervisorSensor()
				return
			} else if action == "auth_block" {
				u.Status = "blocked"
				settingsMu.Unlock()
				_ = saveSettings()
				answerCallbackQuery(token, cb.ID, fmt.Sprintf("❌ %s foi bloqueado.", u.Name))
				sendTelegramMessage(token, adminID, fmt.Sprintf("❌ <b>Usuário Bloqueado:</b> %s (<code>%s</code>)", html.EscapeString(u.Name), targetCID), nil)
				addLog("WARN", fmt.Sprintf("Usuário %s (%s) bloqueado pelo Admin", u.Name, targetCID))
				updateSupervisorSensor()
				return
			}
		}
		settingsMu.Unlock()
	}
	answerCallbackQuery(token, cb.ID, "Ação processada.")
}

func handleIncomingMessage(token string, msg *struct {
	MessageID int `json:"message_id"`
	From      struct {
		ID        int64  `json:"id"`
		FirstName string `json:"first_name"`
		LastName  string `json:"last_name"`
		Username  string `json:"username"`
	} `json:"from"`
	Chat struct {
		ID   int64  `json:"id"`
		Type string `json:"type"`
	} `json:"chat"`
	Text string `json:"text"`
	Date int64  `json:"date"`
}) {
	userChatID := fmt.Sprintf("%d", msg.Chat.ID)
	userName := strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
	if userName == "" {
		userName = msg.From.Username
	}
	if userName == "" {
		userName = fmt.Sprintf("Usuário %s", userChatID)
	}

	text := strings.TrimSpace(msg.Text)

	settingsMu.Lock()
	settings.MessagesReceived++
	adminChatID := settings.AdminChatID
	botName := settings.BotName
	enableAI := settings.EnableAI

	// Auto-claim Admin on first message if none configured
	if adminChatID == "" {
		settings.AdminChatID = userChatID
		adminChatID = userChatID
		settings.TelegramUsers[userChatID] = &TelegramUser{
			ChatID:    userChatID,
			Name:      userName,
			Username:  msg.From.Username,
			Status:    "admin",
			FirstSeen: time.Now(),
			LastSeen:  time.Now(),
		}
		settingsMu.Unlock()
		_ = saveSettings()
		addLog("INFO", fmt.Sprintf("Novo Administrador configurado: %s (%s)", userName, userChatID))
		welcomeAdmin := fmt.Sprintf("👑 <b>Bem-vindo, Administrador!</b>\n\nVocê é o administrador principal do <b>%s</b>.\n\nComandos disponíveis:\n• <code>/status</code> - Ver status do sistema\n• <code>/usuarios</code> - Listar usuários\n• <code>/aprovar &lt;id&gt; [Nome]</code> - Autorizar usuário\n• <code>/bloquear &lt;id&gt;</code> - Bloquear usuário", botName)
		sendTelegramMessage(token, userChatID, welcomeAdmin, nil)
		updateSupervisorSensor()
		return
	}

	user, exists := settings.TelegramUsers[userChatID]
	if !exists {
		// New user requesting access
		user = &TelegramUser{
			ChatID:    userChatID,
			Name:      userName,
			Username:  msg.From.Username,
			Status:    "pending",
			FirstSeen: time.Now(),
			LastSeen:  time.Now(),
		}
		settings.TelegramUsers[userChatID] = user
		settingsMu.Unlock()
		_ = saveSettings()

		addLog("WARN", fmt.Sprintf("Nova solicitação de acesso: %s (%s)", userName, userChatID))

		// Notify user that access is pending
		pendingMsg := fmt.Sprintf("🔒 <b>Acesso Pendente</b>\n\nOlá <b>%s</b>! Este bot é privado da residência.\nSua solicitação de acesso foi enviada ao administrador.", html.EscapeString(userName))
		sendTelegramMessage(token, userChatID, pendingMsg, nil)

		// Alert Admin with Action Buttons
		adminAlert := fmt.Sprintf("🚨 <b>Nova Solicitação de Acesso ao %s!</b>\n\n<b>Nome:</b> %s\n<b>Chat ID:</b> <code>%s</code>\n<b>Username:</b> @%s\n<b>Mensagem:</b> <i>\"%s\"</i>",
			botName, html.EscapeString(userName), userChatID, html.EscapeString(msg.From.Username), html.EscapeString(text))

		markup := &InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{
					{Text: "✅ Autorizar", CallbackData: "auth_allow:" + userChatID},
					{Text: "❌ Bloquear", CallbackData: "auth_block:" + userChatID},
				},
			},
		}
		sendTelegramMessage(token, adminChatID, adminAlert, markup)

		// Create persistent notification in HA
		callSupervisorService("persistent_notification", "create", map[string]interface{}{
			"title":           fmt.Sprintf("🚨 Novo Usuário Telegram: %s", userName),
			"message":         fmt.Sprintf("O usuário **%s** (Chat ID: `%s`) solicitou acesso ao Telegram Bot. Acesse o painel do IABoT para aprovar.", userName, userChatID),
			"notification_id": "iabot_user_" + userChatID,
		})

		updateSupervisorSensor()
		return
	}

	user.LastSeen = time.Now()
	userStatus := user.Status
	settingsMu.Unlock()

	// Check permissions
	if userStatus == "blocked" {
		sendTelegramMessage(token, userChatID, "🚫 Seu acesso está bloqueado pelo administrador.", nil)
		return
	}

	if userStatus == "pending" {
		sendTelegramMessage(token, userChatID, "⏳ Seu acesso ainda está aguardando aprovação do administrador.", nil)
		return
	}

	// User is authorized (admin or allowed)
	isAdmin := (userStatus == "admin" || userChatID == adminChatID)

	// Admin commands
	if isAdmin && strings.HasPrefix(text, "/aprovar") {
		parts := strings.Fields(text)
		if len(parts) < 2 {
			sendTelegramMessage(token, userChatID, "ℹ️ <b>Uso:</b> <code>/aprovar &lt;chat_id&gt; [Nome]</code>", nil)
			return
		}
		targetCID := parts[1]
		customName := ""
		if len(parts) > 2 {
			customName = strings.Join(parts[2:], " ")
		}

		settingsMu.Lock()
		u, exists := settings.TelegramUsers[targetCID]
		if !exists {
			u = &TelegramUser{
				ChatID:    targetCID,
				Name:      customName,
				Status:    "allowed",
				FirstSeen: time.Now(),
				LastSeen:  time.Now(),
			}
			settings.TelegramUsers[targetCID] = u
		} else {
			u.Status = "allowed"
			if customName != "" {
				u.Name = customName
			}
		}
		settingsMu.Unlock()
		_ = saveSettings()

		sendTelegramMessage(token, userChatID, fmt.Sprintf("✅ Usuário <code>%s</code> aprovado com sucesso!", targetCID), nil)
		sendTelegramMessage(token, targetCID, "🎉 <b>Acesso Concedido!</b>\nO Administrador aprovou seu acesso. Agora você pode conversar com o bot!", nil)
		updateSupervisorSensor()
		return
	}

	if isAdmin && strings.HasPrefix(text, "/bloquear") {
		parts := strings.Fields(text)
		if len(parts) < 2 {
			sendTelegramMessage(token, userChatID, "ℹ️ <b>Uso:</b> <code>/bloquear &lt;chat_id&gt;</code>", nil)
			return
		}
		targetCID := parts[1]
		if targetCID == adminChatID {
			sendTelegramMessage(token, userChatID, "⚠️ Você não pode bloquear o administrador principal.", nil)
			return
		}

		settingsMu.Lock()
		if u, exists := settings.TelegramUsers[targetCID]; exists {
			u.Status = "blocked"
		}
		settingsMu.Unlock()
		_ = saveSettings()

		sendTelegramMessage(token, userChatID, fmt.Sprintf("❌ Usuário <code>%s</code> foi bloqueado.", targetCID), nil)
		updateSupervisorSensor()
		return
	}

	if isAdmin && text == "/usuarios" {
		settingsMu.RLock()
		var sb strings.Builder
		sb.WriteString("👥 <b>Usuários Registrados:</b>\n\n")
		for cid, u := range settings.TelegramUsers {
			icon := "👤"
			if u.Status == "admin" {
				icon = "👑"
			} else if u.Status == "allowed" {
				icon = "✅"
			} else if u.Status == "pending" {
				icon = "⏳"
			} else if u.Status == "blocked" {
				icon = "🚫"
			}
			sb.WriteString(fmt.Sprintf("%s <b>%s</b> (@%s)\nID: <code>%s</code> | Status: %s\n\n", icon, html.EscapeString(u.Name), html.EscapeString(u.Username), cid, u.Status))
		}
		settingsMu.RUnlock()
		sendTelegramMessage(token, userChatID, sb.String(), nil)
		return
	}

	if text == "/start" || text == "/ajuda" {
		reply := fmt.Sprintf("👋 <b>Olá, %s!</b>\n\nEu sou o <b>%s</b>, assistente inteligente da casa.\nVocê pode conversar normalmente comigo ou fazer perguntas sobre automações e lembretes!", html.EscapeString(userName), botName)
		sendTelegramMessage(token, userChatID, reply, nil)
		return
	}

	if text == "/status" {
		settingsMu.RLock()
		model := settings.GeminiModel
		sent := settings.MessagesSent
		recv := settings.MessagesReceived
		settingsMu.RUnlock()
		reply := fmt.Sprintf("📊 <b>Status do %s</b>\n\n• Sistema: Online\n• Modelo IA: <code>%s</code>\n• Mensagens enviadas: %d\n• Mensagens recebidas: %d\n• Hora: %s",
			botName, model, sent, recv, time.Now().Format("15:04:05"))
		sendTelegramMessage(token, userChatID, reply, nil)
		return
	}

	// Normal conversation via Gemini AI
	if enableAI {
		addLog("MSG", fmt.Sprintf("[%s]: %s", userName, text))
		reply, err := callGeminiAI(userChatID, text)
		if err != nil {
			addLog("WARN", fmt.Sprintf("Erro IA Gemini: %v", err))
			sendTelegramMessage(token, userChatID, fmt.Sprintf("⚠️ Erro ao consultar IA: %v", err), nil)
			return
		}
		addLog("IA", fmt.Sprintf("Resposta para [%s]: %s", userName, reply))
		// Format Markdown bold/code to HTML or send plain
		formattedReply := strings.ReplaceAll(reply, "**", "<b>")
		formattedReply = strings.ReplaceAll(formattedReply, "**", "</b>")
		err = sendTelegramMessage(token, userChatID, formattedReply, nil)
		if err != nil {
			// Fallback without HTML formatting
			_ = sendTelegramMessage(token, userChatID, html.EscapeString(reply), nil)
		}
	} else {
		sendTelegramMessage(token, userChatID, "🤖 A IA está desativada no momento.", nil)
	}
}

// Broadcast / REST Send
type SendNotificationRequest struct {
	Message string `json:"message"`
	Target  string `json:"target"` // "Jhonatan", "Claudia", "todos", "admin", or <chat_id>
}

func sendNotificationToTargets(target, message string) ([]string, error) {
	settingsMu.RLock()
	token := settings.TelegramBotToken
	adminChatID := settings.AdminChatID
	users := settings.TelegramUsers
	settingsMu.RUnlock()

	if token == "" {
		return nil, fmt.Errorf("token do Telegram não configurado")
	}

	targetClean := strings.ToLower(strings.TrimSpace(target))
	var targets []string

	if targetClean == "" || targetClean == "todos" || targetClean == "all" {
		for cid, u := range users {
			if u.Status == "admin" || u.Status == "allowed" {
				targets = append(targets, cid)
			}
		}
		if len(targets) == 0 && adminChatID != "" {
			targets = append(targets, adminChatID)
		}
	} else if targetClean == "admin" {
		if adminChatID != "" {
			targets = append(targets, adminChatID)
		}
	} else {
		for cid, u := range users {
			if (u.Status == "admin" || u.Status == "allowed") && (strings.EqualFold(u.Name, targetClean) || strings.EqualFold(u.Username, targetClean) || cid == targetClean) {
				targets = append(targets, cid)
			}
		}
		// If specific target not found, fallback to admin
		if len(targets) == 0 && adminChatID != "" {
			targets = append(targets, adminChatID)
		}
	}

	var sent []string
	for _, cid := range targets {
		err := sendTelegramMessage(token, cid, message, nil)
		if err == nil {
			sent = append(sent, cid)
		}
	}

	addLog("INFO", fmt.Sprintf("Notificação enviada para %v: %s", sent, message))
	return sent, nil
}

// HTTP API Handlers
func handleSendTelegram(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req SendNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid JSON payload"}`, http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Message) == "" {
		http.Error(w, `{"error":"Message is empty"}`, http.StatusBadRequest)
		return
	}

	sent, err := sendNotificationToTargets(req.Target, req.Message)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"sent_to": sent,
		"count":   len(sent),
	})
}

func handleGetStatus(w http.ResponseWriter, r *http.Request) {
	settingsMu.RLock()
	defer settingsMu.RUnlock()

	activeCount := 0
	pendingCount := 0
	for _, u := range settings.TelegramUsers {
		if u.Status == "admin" || u.Status == "allowed" {
			activeCount++
		} else if u.Status == "pending" {
			pendingCount++
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":            "Online",
		"version":           AppVersion,
		"bot_name":          settings.BotName,
		"has_token":         settings.TelegramBotToken != "",
		"has_gemini":        settings.GeminiAPIKey != "",
		"gemini_model":      settings.GeminiModel,
		"admin_chat_id":     settings.AdminChatID,
		"active_users":      activeCount,
		"pending_users":     pendingCount,
		"messages_sent":     settings.MessagesSent,
		"messages_received": settings.MessagesReceived,
		"tokens_used":       settings.AITokensUsed,
	})
}

func handleTestNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	settingsMu.RLock()
	token := settings.TelegramBotToken
	adminID := settings.AdminChatID
	botName := settings.BotName
	settingsMu.RUnlock()

	if token == "" || adminID == "" {
		http.Error(w, `{"error":"Token do Telegram ou Chat ID do Administrador não configurados"}`, http.StatusBadRequest)
		return
	}

	testMsg := fmt.Sprintf("🔔 <b>Teste de Notificação Bem-Sucedido!</b>\n\nO assistente <b>%s</b> está conectado e operando normalmente com o Home Assistant!\n🕒 Hora: %s", botName, time.Now().Format("15:04:05"))
	err := sendTelegramMessage(token, adminID, testMsg, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Falha ao enviar mensagem: %v"}`, err), http.StatusInternalServerError)
		return
	}

	callSupervisorService("persistent_notification", "create", map[string]interface{}{
		"title":           "🔔 Teste IABoT Concluído",
		"message":         "Notificação de teste enviada com sucesso ao Telegram do Administrador!",
		"notification_id": "iabot_test_success",
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Notificação de teste enviada com sucesso!",
	})
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		settingsMu.RLock()
		maskedToken := ""
		if len(settings.TelegramBotToken) > 8 {
			maskedToken = settings.TelegramBotToken[:4] + "••••••••" + settings.TelegramBotToken[len(settings.TelegramBotToken)-4:]
		}
		maskedGemini := ""
		if len(settings.GeminiAPIKey) > 8 {
			maskedGemini = settings.GeminiAPIKey[:4] + "••••••••" + settings.GeminiAPIKey[len(settings.GeminiAPIKey)-4:]
		}

		resp := map[string]interface{}{
			"bot_name":           settings.BotName,
			"admin_chat_id":      settings.AdminChatID,
			"enable_ai":          settings.EnableAI,
			"gemini_model":       settings.GeminiModel,
			"models_priority":      settings.ModelsPriority,
			"last_model_used":     settings.LastModelUsed,
			"system_prompt":      settings.SystemPrompt,
			"telegram_bot_token": maskedToken,
			"gemini_api_key":     maskedGemini,
			"has_token":          settings.TelegramBotToken != "",
			"has_gemini":         settings.GeminiAPIKey != "",
		}
		settingsMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == http.MethodPost {
		var payload struct {
			TelegramBotToken string `json:"telegram_bot_token"`
			AdminChatID      string `json:"admin_chat_id"`
			BotName          string `json:"bot_name"`
			EnableAI         bool   `json:"enable_ai"`
			GeminiAPIKey     string `json:"gemini_api_key"`
			GeminiModel      string `json:"gemini_model"`
			ModelsPriority   string `json:"models_priority"`
			SystemPrompt     string `json:"system_prompt"`
		}

		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, `{"error":"Invalid JSON"}`, http.StatusBadRequest)
			return
		}

		settingsMu.Lock()
		if payload.TelegramBotToken != "" && !strings.Contains(payload.TelegramBotToken, "••••") {
			settings.TelegramBotToken = strings.TrimSpace(payload.TelegramBotToken)
		}
		if payload.GeminiAPIKey != "" && !strings.Contains(payload.GeminiAPIKey, "••••") {
			settings.GeminiAPIKey = strings.TrimSpace(payload.GeminiAPIKey)
		}
		if payload.AdminChatID != "" {
			settings.AdminChatID = strings.TrimSpace(payload.AdminChatID)
		}
		if payload.BotName != "" {
			settings.BotName = strings.TrimSpace(payload.BotName)
		}
		if payload.GeminiModel != "" {
			settings.GeminiModel = strings.TrimSpace(payload.GeminiModel)
		}
		if payload.ModelsPriority != "" {
			settings.ModelsPriority = strings.TrimSpace(payload.ModelsPriority)
		}
		if payload.SystemPrompt != "" {
			settings.SystemPrompt = strings.TrimSpace(payload.SystemPrompt)
		}
		settings.EnableAI = payload.EnableAI
		settingsMu.Unlock()

		_ = saveSettings()
		updateSupervisorSensor()
		addLog("INFO", "Configurações do IABoT atualizadas com sucesso")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		return
	}
}

func handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		settingsMu.RLock()
		userList := make([]*TelegramUser, 0, len(settings.TelegramUsers))
		for _, u := range settings.TelegramUsers {
			userList = append(userList, u)
		}
		settingsMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(userList)
		return
	}

	if r.Method == http.MethodPost {
		var req struct {
			Action string `json:"action"` // "approve", "block", "set_admin", "rename", "delete"
			ChatID string `json:"chat_id"`
			Name   string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"Invalid JSON"}`, http.StatusBadRequest)
			return
		}

		settingsMu.Lock()
		u, exists := settings.TelegramUsers[req.ChatID]
		if !exists && req.Action != "delete" {
			settingsMu.Unlock()
			http.Error(w, `{"error":"Usuário não encontrado"}`, http.StatusNotFound)
			return
		}

		token := settings.TelegramBotToken

		switch req.Action {
		case "approve":
			u.Status = "allowed"
			if req.Name != "" {
				u.Name = req.Name
			}
			go sendTelegramMessage(token, req.ChatID, "🎉 <b>Seu acesso foi autorizado pelo Administrador!</b>", nil)
		case "block":
			u.Status = "blocked"
		case "set_admin":
			u.Status = "admin"
			settings.AdminChatID = req.ChatID
		case "rename":
			if req.Name != "" {
				u.Name = req.Name
			}
		case "delete":
			delete(settings.TelegramUsers, req.ChatID)
		}
		settingsMu.Unlock()

		_ = saveSettings()
		updateSupervisorSensor()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		return
	}
}

func handleLogs(w http.ResponseWriter, r *http.Request) {
	logsMu.RLock()
	defer logsMu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(recentLogs)
}

// Web UI / Ingress
func handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}

	htmlContent := `<!DOCTYPE html>
<html lang="pt-BR">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>IABoT - Painel de Controle</title>
  <style>
    :root {
      --bg: #0f172a;
      --card-bg: #1e293b;
      --border: #334155;
      --primary: #0284c7;
      --primary-hover: #0369a1;
      --accent: #8b5cf6;
      --success: #10b981;
      --warning: #f59e0b;
      --danger: #ef4444;
      --text: #f8fafc;
      --text-muted: #94a3b8;
    }
    * { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
    body { background-color: var(--bg); color: var(--text); padding: 20px; line-height: 1.5; }
    .container { max-width: 950px; margin: 0 auto; }
    .header { display: flex; align-items: center; justify-content: space-between; padding-bottom: 20px; border-bottom: 1px solid var(--border); margin-bottom: 24px; }
    .header-title { display: flex; align-items: center; gap: 14px; }
    .header-title h1 { font-size: 24px; font-weight: 700; background: linear-gradient(135deg, #38bdf8, #818cf8); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
    .badge { display: inline-block; padding: 4px 10px; border-radius: 9999px; font-size: 12px; font-weight: 600; }
    .badge-online { background: rgba(16, 185, 129, 0.2); color: #34d399; border: 1px solid rgba(16, 185, 129, 0.4); }
    .badge-admin { background: rgba(245, 158, 11, 0.2); color: #fbbf24; border: 1px solid rgba(245, 158, 11, 0.4); }
    .badge-allowed { background: rgba(16, 185, 129, 0.2); color: #34d399; }
    .badge-pending { background: rgba(239, 68, 68, 0.2); color: #f87171; border: 1px solid rgba(239, 68, 68, 0.4); }
    .nav-tabs { display: flex; gap: 8px; margin-bottom: 20px; border-bottom: 1px solid var(--border); padding-bottom: 8px; flex-wrap: wrap; }
    .tab-btn { background: none; border: none; color: var(--text-muted); font-size: 14px; font-weight: 600; padding: 8px 16px; border-radius: 8px; cursor: pointer; transition: all 0.2s; }
    .tab-btn:hover { color: var(--text); background: rgba(255, 255, 255, 0.05); }
    .tab-btn.active { color: #fff; background: var(--primary); }
    .tab-content { display: none; }
    .tab-content.active { display: block; }
    .card { background: var(--card-bg); border: 1px solid var(--border); border-radius: 12px; padding: 20px; margin-bottom: 20px; box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.2); }
    .card-title { font-size: 16px; font-weight: 600; margin-bottom: 16px; display: flex; align-items: center; justify-content: space-between; }
    .form-group { margin-bottom: 16px; }
    label { display: block; font-size: 13px; font-weight: 600; margin-bottom: 6px; color: var(--text-muted); }
    input[type="text"], input[type="password"], select, textarea { width: 100%; background: #0b1120; border: 1px solid var(--border); border-radius: 8px; padding: 10px 14px; color: #fff; font-size: 14px; }
    input:focus, select:focus, textarea:focus { outline: none; border-color: var(--primary); }
    .btn { display: inline-flex; align-items: center; gap: 6px; padding: 9px 18px; border-radius: 8px; font-size: 14px; font-weight: 600; cursor: pointer; border: none; transition: 0.2s; }
    .btn-primary { background: var(--primary); color: #fff; }
    .btn-primary:hover { background: var(--primary-hover); }
    .btn-success { background: var(--success); color: #fff; }
    .btn-danger { background: var(--danger); color: #fff; }
    .btn-sm { padding: 5px 10px; font-size: 12px; }
    .table-container { overflow-x: auto; }
    table { width: 100%; border-collapse: collapse; text-align: left; font-size: 14px; }
    th { padding: 12px; color: var(--text-muted); border-bottom: 1px solid var(--border); font-size: 12px; text-transform: uppercase; }
    td { padding: 12px; border-bottom: 1px solid rgba(255, 255, 255, 0.05); }
    .code-block { background: #0b1120; border: 1px solid var(--border); border-radius: 8px; padding: 14px; font-family: monospace; font-size: 13px; color: #38bdf8; position: relative; white-space: pre-wrap; word-break: break-all; margin-top: 10px; }
    .copy-btn { position: absolute; top: 10px; right: 10px; background: rgba(255, 255, 255, 0.1); border: none; color: #fff; padding: 4px 8px; border-radius: 4px; font-size: 11px; cursor: pointer; }
    .copy-btn:hover { background: rgba(255, 255, 255, 0.2); }
    .step-box { background: rgba(2, 132, 199, 0.08); border-left: 4px solid var(--primary); padding: 12px 16px; border-radius: 0 8px 8px 0; margin-bottom: 14px; font-size: 14px; }
    .step-num { font-weight: bold; color: #38bdf8; margin-right: 6px; }
    .toast { position: fixed; bottom: 20px; right: 20px; background: #059669; color: #fff; padding: 12px 20px; border-radius: 8px; box-shadow: 0 10px 15px -3px rgba(0,0,0,0.5); opacity: 0; pointer-events: none; transition: 0.3s; z-index: 100; }
    .toast.show { opacity: 1; pointer-events: auto; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <div class="header-title">
        <span style="font-size: 32px;">🤖</span>
        <div>
          <h1>IABoT Assistant</h1>
          <p style="font-size: 13px; color: var(--text-muted);">Telegram Bot &amp; Gemini AI para Home Assistant</p>
        </div>
      </div>
      <div>
        <span class="badge badge-online" id="status-badge">● Online v` + AppVersion + `</span>
      </div>
    </div>

    <div class="nav-tabs">
      <button class="tab-btn active" onclick="switchTab('tab-settings')">⚙️ Configurações &amp; BotFather</button>
      <button class="tab-btn" onclick="switchTab('tab-users')">👥 Usuários do Telegram</button>
      <button class="tab-btn" onclick="switchTab('tab-tests')">🔔 Testes &amp; Automação</button>
      <button class="tab-btn" onclick="switchTab('tab-logs')">📋 Logs do Sistema</button>
    </div>

    <!-- TAB 1: Configurações -->
    <div id="tab-settings" class="tab-content active">
      <div class="card">
        <div class="card-title">
          <span>🚀 Como Criar seu Bot no Telegram (3 Passos Rápidos)</span>
        </div>
        <div class="step-box">
          <span class="step-num">1.</span> Abra o <a href="https://t.me/BotFather" target="_blank" style="color: #38bdf8; text-decoration: underline; font-weight: bold;">@BotFather no Telegram</a> e clique em <b>Iniciar</b>.
        </div>
        <div class="step-box">
          <span class="step-num">2.</span> Digite o comando <code>/newbot</code>. Escolha um nome (ex: <b>Herbie</b>) e um username terminando em <code>_bot</code> (ex: <b>casa_herbie_bot</b>).
        </div>
        <div class="step-box">
          <span class="step-num">3.</span> O BotFather enviará seu <b>HTTP API Token</b>. Copie o token e cole no campo abaixo!
        </div>
      </div>

      <div class="card">
        <div class="card-title">
          <span>🔑 Credenciais &amp; Personalidade</span>
        </div>
        <form id="settings-form" onsubmit="saveSettingsForm(event)">
          <div class="form-group">
            <label>Token HTTP API do Telegram Bot:</label>
            <input type="password" id="telegram_bot_token" placeholder="1234567890:ABCdefGHIjklMNOpqrsTUVwxyz">
            <small style="color: var(--text-muted); display: block; margin-top: 4px;">Nunca compartilhe este token publicamente.</small>
          </div>

          <div class="form-group">
            <label>Chat ID do Administrador (Principal):</label>
            <input type="text" id="admin_chat_id" placeholder="Ex: 123456789 (Preenchido automaticamente ao enviar /start)">
          </div>

          <div class="form-group">
            <label>Chave de API do Google Gemini (IA):</label>
            <input type="password" id="gemini_api_key" placeholder="AIzaSy...">
            <small style="color: var(--text-muted); display: block; margin-top: 4px;">Obtenha gratuitamente em <a href="https://aistudio.google.com/" target="_blank" style="color: #38bdf8;">Google AI Studio</a>.</small>
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 16px;">
            <div class="form-group">
              <label>Nome de Exibição do Bot:</label>
              <input type="text" id="bot_name" value="Herbie">
            </div>
            <div class="form-group">
              <label>Modelo Gemini:</label>
              <select id="gemini_model">
                <option value="gemini-3.6-flash">gemini-3.6-flash (Mais Novo &amp; Principal)</option>
                <option value="gemini-3.5-flash-lite">gemini-3.5-flash-lite (Rápido / Fallback 1)</option>
                <option value="gemini-3.1-flash-lite">gemini-3.1-flash-lite (Econômico / Fallback 2)</option>
              </select>
            </div>
          </div>

          <div class="form-group">
            <label>Fila de Prioridade dos Modelos (Fallback Automático):</label>
            <input type="text" id="models_priority" value="gemini-3.6-flash, gemini-3.5-flash-lite, gemini-3.1-flash-lite">
            <small style="color: var(--text-muted); display: block; margin-top: 4px;">Se o modelo principal atingir limite de cota, o IABoT tenta o próximo da fila automaticamente.</small>
          </div>

          <div class="form-group">
            <label style="display: flex; align-items: center; gap: 8px; cursor: pointer;">
              <input type="checkbox" id="enable_ai" checked>
              <span>Ativar Respostas Inteligentes com IA no Telegram</span>
            </label>
          </div>

          <div class="form-group">
            <label>Prompt de Personalidade do Sistema:</label>
            <textarea id="system_prompt" rows="3"></textarea>
          </div>

          <button type="submit" class="btn btn-primary">💾 Salvar Configurações</button>
        </form>
      </div>
    </div>

    <!-- TAB 2: Usuários -->
    <div id="tab-users" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span>👥 Controle de Acesso ao Telegram Bot</span>
          <button class="btn btn-sm btn-primary" onclick="loadUsers()">🔄 Atualizar</button>
        </div>
        <p style="font-size: 13px; color: var(--text-muted); margin-bottom: 16px;">
          Apenas usuários aprovados podem interagir com a IA ou receber mensagens de automação.
        </p>
        <div class="table-container">
          <table>
            <thead>
              <tr>
                <th>Nome / Username</th>
                <th>Chat ID</th>
                <th>Status</th>
                <th>Ações</th>
              </tr>
            </thead>
            <tbody id="users-table-body">
              <tr><td colspan="4" style="text-align: center; color: var(--text-muted);">Carregando usuários...</td></tr>
            </tbody>
          </table>
        </div>
      </div>
    </div>

    <!-- TAB 3: Testes & Automações -->
    <div id="tab-tests" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span>🔔 Testar Notificação Imediata</span>
        </div>
        <p style="font-size: 13px; color: var(--text-muted); margin-bottom: 12px;">
          Envia uma mensagem de teste para o Administrador no Telegram e gera uma notificação persistente no Home Assistant.
        </p>
        <button class="btn btn-success" onclick="testNotification()">🚀 Disparar Mensagem de Teste</button>
      </div>

      <div class="card">
        <div class="card-title">
          <span>📝 YAML da Automação "Luz do Thor" (Pronto para Colar)</span>
        </div>
        <p style="font-size: 13px; color: var(--text-muted);">
          Copie este bloco e cole na sua automação do Home Assistant:
        </p>
        <div class="code-block" id="yaml-thor">alias: 'Luz do Thor: Ligar e Desligar (Sincronizado)'
description: 'Obedece ao período da casa (Clima e Sol) e notifica no Telegram'
triggers:
  - trigger: state
    entity_id: input_select.periodo_cozinha
    to: dia
    id: ficou_claro
  - trigger: state
    entity_id: input_select.periodo_cozinha
    from: dia
    id: ficou_escuro
conditions: []
actions:
  - choose:
      - conditions:
          - condition: trigger
            id: ficou_claro
        sequence:
          - action: switch.turn_off
            target:
              entity_id: switch.ledthor
      - conditions:
          - condition: trigger
            id: ficou_escuro
        sequence:
          - action: switch.turn_on
            target:
              entity_id: switch.ledthor
          - action: notify.mobile_app_m2012k11ag
            data:
              message: Luz do Thor deve estar ligada agora
          - action: rest_command.herbie_telegram
            data:
              target: Jhonatan
              message: 💡 Luz do Thor foi ligada automaticamente (ficou escuro).
          - action: rest_command.herbie_telegram
            data:
              target: Claudia
              message: 🐶 A luz do cantinho do Thor acabou de acender!
            enabled: false
mode: single<button class="copy-btn" onclick="copySnippet('yaml-thor')">Copiar</button></div>
      </div>

      <div class="card">
        <div class="card-title">
          <span>🎨 Card Lovelace de Ações Rápidas (Avisar Claudia / Status)</span>
        </div>
        <p style="font-size: 13px; color: var(--text-muted);">
          Card do Mushroom para adicionar ao seu Dashboard no Home Assistant:
        </p>
        <div class="code-block" id="yaml-card">type: grid
columns: 2
square: false
cards:
  - type: custom:mushroom-template-card
    primary: IABoT
    secondary: >-
      👥 {{ state_attr('sensor.iabot_status', 'active_users') | default(1) }} usuários · 
      {{ state_attr('sensor.iabot_status', 'messages_sent') | default(0) }} msgs
    icon: mdi:robot-happy
    icon_color: light-blue
    multiline_secondary: true
    tap_action:
      action: navigate
      navigation_path: /hassio/ingress/local_iabot

  - type: custom:mushroom-template-card
    primary: Avisar Claudia
    secondary: Toque = "Chegando!" | Segure = Digitar
    icon: mdi:send-circle
    icon_color: pink
    multiline_secondary: true
    tap_action:
      action: perform-action
      perform_action: rest_command.herbie_telegram
      data:
        target: Claudia
        message: ❤️ Amor, estou chegando! Precisa de alguma coisa?
    hold_action:
      action: more-info
      entity: script.enviar_notificacao_no_telegram<button class="copy-btn" onclick="copySnippet('yaml-card')">Copiar</button></div>
      </div>
    </div>

    <!-- TAB 4: Logs -->
    <div id="tab-logs" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span>📋 Registro de Atividades Recentes</span>
          <button class="btn btn-sm btn-primary" onclick="loadLogs()">🔄 Atualizar</button>
        </div>
        <div id="logs-container" style="max-height: 400px; overflow-y: auto; font-family: monospace; font-size: 12px; background: #0b1120; padding: 12px; border-radius: 8px;">
          Carregando logs...
        </div>
      </div>
    </div>
  </div>

  <div id="toast" class="toast">Ação concluída com sucesso!</div>

  <script>
    function showToast(msg) {
      const t = document.getElementById('toast');
      t.innerText = msg;
      t.classList.add('show');
      setTimeout(() => t.classList.remove('show'), 3000);
    }

    function switchTab(tabId) {
      document.querySelectorAll('.tab-content').forEach(el => el.classList.remove('active'));
      document.querySelectorAll('.tab-btn').forEach(el => el.classList.remove('active'));
      document.getElementById(tabId).classList.add('active');
      event.target.classList.add('active');
      if (tabId === 'tab-users') loadUsers();
      if (tabId === 'tab-logs') loadLogs();
    }

    async function loadSettings() {
      try {
        const res = await fetch('./api/settings');
        const d = await res.json();
        document.getElementById('bot_name').value = d.bot_name || 'Herbie';
        document.getElementById('admin_chat_id').value = d.admin_chat_id || '';
        document.getElementById('gemini_model').value = d.gemini_model || 'gemini-3.6-flash';
        document.getElementById('models_priority').value = d.models_priority || 'gemini-3.6-flash, gemini-3.5-flash-lite, gemini-3.1-flash-lite';
        document.getElementById('enable_ai').checked = d.enable_ai !== false;
        document.getElementById('system_prompt').value = d.system_prompt || '';
        if (d.telegram_bot_token) document.getElementById('telegram_bot_token').placeholder = d.telegram_bot_token;
        if (d.gemini_api_key) document.getElementById('gemini_api_key').placeholder = d.gemini_api_key;
      } catch (e) {
        console.error(e);
      }
    }

    async function saveSettingsForm(e) {
      e.preventDefault();
      const payload = {
        bot_name: document.getElementById('bot_name').value,
        admin_chat_id: document.getElementById('admin_chat_id').value,
        gemini_model: document.getElementById('gemini_model').value,
        models_priority: document.getElementById('models_priority').value,
        enable_ai: document.getElementById('enable_ai').checked,
        system_prompt: document.getElementById('system_prompt').value,
        telegram_bot_token: document.getElementById('telegram_bot_token').value,
        gemini_api_key: document.getElementById('gemini_api_key').value
      };
      try {
        const res = await fetch('./api/settings', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload)
        });
        const d = await res.json();
        if (d.success) {
          showToast('Configurações salvas com sucesso!');
          loadSettings();
        }
      } catch (e) {
        alert('Erro ao salvar: ' + e);
      }
    }

    async function loadUsers() {
      try {
        const res = await fetch('./api/users');
        const users = await res.json();
        const tbody = document.getElementById('users-table-body');
        if (!users || users.length === 0) {
          tbody.innerHTML = '<tr><td colspan="4" style="text-align: center; color: var(--text-muted);">Nenhum usuário detectado ainda. Envie /start para o bot no Telegram!</td></tr>';
          return;
        }
        tbody.innerHTML = users.map(u => {
          let badgeClass = 'badge-allowed';
          if (u.status === 'admin') badgeClass = 'badge-admin';
          if (u.status === 'pending') badgeClass = 'badge-pending';
          if (u.status === 'blocked') badgeClass = 'badge-pending';

          return '' +
            '<tr>' +
            '<td><b>' + escapeHtml(u.name || 'Desconhecido') + '</b> ' + (u.username ? '@' + escapeHtml(u.username) : '') + '</td>' +
            '<td><code>' + u.chat_id + '</code></td>' +
            '<td><span class="badge ' + badgeClass + '">' + u.status.toUpperCase() + '</span></td>' +
            '<td>' +
              (u.status === 'pending' ? '<button class="btn btn-sm btn-success" onclick="userAction(\'approve\', \'' + u.chat_id + '\')">Aprovar</button> ' : '') +
              (u.status !== 'blocked' && u.status !== 'admin' ? '<button class="btn btn-sm btn-danger" onclick="userAction(\'block\', \'' + u.chat_id + '\')">Bloquear</button> ' : '') +
              (u.status === 'blocked' ? '<button class="btn btn-sm btn-success" onclick="userAction(\'approve\', \'' + u.chat_id + '\')">Desbloquear</button> ' : '') +
            '</td>' +
          '</tr>';
        }).join('');
      } catch (e) {
        console.error(e);
      }
    }

    async function userAction(action, chatId) {
      try {
        await fetch('./api/users', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ action: action, chat_id: chatId })
        });
        showToast('Ação concluída!');
        loadUsers();
      } catch (e) {
        alert(e);
      }
    }

    async function testNotification() {
      try {
        const res = await fetch('./api/test_notification', { method: 'POST' });
        const d = await res.json();
        if (d.success) {
          showToast('✅ Mensagem enviada com sucesso ao Telegram!');
        } else {
          alert('Erro: ' + (d.error || 'Falha desconhecida'));
        }
      } catch (e) {
        alert('Erro: ' + e);
      }
    }

    async function loadLogs() {
      try {
        const res = await fetch('./api/logs');
        const logs = await res.json();
        const container = document.getElementById('logs-container');
        if (!logs || logs.length === 0) {
          container.innerHTML = '<span style="color: var(--text-muted)">Sem registros no momento.</span>';
          return;
        }
        container.innerHTML = logs.map(l => {
          let color = '#38bdf8';
          if (l.type === 'WARN') color = '#f59e0b';
          if (l.type === 'IA') color = '#a855f7';
          return '<div style="margin-bottom: 4px;"><span style="color: var(--text-muted)">[' + l.timestamp + ']</span> <b style="color: ' + color + '">[' + l.type + ']</b> ' + escapeHtml(l.message) + '</div>';
        }).join('');
      } catch (e) {
        console.error(e);
      }
    }

    function copySnippet(elementId) {
      const text = document.getElementById(elementId).innerText.replace('Copiar', '').trim();
      navigator.clipboard.writeText(text).then(() => showToast('Copiado para a área de transferência!'));
    }

    function escapeHtml(str) {
      return (str || '').replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    document.addEventListener('DOMContentLoaded', () => {
      loadSettings();
      setInterval(loadLogs, 10000);
    });
  </script>
</body>
</html>`

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(htmlContent))
}

func main() {
	loadSettings()
	addLog("INFO", fmt.Sprintf("Iniciando IABoT v%s...", AppVersion))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Inicia o loop de polling do Telegram
	go startTelegramPolling(ctx)

	// Atualiza sensor do Supervisor a cada 30 segundos
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				updateSupervisorSensor()
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard)
	mux.HandleFunc("/api/send_telegram", handleSendTelegram)
	mux.HandleFunc("/api/telegram/send", handleSendTelegram)
	mux.HandleFunc("/send", handleSendTelegram)
	mux.HandleFunc("/api/status", handleGetStatus)
	mux.HandleFunc("/api/test_notification", handleTestNotification)
	mux.HandleFunc("/api/settings", handleSettings)
	mux.HandleFunc("/api/users", handleUsers)
	mux.HandleFunc("/api/logs", handleLogs)

	port := os.Getenv("PORT")
	if port == "" {
		port = DefaultPort
	}

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		addLog("INFO", fmt.Sprintf("Servidor HTTP rodando na porta %s", port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			addLog("WARN", fmt.Sprintf("Erro no servidor HTTP: %v", err))
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	addLog("INFO", "Encerrando IABoT...")
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
}
