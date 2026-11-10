package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	ProtocolID       = protocol.ID("/ha-p2p-tunnel/1.0.0")
	ProtocolIDReport = protocol.ID("/ha-p2p-tunnel-report/1.0.0")
	AppVersion       = "0.4.0"
	AppDeveloper     = "Jhonatan Garcia (jhona1234)"
	GitHubApkUrl     = "https://github.com/jhona1234-del/HA-P2P-Tunnel/raw/main/ha_p2p_tunnel/ha-p2p-tunnel.apk"
	SettingsFilePath = "/data/settings.json"
	OptionsFilePath  = "/data/options.json"
)

type ConnectionEvent struct {
	PeerID    string    `json:"peer_id"`
	Remote    string    `json:"remote"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Duration  string    `json:"duration"`
	BytesIn   int64     `json:"bytes_in"`
	BytesOut  int64     `json:"bytes_out"`
	Active    bool      `json:"active"`
}

type DeviceSession struct {
	PeerID        string    `json:"peer_id"`
	RemoteAddr    string    `json:"remote_addr"`
	NetworkType   string    `json:"network_type"`
	ActiveStreams int       `json:"active_streams"`
	TotalStreams  int64     `json:"total_streams"`
	BytesIn       int64     `json:"bytes_in"`
	BytesOut      int64     `json:"bytes_out"`
	FirstSeen     time.Time `json:"first_seen"`
	LastSeen      time.Time `json:"last_seen"`
	IsOnline      bool      `json:"is_online"`
}

type ClientReport struct {
	PeerID            string    `json:"peer_id"`
	Device            string    `json:"device"`
	ConnectedAt       string    `json:"connected_at"`
	ConnectDurationMs int64     `json:"connect_duration_ms"`
	UsedCache         bool      `json:"used_cache"`
	HumanSummary      []string  `json:"human_summary"`
	Logs              []string  `json:"logs"`
	ReceivedAt        time.Time `json:"received_at"`
}

type TunnelStats struct {
	mu              sync.Mutex
	ActiveStreams   int64
	TotalStreams    int64
	RejectedStreams int64
	TotalBytesIn    int64
	TotalBytesOut   int64
	HttpsUrl        string
	Devices         map[string]*DeviceSession
	History         []*ConnectionEvent
	ClientReports   []*ClientReport
	StartedAt       time.Time
}

func (s *TunnelStats) setHttpsUrl(url string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.HttpsUrl = url
}

func (s *TunnelStats) getHttpsUrl() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HttpsUrl
}

func (s *TunnelStats) addEvent(e *ConnectionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append([]*ConnectionEvent{e}, s.History...)
	if len(s.History) > 30 {
		s.History = s.History[:30]
	}
}

func (s *TunnelStats) addClientReport(r *ClientReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ClientReports = append([]*ClientReport{r}, s.ClientReports...)
	if len(s.ClientReports) > 10 {
		s.ClientReports = s.ClientReports[:10]
	}
}

var stats = &TunnelStats{
	Devices:   make(map[string]*DeviceSession),
	History:   make([]*ConnectionEvent, 0),
	StartedAt: time.Now(),
}

type ServerLogBuffer struct {
	mu    sync.Mutex
	lines []string
}

func (b *ServerLogBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	scanner := bufio.NewScanner(bytes.NewReader(p))
	for scanner.Scan() {
		text := scanner.Text()
		if text != "" {
			b.lines = append(b.lines, text)
			if len(b.lines) > 200 {
				b.lines = b.lines[len(b.lines)-200:]
			}
		}
	}
	return len(p), nil
}

func (b *ServerLogBuffer) GetLogs() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	return out
}

var serverLogs = &ServerLogBuffer{lines: make([]string, 0)}

func printLog(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	fmt.Print(msg)
	serverLogs.Write([]byte(msg))
}

type countingWriter struct {
	w     io.Writer
	count *int64
}

func (cw *countingWriter) Write(p []byte) (n int, err error) {
	n, err = cw.w.Write(p)
	if n > 0 {
		atomic.AddInt64(cw.count, int64(n))
	}
	return n, err
}

func classifyNetwork(remoteAddr string) string {
	if strings.Contains(remoteAddr, "/ip6/") {
		return "IPv6 Direto"
	}
	return "IPv4 NAT"
}

type cleanTransport struct {
	base http.RoundTripper
}

func (t *cleanTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	req.Header.Del("X-Forwarded-Host")
	req.Header.Del("X-Forwarded-Proto")
	req.Header.Del("X-Forwarded-For")
	req.Header.Del("CF-Connecting-IP")
	req.Header.Del("CF-Ray")
	req.Header.Del("CF-Visitor")
	req.Header.Del("CF-IPCountry")
	req.Header.Del("CDN-Loop")
	req.Header.Set("Host", "127.0.0.1:8123")
	req.Host = "127.0.0.1:8123"
	if req.Header.Get("Origin") != "" {
		req.Header.Set("Origin", "http://127.0.0.1:8123")
	}
	return base.RoundTrip(req)
}

func startHttpsTunnel(ctx context.Context, onUrlUpdate func(rawUrl, fullUrl string)) {
	targetURL, _ := url.Parse("http://127.0.0.1:8123")
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	proxy.Transport = &cleanTransport{base: http.DefaultTransport}

	proxyServer := &http.Server{
		Addr: "127.0.0.1:8098",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			settingsMu.RLock()
			currentPSK := currentSettings.SecretPSK
			settingsMu.RUnlock()

			if currentPSK != "" {
				pskCookie, _ := r.Cookie("ha_p2p_psk")
				pskQuery := r.URL.Query().Get("psk")
				pskHeader := r.Header.Get("X-HA-PSK")

				valid := false
				if pskQuery != "" && subtle.ConstantTimeCompare([]byte(pskQuery), []byte(currentPSK)) == 1 {
					valid = true
					http.SetCookie(w, &http.Cookie{
						Name:     "ha_p2p_psk",
						Value:    currentPSK,
						Path:     "/",
						HttpOnly: true,
						SameSite: http.SameSiteLaxMode,
						MaxAge:   86400 * 30,
					})
				} else if pskCookie != nil && subtle.ConstantTimeCompare([]byte(pskCookie.Value), []byte(currentPSK)) == 1 {
					valid = true
				} else if pskHeader != "" && subtle.ConstantTimeCompare([]byte(pskHeader), []byte(currentPSK)) == 1 {
					valid = true
				}

				if !valid {
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					w.WriteHeader(http.StatusUnauthorized)
					w.Write([]byte(`<!DOCTYPE html><html><head><meta charset="utf-8"><title>HA P2P Tunnel - Acesso Protegido</title><style>body{background:#0f172a;color:#f8fafc;font-family:sans-serif;display:flex;align-items:center;justify-content:center;height:100vh;margin:0;} .card{background:#1e293b;padding:30px;border-radius:12px;border:1px solid #334155;max-width:400px;text-align:center;} input{width:100%;padding:10px;margin:15px 0;border-radius:6px;border:1px solid #475569;background:#0f172a;color:#fff;box-sizing:border-box;} button{background:#3b82f6;color:#fff;border:none;padding:10px 20px;border-radius:6px;cursor:pointer;font-weight:bold;width:100%;}</style></head><body><div class="card"><h2>🔒 Acesso Protegido</h2><p>Este túnel exige autenticação por senha PSK.</p><form method="GET"><input type="password" name="psk" placeholder="Digite a Senha PSK..." required /><button type="submit">Entrar no Home Assistant</button></form></div></body></html>`))
					return
				}
			}

			proxy.ServeHTTP(w, r)
		}),
	}

	go func() {
		if err := proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[HTTPS Proxy] Erro no proxy local: %v", err)
		}
	}()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			log.Printf("[Cloudflare HTTPS] Iniciando túnel rápido na porta 443...")
			cmd := exec.CommandContext(ctx, "cloudflared", "tunnel", "--url", "http://127.0.0.1:8098", "--no-autoupdate")
			stderr, err := cmd.StderrPipe()
			if err != nil {
				log.Printf("[Cloudflare HTTPS] Falha ao capturar logs: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			if err := cmd.Start(); err != nil {
				log.Printf("[Cloudflare HTTPS] Falha ao iniciar cloudflared: %v", err)
				time.Sleep(5 * time.Second)
				continue
			}

			scanner := bufio.NewScanner(stderr)
			re := regexp.MustCompile(`https://[a-zA-Z0-9-]+\.trycloudflare\.com`)

			for scanner.Scan() {
				line := scanner.Text()
				if match := re.FindString(line); match != "" {
					stats.setHttpsUrl(match)
					log.Printf("==========================================================================")
					log.Printf(" [HTTPS 443 ATIVO] URL Direta: %s", match)
					log.Printf("==========================================================================")
					if onUrlUpdate != nil {
						settingsMu.RLock()
						psk := currentSettings.SecretPSK
						settingsMu.RUnlock()
						fullUrl := match
						if psk != "" {
							fullUrl = fmt.Sprintf("%s/?psk=%s", match, url.QueryEscape(psk))
						}
						onUrlUpdate(match, fullUrl)
					}
				}
			}
			stats.setHttpsUrl("")
			_ = cmd.Wait()
			time.Sleep(5 * time.Second)
		}
	}()
}

func computePSKToken(secret string) [32]byte {
	return sha256.Sum256([]byte("ha-p2p-psk-auth-v2:" + secret))
}

// ==========================================================================
// CONFIGURAÇÕES E CONTROLE DE ACESSO DE USUÁRIOS (ZERO DADOS PRIVADOS NO GIT)
// ==========================================================================

type TelegramUser struct {
	ChatID      string `json:"chat_id"`
	Name        string `json:"name"`
	Username    string `json:"username"`
	Status      string `json:"status"` // "admin", "allowed", "pending", "blocked"
	RequestedAt string `json:"requested_at"`
	LastSeen    string `json:"last_seen"`
}

type AppSettings struct {
	SecretPSK            string                   `json:"secret_psk"`
	EnableTelegram       bool                     `json:"enable_telegram"`
	TelegramBotToken     string                   `json:"telegram_bot_token"`
	TelegramChatID       string                   `json:"telegram_chat_id"`
	BotName              string                   `json:"bot_name"`
	EnableAI             bool                     `json:"enable_ai"`
	GeminiAPIKey         string                   `json:"gemini_api_key"`
	GeminiModelsPriority string                   `json:"gemini_models_priority"`
	KnownUsers           map[string]string        `json:"known_users,omitempty"`
	TelegramUsers        map[string]*TelegramUser `json:"telegram_users,omitempty"`
	WizardCompleted      bool                     `json:"wizard_completed"`
}

var (
	currentSettings AppSettings
	settingsMu      sync.RWMutex
	lastNotifiedURL string
	notifyMu        sync.Mutex

	botCancelFunc context.CancelFunc
	botMu         sync.Mutex
)

func loadAppSettings() AppSettings {
	settingsMu.Lock()
	defer settingsMu.Unlock()

	var s AppSettings
	s.BotName = "Herbie"
	s.GeminiModelsPriority = "gemini-3.6-flash, gemini-3.5-flash-lite, gemini-3.1-flash-lite, gemini-2.5-flash"
	s.KnownUsers = make(map[string]string)
	s.TelegramUsers = make(map[string]*TelegramUser)

	if data, err := os.ReadFile(SettingsFilePath); err == nil {
		_ = json.Unmarshal(data, &s)
	} else if data, err := os.ReadFile(OptionsFilePath); err == nil {
		_ = json.Unmarshal(data, &s)
	}

	if s.BotName == "" {
		s.BotName = "Herbie"
	}
	if s.GeminiModelsPriority == "" {
		s.GeminiModelsPriority = "gemini-3.6-flash, gemini-3.5-flash-lite, gemini-3.1-flash-lite, gemini-2.5-flash"
	}
	if s.TelegramUsers == nil {
		s.TelegramUsers = make(map[string]*TelegramUser)
	}
	if s.KnownUsers == nil {
		s.KnownUsers = make(map[string]string)
	}

	// Migra KnownUsers se existirem
	for cid, name := range s.KnownUsers {
		if _, exists := s.TelegramUsers[cid]; !exists {
			st := "allowed"
			if cid == s.TelegramChatID {
				st = "admin"
			}
			s.TelegramUsers[cid] = &TelegramUser{
				ChatID:   cid,
				Name:     name,
				Status:   st,
				LastSeen: time.Now().Format("02/01 15:04"),
			}
		}
	}

	// Garante que o Admin principal esteja cadastrado
	if s.TelegramChatID != "" {
		if _, exists := s.TelegramUsers[s.TelegramChatID]; !exists {
			s.TelegramUsers[s.TelegramChatID] = &TelegramUser{
				ChatID:   s.TelegramChatID,
				Name:     "Administrador Principal",
				Status:   "admin",
				LastSeen: time.Now().Format("02/01 15:04"),
			}
		}
	}

	currentSettings = s
	return s
}

func saveAppSettings(s AppSettings) error {
	settingsMu.Lock()
	currentSettings = s
	data, err := json.MarshalIndent(s, "", "  ")
	settingsMu.Unlock()

	if err != nil {
		return err
	}
	_ = os.MkdirAll("/data", 0755)
	return os.WriteFile(SettingsFilePath, data, 0644)
}

// ==========================================================================
// TELEGRAM BOT & CONTROLE RIGOROSO DE ACESSO
// ==========================================================================

func sendTelegramNotification(token, chatID, message string) {
	if token == "" || chatID == "" {
		return
	}
	go func() {
		apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
		payload := map[string]string{
			"chat_id":    chatID,
			"text":       message,
			"parse_mode": "HTML",
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return
		}
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Post(apiURL, "application/json", bytes.NewBuffer(data))
		if err == nil {
			defer resp.Body.Close()
			log.Printf("[Telegram] Mensagem enviada para %s com sucesso!", chatID)
		} else {
			log.Printf("[Telegram] Erro ao enviar mensagem para %s: %v", chatID, err)
		}
	}()
}

func broadcastTelegramMessage(message, target string) {
	settingsMu.RLock()
	token := currentSettings.TelegramBotToken
	adminChatID := currentSettings.TelegramChatID
	users := currentSettings.TelegramUsers
	settingsMu.RUnlock()

	if token == "" {
		log.Printf("[Telegram Broadcast] Aviso: Token do Telegram não configurado.")
		return
	}

	targets := make([]string, 0)
	targetClean := strings.TrimSpace(strings.ToLower(target))

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

	for _, cid := range targets {
		sendTelegramNotification(token, cid, message)
	}
}

func restartTelegramBot() {
	botMu.Lock()
	defer botMu.Unlock()

	if botCancelFunc != nil {
		botCancelFunc()
		botCancelFunc = nil
	}

	settingsMu.RLock()
	s := currentSettings
	settingsMu.RUnlock()

	if !s.EnableTelegram || s.TelegramBotToken == "" {
		log.Printf("[Telegram Bot] Bot desativado ou sem token configurado.")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	botCancelFunc = cancel

	go runTelegramBotLoop(ctx, s.TelegramBotToken)
}

func runTelegramBotLoop(ctx context.Context, token string) {
	delURL := fmt.Sprintf("https://api.telegram.org/bot%s/deleteWebhook?drop_pending_updates=false", token)
	_, _ = http.Get(delURL)

	log.Printf("[Telegram Bot] Serviço de long-polling iniciado com sucesso.")
	offset := int64(0)
	client := &http.Client{Timeout: 35 * time.Second}

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Telegram Bot] Serviço finalizado.")
			return
		default:
		}

		pollURL := fmt.Sprintf("https://api.telegram.org/bot%s/getUpdates?offset=%d&timeout=25", token, offset)
		resp, err := client.Get(pollURL)
		if err != nil {
			time.Sleep(4 * time.Second)
			continue
		}

		var updateData struct {
			Ok     bool `json:"ok"`
			Result []struct {
				UpdateID int64 `json:"update_id"`
				Message  *struct {
					MessageID int64 `json:"message_id"`
					From      struct {
						ID        int64  `json:"id"`
						FirstName string `json:"first_name"`
						Username  string `json:"username"`
					} `json:"from"`
					Chat struct {
						ID int64 `json:"id"`
					} `json:"chat"`
					Text string `json:"text"`
					Date int64  `json:"date"`
				} `json:"message"`
			} `json:"result"`
		}

		err = json.NewDecoder(resp.Body).Decode(&updateData)
		resp.Body.Close()
		if err != nil || !updateData.Ok {
			time.Sleep(3 * time.Second)
			continue
		}

		for _, upd := range updateData.Result {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}
			if upd.Message == nil || upd.Message.Text == "" {
				continue
			}

			userChatID := fmt.Sprintf("%d", upd.Message.Chat.ID)
			userName := upd.Message.From.FirstName
			username := upd.Message.From.Username
			text := strings.TrimSpace(upd.Message.Text)

			handleTelegramIncoming(token, userChatID, userName, username, text)
		}
	}
}

func handleTelegramIncoming(token, userChatID, userName, username, text string) {
	settingsMu.Lock()
	adminChatID := currentSettings.TelegramChatID
	botName := currentSettings.BotName
	enableAI := currentSettings.EnableAI
	geminiKey := currentSettings.GeminiAPIKey
	if currentSettings.TelegramUsers == nil {
		currentSettings.TelegramUsers = make(map[string]*TelegramUser)
	}

	nowStr := time.Now().Format("02/01 15:04")

	// 1. PRIMEIRO USUÁRIO É REGISTRADO COMO ADMIN
	if adminChatID == "" {
		currentSettings.TelegramChatID = userChatID
		currentSettings.TelegramUsers[userChatID] = &TelegramUser{
			ChatID:      userChatID,
			Name:        userName,
			Username:    username,
			Status:      "admin",
			RequestedAt: nowStr,
			LastSeen:    nowStr,
		}
		settingsMu.Unlock()
		_ = saveAppSettings(currentSettings)

		msg := fmt.Sprintf("👑 <b>Administrador registrado com sucesso!</b>\n\nOlá, %s! Eu sou o <b>%s</b>, assistente do seu Home Assistant.\nSeu Chat ID (<code>%s</code>) agora é o administrador oficial da casa.\n\nVocê tem controle total sobre quem pode usar este bot. Digite /status ou converse comigo naturalmente!", userName, botName, userChatID)
		sendTelegramNotification(token, userChatID, msg)
		return
	}

	// 2. VERIFICA SE O USUÁRIO JÁ EXISTE NO CADASTRO
	u, exists := currentSettings.TelegramUsers[userChatID]
	if !exists {
		// NOVO USUÁRIO DESCONHECIDO!
		u = &TelegramUser{
			ChatID:      userChatID,
			Name:        userName,
			Username:    username,
			Status:      "pending",
			RequestedAt: nowStr,
			LastSeen:    nowStr,
		}
		currentSettings.TelegramUsers[userChatID] = u
		settingsMu.Unlock()
		_ = saveAppSettings(currentSettings)

		// Responde para o usuário solicitante
		pendingMsg := fmt.Sprintf("⏳ <b>Acesso Pendente de Aprovação</b>\n\nOlá, %s! O acesso a este bot é restrito aos moradores autorizados da casa.\nUma notificação foi enviada ao administrador. Por favor, aguarde a liberação.", userName)
		sendTelegramNotification(token, userChatID, pendingMsg)

		// Notifica o Administrador no Telegram com comando de aprovação
		userDisplay := userName
		if username != "" {
			userDisplay += fmt.Sprintf(" (@%s)", username)
		}
		adminAlert := fmt.Sprintf("🔔 <b>Novo Pedido de Acesso ao Bot!</b>\n\n👤 <b>Nome:</b> %s\n🆔 <b>Chat ID:</b> <code>%s</code>\n💬 <b>Mensagem:</b> <i>\"%s\"</i>\n\n<b>Para autorizar, responda com:</b>\n<code>/aprovar %s</code>\n\n<b>Para recusar/bloquear:</b>\n<code>/bloquear %s</code>", userDisplay, userChatID, text, userChatID, userChatID)
		sendTelegramNotification(token, adminChatID, adminAlert)

		// Notifica o Home Assistant via Persistent Notification
		haNotif := fmt.Sprintf("O usuário **%s** (Chat ID: `%s`) solicitou acesso ao bot do Telegram.\n\nMensagem: *\"%s\"*\n\n👉 Acesse o painel do HA P2P Tunnel ou responda `/aprovar %s` no Telegram para autorizar.", userDisplay, userChatID, text, userChatID)
		callHomeAssistantService("persistent_notification", "create", map[string]string{
			"title":           fmt.Sprintf("🔔 Novo Usuário Telegram: %s", userName),
			"message":         haNotif,
			"notification_id": fmt.Sprintf("telegram_req_%s", userChatID),
		})
		return
	}

	u.LastSeen = nowStr
	if userName != "" {
		u.Name = userName
	}
	if username != "" {
		u.Username = username
	}
	userStatus := u.Status
	settingsMu.Unlock()

	// 3. SE O USUÁRIO FOR PENDENTE OU BLOQUEADO
	if userStatus == "pending" {
		msg := "⏳ <b>Acesso em Análise</b>\n\nSua solicitação de acesso ainda está sob avaliação do administrador da casa. Aguarde a liberação."
		sendTelegramNotification(token, userChatID, msg)
		return
	}
	if userStatus == "blocked" {
		msg := "⛔ <b>Acesso Bloqueado</b>\n\nSeu acesso a este bot foi bloqueado pelo administrador."
		sendTelegramNotification(token, userChatID, msg)
		return
	}

	// 4. SE FOR O ADMINISTRADOR - PROCESSAR COMANDOS DE ADMINISTRAÇÃO
	isAdmin := (userStatus == "admin" || userChatID == adminChatID)
	if isAdmin {
		if strings.HasPrefix(text, "/aprovar") {
			parts := strings.Fields(text)
			if len(parts) < 2 {
				sendTelegramNotification(token, userChatID, "⚠️ <b>Uso:</b> <code>/aprovar &lt;chat_id&gt; [Nome]</code>\nExemplo: <code>/aprovar 123456789 Claudia</code>")
				return
			}
			targetCID := parts[1]
			customName := ""
			if len(parts) >= 3 {
				customName = strings.Join(parts[2:], " ")
			}

			settingsMu.Lock()
			targetU, found := currentSettings.TelegramUsers[targetCID]
			if !found {
				targetU = &TelegramUser{
					ChatID:      targetCID,
					Name:        customName,
					Status:      "allowed",
					RequestedAt: nowStr,
					LastSeen:    nowStr,
				}
				currentSettings.TelegramUsers[targetCID] = targetU
			} else {
				targetU.Status = "allowed"
				if customName != "" {
					targetU.Name = customName
				}
			}
			settingsMu.Unlock()
			_ = saveAppSettings(currentSettings)

			// Remove notificação persistente do HA
			callHomeAssistantService("persistent_notification", "dismiss", map[string]string{
				"notification_id": fmt.Sprintf("telegram_req_%s", targetCID),
			})

			// Confirma para o admin
			displayName := targetU.Name
			if displayName == "" {
				displayName = targetCID
			}
			adminConfirm := fmt.Sprintf("✅ <b>Usuário Aprovado com Sucesso!</b>\n\n👤 <b>Nome:</b> %s\n🆔 <b>Chat ID:</b> <code>%s</code>\nStatus: 🟢 Autorizado", displayName, targetCID)
			sendTelegramNotification(token, userChatID, adminConfirm)

			// Notifica o usuário aprovado
			userWelcome := fmt.Sprintf("🎉 <b>Parabéns! Seu acesso foi liberado!</b>\n\nO administrador da casa aprovou seu acesso ao assistente <b>%s</b>.\nDigite /status para ver os dispositivos ou converse comigo normalmente!", botName)
			sendTelegramNotification(token, targetCID, userWelcome)
			return
		}

		if strings.HasPrefix(text, "/bloquear") {
			parts := strings.Fields(text)
			if len(parts) < 2 {
				sendTelegramNotification(token, userChatID, "⚠️ <b>Uso:</b> <code>/bloquear &lt;chat_id&gt;</code>")
				return
			}
			targetCID := parts[1]
			if targetCID == adminChatID {
				sendTelegramNotification(token, userChatID, "❌ Você não pode bloquear o administrador principal.")
				return
			}

			settingsMu.Lock()
			if targetU, found := currentSettings.TelegramUsers[targetCID]; found {
				targetU.Status = "blocked"
			} else {
				currentSettings.TelegramUsers[targetCID] = &TelegramUser{
					ChatID: targetCID,
					Status: "blocked",
				}
			}
			settingsMu.Unlock()
			_ = saveAppSettings(currentSettings)

			sendTelegramNotification(token, userChatID, fmt.Sprintf("⛔ <b>Usuário Bloqueado:</b> Chat ID <code>%s</code> agora está proibido de usar o bot.", targetCID))
			sendTelegramNotification(token, targetCID, "⛔ Seu acesso a este bot foi revogado pelo administrador.")
			return
		}

		if strings.HasPrefix(text, "/usuarios") {
			settingsMu.RLock()
			users := currentSettings.TelegramUsers
			settingsMu.RUnlock()

			var sb strings.Builder
			sb.WriteString("👥 <b>Usuários do Telegram Cadastrados:</b>\n\n")
			for cid, usr := range users {
				tag := "🟢 Autorizado"
				if usr.Status == "admin" {
					tag = "👑 Admin"
				} else if usr.Status == "pending" {
					tag = "🟡 Pendente (/aprovar " + cid + ")"
				} else if usr.Status == "blocked" {
					tag = "🔴 Bloqueado"
				}
				nameStr := usr.Name
				if nameStr == "" {
					nameStr = "Sem nome"
				}
				sb.WriteString(fmt.Sprintf("• <b>%s</b> (<code>%s</code>): %s\n", nameStr, cid, tag))
			}
			sendTelegramNotification(token, userChatID, sb.String())
			return
		}
	}

	// 5. COMANDOS GERAIS PARA USUÁRIOS AUTORIZADOS
	switch {
	case strings.HasPrefix(text, "/start"):
		msg := fmt.Sprintf("🤖 <b>Olá, %s! Eu sou o %s.</b>\n\nEstou pronto para gerenciar sua casa e manter seu acesso seguro.\n\n<b>Comandos disponíveis:</b>\n/status - Dispositivos ativos e túnel\n/tunnel - Link direto HTTPS e Peer ID\n/ajuda - Como conversar comigo", userName, botName)
		if isAdmin {
			msg += "\n\n<b>Comandos de Administrador:</b>\n/usuarios - Ver lista de usuários\n/aprovar &lt;id&gt; - Liberar novo usuário\n/bloquear &lt;id&gt; - Bloquear usuário"
		}
		sendTelegramNotification(token, userChatID, msg)

	case strings.HasPrefix(text, "/tunnel"):
		httpsUrl := stats.getHttpsUrl()
		settingsMu.RLock()
		psk := currentSettings.SecretPSK
		settingsMu.RUnlock()
		fullUrl := httpsUrl
		if httpsUrl != "" && psk != "" {
			fullUrl = fmt.Sprintf("%s/?psk=%s", httpsUrl, url.QueryEscape(psk))
		}
		msg := fmt.Sprintf("🚀 <b>HA P2P Tunnel</b>\n\n🔗 <b>Link Direto HTTPS (Porta 443):</b>\n<a href=\"%s\">%s</a>\n\n🔐 <b>Senha PSK:</b> <code>%s</code>", fullUrl, fullUrl, psk)
		sendTelegramNotification(token, userChatID, msg)

	case strings.HasPrefix(text, "/status"):
		states := getFilteredHomeAssistantStates()
		ligados := make([]string, 0)
		for _, s := range states {
			if s["state"] == "on" && (strings.HasPrefix(s["entity"], "light.") || strings.HasPrefix(s["entity"], "switch.")) {
				ligados = append(ligados, s["name"])
			}
		}
		statusText := "💡 <b>Dispositivos Ligados:</b>\n- " + strings.Join(ligados, "\n- ")
		if len(ligados) == 0 {
			statusText = "💤 <b>Tudo desligado na casa.</b>"
		}
		tunnelUrl := stats.getHttpsUrl()
		tunnelStatus := "🔴 Desconectado"
		if tunnelUrl != "" {
			tunnelStatus = "🟢 Conectado (HTTPS + P2P)"
		}
		fullMsg := fmt.Sprintf("%s\n\n🛡️ <b>Status do Túnel:</b> %s", statusText, tunnelStatus)
		sendTelegramNotification(token, userChatID, fullMsg)

	case strings.HasPrefix(text, "/ajuda"):
		msg := fmt.Sprintf("💡 <b>Como conversar com o %s:</b>\n\nVocê pode me pedir para:\n• <i>'Ligar a luz da sala'</i>\n• <i>'Como está a casa agora?'</i>\n• <i>'Desligar tudo'</i>\n\nEu respondo de forma rápida e controlo os dispositivos usando inteligência artificial.", botName)
		sendTelegramNotification(token, userChatID, msg)

	default:
		if enableAI && geminiKey != "" {
			sendTelegramNotification(token, userChatID, "⏳ <i>Consultando dispositivos...</i>")
			go func() {
				reply, err := askGemini(text, userName)
				if err != nil {
					sendTelegramNotification(token, userChatID, "⚠️ Erro ao consultar IA: "+err.Error())
				} else {
					sendTelegramNotification(token, userChatID, reply)
				}
			}()
		} else {
			msg := "💡 Para conversar comigo com inteligência artificial, ative o Assistente IA e configure sua chave do Google Gemini no painel do add-on (⚙️ Configurações)."
			sendTelegramNotification(token, userChatID, msg)
		}
	}
}

// ==========================================================================
// MOTOR DE IA GOOGLE GEMINI (PADRÃO FALLBACK CEREBELO)
// ==========================================================================

func askGemini(userText, userName string) (string, error) {
	settingsMu.RLock()
	apiKey := currentSettings.GeminiAPIKey
	modelsStr := currentSettings.GeminiModelsPriority
	botName := currentSettings.BotName
	settingsMu.RUnlock()

	if apiKey == "" {
		return "Chave de API do Google Gemini não configurada.", nil
	}

	models := strings.Split(modelsStr, ",")
	cleanModels := make([]string, 0)
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m != "" {
			cleanModels = append(cleanModels, m)
		}
	}
	if len(cleanModels) == 0 {
		cleanModels = []string{"gemini-3.6-flash", "gemini-3.5-flash-lite", "gemini-3.1-flash-lite", "gemini-2.5-flash"}
	}

	states := getFilteredHomeAssistantStates()
	var stateSummary strings.Builder
	for _, s := range states {
		stateSummary.WriteString(fmt.Sprintf("- %s (%s): %s\n", s["name"], s["entity"], s["state"]))
	}

	prompt := fmt.Sprintf(`Você é o %s, assistente inteligente de automação residencial.
Responda em português de forma concisa, educada e direta (1 a 3 frases no máximo).

REGRAS DE CONTROLE:
Quando o usuário pedir para ligar ou desligar qualquer dispositivo, além de responder confirmando a ação, inclua exatamente no final da sua resposta a tag:
[ACTION: {"domain": "light", "service": "turn_on", "entity_id": "light.sala"}]
(utilize a entity_id exata correspondente da lista abaixo).

ESTADO ATUAL DOS DISPOSITIVOS:
%s

MENSAGEM DE %s:
%s`, botName, stateSummary.String(), userName, userText)

	type Part struct {
		Text string `json:"text"`
	}
	type Content struct {
		Parts []Part `json:"parts"`
	}
	type GenRequest struct {
		Contents []Content `json:"contents"`
	}

	reqBody := GenRequest{
		Contents: []Content{
			{Parts: []Part{{Text: prompt}}},
		},
	}
	reqData, _ := json.Marshal(reqBody)

	client := &http.Client{Timeout: 15 * time.Second}

	for _, model := range cleanModels {
		endpoint := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, apiKey)
		req, err := http.NewRequest("POST", endpoint, bytes.NewBuffer(reqData))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var genResp struct {
				Candidates []struct {
					Content struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
				} `json:"candidates"`
			}
			err = json.NewDecoder(resp.Body).Decode(&genResp)
			resp.Body.Close()
			if err == nil && len(genResp.Candidates) > 0 && len(genResp.Candidates[0].Content.Parts) > 0 {
				rawText := genResp.Candidates[0].Content.Parts[0].Text

				reAction := regexp.MustCompile(`\[ACTION:\s*(\{.*?\})\]`)
				if match := reAction.FindStringSubmatch(rawText); len(match) > 1 {
					var act struct {
						Domain   string `json:"domain"`
						Service  string `json:"service"`
						EntityID string `json:"entity_id"`
					}
					if json.Unmarshal([]byte(match[1]), &act) == nil && act.Domain != "" && act.Service != "" && act.EntityID != "" {
						callHomeAssistantService(act.Domain, act.Service, map[string]string{"entity_id": act.EntityID})
						log.Printf("[IA Action] Executado: %s.%s em %s", act.Domain, act.Service, act.EntityID)
					}
					rawText = strings.TrimSpace(reAction.ReplaceAllString(rawText, ""))
				}

				return rawText, nil
			}
		} else {
			resp.Body.Close()
			log.Printf("[Gemini IA] Modelo %s retornou status %d. Tentando fallback...", model, resp.StatusCode)
		}
	}

	return "⚠️ Todos os modelos de IA estão temporariamente sem cota ou indisponíveis no momento. Tente novamente em instantes.", nil
}

// ==========================================================================
// HOME ASSISTANT API & WEBSOCKET CLIENT (ZERO YAML NOTIFICATIONS)
// ==========================================================================

func getFilteredHomeAssistantStates() []map[string]string {
	supervisorToken := os.Getenv("SUPERVISOR_TOKEN")
	if supervisorToken == "" {
		return nil
	}
	url := "http://supervisor/core/api/states"
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+supervisorToken)
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil {
			resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	var states []struct {
		EntityID   string                 `json:"entity_id"`
		State      string                 `json:"state"`
		Attributes map[string]interface{} `json:"attributes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&states); err != nil {
		return nil
	}

	filtered := make([]map[string]string, 0)
	for _, s := range states {
		ent := s.EntityID
		isDevice := strings.HasPrefix(ent, "light.") || strings.HasPrefix(ent, "switch.") ||
			strings.HasPrefix(ent, "binary_sensor.") || strings.HasPrefix(ent, "sensor.") ||
			strings.HasPrefix(ent, "weather.") || strings.Contains(ent, "tunnel")

		if isDevice {
			name := ent
			if fn, ok := s.Attributes["friendly_name"].(string); ok && fn != "" {
				name = fn
			}
			filtered = append(filtered, map[string]string{
				"entity": ent,
				"name":   name,
				"state":  s.State,
			})
		}
	}
	return filtered
}

func startHAWebSocketListener(ctx context.Context) {
	supervisorToken := os.Getenv("SUPERVISOR_TOKEN")
	if supervisorToken == "" {
		return
	}
	wsURL := "ws://supervisor/core/websocket"

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			dialer := websocket.DefaultDialer
			conn, _, err := dialer.Dial(wsURL, nil)
			if err != nil {
				time.Sleep(10 * time.Second)
				continue
			}

			var authReq map[string]interface{}
			if err := conn.ReadJSON(&authReq); err != nil {
				conn.Close()
				time.Sleep(5 * time.Second)
				continue
			}

			authMsg := map[string]string{
				"type":         "auth",
				"access_token": supervisorToken,
			}
			if err := conn.WriteJSON(authMsg); err != nil {
				conn.Close()
				time.Sleep(5 * time.Second)
				continue
			}

			var authResp map[string]interface{}
			if err := conn.ReadJSON(&authResp); err != nil || authResp["type"] != "auth_ok" {
				conn.Close()
				time.Sleep(5 * time.Second)
				continue
			}

			log.Printf("[HA WebSocket] Autenticado com sucesso! Ouvindo eventos 'p2p_telegram'...")

			subMsg := map[string]interface{}{
				"id":         1,
				"type":       "subscribe_events",
				"event_type": "p2p_telegram",
			}
			if err := conn.WriteJSON(subMsg); err != nil {
				conn.Close()
				time.Sleep(5 * time.Second)
				continue
			}

			for {
				var msg map[string]interface{}
				if err := conn.ReadJSON(&msg); err != nil {
					log.Printf("[HA WebSocket] Desconectado: %v. Reconectando em 5s...", err)
					conn.Close()
					break
				}
				if msg["type"] == "event" {
					if evt, ok := msg["event"].(map[string]interface{}); ok {
						if data, ok := evt["data"].(map[string]interface{}); ok {
							msgText := fmt.Sprintf("%v", data["message"])
							target := ""
							if t, exists := data["target"]; exists && t != nil {
								target = fmt.Sprintf("%v", t)
							}
							if msgText != "" && msgText != "<nil>" {
								log.Printf("[HA WebSocket Event] Evento 'p2p_telegram' recebido: %s", msgText)
								broadcastTelegramMessage(msgText, target)
							}
						}
					}
				}
			}
			time.Sleep(5 * time.Second)
		}
	}()
}

func callHomeAssistantService(domain, service string, payload interface{}) {
	supervisorToken := os.Getenv("SUPERVISOR_TOKEN")
	if supervisorToken == "" {
		return
	}
	url := fmt.Sprintf("http://supervisor/core/api/services/%s/%s", domain, service)
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+supervisorToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		log.Printf("[HA Service] Chamada %s.%s executada!", domain, service)
	} else {
		log.Printf("[HA Service] Erro: %s.%s: %v", domain, service, err)
	}
}

func postEntityState(entityID, state string, attributes map[string]interface{}) {
	supervisorToken := os.Getenv("SUPERVISOR_TOKEN")
	if supervisorToken == "" {
		return
	}
	url := fmt.Sprintf("http://supervisor/core/api/states/%s", entityID)
	body := map[string]interface{}{
		"state":      state,
		"attributes": attributes,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return
	}
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+supervisorToken)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		log.Printf("[HA Core] Entidade %s atualizada: %s", entityID, state)
	}
}

func updateHomeAssistantEntities(peerID, httpsUrl, fullHttpsUrl, psk string) {
	supervisorToken := os.Getenv("SUPERVISOR_TOKEN")
	if supervisorToken == "" {
		return
	}
	go func() {
		statusState := "Online"
		binaryState := "on"
		if httpsUrl == "" {
			statusState = "Iniciando"
			binaryState = "off"
		}

		attrs := map[string]interface{}{
			"peer_id":        peerID,
			"https_url":      httpsUrl,
			"https_auth_url": fullHttpsUrl,
			"psk":            psk,
			"icon":           "mdi:tunnel",
			"last_updated":   time.Now().Format(time.RFC3339),
		}

		// 1. sensor.ha_p2p_tunnel_status
		statusAttrs := make(map[string]interface{})
		for k, v := range attrs {
			statusAttrs[k] = v
		}
		statusAttrs["friendly_name"] = "HA P2P Tunnel Status"
		postEntityState("sensor.ha_p2p_tunnel_status", statusState, statusAttrs)

		// 2. binary_sensor.ha_p2p_tunnel
		binAttrs := make(map[string]interface{})
		for k, v := range attrs {
			binAttrs[k] = v
		}
		binAttrs["friendly_name"] = "Túnel HA P2P"
		binAttrs["device_class"] = "connectivity"
		postEntityState("binary_sensor.ha_p2p_tunnel", binaryState, binAttrs)

		// 3. sensor.ha_p2p_tunnel_url
		if fullHttpsUrl != "" {
			urlAttrs := map[string]interface{}{
				"friendly_name": "Link Direto HA P2P Tunnel",
				"icon":          "mdi:link-variant",
				"peer_id":       peerID,
			}
			postEntityState("sensor.ha_p2p_tunnel_url", fullHttpsUrl, urlAttrs)

			// 4. sensor.ha_p2p_tunnel_peer_id
			peerAttrs := map[string]interface{}{
				"friendly_name": "Peer ID HA P2P Tunnel",
				"icon":          "mdi:identifier",
			}
			postEntityState("sensor.ha_p2p_tunnel_peer_id", peerID, peerAttrs)

			// 5. Notificação nativa persistente no Home Assistant
			callHomeAssistantService("persistent_notification", "create", map[string]string{
				"title":           "🚀 HA P2P Tunnel Conectado",
				"message":         fmt.Sprintf("🔗 **Link Direto:** [%s](%s)\n\n🔑 **Peer ID:** `%s`", fullHttpsUrl, fullHttpsUrl, peerID),
				"notification_id": "ha_p2p_tunnel_connected",
			})

			// 6. notify.notify geral
			callHomeAssistantService("notify", "notify", map[string]string{
				"title":   "HA P2P Tunnel",
				"message": "🚀 HA P2P Tunnel Conectado: " + fullHttpsUrl,
			})
		}
	}()
}

func loadOrCreateKey(path string) (crypto.PrivKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		return crypto.UnmarshalPrivateKey(data)
	}
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	data, _ := crypto.MarshalPrivateKey(priv)
	_ = os.WriteFile(path, data, 0600)
	return priv, nil
}

// ==========================================================================
// SERVIDOR WEB INGRESS COM GESTÃO DE USUÁRIOS & ATALHOS DE NOTIFICAÇÃO
// ==========================================================================

func startIngressServer(port string, host host.Host, hostID string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		stats.mu.Lock()
		defer stats.mu.Unlock()

		devs := make([]*DeviceSession, 0, len(stats.Devices))
		for _, d := range stats.Devices {
			devs = append(devs, d)
		}

		reports := make([]*ClientReport, 0, len(stats.ClientReports))
		for _, rep := range stats.ClientReports {
			reports = append(reports, rep)
		}

		settingsMu.RLock()
		s := currentSettings
		settingsMu.RUnlock()

		fullHttpsUrl := stats.HttpsUrl
		if stats.HttpsUrl != "" && s.SecretPSK != "" {
			fullHttpsUrl = fmt.Sprintf("%s/?psk=%s", stats.HttpsUrl, url.QueryEscape(s.SecretPSK))
		}

		pendingCount := 0
		allowedCount := 0
		for _, u := range s.TelegramUsers {
			if u.Status == "pending" {
				pendingCount++
			} else if u.Status == "allowed" || u.Status == "admin" {
				allowedCount++
			}
		}

		data := map[string]interface{}{
			"active_streams":       atomic.LoadInt64(&stats.ActiveStreams),
			"total_streams":        atomic.LoadInt64(&stats.TotalStreams),
			"rejected_streams":     atomic.LoadInt64(&stats.RejectedStreams),
			"total_bytes_in":       atomic.LoadInt64(&stats.TotalBytesIn),
			"total_bytes_out":      atomic.LoadInt64(&stats.TotalBytesOut),
			"https_url":            stats.HttpsUrl,
			"https_auth_url":       fullHttpsUrl,
			"peer_id":              hostID,
			"devices":              devs,
			"history":              stats.History,
			"client_reports":       reports,
			"uptime":               time.Since(stats.StartedAt).Round(time.Second).String(),
			"bot_name":             s.BotName,
			"telegram_active":      s.EnableTelegram && s.TelegramBotToken != "",
			"ai_active":            s.EnableAI && s.GeminiAPIKey != "",
			"wizard_completed":     s.WizardCompleted,
			"telegram_users":       s.TelegramUsers,
			"pending_users_count":  pendingCount,
			"allowed_users_count":  allowedCount,
		}
		json.NewEncoder(w).Encode(data)
	})

	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet {
			settingsMu.RLock()
			s := currentSettings
			settingsMu.RUnlock()

			maskedToken := ""
			if s.TelegramBotToken != "" {
				maskedToken = "••••••••"
			}
			maskedKey := ""
			if s.GeminiAPIKey != "" {
				maskedKey = "••••••••"
			}
			maskedPSK := ""
			if s.SecretPSK != "" {
				maskedPSK = "••••••••"
			}

			resp := map[string]interface{}{
				"secret_psk":             maskedPSK,
				"has_psk":                s.SecretPSK != "",
				"enable_telegram":        s.EnableTelegram,
				"telegram_bot_token":     maskedToken,
				"has_telegram_token":     s.TelegramBotToken != "",
				"telegram_chat_id":       s.TelegramChatID,
				"bot_name":               s.BotName,
				"enable_ai":              s.EnableAI,
				"gemini_api_key":         maskedKey,
				"has_gemini_key":         s.GeminiAPIKey != "",
				"gemini_models_priority": s.GeminiModelsPriority,
				"wizard_completed":       s.WizardCompleted,
				"telegram_users":         s.TelegramUsers,
			}
			json.NewEncoder(w).Encode(resp)
			return
		}

		if r.Method == http.MethodPost {
			var req struct {
				SecretPSK            string `json:"secret_psk"`
				EnableTelegram       bool   `json:"enable_telegram"`
				TelegramBotToken     string `json:"telegram_bot_token"`
				TelegramChatID       string `json:"telegram_chat_id"`
				BotName              string `json:"bot_name"`
				EnableAI             bool   `json:"enable_ai"`
				GeminiAPIKey         string `json:"gemini_api_key"`
				GeminiModelsPriority string `json:"gemini_models_priority"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, `{"error":"JSON inválido"}`, http.StatusBadRequest)
				return
			}

			settingsMu.Lock()
			newS := currentSettings

			if req.SecretPSK != "••••••••" {
				newS.SecretPSK = strings.TrimSpace(req.SecretPSK)
			}
			newS.EnableTelegram = req.EnableTelegram
			if req.TelegramBotToken != "••••••••" && req.TelegramBotToken != "" {
				newS.TelegramBotToken = strings.TrimSpace(req.TelegramBotToken)
			}
			if req.TelegramChatID != "" {
				newS.TelegramChatID = strings.TrimSpace(req.TelegramChatID)
			}
			if req.BotName != "" {
				newS.BotName = strings.TrimSpace(req.BotName)
			}
			newS.EnableAI = req.EnableAI
			if req.GeminiAPIKey != "••••••••" && req.GeminiAPIKey != "" {
				newS.GeminiAPIKey = strings.TrimSpace(req.GeminiAPIKey)
			}
			if req.GeminiModelsPriority != "" {
				newS.GeminiModelsPriority = strings.TrimSpace(req.GeminiModelsPriority)
			}
			newS.WizardCompleted = true
			settingsMu.Unlock()

			if err := saveAppSettings(newS); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
				return
			}

			restartTelegramBot()
			json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Configurações salvas e aplicadas!"})
			return
		}
		http.Error(w, `{"error":"Método não permitido"}`, http.StatusMethodNotAllowed)
	})

	// Gerenciamento de Usuários (Aprovar, Bloquear, Remover)
	mux.HandleFunc("/api/users/action", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Apenas método POST é suportado"}`, http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ChatID string `json:"chat_id"`
			Action string `json:"action"` // "approve", "block", "delete"
			Name   string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"JSON inválido"}`, http.StatusBadRequest)
			return
		}

		req.ChatID = strings.TrimSpace(req.ChatID)
		if req.ChatID == "" {
			http.Error(w, `{"error":"chat_id obrigatório"}`, http.StatusBadRequest)
			return
		}

		settingsMu.Lock()
		if currentSettings.TelegramUsers == nil {
			currentSettings.TelegramUsers = make(map[string]*TelegramUser)
		}
		u, found := currentSettings.TelegramUsers[req.ChatID]
		if !found && req.Action != "delete" {
			u = &TelegramUser{
				ChatID:      req.ChatID,
				Name:        req.Name,
				Status:      "pending",
				RequestedAt: time.Now().Format("02/01 15:04"),
				LastSeen:    time.Now().Format("02/01 15:04"),
			}
			currentSettings.TelegramUsers[req.ChatID] = u
		}

		token := currentSettings.TelegramBotToken
		botName := currentSettings.BotName

		if req.Action == "approve" {
			if u != nil {
				u.Status = "allowed"
				if req.Name != "" {
					u.Name = req.Name
				}
			}
			callHomeAssistantService("persistent_notification", "dismiss", map[string]string{
				"notification_id": fmt.Sprintf("telegram_req_%s", req.ChatID),
			})
			sendTelegramNotification(token, req.ChatID, fmt.Sprintf("🎉 <b>Parabéns! Seu acesso foi liberado!</b>\n\nO administrador aprovou seu acesso ao assistente <b>%s</b>. Digite /start para começar!", botName))
		} else if req.Action == "block" {
			if u != nil {
				u.Status = "blocked"
			}
			sendTelegramNotification(token, req.ChatID, "⛔ Seu acesso a este bot foi revogado pelo administrador.")
		} else if req.Action == "delete" {
			delete(currentSettings.TelegramUsers, req.ChatID)
		}

		updatedSettings := currentSettings
		settingsMu.Unlock()

		_ = saveAppSettings(updatedSettings)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "ok",
			"users":  updatedSettings.TelegramUsers,
		})
	})

	// Botão de Teste de Notificação
	mux.HandleFunc("/api/test_notification", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Apenas método POST é suportado"}`, http.StatusMethodNotAllowed)
			return
		}
		settingsMu.RLock()
		token := currentSettings.TelegramBotToken
		adminChatID := currentSettings.TelegramChatID
		botName := currentSettings.BotName
		settingsMu.RUnlock()

		if token == "" || adminChatID == "" {
			http.Error(w, `{"error":"Configure e salve o token e o chat ID antes de testar"}`, http.StatusBadRequest)
			return
		}

		testMsg := fmt.Sprintf("🧪 <b>Teste de Notificação Bem-Sucedido!</b>\n\nO assistente <b>%s</b> está conectado e comunicando normalmente com o Home Assistant!\n🕒 Hora: %s", botName, time.Now().Format("15:04:05"))
		sendTelegramNotification(token, adminChatID, testMsg)

		callHomeAssistantService("persistent_notification", "create", map[string]string{
			"title":           "🧪 Teste de Notificação Telegram",
			"message":         "Notificação de teste disparada com sucesso para o Administrador!",
			"notification_id": "telegram_test_success",
		})

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"message": "Notificação de teste enviada com sucesso ao Telegram e ao Home Assistant!",
		})
	})

	// Endpoints REST de Notificação para Automações e rest_command
	notifyHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			http.Error(w, `{"error":"Apenas método POST é suportado"}`, http.StatusMethodNotAllowed)
			return
		}
		var payload struct {
			Message string `json:"message"`
			Target  string `json:"target"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, `{"error":"JSON inválido"}`, http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(payload.Message) == "" {
			http.Error(w, `{"error":"Campo 'message' é obrigatório"}`, http.StatusBadRequest)
			return
		}

		broadcastTelegramMessage(payload.Message, payload.Target)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "sucesso",
			"message":   payload.Message,
			"target":    payload.Target,
			"delivered": true,
		})
	}

	mux.HandleFunc("/api/send_telegram", notifyHandler)
	mux.HandleFunc("/api/notify", notifyHandler)

	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"logs": serverLogs.GetLogs(),
		})
	})

	mux.HandleFunc("/download/ha-p2p-tunnel.apk", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, GitHubApkUrl, http.StatusFound)
	})

	mux.HandleFunc("/qr", func(w http.ResponseWriter, r *http.Request) {
		rawUrl := r.URL.Query().Get("url")
		if rawUrl == "" {
			rawUrl = GitHubApkUrl
		}
		png, err := qrcode.Encode(rawUrl, qrcode.Medium, 280)
		if err != nil {
			http.Error(w, "Erro ao gerar QR Code", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	})

	htmlTemplate := `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
<title>HA P2P Tunnel v0.4.0</title>
<style>
:root {
  --bg-primary: #0b0f19;
  --bg-secondary: #111827;
  --bg-card: #1f2937;
  --bg-input: #0f172a;
  --border: #374151;
  --border-focus: #3b82f6;
  --text-main: #f9fafb;
  --text-muted: #9ca3af;
  --accent: #3b82f6;
  --accent-hover: #2563eb;
  --green: #10b981;
  --yellow: #f59e0b;
  --red: #ef4444;
}
* { box-sizing: border-box; margin: 0; padding: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
body { background: var(--bg-primary); color: var(--text-main); padding: 20px; line-height: 1.5; }
.container { max-width: 1200px; margin: 0 auto; }

/* HEADER */
.header { display: flex; justify-content: space-between; align-items: center; flex-wrap: wrap; gap: 15px; margin-bottom: 25px; padding-bottom: 15px; border-bottom: 1px solid var(--border); }
.brand h1 { font-size: 22px; font-weight: 700; display: flex; align-items: center; gap: 10px; }
.brand p { font-size: 13px; color: var(--text-muted); margin-top: 2px; }
.status-pills { display: flex; gap: 8px; flex-wrap: wrap; }
.pill { font-size: 11px; font-weight: 600; padding: 4px 10px; border-radius: 9999px; display: inline-flex; align-items: center; gap: 6px; }
.pill-green { background: rgba(16, 185, 129, 0.15); color: #34d399; border: 1px solid rgba(16, 185, 129, 0.3); }
.pill-yellow { background: rgba(245, 158, 11, 0.15); color: #fbbf24; border: 1px solid rgba(245, 158, 11, 0.3); }
.pill-red { background: rgba(239, 68, 68, 0.15); color: #f87171; border: 1px solid rgba(239, 68, 68, 0.3); }
.pill-gray { background: rgba(156, 163, 175, 0.15); color: #9ca3af; border: 1px solid rgba(156, 163, 175, 0.3); }

.header-actions { display: flex; gap: 10px; flex-wrap: wrap; }
.btn { background: var(--bg-card); color: var(--text-main); border: 1px solid var(--border); padding: 8px 14px; border-radius: 8px; font-size: 13px; font-weight: 600; cursor: pointer; text-decoration: none; display: inline-flex; align-items: center; gap: 6px; transition: all 0.2s; }
.btn:hover { background: #374151; border-color: #4b5563; }
.btn-primary { background: var(--accent); border-color: var(--accent); color: #fff; }
.btn-primary:hover { background: var(--accent-hover); }
.btn-success { background: #059669; border-color: #059669; color: #fff; }
.btn-success:hover { background: #047857; }
.btn-danger { background: #dc2626; border-color: #dc2626; color: #fff; }
.btn-danger:hover { background: #b91c1c; }
.btn-sm { padding: 4px 8px; font-size: 11px; border-radius: 6px; }

/* GRID */
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 20px; margin-bottom: 25px; }
.card { background: var(--bg-secondary); border: 1px solid var(--border); border-radius: 12px; padding: 20px; }
.card h2 { font-size: 15px; font-weight: 600; margin-bottom: 15px; display: flex; align-items: center; justify-content: space-between; }

/* QR BOX */
.qr-container { display: flex; flex-direction: column; align-items: center; gap: 12px; text-align: center; }
.qr-box { background: #fff; padding: 10px; border-radius: 10px; box-shadow: 0 4px 12px rgba(0,0,0,0.3); width: 200px; height: 200px; }
.qr-box img { width: 100%; height: 100%; }

/* TABLES */
table { width: 100%; border-collapse: collapse; font-size: 12px; text-align: left; }
th { color: var(--text-muted); font-weight: 600; padding: 10px; border-bottom: 1px solid var(--border); }
td { padding: 10px; border-bottom: 1px solid rgba(255,255,255,0.05); }

/* MODAL */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.75); backdrop-filter: blur(4px); display: none; align-items: center; justify-content: center; z-index: 9999; padding: 20px; }
.modal { background: var(--bg-secondary); border: 1px solid var(--border); border-radius: 14px; max-width: 600px; width: 100%; max-height: 90vh; overflow-y: auto; padding: 25px; box-shadow: 0 20px 25px -5px rgba(0,0,0,0.5); }
.modal-header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 20px; }
.modal-header h3 { font-size: 18px; font-weight: 700; }
.modal-close { background: none; border: none; color: var(--text-muted); font-size: 20px; cursor: pointer; }

.form-group { margin-bottom: 16px; }
.form-label { display: block; font-size: 12px; font-weight: 600; margin-bottom: 6px; color: var(--text-main); }
.form-input { width: 100%; padding: 10px 12px; background: var(--bg-input); border: 1px solid var(--border); border-radius: 8px; color: #fff; font-size: 13px; outline: none; }
.form-input:focus { border-color: var(--border-focus); }
.form-hint { font-size: 11px; color: var(--text-muted); margin-top: 4px; }
.switch-label { display: flex; align-items: center; gap: 10px; cursor: pointer; font-size: 13px; font-weight: 600; }

.tip-box { background: rgba(59, 130, 246, 0.1); border: 1px solid rgba(59, 130, 246, 0.3); border-radius: 8px; padding: 12px; font-size: 12px; color: #bfdbfe; margin-top: 15px; }
.tip-box code { background: rgba(0,0,0,0.3); padding: 2px 5px; border-radius: 4px; font-family: monospace; }
</style>
</head>
<body>
<div class="container">
  <!-- HEADER -->
  <header class="header">
    <div class="brand">
      <h1>🌐 HA P2P Tunnel <span style="font-size:12px; font-weight:600; background:rgba(59,130,246,0.2); color:#60a5fa; padding:2px 8px; border-radius:6px;">v{{APP_VERSION}}</span></h1>
      <p>Acesso Remoto Híbrido P2P & HTTPS 443 • Assistente IA & Telegram Integrados</p>
    </div>

    <div class="status-pills">
      <span id="pillHttps" class="pill pill-yellow">● Conectando HTTPS...</span>
      <span id="pillTelegram" class="pill pill-gray">● Bot Telegram: Inativo</span>
      <span id="pillAI" class="pill pill-gray">● Assistente IA: Inativo</span>
    </div>

    <div class="header-actions">
      <button class="btn btn-success" onclick="testTelegramNotification()">🔔 Testar Notificação</button>
      <button class="btn btn-primary" onclick="openSettingsModal()">⚙️ Configurações & Bot</button>
      <a href="{{DIRECT_DOWNLOAD_URL}}" class="btn">📱 Baixar APK Oficial</a>
      <button class="btn" onclick="toggleLogs()">📄 Logs</button>
    </div>
  </header>

  <!-- WIZARD BANNER (SE AINDA NÃO CONFIGURADO) -->
  <div id="wizardBanner" style="display:none; background:linear-gradient(90deg, #1e3a8a, #312e81); border:1px solid #4338ca; border-radius:12px; padding:18px; margin-bottom:25px; align-items:center; justify-content:space-between; flex-wrap:wrap; gap:15px;">
    <div>
      <h3 style="font-size:16px; font-weight:700; margin-bottom:4px;">🚀 Nova Versão 0.4.0: Ative o Bot do Telegram e a IA Gemini!</h3>
      <p style="font-size:12px; color:#c7d2fe;">Gerencie sua casa, receba notificações e controle luzes diretamente pelo Telegram sem depender de add-ons externos.</p>
    </div>
    <button class="btn btn-primary" onclick="openSettingsModal()">Configurar Agora</button>
  </div>

  <!-- GRID PRINCIPAL -->
  <div class="grid">
    <!-- CARD 1: LINK DIRETO & ACESSO WEB -->
    <div class="card">
      <h2>🔗 Acesso HTTPS Direto (Porta 443) <span id="httpsBadge" class="pill pill-yellow">Iniciando...</span></h2>
      <p style="font-size:12px; color:var(--text-muted); margin-bottom:12px;">Funciona em qualquer navegador, computador do trabalho, iPhone e redes restritas com firewall.</p>
      
      <div style="background:var(--bg-input); border:1px solid var(--border); border-radius:8px; padding:12px; word-break:break-all; font-family:monospace; font-size:12px; margin-bottom:12px;" id="httpsUrlBox">
        Aguardando Cloudflare conectar...
      </div>

      <div style="display:flex; gap:10px;">
        <a id="btnOpenHttps" href="#" target="_blank" class="btn btn-primary" style="display:none;">Abrir Home Assistant</a>
        <button class="btn" onclick="copyHttpsUrl()">Copiar Link</button>
      </div>
    </div>

    <!-- CARD 2: APP ANDROID P2P & QR CODE -->
    <div class="card">
      <h2>📱 App Android P2P Global</h2>
      <div class="qr-container">
        <div class="qr-box">
          <img id="qrImg" src="qr" alt="QR Code" />
        </div>
        <p style="font-size:11px; color:var(--text-muted);">Aponte a câmera do celular para baixar o APK diretamente ou configurar o Peer ID.</p>
        <div style="background:var(--bg-input); border:1px solid var(--border); border-radius:6px; padding:6px 10px; font-size:11px; font-family:monospace; max-width:100%; overflow:hidden; text-overflow:ellipsis;">
          Peer ID: <b>{{HOST_ID}}</b>
        </div>
      </div>
    </div>

    <!-- CARD 3: NOTIFICAÇÕES SEM YAML NO HOME ASSISTANT -->
    <div class="card">
      <h2>🔔 Notificações Automáticas no Telegram</h2>
      <p style="font-size:12px; color:var(--text-muted); margin-bottom:10px;">Dispare avisos no Telegram a partir de <b>qualquer automação</b> do Home Assistant de 2 formas simples:</p>

      <div class="tip-box" style="margin-top:0; margin-bottom:10px;">
        <b>Opção 1: Direto na Automação (Zero YAML!):</b><br/>
        Ação: <code>Disparar evento</code><br/>
        Tipo do evento: <code>p2p_telegram</code><br/>
        Dados do evento: <code>message: "Luz ligada!"</code>
        <div style="margin-top:8px;">
          <button class="btn btn-sm btn-primary" onclick="copyEventYaml()">📋 Copiar Código YAML da Ação</button>
        </div>
      </div>

      <div class="tip-box" style="margin-top:0;">
        <b>Opção 2: Via REST Command (Compatível):</b><br/>
        URL: <code>http://local-ha-p2p-tunnel:8099/api/send_telegram</code><br/>
        Payload: <code>{"message": "Sua mensagem", "target": "admin"}</code>
      </div>
    </div>
  </div>

  <!-- CARD DE CONTROLE DE USUÁRIOS DO TELEGRAM -->
  <div class="card" style="margin-bottom:25px;">
    <h2>
      <span>👥 Controle de Usuários do Telegram</span>
      <span id="userSummaryBadge" class="pill pill-green">0 Autorizados</span>
    </h2>
    <p style="font-size:12px; color:var(--text-muted); margin-bottom:15px;">
      Apenas usuários autorizados pelo Administrador podem interagir com o bot. Quando alguém novo tentar conversar, um alerta com o Chat ID aparecerá aqui e no seu Telegram.
    </p>

    <div style="overflow-x:auto;">
      <table>
        <thead>
          <tr>
            <th>Status</th>
            <th>Nome / Usuário</th>
            <th>Chat ID</th>
            <th>Solicitado em</th>
            <th>Último Acesso</th>
            <th style="text-align:right;">Ações do Administrador</th>
          </tr>
        </thead>
        <tbody id="usersTableBody">
          <tr><td colspan="6" style="text-align:center; color:#64748b; padding:20px;">Nenhum usuário cadastrado até o momento. Envie /start no bot para registrar o Administrador!</td></tr>
        </tbody>
      </table>
    </div>
  </div>

  <!-- DISPOSITIVOS CONECTADOS -->
  <div class="card" style="margin-bottom:25px;">
    <h2>📱 Dispositivos P2P Conectados</h2>
    <div style="overflow-x:auto;">
      <table>
        <thead>
          <tr>
            <th>Status</th>
            <th>Dispositivo</th>
            <th>Tipo de Rede</th>
            <th>Streams Ativas</th>
            <th>Tráfego</th>
            <th>Primeiro Acesso</th>
            <th>Última Atividade</th>
          </tr>
        </thead>
        <tbody id="devicesBody">
          <tr><td colspan="7" style="text-align:center; color:#64748b; padding:20px;">Nenhum dispositivo conectado no momento.</td></tr>
        </tbody>
      </table>
    </div>
  </div>

  <!-- MODAL DE CONFIGURAÇÕES -->
  <div class="modal-overlay" id="settingsModal">
    <div class="modal">
      <div class="modal-header">
        <h3>⚙️ Configurações do HA P2P Tunnel</h3>
        <button class="modal-close" onclick="closeSettingsModal()">&times;</button>
      </div>

      <form id="settingsForm" onsubmit="saveSettings(event)">
        <!-- SEÇÃO TELEGRAM -->
        <div style="border-bottom:1px solid var(--border); padding-bottom:15px; margin-bottom:15px;">
          <h4 style="font-size:14px; margin-bottom:12px; color:#60a5fa;">🤖 Bot do Telegram</h4>
          
          <div class="form-group">
            <label class="switch-label">
              <input type="checkbox" id="cfgEnableTelegram" style="width:16px; height:16px;" />
              Ativar Bot do Telegram integrado
            </label>
          </div>

          <div class="form-group">
            <label class="form-label">Nome do Bot</label>
            <input type="text" id="cfgBotName" class="form-input" placeholder="Ex: Herbie ou Jarvis" />
          </div>

          <div class="form-group">
            <label class="form-label">Token do Bot (Telegram)</label>
            <input type="password" id="cfgTelegramToken" class="form-input" placeholder="Cole o token do @BotFather" />
            <div class="form-hint">Obtenha gratuitamente criando um bot no Telegram com o <b>@BotFather</b>.</div>
          </div>

          <div class="form-group">
            <label class="form-label">Chat ID do Administrador</label>
            <input type="text" id="cfgTelegramChatID" class="form-input" placeholder="Ex: 123456789 ou deixe em branco" />
            <div class="form-hint">Dica: Se deixar em branco, o primeiro usuário que mandar <code>/start</code> no bot será cadastrado automaticamente como admin!</div>
          </div>
        </div>

        <!-- SEÇÃO IA GEMINI -->
        <div style="border-bottom:1px solid var(--border); padding-bottom:15px; margin-bottom:15px;">
          <h4 style="font-size:14px; margin-bottom:12px; color:#a78bfa;">🧠 Assistente IA (Google Gemini)</h4>

          <div class="form-group">
            <label class="switch-label">
              <input type="checkbox" id="cfgEnableAI" style="width:16px; height:16px;" />
              Ativar Inteligência Artificial para conversas e controle de luzes
            </label>
          </div>

          <div class="form-group">
            <label class="form-label">Chave de API do Google Gemini</label>
            <input type="password" id="cfgGeminiKey" class="form-input" placeholder="AIzaSy..." />
            <div class="form-hint">Gere gratuitamente no <b>Google AI Studio</b> (aistudio.google.com).</div>
          </div>

          <div class="form-group">
            <label class="form-label">Fila de Prioridade de Modelos (Fallback automático)</label>
            <input type="text" id="cfgGeminiModels" class="form-input" placeholder="gemini-3.6-flash, gemini-3.5-flash-lite, gemini-3.1-flash-lite, gemini-2.5-flash" />
            <div class="form-hint">Se um modelo atingir a cota (429), o próximo da fila assume instantaneamente.</div>
          </div>
        </div>

        <!-- SEÇÃO TÚNEL & PSK -->
        <div style="margin-bottom:20px;">
          <h4 style="font-size:14px; margin-bottom:12px; color:#34d399;">🔐 Segurança e Proteção</h4>
          
          <div class="form-group">
            <label class="form-label">Senha PSK do Túnel (Opcional)</label>
            <input type="password" id="cfgSecretPSK" class="form-input" placeholder="Digite uma senha ou deixe em branco" />
            <div class="form-hint">Protege a URL Cloudflare e a stream P2P do app móvel com criptografia SHA-256.</div>
          </div>
        </div>

        <div style="display:flex; justify-content:flex-end; gap:10px;">
          <button type="button" class="btn" onclick="closeSettingsModal()">Cancelar</button>
          <button type="submit" class="btn btn-primary">💾 Salvar Configurações</button>
        </div>
      </form>
    </div>
  </div>

  <!-- MODAL DE LOGS -->
  <div class="modal-overlay" id="logsModal">
    <div class="modal" style="max-width:800px;">
      <div class="modal-header">
        <h3>📄 Logs do Servidor</h3>
        <button class="modal-close" onclick="toggleLogs()">&times;</button>
      </div>
      <pre id="logsContent" style="background:#0f172a; padding:15px; border-radius:8px; font-size:11px; max-height:450px; overflow-y:auto; color:#94a3b8; font-family:monospace;"></pre>
    </div>
  </div>
</div>

<script>
function getEndpoint(path) {
  let base = window.location.pathname;
  if (!base.endsWith("/")) base += "/";
  return base + path;
}

function openSettingsModal() {
  fetch(getEndpoint("api/settings"))
    .then(r => r.json())
    .then(s => {
      document.getElementById("cfgEnableTelegram").checked = s.enable_telegram;
      document.getElementById("cfgBotName").value = s.bot_name || "Herbie";
      document.getElementById("cfgTelegramToken").value = s.telegram_bot_token || "";
      document.getElementById("cfgTelegramChatID").value = s.telegram_chat_id || "";
      document.getElementById("cfgEnableAI").checked = s.enable_ai;
      document.getElementById("cfgGeminiKey").value = s.gemini_api_key || "";
      document.getElementById("cfgGeminiModels").value = s.gemini_models_priority || "gemini-3.6-flash, gemini-3.5-flash-lite, gemini-3.1-flash-lite, gemini-2.5-flash";
      document.getElementById("cfgSecretPSK").value = s.secret_psk || "";

      document.getElementById("settingsModal").style.display = "flex";
    });
}

function closeSettingsModal() {
  document.getElementById("settingsModal").style.display = "none";
}

function saveSettings(e) {
  e.preventDefault();
  const payload = {
    enable_telegram: document.getElementById("cfgEnableTelegram").checked,
    bot_name: document.getElementById("cfgBotName").value,
    telegram_bot_token: document.getElementById("cfgTelegramToken").value,
    telegram_chat_id: document.getElementById("cfgTelegramChatID").value,
    enable_ai: document.getElementById("cfgEnableAI").checked,
    gemini_api_key: document.getElementById("cfgGeminiKey").value,
    gemini_models_priority: document.getElementById("cfgGeminiModels").value,
    secret_psk: document.getElementById("cfgSecretPSK").value
  };

  fetch(getEndpoint("api/settings"), {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify(payload)
  })
  .then(r => r.json())
  .then(res => {
    alert("✅ Configurações salvas com sucesso!");
    closeSettingsModal();
    updateStats();
  })
  .catch(err => alert("Erro ao salvar: " + err));
}

function testTelegramNotification() {
  if (!confirm("Deseja disparar uma notificação de teste para o Administrador do Telegram?")) return;
  fetch(getEndpoint("api/test_notification"), {method: "POST"})
    .then(r => r.json())
    .then(data => {
      if (data.status === "ok") {
        alert("✅ " + data.message);
      } else {
        alert("⚠️ " + (data.error || "Falha ao enviar"));
      }
    })
    .catch(err => alert("Erro ao testar: " + err));
}

function copyEventYaml() {
  const yaml = '- action: event.fire\n  data:\n    event_type: p2p_telegram\n    event_data:\n      message: "Sua mensagem aqui"\n      target: "admin"';
  navigator.clipboard.writeText(yaml).then(() => alert("Código YAML copiado para a área de transferência!\nBasta colar nas suas automações do Home Assistant."));
}

function handleUserAction(chatId, action, userName) {
  const promptName = (action === 'approve' && !userName) ? prompt("Nome para identificar este usuário:", "Morador") : userName;
  fetch(getEndpoint("api/users/action"), {
    method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({chat_id: chatId, action: action, name: promptName || ""})
  })
  .then(r => r.json())
  .then(() => {
    updateStats();
  })
  .catch(err => alert("Erro ao executar ação: " + err));
}

function toggleLogs() {
  const m = document.getElementById("logsModal");
  if (m.style.display === "flex") {
    m.style.display = "none";
  } else {
    fetch(getEndpoint("api/logs"))
      .then(r => r.json())
      .then(data => {
        document.getElementById("logsContent").innerText = (data.logs || []).join("\n");
        m.style.display = "flex";
      });
  }
}

function copyHttpsUrl() {
  const url = document.getElementById("httpsUrlBox").innerText;
  navigator.clipboard.writeText(url).then(() => alert("Link copiado para a área de transferência!"));
}

function formatBytes(bytes) {
  if (!bytes || bytes === 0) return "0 KB";
  const kb = bytes / 1024;
  if (kb < 1024) return kb.toFixed(1) + " KB";
  return (kb / 1024).toFixed(2) + " MB";
}

function updateStats() {
  fetch(getEndpoint("api/stats"))
    .then(r => r.json())
    .then(data => {
      // Pills
      if (data.https_url) {
        document.getElementById("pillHttps").className = "pill pill-green";
        document.getElementById("pillHttps").innerText = "● HTTPS Conectado";
        document.getElementById("httpsBadge").className = "pill pill-green";
        document.getElementById("httpsBadge").innerText = "Online";
        document.getElementById("httpsUrlBox").innerText = data.https_auth_url;
        document.getElementById("btnOpenHttps").href = data.https_auth_url;
        document.getElementById("btnOpenHttps").style.display = "inline-flex";
      } else {
        document.getElementById("pillHttps").className = "pill pill-yellow";
        document.getElementById("pillHttps").innerText = "● Conectando HTTPS...";
      }

      if (data.telegram_active) {
        document.getElementById("pillTelegram").className = "pill pill-green";
        document.getElementById("pillTelegram").innerText = "● Bot " + (data.bot_name || "Herbie") + ": Ativo";
      } else {
        document.getElementById("pillTelegram").className = "pill pill-gray";
        document.getElementById("pillTelegram").innerText = "● Bot Telegram: Inativo";
      }

      if (data.ai_active) {
        document.getElementById("pillAI").className = "pill pill-green";
        document.getElementById("pillAI").innerText = "● IA Gemini: Ativa";
      } else {
        document.getElementById("pillAI").className = "pill pill-gray";
        document.getElementById("pillAI").innerText = "● IA Gemini: Inativa";
      }

      // Wizard Banner
      if (!data.wizard_completed && !data.telegram_active) {
        document.getElementById("wizardBanner").style.display = "flex";
      } else {
        document.getElementById("wizardBanner").style.display = "none";
      }

      // Tabela de Usuários do Telegram
      if (data.telegram_users && Object.keys(data.telegram_users).length > 0) {
        let uRows = "";
        let pending = 0;
        let allowed = 0;
        for (const [cid, u] of Object.entries(data.telegram_users)) {
          let statusPill = "";
          let actionBtns = "";

          if (u.status === "admin") {
            statusPill = '<span class="pill pill-green">👑 Administrador</span>';
            actionBtns = '<span style="color:#64748b; font-size:11px;">Principal</span>';
            allowed++;
          } else if (u.status === "allowed") {
            statusPill = '<span class="pill pill-green">🟢 Autorizado</span>';
            actionBtns = '<button class="btn btn-sm btn-danger" onclick="handleUserAction(\'' + cid + '\', \'block\')">⛔ Bloquear</button> ' +
                         '<button class="btn btn-sm" onclick="handleUserAction(\'' + cid + '\', \'delete\')">🗑️</button>';
            allowed++;
          } else if (u.status === "pending") {
            statusPill = '<span class="pill pill-yellow">🟡 Pendente de Aprovação</span>';
            actionBtns = '<button class="btn btn-sm btn-success" onclick="handleUserAction(\'' + cid + '\', \'approve\', \'' + (u.name || '') + '\')">✅ Aprovar</button> ' +
                         '<button class="btn btn-sm btn-danger" onclick="handleUserAction(\'' + cid + '\', \'block\')">⛔ Bloquear</button>';
            pending++;
          } else if (u.status === "blocked") {
            statusPill = '<span class="pill pill-red">🔴 Bloqueado</span>';
            actionBtns = '<button class="btn btn-sm btn-success" onclick="handleUserAction(\'' + cid + '\', \'approve\', \'' + (u.name || '') + '\')">Desbloquear</button> ' +
                         '<button class="btn btn-sm" onclick="handleUserAction(\'' + cid + '\', \'delete\')">🗑️</button>';
          }

          const uname = u.username ? ' (@' + u.username + ')' : '';
          const displayName = (u.name || 'Sem nome') + uname;

          uRows += '<tr>' +
            '<td>' + statusPill + '</td>' +
            '<td><b>' + displayName + '</b></td>' +
            '<td><code style="background:rgba(0,0,0,0.3); padding:2px 6px; border-radius:4px;">' + cid + '</code></td>' +
            '<td>' + (u.requested_at || '-') + '</td>' +
            '<td>' + (u.last_seen || '-') + '</td>' +
            '<td style="text-align:right;">' + actionBtns + '</td>' +
          '</tr>';
        }
        document.getElementById("usersTableBody").innerHTML = uRows;

        if (pending > 0) {
          document.getElementById("userSummaryBadge").className = "pill pill-yellow";
          document.getElementById("userSummaryBadge").innerText = pending + " Pendente(s) • " + allowed + " Autorizado(s)";
        } else {
          document.getElementById("userSummaryBadge").className = "pill pill-green";
          document.getElementById("userSummaryBadge").innerText = allowed + " Usuário(s) Autorizado(s)";
        }
      }

      // Dispositivos P2P
      if (data.devices && data.devices.length > 0) {
        let rows = "";
        data.devices.forEach(d => {
          const statusTag = d.is_online ? '<span class="pill pill-green">● Online</span>' : '<span style="color:#64748b">Offline</span>';
          const traf = formatBytes((d.bytes_in || 0) + (d.bytes_out || 0));
          const peerShort = d.peer_id.slice(0, 8) + "..." + d.peer_id.slice(-6);
          rows += '<tr>' +
            '<td>' + statusTag + '</td>' +
            '<td><b>Celular Android</b><br><span style="font-family:monospace; font-size:11px; color:#94a3b8;">' + peerShort + '</span></td>' +
            '<td><span class="pill pill-green" style="font-size:10px;">' + d.network_type + '</span></td>' +
            '<td>' + d.active_streams + '</td>' +
            '<td>' + traf + '</td>' +
            '<td>' + new Date(d.first_seen).toLocaleTimeString() + '</td>' +
            '<td>' + new Date(d.last_seen).toLocaleTimeString() + '</td>' +
          '</tr>';
        });
        document.getElementById("devicesBody").innerHTML = rows;
      }
    })
    .catch(err => console.warn("Erro ao atualizar stats:", err));
}

setInterval(updateStats, 2500);
updateStats();
</script>
</body>
</html>`

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		html := htmlTemplate
		html = strings.ReplaceAll(html, "{{APP_VERSION}}", AppVersion)
		html = strings.ReplaceAll(html, "{{DEVELOPER}}", AppDeveloper)
		html = strings.ReplaceAll(html, "{{DIRECT_DOWNLOAD_URL}}", GitHubApkUrl)
		html = strings.ReplaceAll(html, "{{HOST_ID}}", hostID)
		w.Write([]byte(html))
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[Ingress] Erro no servidor web: %v", err)
		}
	}()
}

// ==========================================================================
// FUNÇÃO PRINCIPAL
// ==========================================================================

func main() {
	keyPath := flag.String("key", "/data/peer.key", "Caminho da chave privada")
	ingressPort := flag.String("ingress-port", "8099", "Porta do painel Ingress Web")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	priv, err := loadOrCreateKey(*keyPath)
	if err != nil {
		log.Fatalf("[ERRO FATAL] Falha ao carregar chave: %v", err)
	}

	appSettings := loadAppSettings()
	secretPSK := appSettings.SecretPSK
	pskEnabled := secretPSK != ""

	var opts []libp2p.Option
	opts = append(opts,
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/41820",
			"/ip4/0.0.0.0/udp/41820/quic-v1",
			"/ip6/::/tcp/41820",
			"/ip6/::/udp/41820/quic-v1",
		),
		libp2p.NATPortMap(),
		libp2p.EnableNATService(),
		libp2p.EnableHolePunching(),
		libp2p.EnableAutoRelayWithStaticRelays(dht.GetDefaultBootstrapPeerAddrInfos()),
	)

	host, err := libp2p.New(opts...)
	if err != nil {
		log.Fatalf("[ERRO FATAL] Falha ao iniciar libp2p: %v", err)
	}
	defer host.Close()

	peerIDStr := host.ID().String()
	log.Printf("==========================================================================")
	log.Printf(" [P2P-TUNNEL] Peer ID Persistente do Servidor: %s", peerIDStr)
	log.Printf("==========================================================================")

	onUrlUpdate := func(rawUrl, fullUrl string) {
		notifyMu.Lock()
		if lastNotifiedURL == fullUrl {
			notifyMu.Unlock()
			return
		}
		lastNotifiedURL = fullUrl
		notifyMu.Unlock()

		settingsMu.RLock()
		psk := currentSettings.SecretPSK
		token := currentSettings.TelegramBotToken
		chatID := currentSettings.TelegramChatID
		settingsMu.RUnlock()

		updateHomeAssistantEntities(peerIDStr, rawUrl, fullUrl, psk)

		msg := fmt.Sprintf("🚀 <b>HA P2P Tunnel Conectado!</b>\n\n🔗 <b>Link Direto (Cloudflare HTTPS):</b>\n<a href=\"%s\">%s</a>\n\n🔑 <b>Peer ID (App Android P2P):</b>\n<code>%s</code>\n\n🔐 <b>Senha PSK:</b> <code>%s</code>", fullUrl, fullUrl, peerIDStr, psk)
		sendTelegramNotification(token, chatID, msg)
	}

	startHttpsTunnel(ctx, onUrlUpdate)
	startIngressServer(*ingressPort, host, peerIDStr)
	startHAWebSocketListener(ctx)
	restartTelegramBot()

	updateHomeAssistantEntities(peerIDStr, "", "", secretPSK)

	kademliaDHT, err := dht.New(host, dht.Mode(dht.ModeServer))
	if err != nil {
		log.Fatalf("[ERRO FATAL] Falha na DHT: %v", err)
	}
	_ = kademliaDHT.Bootstrap(ctx)

	for _, addr := range dht.DefaultBootstrapPeers {
		pi, err := peer.AddrInfoFromP2pAddr(addr)
		if err == nil {
			go host.Connect(ctx, *pi)
		}
	}

	routingDiscovery := routing.NewRoutingDiscovery(kademliaDHT)
	go func() {
		for {
			util.Advertise(ctx, routingDiscovery, peerIDStr)
			time.Sleep(1 * time.Minute)
		}
	}()

	host.SetStreamHandler(ProtocolIDReport, func(s network.Stream) {
		defer s.Close()
		s.SetReadDeadline(time.Now().Add(10 * time.Second))
		var report ClientReport
		if err := json.NewDecoder(s).Decode(&report); err == nil {
			report.ReceivedAt = time.Now()
			stats.addClientReport(&report)
			log.Printf("[+] [DIAGNÓSTICO] Relatório de conexão recebido do cliente %s (Dispositivo: %s, Duração: %dms)",
				report.PeerID, report.Device, report.ConnectDurationMs)
		}
	})

	host.SetStreamHandler(ProtocolID, func(s network.Stream) {
		defer s.Close()

		remotePeer := s.Conn().RemotePeer().String()
		remoteAddr := s.Conn().RemoteMultiaddr().String()

		settingsMu.RLock()
		currentPSK := currentSettings.SecretPSK
		isPSKActive := currentPSK != ""
		settingsMu.RUnlock()

		if isPSKActive {
			s.SetReadDeadline(time.Now().Add(5 * time.Second))
			tokenBuf := make([]byte, 32)
			_, err := io.ReadFull(s, tokenBuf)
			s.SetReadDeadline(time.Time{})

			expected := computePSKToken(currentPSK)
			if err != nil || subtle.ConstantTimeCompare(tokenBuf, expected[:]) != 1 {
				log.Printf("[SEGURANÇA] Conexão REJEITADA de %s: Chave PSK incorreta ou ausente!", remotePeer)
				atomic.AddInt64(&stats.RejectedStreams, 1)
				_, _ = s.Write([]byte{0x00})
				return
			}
			_, _ = s.Write([]byte{0x01})
		}

		startTime := time.Now()
		netType := classifyNetwork(remoteAddr)

		stats.mu.Lock()
		dev, exists := stats.Devices[remotePeer]
		if !exists {
			dev = &DeviceSession{
				PeerID:      remotePeer,
				RemoteAddr:  remoteAddr,
				NetworkType: netType,
				FirstSeen:   startTime,
			}
			stats.Devices[remotePeer] = dev
		}
		dev.ActiveStreams++
		dev.TotalStreams++
		dev.RemoteAddr = remoteAddr
		dev.NetworkType = netType
		dev.LastSeen = startTime
		dev.IsOnline = true
		stats.mu.Unlock()

		atomic.AddInt64(&stats.ActiveStreams, 1)
		atomic.AddInt64(&stats.TotalStreams, 1)

		event := &ConnectionEvent{
			PeerID:    remotePeer,
			Remote:    remoteAddr,
			StartTime: startTime,
			Active:    true,
		}
		stats.addEvent(event)

		haConn, err := net.DialTimeout("tcp", "127.0.0.1:8123", 5*time.Second)
		if err != nil {
			log.Printf("[-] [ERRO] Falha ao conectar ao Home Assistant (127.0.0.1:8123): %v", err)
			atomic.AddInt64(&stats.ActiveStreams, -1)
			stats.mu.Lock()
			if dev, exists := stats.Devices[remotePeer]; exists {
				dev.ActiveStreams--
				if dev.ActiveStreams <= 0 {
					dev.ActiveStreams = 0
					dev.IsOnline = false
				}
			}
			stats.mu.Unlock()
			event.Active = false
			event.EndTime = time.Now()
			event.Duration = "0s (falha)"
			return
		}
		defer haConn.Close()

		var bytesIn, bytesOut int64
		haWriter := &countingWriter{w: haConn, count: &bytesIn}
		streamWriter := &countingWriter{w: s, count: &bytesOut}

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = io.Copy(haWriter, s)
		}()
		go func() {
			defer wg.Done()
			_, _ = io.Copy(streamWriter, haConn)
		}()
		wg.Wait()

		duration := time.Since(startTime).Round(time.Millisecond)
		atomic.AddInt64(&stats.ActiveStreams, -1)
		atomic.AddInt64(&stats.TotalBytesIn, bytesIn)
		atomic.AddInt64(&stats.TotalBytesOut, bytesOut)

		stats.mu.Lock()
		if dev, exists := stats.Devices[remotePeer]; exists {
			dev.ActiveStreams--
			dev.BytesIn += bytesIn
			dev.BytesOut += bytesOut
			dev.LastSeen = time.Now()
			if dev.ActiveStreams <= 0 {
				dev.ActiveStreams = 0
				dev.IsOnline = false
			}
		}
		stats.mu.Unlock()

		event.Active = false
		event.EndTime = time.Now()
		event.Duration = duration.String()
		event.BytesIn = bytesIn
		event.BytesOut = bytesOut
	})

	log.SetOutput(io.MultiWriter(os.Stdout, serverLogs))

	printLog("==========================================================================\n")
	printLog("        HA P2P TUNNEL v%s - Desenvolvido por %s        \n", AppVersion, AppDeveloper)
	printLog("==========================================================================\n")
	printLog(" Data/Hora de Início: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	printLog(" PEER ID DO SEU HOME ASSISTANT:\n   >>> %s <<<\n", peerIDStr)
	printLog(" Painel Ingress Web: Ativo na porta %s\n", *ingressPort)
	if pskEnabled {
		printLog(" Rede Privada (PSK): [ATIVADA] - Autenticação por Stream ativa.\n")
	} else {
		printLog(" Rede Privada (PSK): [DESATIVADA] - Qualquer cliente com seu PeerID pode conectar.\n")
	}
	printLog(" Protocolos: IPv4 + IPv6 | QUIC + TCP | UPnP NAT-PMP Ativo\n")
	printLog(" Assistente IA & Telegram: Pronto para receber comandos e automações.\n")
	printLog("==========================================================================\n")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	sig := <-sigChan
	fmt.Println("\n==========================================================================")
	fmt.Printf(" [FINALIZAÇÃO] Sinal recebido: %v às %s\n", sig, time.Now().Format("15:04:05"))
	fmt.Println(" Fechando nós P2P, conexões ativas e desligando serviço...")
	cancel()
	_ = host.Close()
	fmt.Println(" HOME ASSISTANT P2P TUNNEL ENCERRADO COM SUCESSO.")
	fmt.Println("==========================================================================")
}
