package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	AppVersion       = "1.4.2"
	AppDeveloper     = "Jhonatan Garcia (jhona1234)"
	DefaultPort      = "8099"
	SettingsFilePath = "/data/iabot_settings.json"
	LegacyFilePath   = "/data/settings.json"
)

// Telegram User Struct
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
	Type      string `json:"type"` // "INFO", "WARN", "MSG", "IA", "HA", "VOICE"
	Message   string `json:"message"`
}

type IABoTSettings struct {
	TelegramBotToken  string                   `json:"telegram_bot_token"`
	BotUsername       string                   `json:"bot_username"`
	AdminChatID       string                   `json:"admin_chat_id"`
	BotName           string                   `json:"bot_name"`
	EnableAI          bool                     `json:"enable_ai"`
	GeminiAPIKey      string                   `json:"gemini_api_key"`
	GeminiModel       string                   `json:"gemini_model"`
	ModelsPriority    string                   `json:"models_priority"`
	LastModelUsed     string                   `json:"last_model_used"`
	VoiceResponseMode string                   `json:"voice_response_mode"` // "auto", "always", "never"
	SystemPrompt      string                   `json:"system_prompt"`
	TelegramUsers     map[string]*TelegramUser `json:"telegram_users"`
	MessagesSent      int64                    `json:"messages_sent"`
	MessagesReceived  int64                    `json:"messages_received"`
	AITokensUsed      int64                    `json:"ai_tokens_used"`
}

var (
	settingsMu sync.RWMutex
	settings   IABoTSettings
	logsMu     sync.RWMutex
	recentLogs []LogEntry

	chatHistoryMu sync.RWMutex
	chatHistory   = make(map[string][]GeminiContent)

	httpClient = &http.Client{Timeout: 45 * time.Second}

	defaultModelsPriority = []string{
		"gemini-3.5-flash-lite",
		"gemini-3.1-flash-lite",
		"gemini-3.6-flash",
	}
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
		BotName:           "Herbie",
		EnableAI:          true,
		GeminiModel:       "gemini-3.5-flash-lite",
		ModelsPriority:    "gemini-3.5-flash-lite, gemini-3.1-flash-lite, gemini-3.6-flash",
		VoiceResponseMode: "auto",
		SystemPrompt:      "Você é o assistente inteligente da casa de Jhonatan e Claudia. Você é amigável, direto, prestativo e fala português do Brasil com naturalidade. Você tem controle direto dos dispositivos da casa.",
		TelegramUsers:     make(map[string]*TelegramUser),
	}

	searchPaths := []string{
		"/config/iabot_settings.json",
		"/data/iabot_settings.json",
		"/share/iabot_settings.json",
		"/data/settings.json",
		"/config/settings.json",
	}

	for _, p := range searchPaths {
		if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
			var loaded IABoTSettings
			if err := json.Unmarshal(data, &loaded); err == nil {
				if loaded.TelegramBotToken != "" || loaded.GeminiAPIKey != "" {
					settings = loaded
					break
				} else if settings.TelegramBotToken == "" {
					settings = loaded
				}
			}
		}
	}

	if settings.TelegramUsers == nil {
		settings.TelegramUsers = make(map[string]*TelegramUser)
	}
	if settings.BotName == "" {
		settings.BotName = "Herbie"
	}
	if settings.VoiceResponseMode == "" {
		settings.VoiceResponseMode = "auto"
	}

	// Migração obrigatória: garantir que o modelo padrão seja 3.5-flash-lite
	if strings.Contains(settings.GeminiModel, "1.5") || strings.Contains(settings.GeminiModel, "2.5") || settings.GeminiModel == "" {
		settings.GeminiModel = "gemini-3.5-flash-lite"
	}
	if strings.Contains(settings.ModelsPriority, "1.5") || strings.Contains(settings.ModelsPriority, "2.5") || settings.ModelsPriority == "" {
		settings.ModelsPriority = "gemini-3.5-flash-lite, gemini-3.1-flash-lite, gemini-3.6-flash"
	}
}

func saveSettings() error {
	settingsMu.RLock()
	data, err := json.MarshalIndent(settings, "", "  ")
	settingsMu.RUnlock()
	if err != nil {
		return err
	}
	_ = os.WriteFile(SettingsFilePath, data, 0644)
	_ = os.WriteFile("/config/iabot_settings.json", data, 0644)
	_ = os.WriteFile("/share/iabot_settings.json", data, 0644)
	return nil
}

// ==========================================
// Home Assistant Supervisor API
// ==========================================

type HAEntityState struct {
	EntityID   string                 `json:"entity_id"`
	State      string                 `json:"state"`
	Attributes map[string]interface{} `json:"attributes"`
}

func getHAEntities() []HAEntityState {
	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		return nil
	}
	req, err := http.NewRequest("GET", "http://supervisor/core/api/states", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var states []HAEntityState
	_ = json.NewDecoder(resp.Body).Decode(&states)
	return states
}

func callHAEntityService(domain, service, entityID string) error {
	token := os.Getenv("SUPERVISOR_TOKEN")
	if token == "" {
		return fmt.Errorf("sem permissao SUPERVISOR_TOKEN")
	}
	url := fmt.Sprintf("http://supervisor/core/api/services/%s/%s", domain, service)
	body, _ := json.Marshal(map[string]string{"entity_id": entityID})
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}


type TunnelInfo struct {
	Status        string `json:"status"`
	PeerID        string `json:"peer_id"`
	HttpsURL      string `json:"https_url"`
	PSK           string `json:"psk"`
	ActiveStreams int64  `json:"active_streams"`
}

func getTunnelLiveStatus() *TunnelInfo {
	// 1. Tenta ler direto da API local na porta 8098 (host_network ultra-rápido)
	resp, err := httpClient.Get("http://127.0.0.1:8098/api/status")
	if err == nil {
		defer resp.Body.Close()
		var info TunnelInfo
		if err := json.NewDecoder(resp.Body).Decode(&info); err == nil && info.Status != "" {
			return &info
		}
	}

	// 2. Fallback via entidades do Supervisor
	entities := getHAEntities()
	for _, e := range entities {
		if e.EntityID == "sensor.ha_p2p_tunnel_status" {
			info := TunnelInfo{Status: e.State}
			if u, ok := e.Attributes["https_url"].(string); ok {
				info.HttpsURL = u
			}
			if id, ok := e.Attributes["peer_id"].(string); ok {
				info.PeerID = id
			}
			if k, ok := e.Attributes["psk"].(string); ok {
				info.PSK = k
			}
			return &info
		}
	}
	return nil
}

func buildHAContext() string {
	var sb strings.Builder
	sb.WriteString("\n\nESTADO ATUAL DA CASA EM TEMPO REAL:\n")

	// Túnel P2P
	tInfo := getTunnelLiveStatus()
	if tInfo != nil && tInfo.Status != "" {
		sb.WriteString(fmt.Sprintf("- Túnel HA P2P: %s\n", tInfo.Status))
		if tInfo.HttpsURL != "" {
			sb.WriteString(fmt.Sprintf("- Endereço HTTPS 443 do Túnel: %s\n", tInfo.HttpsURL))
		}
		if tInfo.PeerID != "" {
			sb.WriteString(fmt.Sprintf("- Peer ID do Túnel: %s\n", tInfo.PeerID))
		}
		if tInfo.PSK != "" {
			sb.WriteString(fmt.Sprintf("- Senha PSK da Rede Privada: %s\n", tInfo.PSK))
		} else {
			sb.WriteString("- Senha PSK da Rede Privada: Desativada (Acesso livre)\n")
		}
	} else {
		sb.WriteString("- Túnel HA P2P: Offline ou não detectado.\n")
	}

	// Dispositivos ligados
	entities := getHAEntities()
	var turnedOn []string
	for _, e := range entities {
		if (strings.HasPrefix(e.EntityID, "light.") || strings.HasPrefix(e.EntityID, "switch.")) && e.State == "on" {
			fn := e.EntityID
			if n, ok := e.Attributes["friendly_name"].(string); ok && n != "" {
				fn = n
			}
			turnedOn = append(turnedOn, fn)
		}
	}
	if len(turnedOn) > 0 {
		sb.WriteString(fmt.Sprintf("- Dispositivos ligados na casa (%d): %s\n", len(turnedOn), strings.Join(turnedOn, ", ")))
	} else {
		sb.WriteString("- Dispositivos ligados na casa: Todos desligados no momento.\n")
	}

	return sb.String()
}

func tryControlDevice(text string) (string, bool) {
	textLower := strings.ToLower(strings.TrimSpace(text))
	isTurnOn := strings.HasPrefix(textLower, "ligar ") || strings.HasPrefix(textLower, "acender ") || strings.HasPrefix(textLower, "liga ") || strings.HasPrefix(textLower, "ativa ")
	isTurnOff := strings.HasPrefix(textLower, "desligar ") || strings.HasPrefix(textLower, "apagar ") || strings.HasPrefix(textLower, "desliga ") || strings.HasPrefix(textLower, "desativa ")

	if !isTurnOn && !isTurnOff {
		return "", false
	}

	var targetName string
	if isTurnOn {
		for _, prefix := range []string{"ligar o ", "ligar a ", "ligar ", "acender o ", "acender a ", "acender ", "liga o ", "liga a ", "liga "} {
			if strings.HasPrefix(textLower, prefix) {
				targetName = strings.TrimSpace(textLower[len(prefix):])
				break
			}
		}
	} else {
		for _, prefix := range []string{"desligar o ", "desligar a ", "desligar ", "apagar o ", "apagar a ", "apagar ", "desliga o ", "desliga a ", "desliga "} {
			if strings.HasPrefix(textLower, prefix) {
				targetName = strings.TrimSpace(textLower[len(prefix):])
				break
			}
		}
	}

	if targetName == "" {
		return "", false
	}

	entities := getHAEntities()
	var matchedEntity *HAEntityState
	for _, e := range entities {
		if !strings.HasPrefix(e.EntityID, "light.") && !strings.HasPrefix(e.EntityID, "switch.") && !strings.HasPrefix(e.EntityID, "fan.") {
			continue
		}
		fn := ""
		if n, ok := e.Attributes["friendly_name"].(string); ok {
			fn = strings.ToLower(n)
		}
		id := strings.ToLower(e.EntityID)

		if strings.EqualFold(fn, targetName) || strings.EqualFold(id, targetName) || strings.Contains(fn, targetName) || strings.Contains(id, targetName) {
			matchedEntity = &e
			break
		}
	}

	if matchedEntity != nil {
		parts := strings.Split(matchedEntity.EntityID, ".")
		domain := parts[0]
		action := "turn_on"
		actionLabel := "Liguei"
		if isTurnOff {
			action = "turn_off"
			actionLabel = "Desliguei"
		}
		err := callHAEntityService(domain, action, matchedEntity.EntityID)
		friendlyName := matchedEntity.EntityID
		if fn, ok := matchedEntity.Attributes["friendly_name"].(string); ok && fn != "" {
			friendlyName = fn
		}
		if err != nil {
			addLog("WARN", fmt.Sprintf("Erro ao controlar %s: %v", matchedEntity.EntityID, err))
			return fmt.Sprintf("⚠️ Erro ao tentar controlar %s: %v", friendlyName, err), true
		}
		addLog("HA", fmt.Sprintf("%s dispositivo %s via Telegram", actionLabel, matchedEntity.EntityID))
		return fmt.Sprintf("✅ <b>%s</b> o dispositivo: <b>%s</b>", actionLabel, html.EscapeString(friendlyName)), true
	}

	return "", false
}

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

// ==========================================
// Telegram API & Audio Voice Engine
// ==========================================

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}


var (
	reCodeBlock        = regexp.MustCompile("(?s)```(?:[a-zA-Z0-9_-]+)?\\n?(.*?)```")
	reInlineCode       = regexp.MustCompile("`([^`\\n]+)`")
	reBold             = regexp.MustCompile(`(?s)\*\*(.+?)\*\*`)
	reItalicStar       = regexp.MustCompile(`(?s)\*([^*\n]+?)\*`)
	reItalicUnderscore = regexp.MustCompile(`(?s)_([^\n_]+?)_`)
)

func fetchBotInfo(token string) {
	if token == "" {
		return
	}
	url := fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token)
	resp, err := httpClient.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var res struct {
		OK     bool `json:"ok"`
		Result struct {
			FirstName string `json:"first_name"`
			Username  string `json:"username"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err == nil && res.OK {
		settingsMu.Lock()
		settings.BotUsername = res.Result.Username
		if (settings.BotName == "" || settings.BotName == "Herbie") && res.Result.FirstName != "" {
			settings.BotName = res.Result.FirstName
		}
		settingsMu.Unlock()
		_ = saveSettings()
	}
}

func formatTelegramHTML(text string) string {
	if text == "" {
		return ""
	}

	var codeBlocks []string
	text = reCodeBlock.ReplaceAllStringFunc(text, func(m string) string {
		sub := reCodeBlock.FindStringSubmatch(m)
		if len(sub) > 1 {
			codeBlocks = append(codeBlocks, "<pre><code>"+html.EscapeString(sub[1])+"</code></pre>")
			return fmt.Sprintf("___CB_%d___", len(codeBlocks)-1)
		}
		return m
	})

	var inlineCodes []string
	text = reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		sub := reInlineCode.FindStringSubmatch(m)
		if len(sub) > 1 {
			inlineCodes = append(inlineCodes, "<code>"+html.EscapeString(sub[1])+"</code>")
			return fmt.Sprintf("___IC_%d___", len(inlineCodes)-1)
		}
		return m
	})

	text = html.EscapeString(text)
	text = reBold.ReplaceAllString(text, "<b>$1</b>")
	text = reItalicStar.ReplaceAllString(text, "<i>$1</i>")
	text = reItalicUnderscore.ReplaceAllString(text, "<i>$1</i>")

	for idx, cb := range codeBlocks {
		text = strings.ReplaceAll(text, fmt.Sprintf("___CB_%d___", idx), cb)
	}
	for idx, ic := range inlineCodes {
		text = strings.ReplaceAll(text, fmt.Sprintf("___IC_%d___", idx), ic)
	}

	return text
}

func stripHTMLTags(s string) string {
	s = strings.ReplaceAll(s, "<b>", "")
	s = strings.ReplaceAll(s, "</b>", "")
	s = strings.ReplaceAll(s, "<i>", "")
	s = strings.ReplaceAll(s, "</i>", "")
	s = strings.ReplaceAll(s, "<code>", "")
	s = strings.ReplaceAll(s, "</code>", "")
	s = strings.ReplaceAll(s, "<pre>", "")
	s = strings.ReplaceAll(s, "</pre>", "")
	return html.UnescapeString(s)
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
		// Fallback sem parse_mode em caso de erro no HTML
		fallbackBody := map[string]interface{}{
			"chat_id": chatID,
			"text":    stripHTMLTags(text),
		}
		if markup != nil {
			fallbackBody["reply_markup"] = markup
		}
		fbPayload, _ := json.Marshal(fallbackBody)
		fbResp, fbErr := httpClient.Post(url, "application/json", bytes.NewBuffer(fbPayload))
		if fbErr == nil {
			defer fbResp.Body.Close()
			if fbResp.StatusCode == http.StatusOK {
				settingsMu.Lock()
				settings.MessagesSent++
				settingsMu.Unlock()
				return nil
			}
		}
		return fmt.Errorf("telegram status %d: %s", resp.StatusCode, string(respBytes))
	}

	settingsMu.Lock()
	settings.MessagesSent++
	settingsMu.Unlock()
	return nil
}

func generateTTSAudio(text string) ([]byte, error) {
	// Limpa tags HTML
	cleanText := strings.ReplaceAll(text, "<b>", "")
	cleanText = strings.ReplaceAll(cleanText, "</b>", "")
	cleanText = strings.ReplaceAll(cleanText, "<code>", "")
	cleanText = strings.ReplaceAll(cleanText, "</code>", "")
	cleanText = strings.ReplaceAll(cleanText, "<i>", "")
	cleanText = strings.ReplaceAll(cleanText, "</i>", "")
	cleanText = strings.TrimSpace(cleanText)

	if len(cleanText) > 200 {
		cleanText = cleanText[:200]
	}

	ttsURL := fmt.Sprintf("https://translate.google.com/translate_tts?ie=UTF-8&tl=pt-BR&client=tw-ob&q=%s", url.QueryEscape(cleanText))
	req, err := http.NewRequest("GET", ttsURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tts status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

func sendTelegramVoice(token, chatID string, audioBytes []byte, caption string) error {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)

	_ = w.WriteField("chat_id", chatID)
	if caption != "" {
		_ = w.WriteField("caption", caption)
	}

	part, err := w.CreateFormFile("voice", "voice.mp3")
	if err != nil {
		return err
	}
	_, err = part.Write(audioBytes)
	if err != nil {
		return err
	}

	err = w.Close()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendVoice", token)
	req, err := http.NewRequest("POST", url, &b)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("telegram voice status %d: %s", resp.StatusCode, string(respBytes))
	}

	settingsMu.Lock()
	settings.MessagesSent++
	settingsMu.Unlock()
	return nil
}

func downloadTelegramFile(token, fileID string) ([]byte, error) {
	urlPath := fmt.Sprintf("https://api.telegram.org/bot%s/getFile?file_id=%s", token, fileID)
	resp, err := httpClient.Get(urlPath)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var getFileResp struct {
		OK     bool `json:"ok"`
		Result struct {
			FilePath string `json:"file_path"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&getFileResp); err != nil {
		return nil, err
	}

	downloadURL := fmt.Sprintf("https://api.telegram.org/file/bot%s/%s", token, getFileResp.Result.FilePath)
	fileResp, err := httpClient.Get(downloadURL)
	if err != nil {
		return nil, err
	}
	defer fileResp.Body.Close()

	return io.ReadAll(fileResp.Body)
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

// ==========================================
// Gemini AI Engine com Multimodal e Fallback Silencioso
// ==========================================

type InlineDataPart struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type GeminiPart struct {
	Text       string          `json:"text,omitempty"`
	InlineData *InlineDataPart `json:"inlineData,omitempty"`
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

func callGeminiAI(userChatID, userPrompt string, audioBytes []byte) (string, error) {
	settingsMu.RLock()
	apiKey := settings.GeminiAPIKey
	selectedModel := settings.GeminiModel
	priorityStr := settings.ModelsPriority
	sysPrompt := settings.SystemPrompt
	settingsMu.RUnlock()

	if apiKey == "" {
		return "🤖 A IA está desativada ou a chave do Gemini ainda não foi configurada no painel do IABoT.", nil
	}

	var parts []GeminiPart
	if len(audioBytes) > 0 {
		parts = append(parts, GeminiPart{
			InlineData: &InlineDataPart{
				MimeType: "audio/ogg",
				Data:     base64.StdEncoding.EncodeToString(audioBytes),
			},
		})
		parts = append(parts, GeminiPart{
			Text: "Transcreva este áudio do usuário e atenda prestativo ao pedido sobre a casa ou tire a dúvida em português.",
		})
	} else {
		parts = append(parts, GeminiPart{Text: userPrompt})
	}

	chatHistoryMu.Lock()
	history := chatHistory[userChatID]
	history = append(history, GeminiContent{
		Role:  "user",
		Parts: parts,
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
	haContext := buildHAContext()
	fullSystemPrompt := sysPrompt + haContext
	reqBody.SystemInstruction = &GeminiContent{
		Role:  "system",
		Parts: []GeminiPart{{Text: fullSystemPrompt}},
	}
	payload, _ := json.Marshal(reqBody)

	var lastErr error
	for _, modelName := range modelsToTry {
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)
		resp, err := httpClient.Post(url, "application/json", bytes.NewBuffer(payload))
		if err != nil {
			lastErr = err
			addLog("WARN", fmt.Sprintf("Modelo %s erro de rede (%v). Alternando imediatamente...", modelName, err))
			continue
		}

		var geminiResp GeminiResponse
		err = json.NewDecoder(resp.Body).Decode(&geminiResp)
		resp.Body.Close()

		if err != nil {
			lastErr = err
			addLog("WARN", fmt.Sprintf("Modelo %s erro de decode (%v). Alternando imediatamente...", modelName, err))
			continue
		}

		if geminiResp.Error != nil {
			lastErr = fmt.Errorf("%s", geminiResp.Error.Message)
			addLog("WARN", fmt.Sprintf("Modelo %s cota/erro (%s). Alternando instantaneamente...", modelName, geminiResp.Error.Message))
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

			addLog("IA", fmt.Sprintf("Resposta gerada via [%s] (%d tokens)", modelName, geminiResp.UsageMetadata.TotalTokenCount))
			return replyText, nil
		}
	}

	addLog("WARN", fmt.Sprintf("Todos os modelos da fila falharam: %v", lastErr))
	if lastErr != nil {
		errStr := strings.ToLower(lastErr.Error())
		if strings.Contains(errStr, "api key") || strings.Contains(errStr, "api_key") || strings.Contains(errStr, "not valid") {
			return "⚠️ A chave da API do Gemini configurada é inválida ou expirou. Por favor, acesse o painel do IABoT e salve uma chave válida do Google AI Studio.", nil
		}
	}
	return fmt.Sprintf("⚠️ Todos os modelos da fila (%s) estão ocupados no momento. Aguarde alguns instantes!", strings.Join(modelsToTry, ", ")), nil
}

// ==========================================
// Telegram Polling
// ==========================================

type TelegramVoice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
}

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
		Text  string         `json:"text"`
		Voice *TelegramVoice `json:"voice"`
		Date  int64          `json:"date"`
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

			if up.CallbackQuery != nil {
				handleCallbackQuery(token, up.CallbackQuery)
				continue
			}

			if up.Message != nil && (up.Message.Text != "" || up.Message.Voice != nil) {
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
	Text  string         `json:"text"`
	Voice *TelegramVoice `json:"voice"`
	Date  int64          `json:"date"`
}) {
	userChatID := fmt.Sprintf("%d", msg.Chat.ID)
	userName := strings.TrimSpace(msg.From.FirstName + " " + msg.From.LastName)
	if userName == "" {
		userName = msg.From.Username
	}
	if userName == "" {
		userName = fmt.Sprintf("Usuário %s", userChatID)
	}

	isVoiceInput := (msg.Voice != nil)
	text := strings.TrimSpace(msg.Text)

	settingsMu.Lock()
	settings.MessagesReceived++
	adminChatID := settings.AdminChatID
	botName := settings.BotName
	enableAI := settings.EnableAI
	voiceMode := settings.VoiceResponseMode

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
		welcomeAdmin := fmt.Sprintf("👑 <b>Bem-vindo, Administrador!</b>\n\nVocê é o administrador principal do <b>%s</b>.\n\nComandos disponíveis:\n• <code>/status</code> - Ver status e luzes ligadas\n• <code>/usuarios</code> - Listar usuários autorizados\n• <code>/aprovar &lt;id&gt; [Nome]</code> - Autorizar usuário\n• <code>/bloquear &lt;id&gt;</code> - Bloquear usuário\n\n💡 Você também pode dizer ou mandar áudio com <i>\"ligar astronauta\"</i> ou <i>\"apagar ledthor\"</i>!", botName)
		sendTelegramMessage(token, userChatID, welcomeAdmin, nil)
		updateSupervisorSensor()
		return
	}

	user, exists := settings.TelegramUsers[userChatID]
	if !exists {
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

		pendingMsg := fmt.Sprintf("🔒 <b>Acesso Pendente</b>\n\nOlá <b>%s</b>! Este bot é privado da residência.\nSua solicitação de acesso foi enviada ao administrador.", html.EscapeString(userName))
		sendTelegramMessage(token, userChatID, pendingMsg, nil)

		adminAlert := fmt.Sprintf("🚨 <b>Nova Solicitação de Acesso ao %s!</b>\n\n<b>Nome:</b> %s\n<b>Chat ID:</b> <code>%s</code>\n<b>Username:</b> @%s",
			botName, html.EscapeString(userName), userChatID, html.EscapeString(msg.From.Username))

		markup := &InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{
					{Text: "✅ Autorizar", CallbackData: "auth_allow:" + userChatID},
					{Text: "❌ Bloquear", CallbackData: "auth_block:" + userChatID},
				},
			},
		}
		sendTelegramMessage(token, adminChatID, adminAlert, markup)

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

	if userStatus == "blocked" {
		sendTelegramMessage(token, userChatID, "🚫 Seu acesso está bloqueado pelo administrador.", nil)
		return
	}

	if userStatus == "pending" {
		sendTelegramMessage(token, userChatID, "⏳ Seu acesso ainda está aguardando aprovação do administrador.", nil)
		return
	}

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
		reply := fmt.Sprintf("👋 <b>Olá, %s!</b>\n\nEu sou o <b>%s</b>, assistente inteligente da casa.\nVocê pode:\n• Comandar a casa: <i>\"ligar astronauta\"</i>, <i>\"apagar ledthor\"</i>\n• Ver status da casa: <code>/status</code>\n• Ver túnel P2P e links: <code>/tunel</code>\n• Bater papo por texto ou mensagem de voz!", html.EscapeString(userName), botName)
		sendTelegramMessage(token, userChatID, reply, nil)
		return
	}

	if text == "/status" {
		entities := getHAEntities()
		var turnedOn []string
		for _, e := range entities {
			if (strings.HasPrefix(e.EntityID, "light.") || strings.HasPrefix(e.EntityID, "switch.")) && e.State == "on" {
				fn := e.EntityID
				if n, ok := e.Attributes["friendly_name"].(string); ok && n != "" {
					fn = n
				}
				turnedOn = append(turnedOn, "- "+fn)
			}
		}

		settingsMu.RLock()
		model := settings.GeminiModel
		lastModel := settings.LastModelUsed
		if lastModel == "" {
			lastModel = model
		}
		sent := settings.MessagesSent
		recv := settings.MessagesReceived
		settingsMu.RUnlock()

		var sb strings.Builder
		if len(turnedOn) > 0 {
			sb.WriteString("💡 <b>Ligados:</b>\n")
			sb.WriteString(strings.Join(turnedOn, "\n"))
			sb.WriteString("\n\n")
		} else {
			sb.WriteString("🌑 <b>Tudo apagado na casa.</b>\n\n")
		}

		sb.WriteString(fmt.Sprintf("🤖 <b>Status do %s</b>\n\n• Sistema: Online\n• Modelo IA: <code>%s</code>\n• Mensagens enviadas: %d\n• Mensagens recebidas: %d\n• Hora: %s",
			botName, lastModel, sent, recv, time.Now().Format("15:04:05")))

		sendTelegramMessage(token, userChatID, sb.String(), nil)
		return
	}

	// Consulta Rápida do Túnel P2P (Peer ID, HTTPS, PSK)
	textLowerClean := strings.ToLower(strings.TrimSpace(text))
	isTunnelQuery := text == "/tunel" || text == "/tunnel" ||
		textLowerClean == "tunel" || textLowerClean == "túnel" || textLowerClean == "tunel?" || textLowerClean == "túnel?" ||
		strings.Contains(textLowerClean, "peer id") ||
		strings.Contains(textLowerClean, "link do tunel") || strings.Contains(textLowerClean, "link do túnel") ||
		strings.Contains(textLowerClean, "endereco do tunel") || strings.Contains(textLowerClean, "endereço do túnel") ||
		strings.Contains(textLowerClean, "como está túnel") || strings.Contains(textLowerClean, "como esta túnel") ||
		strings.Contains(textLowerClean, "como está o túnel") || strings.Contains(textLowerClean, "como esta o tunel") ||
		strings.Contains(textLowerClean, "qual https") || strings.Contains(textLowerClean, "qual o https")

	if isTunnelQuery {
		tInfo := getTunnelLiveStatus()
		if tInfo != nil && tInfo.Status != "" {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("🚀 <b>HA P2P Tunnel:</b> %s\n\n", tInfo.Status))
			if tInfo.HttpsURL != "" {
				sb.WriteString(fmt.Sprintf("🔗 <b>HTTPS 443:</b> %s\n", tInfo.HttpsURL))
			}
			if tInfo.PeerID != "" {
				sb.WriteString(fmt.Sprintf("🆔 <b>Peer ID:</b> <code>%s</code>\n", tInfo.PeerID))
			}
			if tInfo.PSK != "" {
				sb.WriteString(fmt.Sprintf("🔑 <b>PSK:</b> <code>%s</code>\n", tInfo.PSK))
			} else {
				sb.WriteString("🔑 <b>PSK:</b> Desativada (Acesso livre)\n")
			}
			sendTelegramMessage(token, userChatID, sb.String(), nil)
			return
		} else {
			sendTelegramMessage(token, userChatID, "⚠️ O complemento <b>HA P2P Tunnel</b> não está ativo ou não foi detectado no momento.", nil)
			return
		}
	}

	// 1. Controle Direto de Dispositivos por Texto
	if !isVoiceInput {
		if reply, handled := tryControlDevice(text); handled {
			sendTelegramMessage(token, userChatID, reply, nil)
			return
		}
	}

	// 2. Processamento com IA Gemini (Texto ou Mensagem de Voz)
	if enableAI {
		var audioBytes []byte
		if isVoiceInput {
			addLog("VOICE", fmt.Sprintf("Recebida mensagem de voz de [%s]", userName))
			downloaded, err := downloadTelegramFile(token, msg.Voice.FileID)
			if err != nil {
				addLog("WARN", fmt.Sprintf("Falha ao baixar áudio do Telegram: %v", err))
				sendTelegramMessage(token, userChatID, "⚠️ Não consegui baixar o áudio enviado.", nil)
				return
			}
			audioBytes = downloaded
		} else {
			addLog("MSG", fmt.Sprintf("[%s]: %s", userName, text))
		}

		reply, err := callGeminiAI(userChatID, text, audioBytes)
		if err != nil {
			addLog("WARN", fmt.Sprintf("Erro IA Gemini: %v", err))
			sendTelegramMessage(token, userChatID, fmt.Sprintf("⚠️ Erro ao consultar IA: %v", err), nil)
			return
		}

		// Se o Gemini transcreveu e identificou controle de dispositivo:
		if actionReply, handled := tryControlDevice(reply); handled {
			reply = actionReply
		}

		addLog("IA", fmt.Sprintf("Resposta para [%s]: %s", userName, reply))

		// Determina se deve responder por Áudio
		shouldReplyVoice := (voiceMode == "always") || (voiceMode == "auto" && isVoiceInput)

		if shouldReplyVoice {
			audioResp, err := generateTTSAudio(reply)
			if err == nil && len(audioResp) > 0 {
				err = sendTelegramVoice(token, userChatID, audioResp, "")
				if err == nil {
					return
				}
				addLog("WARN", fmt.Sprintf("Falha ao enviar voz no Telegram: %v. Enviando texto.", err))
			}
		}

		// Envio por Texto padrão com formatação limpa
		formattedReply := formatTelegramHTML(reply)
		_ = sendTelegramMessage(token, userChatID, formattedReply, nil)
	} else {
		sendTelegramMessage(token, userChatID, "🤖 A IA está desativada no momento.", nil)
	}
}

// ==========================================
// REST Send Notification
// ==========================================

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
		"status":              "Online",
		"version":             AppVersion,
		"bot_name":            settings.BotName,
		"bot_username":       settings.BotUsername,
		"has_token":           settings.TelegramBotToken != "",
		"has_gemini":          settings.GeminiAPIKey != "",
		"gemini_model":        settings.GeminiModel,
		"models_priority":     settings.ModelsPriority,
		"last_model_used":     settings.LastModelUsed,
		"voice_response_mode": settings.VoiceResponseMode,
		"admin_chat_id":       settings.AdminChatID,
		"active_users":        activeCount,
		"pending_users":       pendingCount,
		"messages_sent":       settings.MessagesSent,
		"messages_received":   settings.MessagesReceived,
		"tokens_used":         settings.AITokensUsed,
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

	testMsg := fmt.Sprintf("🔔 <b>Teste de Notificação Bem-Sucedido!</b>\n\nO assistente <b>%s</b> está conectado e operando normalmente!\n🕒 Hora: %s", botName, time.Now().Format("15:04:05"))
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
		"message": "Notificação enviada com sucesso!",
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
			"bot_name":            settings.BotName,
		"bot_username":       settings.BotUsername,
			"admin_chat_id":       settings.AdminChatID,
			"enable_ai":           settings.EnableAI,
			"gemini_model":        settings.GeminiModel,
			"models_priority":     settings.ModelsPriority,
			"last_model_used":     settings.LastModelUsed,
			"voice_response_mode": settings.VoiceResponseMode,
			"system_prompt":       settings.SystemPrompt,
			"telegram_bot_token":  maskedToken,
			"gemini_api_key":      maskedGemini,
			"has_token":           settings.TelegramBotToken != "",
			"has_gemini":          settings.GeminiAPIKey != "",
		}
		settingsMu.RUnlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == http.MethodPost {
		var payload struct {
			TelegramBotToken  string `json:"telegram_bot_token"`
			AdminChatID       string `json:"admin_chat_id"`
			BotName           string `json:"bot_name"`
			EnableAI          bool   `json:"enable_ai"`
			GeminiAPIKey      string `json:"gemini_api_key"`
			GeminiModel       string `json:"gemini_model"`
			ModelsPriority    string `json:"models_priority"`
			VoiceResponseMode string `json:"voice_response_mode"`
			SystemPrompt      string `json:"system_prompt"`
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
		if payload.VoiceResponseMode != "" {
			settings.VoiceResponseMode = strings.TrimSpace(payload.VoiceResponseMode)
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

// ==========================================
// Ingress Dashboard Web
// ==========================================

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
  <title>IABoT - Assistente Inteligente</title>
  <style>
    :root {
      --bg: #090d16;
      --card-bg: #131c2e;
      --border: #22324d;
      --primary: #06b6d4;
      --primary-hover: #0891b2;
      --accent: #a855f7;
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
    .header-title h1 { font-size: 24px; font-weight: 700; background: linear-gradient(135deg, #06b6d4, #a855f7); -webkit-background-clip: text; -webkit-text-fill-color: transparent; }
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
    .card { background: var(--card-bg); border: 1px solid var(--border); border-radius: 12px; padding: 20px; margin-bottom: 20px; box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.3); }
    .card-title { font-size: 16px; font-weight: 600; margin-bottom: 16px; display: flex; align-items: center; justify-content: space-between; }
    .form-group { margin-bottom: 16px; }
    label { display: block; font-size: 13px; font-weight: 600; margin-bottom: 6px; color: var(--text-muted); }
    input[type="text"], input[type="password"], select, textarea { width: 100%; background: #070b13; border: 1px solid var(--border); border-radius: 8px; padding: 10px 14px; color: #fff; font-size: 14px; }
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
    .code-block { background: #070b13; border: 1px solid var(--border); border-radius: 8px; padding: 14px; font-family: monospace; font-size: 13px; color: #38bdf8; position: relative; white-space: pre-wrap; word-break: break-all; margin-top: 10px; }
    .copy-btn { position: absolute; top: 10px; right: 10px; background: rgba(255, 255, 255, 0.1); border: none; color: #fff; padding: 4px 8px; border-radius: 4px; font-size: 11px; cursor: pointer; }
    .copy-btn:hover { background: rgba(255, 255, 255, 0.2); }
    .step-box { background: rgba(6, 182, 212, 0.08); border-left: 4px solid var(--primary); padding: 12px 16px; border-radius: 0 8px 8px 0; margin-bottom: 14px; font-size: 14px; }
    .step-num { font-weight: bold; color: #06b6d4; margin-right: 6px; }
    .toast { position: fixed; bottom: 20px; right: 20px; background: #059669; color: #fff; padding: 12px 20px; border-radius: 8px; box-shadow: 0 10px 15px -3px rgba(0,0,0,0.5); opacity: 0; pointer-events: none; transition: 0.3s; z-index: 100; }
    .toast.show { opacity: 1; pointer-events: auto; }
  </style>
</head>
<body>
  <div class="container">
    <div class="header">
      <div class="header-title">
        <span style="font-size: 34px;">🤖</span>
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
      <button class="tab-btn" onclick="switchTab('tab-cards')">📱 Cards Prontos (Mushroom)</button>
      <button class="tab-btn" onclick="switchTab('tab-logs')">📋 Logs do Sistema</button>
    </div>

    <!-- TAB 1: Configurações -->
    <div id="tab-settings" class="tab-content active">
      <!-- Bot Actions Bar (Visível quando o Token já está preenchido) -->
      <div id="bot-actions-bar" style="display: none; gap: 10px; margin-bottom: 20px; flex-wrap: wrap; align-items: center;">
        <a id="bot-chat-btn" href="https://t.me/BotFather" target="_blank" class="btn btn-primary">💬 Conversar com o Bot no Telegram</a>
        <a href="https://t.me/BotFather" target="_blank" class="btn btn-sm" style="background: rgba(255,255,255,0.1); color: #fff;">⚙️ Gerenciar Bot no @BotFather</a>
        <button type="button" class="btn btn-sm" style="background: rgba(255,255,255,0.1); color: #fff;" onclick="toggleBotFatherTutorial()">❓ Guia @BotFather (Ajuda)</button>
      </div>

      <!-- Tutorial BotFather (Exibido se o token estiver vazio ou quando o usuário clica no botão de ajuda) -->
      <div id="botfather-tutorial" class="card" style="display: none;">
        <div class="card-title">
          <span>🚀 Como Criar seu Bot no Telegram com o @BotFather</span>
          <button type="button" class="btn btn-sm" style="background: rgba(255,255,255,0.1); color: #fff;" onclick="toggleBotFatherTutorial()">✕ Fechar</button>
        </div>
        <div class="step-box">
          <span class="step-num">1.</span> Abra o <a href="https://t.me/BotFather" target="_blank" style="color: #06b6d4; text-decoration: underline; font-weight: bold;">@BotFather no Telegram</a> e clique em <b>Iniciar</b>.
        </div>
        <div class="step-box">
          <span class="step-num">2.</span> Digite <code>/newbot</code>. Escolha o nome e o username. Exemplos inspirados nos assistentes do Homem de Ferro:
          <ul style="margin-top: 6px; margin-left: 20px; color: var(--text);">
            <li><b>J.A.R.V.I.S.</b> ➔ Username: <code>meu_jarvis_bot</code></li>
            <li><b>Sexta-Feira (F.R.I.D.A.Y.)</b> ➔ Username: <code>minha_sextafeira_bot</code></li>
            <li><b>E.D.I.T.H.</b> ➔ Username: <code>minha_edith_bot</code></li>
            <li><b>Herbie</b> ➔ Username: <code>meu_herbie_bot</code></li>
          </ul>
        </div>
        <div class="step-box">
          <span class="step-num">3.</span> O BotFather enviará seu <b>HTTP API Token</b>. Cole no campo abaixo e salve!
        </div>
      </div>

      <div class="card">
        <div class="card-title">
          <span>🔑 Credenciais &amp; Modelos Gemini</span>
        </div>
        <form id="settings-form" onsubmit="saveSettingsForm(event)">
          <div class="form-group">
            <label>Token HTTP API do Telegram Bot:</label>
            <input type="password" id="telegram_bot_token" placeholder="1234567890:ABCdefGHIjklMNOpqrsTUVwxyz">
            <small style="color: var(--text-muted); display: block; margin-top: 4px;">Protegido localmente no Home Assistant.</small>
          </div>

          <div class="form-group">
            <label>Chat ID do Administrador (Principal):</label>
            <input type="text" id="admin_chat_id" placeholder="Ex: 123456789 (Preenchido automaticamente ao enviar /start)">
          </div>

          <div class="form-group">
            <label>Chave de API do Google Gemini (IA):</label>
            <input type="password" id="gemini_api_key" placeholder="AIzaSy...">
            <small style="color: var(--text-muted); display: block; margin-top: 4px;">Obtenha gratuitamente em <a href="https://aistudio.google.com/" target="_blank" style="color: #06b6d4;">Google AI Studio</a>.</small>
          </div>

          <div style="display: grid; grid-template-columns: 1fr 1fr; gap: 16px;">
            <div class="form-group">
              <label>Nome de Exibição do Bot:</label>
              <input type="text" id="bot_name" value="Herbie">
            </div>
            <div class="form-group">
              <label>Modelo Gemini Principal:</label>
              <select id="gemini_model">
                <option value="gemini-3.5-flash-lite">gemini-3.5-flash-lite (Padrão / 500 RPD / 15 RPM)</option>
                <option value="gemini-3.1-flash-lite">gemini-3.1-flash-lite (Econômico / 500 RPD / 15 RPM)</option>
                <option value="gemini-3.6-flash">gemini-3.6-flash (Mais Inteligente / Atenção: 20 RPD)</option>
              </select>
            </div>
          </div>

          <div class="form-group">
            <label>Fila de Fallback Automático (Tentativa Instantânea e Silenciosa):</label>
            <input type="text" id="models_priority" value="gemini-3.5-flash-lite, gemini-3.1-flash-lite, gemini-3.6-flash">
            <small style="color: var(--text-muted); display: block; margin-top: 4px;">
              💡 <b>Sobre Limites:</b> O Gemini 3.5 Lite e 3.1 Lite oferecem 500 requisições/dia. O Gemini 3.6 Flash oferece apenas 20 requisições/dia na conta gratuita.
            </small>
          </div>

          <div class="form-group">
            <label>Modo de Resposta por Voz / Áudio no Telegram:</label>
            <select id="voice_response_mode">
              <option value="auto">Áudio se eu mandar áudio (Recomendado)</option>
              <option value="always">Responder sempre por áudio (Voz)</option>
              <option value="never">Responder sempre por texto</option>
            </select>
            <small style="color: var(--text-muted); display: block; margin-top: 4px;">Gera mensagem de voz natural em português via áudio no Telegram.</small>
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

          <div style="display: flex; gap: 12px; align-items: center;">
            <button type="submit" class="btn btn-primary">💾 Salvar Configurações</button>
            <button type="button" class="btn btn-success" onclick="testNotification()">🚀 Testar Envio no Telegram</button>
          </div>
        </form>
      </div>
    </div>

    <!-- TAB 2: Usuários -->
    <div id="tab-users" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span>👥 Controle de Usuários do Telegram</span>
          <button class="btn btn-sm btn-primary" onclick="loadUsers()">🔄 Atualizar</button>
        </div>
        <p style="font-size: 13px; color: var(--text-muted); margin-bottom: 16px;">
          Apenas usuários aprovados podem comandar a casa ou bater papo. Estranhos ficam bloqueados até autorização do Admin.
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

    <!-- TAB 3: Cards Mushroom Prontos (Personalizados Dinamicamente) -->
    <div id="tab-cards" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span>Card 1: Notificação Rápida Personalizada (Estilo Mushroom)</span>
        </div>
        <p style="font-size: 13px; color: var(--text-muted);">
          Card gerado dinamicamente para os contatos autorizados da casa:
        </p>
        <div class="code-block" id="card-1">Carregando card personalizado...<button class="copy-btn" onclick="copySnippet('card-1')">Copiar</button></div>
      </div>

      <div class="card">
        <div class="card-title">
          <span>Card 2: Grade de Mensagens com 1 Toque (Chegando, Recado, Boa Noite)</span>
        </div>
        <p style="font-size: 13px; color: var(--text-muted);">
          Grade compacta para disparar avisos frequentes no Telegram com 1 toque:
        </p>
        <div class="code-block" id="card-2">Carregando grade de avisos...<button class="copy-btn" onclick="copySnippet('card-2')">Copiar</button></div>
      </div>

      <div class="card">
        <div class="card-title">
          <span>Card 3: Status IABoT &amp; Atalho para Ingress</span>
        </div>
        <p style="font-size: 13px; color: var(--text-muted);">
          Card com contadores de mensagens, modelo ativo e atalho direto:
        </p>
        <div class="code-block" id="card-3">type: custom:mushroom-template-card
primary: IABoT
secondary: >-
  👥 {{ state_attr('sensor.iabot_status', 'active_users') | default(1) }} usuários · 
  {{ state_attr('sensor.iabot_status', 'messages_sent') | default(0) }} enviadas
icon: mdi:robot-happy
icon_color: light-blue
multiline_secondary: true
tap_action:
  action: navigate
  navigation_path: /iabot<button class="copy-btn" onclick="copySnippet('card-3')">Copiar</button></div>
      </div>

      <div class="card">
        <div class="card-title">
          <span>⚙️ Configuração da Ação no configuration.yaml</span>
        </div>
        <p style="font-size: 13px; color: var(--text-muted);">
          Adicione ao seu configuration.yaml para liberar as ações no Home Assistant:
        </p>
        <div class="code-block" id="yaml-conf">rest_command:
  iabot_telegram:
    url: "http://127.0.0.1:8099/api/send_telegram"
    method: POST
    headers:
      content-type: "application/json"
    payload: '{"message": "{{ message }}", "target": "{{ target | default(\x27todos\x27) }}"}'

  herbie_telegram:
    url: "http://127.0.0.1:8099/api/send_telegram"
    method: POST
    headers:
      content-type: "application/json"
    payload: '{"message": "{{ message }}", "target": "{{ target | default(\x27todos\x27) }}"}'<button class="copy-btn" onclick="copySnippet('yaml-conf')">Copiar</button></div>
      </div>
    </div>

    <!-- TAB 4: Logs -->
    <div id="tab-logs" class="tab-content">
      <div class="card">
        <div class="card-title">
          <span>📋 Registro de Atividades Recentes</span>
          <button class="btn btn-sm btn-primary" onclick="loadLogs()">🔄 Atualizar</button>
        </div>
        <div id="logs-container" style="max-height: 400px; overflow-y: auto; font-family: monospace; font-size: 12px; background: #070b13; padding: 12px; border-radius: 8px;">
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

    function toggleBotFatherTutorial() {
      const el = document.getElementById('botfather-tutorial');
      el.style.display = (el.style.display === 'none' || el.style.display === '') ? 'block' : 'none';
    }

    function updateBotActions(d) {
      const hasToken = !!d.has_token;
      const botName = d.bot_name || 'Herbie';
      const botUsername = d.bot_username || '';

      const tutorial = document.getElementById('botfather-tutorial');
      const actionsBar = document.getElementById('bot-actions-bar');
      const chatBtn = document.getElementById('bot-chat-btn');

      if (hasToken) {
        tutorial.style.display = 'none';
        actionsBar.style.display = 'flex';
        if (botUsername) {
          chatBtn.innerText = '💬 Abrir ' + botName + ' (@' + botUsername + ') no Telegram';
          chatBtn.href = 'https://t.me/' + botUsername;
        } else {
          chatBtn.innerText = '💬 Abrir ' + botName + ' no Telegram';
          chatBtn.href = 'https://t.me/BotFather';
        }
      } else {
        tutorial.style.display = 'block';
        actionsBar.style.display = 'none';
      }
    }

    function updateDynamicCards(users) {
      const approved = (users || []).filter(u => u.status === 'admin' || u.status === 'allowed');
      let targetUser = 'Familia';
      if (approved.length > 1) {
        targetUser = approved[1].name || approved[1].username || 'Familia';
      } else if (approved.length > 0) {
        targetUser = approved[0].name || approved[0].username || 'Familia';
      }

      const card1 = 'type: custom:mushroom-template-card\n' +
        'primary: Avisar ' + targetUser + '\n' +
        'secondary: >-\n' +
        '  Toque = "Chegando!" | Segure = Digitar\n' +
        'icon: mdi:send-circle\n' +
        'icon_color: green\n' +
        'multiline_secondary: true\n' +
        'tap_action:\n' +
        '  action: perform-action\n' +
        '  perform_action: rest_command.iabot_telegram\n' +
        '  data:\n' +
        '    target: ' + targetUser + '\n' +
        '    message: 🚗 Estou a caminho de casa agora!\n' +
        'hold_action:\n' +
        '  action: more-info\n' +
        '  entity: script.enviar_iabot_telegram';
      const c1El = document.getElementById('card-1');
      if (c1El) c1El.innerHTML = card1 + '<button class="copy-btn" onclick="copySnippet(\'card-1\')">Copiar</button>';

      const card2 = 'type: grid\n' +
        'columns: 3\n' +
        'square: false\n' +
        'cards:\n' +
        '  - type: custom:mushroom-template-card\n' +
        '    primary: Chegando!\n' +
        '    icon: mdi:car-hatchback\n' +
        '    icon_color: green\n' +
        '    tap_action:\n' +
        '      action: perform-action\n' +
        '      perform_action: rest_command.iabot_telegram\n' +
        '      data:\n' +
        '        target: todos\n' +
        '        message: 🚗 Estou a caminho de casa agora!\n\n' +
        '  - type: custom:mushroom-template-card\n' +
        '    primary: Recado\n' +
        '    icon: mdi:message-text\n' +
        '    icon_color: amber\n' +
        '    tap_action:\n' +
        '      action: perform-action\n' +
        '      perform_action: rest_command.iabot_telegram\n' +
        '      data:\n' +
        '        target: ' + targetUser + '\n' +
        '        message: 🛒 Precisa de algo do mercado?\n\n' +
        '  - type: custom:mushroom-template-card\n' +
        '    primary: Boa Noite\n' +
        '    icon: mdi:weather-night\n' +
        '    icon_color: indigo\n' +
        '    tap_action:\n' +
        '      action: perform-action\n' +
        '      perform_action: rest_command.iabot_telegram\n' +
        '      data:\n' +
        '        target: todos\n' +
        '        message: 🌙 Casa em modo descanso. Boa noite!';
      const c2El = document.getElementById('card-2');
      if (c2El) c2El.innerHTML = card2 + '<button class="copy-btn" onclick="copySnippet(\'card-2\')">Copiar</button>';
    }

    async function loadSettings() {
      try {
        const res = await fetch('./api/settings');
        const d = await res.json();
        document.getElementById('bot_name').value = d.bot_name || 'Herbie';
        document.getElementById('admin_chat_id').value = d.admin_chat_id || '';
        document.getElementById('gemini_model').value = d.gemini_model || 'gemini-3.5-flash-lite';
        document.getElementById('models_priority').value = d.models_priority || 'gemini-3.5-flash-lite, gemini-3.1-flash-lite, gemini-3.6-flash';
        document.getElementById('voice_response_mode').value = d.voice_response_mode || 'auto';
        document.getElementById('enable_ai').checked = d.enable_ai !== false;
        document.getElementById('system_prompt').value = d.system_prompt || '';
        if (d.telegram_bot_token) document.getElementById('telegram_bot_token').placeholder = d.telegram_bot_token;
        if (d.gemini_api_key) document.getElementById('gemini_api_key').placeholder = d.gemini_api_key;
        updateBotActions(d);
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
        voice_response_mode: document.getElementById('voice_response_mode').value,
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
        updateDynamicCards(users);
        tbody.innerHTML = users.map(u => {
          let badgeClass = 'badge-allowed';
          if (u.status === 'admin') badgeClass = 'badge-admin';
          if (u.status === 'pending') badgeClass = 'badge-pending';
          if (u.status === 'blocked') badgeClass = 'badge-pending';

          var btns = '';
          if (u.status === 'pending') btns += '<button class="btn btn-sm btn-success" onclick="userAction(\x27approve\x27, \x27' + u.chat_id + '\x27)">Aprovar</button> ';
          if (u.status !== 'blocked' && u.status !== 'admin') btns += '<button class="btn btn-sm btn-danger" onclick="userAction(\x27block\x27, \x27' + u.chat_id + '\x27)">Bloquear</button> ';
          if (u.status === 'blocked') btns += '<button class="btn btn-sm btn-success" onclick="userAction(\x27approve\x27, \x27' + u.chat_id + '\x27)">Desbloquear</button> ';

          return '<tr>' +
            '<td><b>' + escapeHtml(u.name || 'Desconhecido') + '</b> ' + (u.username ? '@' + escapeHtml(u.username) : '') + '</td>' +
            '<td><code>' + u.chat_id + '</code></td>' +
            '<td><span class="badge ' + badgeClass + '">' + u.status.toUpperCase() + '</span></td>' +
            '<td>' + btns + '</td>' +
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
          let color = '#06b6d4';
          if (l.type === 'WARN') color = '#f59e0b';
          if (l.type === 'IA') color = '#a855f7';
          if (l.type === 'HA') color = '#10b981';
          if (l.type === 'VOICE') color = '#ec4899';
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
      loadUsers();
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
	settingsMu.RLock()
	curToken := settings.TelegramBotToken
	settingsMu.RUnlock()
	if curToken != "" {
		go fetchBotInfo(curToken)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go startTelegramPolling(ctx)

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
