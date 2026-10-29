package main

import (
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
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
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
	AppVersion       = "0.2.0"
	AppDeveloper     = "Jhonatan Garcia (jhona1234)"
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
	Devices         map[string]*DeviceSession
	History         []*ConnectionEvent
	ClientReports   []*ClientReport
	StartedAt       time.Time
}

var stats = &TunnelStats{
	StartedAt:     time.Now(),
	Devices:       make(map[string]*DeviceSession),
	History:       make([]*ConnectionEvent, 0),
	ClientReports: make([]*ClientReport, 0),
}

func classifyNetwork(remote string) string {
	isV6 := strings.Contains(remote, "/ip6/")
	isQuic := strings.Contains(remote, "/quic")
	isRelay := strings.Contains(remote, "/p2p-circuit")

	proto := "TCP"
	if isQuic {
		proto = "QUIC"
	}
	if isRelay {
		return "Relay (" + proto + ")"
	}
	if isV6 {
		return "IPv6 Direto (" + proto + ")"
	}
	return "IPv4 Direto (" + proto + ")"
}

func (s *TunnelStats) addEvent(e *ConnectionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append([]*ConnectionEvent{e}, s.History...)
	if len(s.History) > 40 {
		s.History = s.History[:40]
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

func computePSKToken(secret string) [32]byte {
	return sha256.Sum256([]byte("ha-p2p-psk-auth-v2:" + secret))
}

func readOptionsPSK() string {
	data, err := os.ReadFile("/data/options.json")
	if err != nil {
		return ""
	}
	var opts struct {
		SecretPSK string `json:"secret_psk"`
	}
	if err := json.Unmarshal(data, &opts); err == nil {
		return strings.TrimSpace(opts.SecretPSK)
	}
	return ""
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

type countingWriter struct {
	w     io.Writer
	count *int64
}

func (cw *countingWriter) Write(p []byte) (n int, err error) {
	n, err = cw.w.Write(p)
	atomic.AddInt64(cw.count, int64(n))
	return
}

func getLocalLANIP() string {
	addrs, err := net.InterfaceAddrs()
	if err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					s := ip4.String()
					if strings.HasPrefix(s, "192.168.") || strings.HasPrefix(s, "10.") {
						return s
					}
				}
			}
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ip4 := ipnet.IP.To4(); ip4 != nil {
					s := ip4.String()
					if !strings.HasPrefix(s, "172.17.") && !strings.HasPrefix(s, "172.30.") {
						return s
					}
				}
			}
		}
	}
	return "192.168.100.10"
}

func startIngressServer(port string, hostID string, secretPSK string) {
	mux := http.NewServeMux()
	pskEnabled := secretPSK != ""
	lanIP := getLocalLANIP()

	apkHandler := func(w http.ResponseWriter, r *http.Request) {
		apkPaths := []string{
			"/app/ha-p2p-tunnel.apk",
			"/data/ha-p2p-tunnel.apk",
			"./ha-p2p-tunnel.apk",
		}
		var foundPath string
		for _, p := range apkPaths {
			if _, err := os.Stat(p); err == nil {
				foundPath = p
				break
			}
		}
		if foundPath == "" {
			http.Error(w, "APK não encontrado no servidor do Add-on.", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
		w.Header().Set("Content-Disposition", "attachment; filename=\"HA_P2P_Tunnel.apk\"")
		http.ServeFile(w, r, foundPath)
	}

	mux.HandleFunc("/download/", apkHandler)
	mux.HandleFunc("/download/ha-p2p-tunnel.apk", apkHandler)
	mux.HandleFunc("/ha-p2p-tunnel.apk", apkHandler)
	mux.HandleFunc("/HA_P2P_Tunnel.apk", apkHandler)

	mux.HandleFunc("/qr-apk.png", func(w http.ResponseWriter, r *http.Request) {
		downloadUrl := fmt.Sprintf("http://%s:8099/download/ha-p2p-tunnel.apk", lanIP)
		png, err := qrcode.Encode(downloadUrl, qrcode.Medium, 260)
		if err != nil {
			http.Error(w, "Erro ao gerar QR", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(png)
	})

	mux.HandleFunc("/qr-peer.png", func(w http.ResponseWriter, r *http.Request) {
		qrContent := hostID
		if pskEnabled {
			qrContent = fmt.Sprintf("hap2p://connect?peer=%s&psk=%s", hostID, secretPSK)
		}
		png, err := qrcode.Encode(qrContent, qrcode.Medium, 260)
		if err != nil {
			http.Error(w, "Erro ao gerar QR", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(png)
	})

	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		stats.mu.Lock()
		defer stats.mu.Unlock()

		var devList []*DeviceSession
		for _, d := range stats.Devices {
			devList = append(devList, d)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active_streams":   atomic.LoadInt64(&stats.ActiveStreams),
			"total_streams":    atomic.LoadInt64(&stats.TotalStreams),
			"rejected_streams": atomic.LoadInt64(&stats.RejectedStreams),
			"total_bytes_in":   atomic.LoadInt64(&stats.TotalBytesIn),
			"total_bytes_out":  atomic.LoadInt64(&stats.TotalBytesOut),
			"uptime":           time.Since(stats.StartedAt).Round(time.Second).String(),
			"devices":          devList,
			"history":          stats.History,
			"client_reports":   stats.ClientReports,
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		pskBadge := `<span class="badge badge-off">Desativada (Livre)</span>`
		if pskEnabled {
			pskBadge = `<span class="badge badge-on">Ativada (Protegida)</span>`
		}
		directDownloadUrl := fmt.Sprintf("http://%s:8099/download/ha-p2p-tunnel.apk", lanIP)

		html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>HA P2P Tunnel v%s - por %s</title>
<style>
  :root { --bg: #0b1120; --card: #1e293b; --text: #f8fafc; --primary: #38bdf8; --success: #22c55e; --border: #334155; }
  * { box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; background: var(--bg); color: var(--text); margin: 0; padding: 20px; }
  .container { max-width: 980px; margin: 0 auto; }
  .header { display: flex; align-items: center; justify-content: space-between; border-bottom: 1px solid var(--border); padding-bottom: 16px; margin-bottom: 24px; flex-wrap: wrap; gap: 12px; }
  .title { font-size: 22px; font-weight: 700; color: var(--primary); display: flex; align-items: center; gap: 10px; }
  .dev-tag { font-size: 13px; color: #94a3b8; font-weight: normal; margin-left: 8px; }
  .status-dot { width: 12px; height: 12px; background: var(--success); border-radius: 50%%%%; display: inline-block; box-shadow: 0 0 12px var(--success); }
  .grid { display: grid; grid-template-columns: 1fr 1fr; gap: 20px; margin-bottom: 24px; }
  @media(max-width: 720px) { .grid { grid-template-columns: 1fr; } }
  .card { background: var(--card); border: 1px solid var(--border); border-radius: 14px; padding: 20px; display: flex; flex-direction: column; }
  .card h2 { margin-top: 0; font-size: 15px; color: #94a3b8; text-transform: uppercase; letter-spacing: 0.06em; margin-bottom: 14px; display: flex; align-items: center; gap: 8px; }
  .step-num { background: var(--primary); color: #0b1120; font-size: 12px; font-weight: 800; border-radius: 50%%%%; width: 22px; height: 22px; display: inline-flex; align-items: center; justify-content: center; }
  .qr-box { text-align: center; flex: 1; display: flex; flex-direction: column; align-items: center; justify-content: space-between; }
  .qr-img { background: #fff; padding: 10px; border-radius: 12px; max-width: 210px; height: auto; box-shadow: 0 4px 14px rgba(0,0,0,0.5); margin: 8px 0; }
  .qr-desc { font-size: 13px; color: #94a3b8; margin: 4px 0 12px 0; text-align: center; }
  .btn { background: #0284c7; color: white; border: none; padding: 10px 16px; border-radius: 8px; font-weight: 700; font-size: 13px; cursor: pointer; text-decoration: none; text-align: center; transition: 0.2s; display: inline-flex; align-items: center; justify-content: center; gap: 6px; }
  .btn:hover { background: #0369a1; }
  .btn-green { background: #16a34a; }
  .btn-green:hover { background: #15803d; }
  .btn-outline { background: transparent; border: 1px solid var(--border); color: #94a3b8; }
  .btn-outline:hover { background: #334155; color: #fff; }
  .link-direct { margin-top: 10px; font-size: 12px; color: #94a3b8; word-break: break-all; }
  .link-direct a { color: #38bdf8; text-decoration: underline; }
  .peer-id-box { background: #0b1120; padding: 10px; border-radius: 8px; border: 1px solid var(--border); word-break: break-all; font-family: monospace; font-size: 12px; color: #38bdf8; margin-bottom: 10px; width: 100%%%%; }
  .stat-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 14px; margin-bottom: 20px; }
  .stat-card { background: #0f172a; border: 1px solid var(--border); border-radius: 10px; padding: 14px; text-align: center; }
  .stat-card .val { font-size: 20px; font-weight: 700; color: #fff; margin-top: 6px; }
  .stat-card .lbl { font-size: 11px; text-transform: uppercase; color: #94a3b8; letter-spacing: 0.05em; }
  .badge { padding: 3px 8px; border-radius: 6px; font-size: 11px; font-weight: 600; display: inline-block; }
  .badge-on { background: #15803d; color: #dcfce7; }
  .badge-off { background: #475569; color: #f1f5f9; }
  .table-box { background: var(--card); border: 1px solid var(--border); border-radius: 14px; padding: 20px; overflow-x: auto; margin-top: 20px; }
  table { width: 100%%%%; border-collapse: collapse; font-size: 13px; text-align: left; }
  th { color: #94a3b8; padding: 12px 10px; border-bottom: 1px solid var(--border); font-size: 12px; text-transform: uppercase; letter-spacing: 0.05em; }
  td { padding: 12px 10px; border-bottom: 1px solid #1e293b; color: #e2e8f0; }
  tr:last-child td { border-bottom: none; }
  .live-tag { color: var(--success); font-weight: 700; }
  .diag-card { background: #0f172a; border: 1px solid #1e3a8a; border-radius: 12px; padding: 16px; margin-top: 20px; display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 14px; }
</style>
</head>
<body>
<div class="container">
  <div class="header">
    <div class="title">
      <span class="status-dot"></span> HA P2P Tunnel 
      <span class="dev-tag">v%s (Beta) • Desenvolvido por: <b>%s</b></span>
    </div>
    <div>Rede Privada (PSK): %s</div>
  </div>

  <div class="grid">
    <!-- PASSO 1: Baixar App Android -->
    <div class="card qr-box">
      <h2><span class="step-num">1</span> Baixar App Android</h2>
      <div class="qr-desc">Aponte a câmera do celular para baixar o APK:</div>
      <img class="qr-img" src="./qr-apk.png" alt="QR Code Download APK">
      <a class="btn btn-green" href="%s" target="_blank" download="HA_P2P_Tunnel.apk">⬇ Baixar APK Direto</a>
      <div class="link-direct">
        Link na rede local:<br>
        <a href="%s" target="_blank">%s</a>
      </div>
    </div>

    <!-- PASSO 2: Conectar o App -->
    <div class="card qr-box">
      <h2><span class="step-num">2</span> Conectar com o App</h2>
      <div class="qr-desc">No App, toque no botão <b>📷 Escanear QR Code do HA</b>:</div>
      <img class="qr-img" src="./qr-peer.png" alt="QR Code Conexão">
      <div class="peer-id-box" id="peerText">%s</div>
      <button class="btn" id="btnCopyPeer" onclick="copyPeer()"> Copiar Peer ID</button>
    </div>
  </div>

  <!-- Métricas do Túnel -->
  <div class="card">
    <h2>Métricas em Tempo Real</h2>
    <div class="stat-grid">
      <div class="stat-card"><div class="lbl">Dispositivos Ativos</div><div class="val" id="activeDevices" style="color:var(--success)">0</div></div>
      <div class="stat-card"><div class="lbl">Streams HTTP Paralelas</div><div class="val" id="activeStreams" style="color:#38bdf8">0</div></div>
      <div class="stat-card"><div class="lbl">Tráfego Enviado</div><div class="val" id="totalBytesOut">0 KB</div></div>
      <div class="stat-card"><div class="lbl">Tráfego Recebido</div><div class="val" id="totalBytesIn">0 KB</div></div>
      <div class="stat-card"><div class="lbl">Uptime</div><div class="val" id="uptime">0s</div></div>
    </div>
  </div>

  <!-- Card Limpo de Diagnóstico do Celular com Botões de Cópia e Exportação -->
  <div id="diagCardContainer" style="display:none;" class="diag-card">
    <div>
      <div style="font-size:14px; font-weight:700; color:#38bdf8; display:flex; align-items:center; gap:8px;">
        <span>📱 Relatório de Diagnóstico do Celular</span>
        <span id="diagBadgeCache" class="badge" style="background:#064e3b; color:#6ee7b7;">Cache</span>
      </div>
      <div id="diagInfoText" style="font-size:12px; color:#94a3b8; margin-top:4px;">
        Recebido às 00:00:00 • Tempo de busca: 0ms
      </div>
    </div>
    <div style="display:flex; gap:10px; flex-wrap:wrap;">
      <button class="btn" id="btnCopyHuman" onclick="copyHumanSummary()">📄 Copiar Resumo (Humano)</button>
      <button class="btn" id="btnCopyTech" onclick="copyTechLogs()">📋 Copiar Logs (Técnico)</button>
      <button class="btn btn-outline" id="btnExportCSV" onclick="exportCSV()">📥 Baixar CSV</button>
    </div>
  </div>

  <!-- Tabela Limpa e Inteligente de Dispositivos Conectados -->
  <div class="table-box">
    <div style="display:flex; justify-content:space-between; align-items:center; margin-bottom:12px;">
      <h2 style="margin:0; color:#94a3b8; font-size:15px;">📱 Dispositivos Conectados</h2>
      <button class="btn btn-outline" style="font-size:11px; padding:6px 12px;" onclick="toggleRawView()" id="btnToggleRaw">Mostrar Canais HTTP Detalhados</button>
    </div>

    <!-- Tabela Agrupada por Dispositivo (Sem duplicações!) -->
    <table id="tableDevices">
      <thead>
        <tr>
          <th>Status</th>
          <th>Dispositivo</th>
          <th>Tipo de Conexão</th>
          <th>Canais Paralelos</th>
          <th>Tráfego</th>
          <th>Primeiro Acesso</th>
          <th>Última Atividade</th>
        </tr>
      </thead>
      <tbody id="devicesBody">
        <tr><td colspan="7" style="text-align:center; color:#64748b; padding:20px;">Nenhum dispositivo conectado no momento.</td></tr>
      </tbody>
    </table>

    <!-- Tabela Técnica Opcional de Streams HTTP (Inicia oculta) -->
    <div id="rawStreamsContainer" style="display:none; margin-top:20px;">
      <h3 style="font-size:13px; color:#94a3b8; margin-bottom:8px;">Canais HTTP Individuais (Streams Multiplexadas):</h3>
      <table id="tableRaw">
        <thead>
          <tr>
            <th>Status</th>
            <th>Peer ID Remoto</th>
            <th>Endereço Remoto</th>
            <th>Início</th>
            <th>Duração</th>
            <th>Trafegado</th>
          </tr>
        </thead>
        <tbody id="rawBody"></tbody>
      </table>
    </div>
  </div>
</div>

<script>
let lastReport = null;
let lastStats = null;
let showRaw = false;

function toggleRawView() {
  showRaw = !showRaw;
  document.getElementById("rawStreamsContainer").style.display = showRaw ? "block" : "none";
  document.getElementById("btnToggleRaw").innerText = showRaw ? "Ocultar Canais HTTP" : "Mostrar Canais HTTP Detalhados";
}

function copyPeer() {
  const text = document.getElementById("peerText").innerText.trim();
  const btn = document.getElementById("btnCopyPeer");
  copyToClipboard(text, btn, "✓ Copiado com Sucesso!", " Copiar Peer ID");
}

function copyToClipboard(text, btn, successMsg, defaultMsg) {
  function onSuccess() {
    btn.innerText = successMsg;
    btn.style.background = "#16a34a";
    setTimeout(() => {
      btn.innerText = defaultMsg;
      btn.style.background = "";
    }, 2500);
  }
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(onSuccess).catch(fallback);
  } else {
    fallback();
  }
  function fallback() {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.left = "-9999px";
    document.body.appendChild(ta);
    ta.select();
    try {
      document.execCommand("copy");
      onSuccess();
    } catch(e) {}
    document.body.removeChild(ta);
  }
}

function copyHumanSummary() {
  if (!lastReport) return;
  const btn = document.getElementById("btnCopyHuman");
  let text = "=== RELATÓRIO DO TÚNEL HOME ASSISTANT P2P ===\n";
  if (lastReport.human_summary && lastReport.human_summary.length > 0) {
    text += lastReport.human_summary.join("\n") + "\n";
  } else {
    text += "Dispositivo: " + lastReport.device + "\n";
    text += "Tempo de Conexão: " + (lastReport.connect_duration_ms / 1000).toFixed(1) + " segundos\n";
    text += "Modo: " + (lastReport.used_cache ? "Cache Instantâneo" : "Varredura DHT") + "\n";
  }
  copyToClipboard(text, btn, "✓ Resumo Copiado!", "📄 Copiar Resumo (Humano)");
}

function copyTechLogs() {
  if (!lastReport || !lastReport.logs) return;
  const btn = document.getElementById("btnCopyTech");
  const text = lastReport.logs.join("\n");
  copyToClipboard(text, btn, "✓ Logs Copiados!", "📋 Copiar Logs (Técnico)");
}

function exportCSV() {
  if (!lastStats) return;
  let csv = "data:text/csv;charset=utf-8,";
  csv += "Status,Dispositivo,PeerID,Tipo_Conexao,Canais_Paralelos,Trafego_KB,Primeiro_Acesso,Ultima_Atividade\n";
  
  if (lastStats.devices && lastStats.devices.length > 0) {
    lastStats.devices.forEach(d => {
      const status = d.is_online ? "Online" : "Offline";
      const trafKB = (((d.bytes_in || 0) + (d.bytes_out || 0)) / 1024).toFixed(1);
      csv += [
        status,
        "Celular Android",
        d.peer_id,
        '"' + (d.network_type || "P2P") + '"',
        d.active_streams,
        trafKB,
        '"' + new Date(d.first_seen).toLocaleString() + '"',
        '"' + new Date(d.last_seen).toLocaleString() + '"'
      ].join(",") + "\n";
    });
  }
  
  const encodedUri = encodeURI(csv);
  const link = document.createElement("a");
  link.setAttribute("href", encodedUri);
  link.setAttribute("download", "conexoes_ha_p2p_" + new Date().toISOString().slice(0,10) + ".csv");
  document.body.appendChild(link);
  link.click();
  document.body.removeChild(link);
}

function formatBytes(bytes) {
  if (bytes === 0) return "0 KB";
  const kb = bytes / 1024;
  if (kb < 1024) return kb.toFixed(1) + " KB";
  return (kb / 1024).toFixed(2) + " MB";
}

function updateStats() {
  fetch("./api/stats")
    .then(r => r.json())
    .then(data => {
      lastStats = data;
      document.getElementById("activeStreams").innerText = data.active_streams;
      document.getElementById("totalStreams").innerText = data.total_streams;
      document.getElementById("uptime").innerText = data.uptime;
      if (data.total_bytes_out) document.getElementById("totalBytesOut").innerText = formatBytes(data.total_bytes_out);
      if (data.total_bytes_in) document.getElementById("totalBytesIn").innerText = formatBytes(data.total_bytes_in);

      // Atualiza relatório de diagnóstico do celular
      if (data.client_reports && data.client_reports.length > 0) {
        lastReport = data.client_reports[0];
        document.getElementById("diagCardContainer").style.display = "flex";
        
        const badge = document.getElementById("diagBadgeCache");
        if (lastReport.used_cache) {
          badge.innerText = "Cache Instantâneo (< 100ms)";
          badge.style.background = "#064e3b";
          badge.style.color = "#6ee7b7";
        } else {
          badge.innerText = "Varredura DHT Global";
          badge.style.background = "#1e3a8a";
          badge.style.color = "#bfdbfe";
        }

        const durSec = (lastReport.connect_duration_ms / 1000).toFixed(1);
        const timeStr = new Date(lastReport.received_at).toLocaleTimeString();
        document.getElementById("diagInfoText").innerText = 
          "Dispositivo: " + lastReport.device + " • Tempo de conexão: " + durSec + "s • Recebido às " + timeStr;
      }

      // 1. Tabela Limpa de Dispositivos Conectados
      let activeDevCount = 0;
      if (data.devices && data.devices.length > 0) {
        let devRows = "";
        data.devices.forEach(d => {
          if (d.is_online) activeDevCount++;
          const statusTag = d.is_online 
            ? '<span class="live-tag">● Online</span>' 
            : '<span style="color:#64748b">Offline</span>';
          const traf = formatBytes((d.bytes_in || 0) + (d.bytes_out || 0));
          const peerShort = d.peer_id.slice(0, 10) + "..." + d.peer_id.slice(-6);
          const isV6 = d.network_type && d.network_type.includes("IPv6");
          const ipBadge = isV6 
            ? '<span class="badge" style="background:#581c87; color:#e9d5ff;">' + d.network_type + '</span>' 
            : '<span class="badge" style="background:#1e3a8a; color:#bfdbfe;">' + d.network_type + '</span>';

          const streamsInfo = d.active_streams > 0 
            ? '<b style="color:#38bdf8">' + d.active_streams + ' ativas</b> (Dashboard / WS)' 
            : '<span style="color:#64748b">Inativo</span>';

          devRows += '<tr>' +
            '<td>' + statusTag + '</td>' +
            '<td><b>Celular Android</b><br><span style="font-family:monospace; font-size:11px; color:#94a3b8;" title="' + d.peer_id + '">' + peerShort + '</span></td>' +
            '<td>' + ipBadge + '<br><span style="font-size:10px; color:#64748b;">' + d.remote_addr + '</span></td>' +
            '<td>' + streamsInfo + '</td>' +
            '<td>' + traf + '</td>' +
            '<td>' + new Date(d.first_seen).toLocaleTimeString() + '</td>' +
            '<td>' + new Date(d.last_seen).toLocaleTimeString() + '</td>' +
          '</tr>';
        });
        document.getElementById("devicesBody").innerHTML = devRows;
      } else {
        document.getElementById("devicesBody").innerHTML = 
          '<tr><td colspan="7" style="text-align:center; color:#64748b; padding:20px;">Nenhum dispositivo conectado no momento.</td></tr>';
      }
      document.getElementById("activeDevices").innerText = activeDevCount;

      // 2. Tabela Opcional de Streams HTTP Detalhadas
      if (data.history && data.history.length > 0) {
        let rawRows = "";
        data.history.forEach(h => {
          const statusTag = h.active 
            ? '<span class="live-tag">● Conectado</span>' 
            : '<span style="color:#64748b">Encerrado</span>';
          const dur = h.active ? '-' : (h.duration || '-');
          const traf = formatBytes((h.bytes_in || 0) + (h.bytes_out || 0));
          const peerShort = h.peer_id.slice(0, 10) + "..." + h.peer_id.slice(-6);
          const isV6 = h.remote.includes("/ip6/");
          const ipBadge = isV6 
            ? '<span class="badge" style="background:#581c87; color:#e9d5ff; font-size:10px;">IPv6</span>' 
            : '<span class="badge" style="background:#1e3a8a; color:#bfdbfe; font-size:10px;">IPv4</span>';

          rawRows += '<tr>' +
            '<td>' + statusTag + '</td>' +
            '<td style="font-family:monospace;" title="' + h.peer_id + '">' + peerShort + '</td>' +
            '<td style="font-size:11px;">' + ipBadge + ' ' + h.remote + '</td>' +
            '<td>' + new Date(h.start_time).toLocaleTimeString() + '</td>' +
            '<td>' + dur + '</td>' +
            '<td>' + traf + '</td>' +
          '</tr>';
        });
        document.getElementById("rawBody").innerHTML = rawRows;
      }
    }).catch(() => {});
}
setInterval(updateStats, 2000);
updateStats();
</script>
</body>
</html>`, AppVersion, AppDeveloper, AppVersion, AppDeveloper, pskBadge, directDownloadUrl, directDownloadUrl, directDownloadUrl, hostID)
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

	secretPSK := readOptionsPSK()
	pskEnabled := secretPSK != ""

	var opts []libp2p.Option
	opts = append(opts,
		libp2p.Identity(priv),
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
			"/ip6/::/tcp/0",
			"/ip6/::/udp/0/quic-v1",
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

	startIngressServer(*ingressPort, peerIDStr, secretPSK)

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

	// Handler de relatórios de diagnóstico enviados pelo Celular
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

	// Handler do tráfego do túnel P2P
	host.SetStreamHandler(ProtocolID, func(s network.Stream) {
		defer s.Close()

		remotePeer := s.Conn().RemotePeer().String()
		remoteAddr := s.Conn().RemoteMultiaddr().String()

		// 1. Verificação de Chave PSK (Stream Auth)
		if pskEnabled {
			s.SetReadDeadline(time.Now().Add(5 * time.Second))
			tokenBuf := make([]byte, 32)
			_, err := io.ReadFull(s, tokenBuf)
			s.SetReadDeadline(time.Time{})

			expected := computePSKToken(secretPSK)
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

		// Registra e agrupa no Dispositivo
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

	fmt.Println("==========================================================================")
	fmt.Printf("        HA P2P TUNNEL v%s (Beta) - Desenvolvido por %s        \n", AppVersion, AppDeveloper)
	fmt.Println("==========================================================================")
	fmt.Printf(" Data/Hora de Início: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf(" PEER ID DO SEU HOME ASSISTANT:\n   >>> %s <<<\n", peerIDStr)
	fmt.Printf(" Painel Web / Ingress: Ativo na porta %s (Acesse pelo Home Assistant)\n", *ingressPort)
	if pskEnabled {
		fmt.Println(" Rede Privada (PSK): [ATIVADA] - Autenticação por Stream ativa.")
	} else {
		fmt.Println(" Rede Privada (PSK): [DESATIVADA] - Qualquer cliente com seu PeerID pode conectar.")
	}
	fmt.Println(" Protocolos: IPv4 + IPv6 | QUIC + TCP | UPnP NAT-PMP Ativo")
	fmt.Println("==========================================================================")

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
