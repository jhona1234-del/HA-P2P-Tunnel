package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/pnet"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/libp2p/go-libp2p/p2p/discovery/util"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	ProtocolID   = "/ha-p2p-tunnel/1.0.0"
	AppVersion   = "0.1.0"
	AppDeveloper = "Jho"
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

type TunnelStats struct {
	mu            sync.Mutex
	ActiveStreams int64
	TotalStreams  int64
	History       []*ConnectionEvent
	StartedAt     time.Time
}

var stats = &TunnelStats{
	StartedAt: time.Now(),
	History:   make([]*ConnectionEvent, 0),
}

func (s *TunnelStats) addEvent(e *ConnectionEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.History = append([]*ConnectionEvent{e}, s.History...)
	if len(s.History) > 30 {
		s.History = s.History[:30]
	}
}

func derivePSK(secret string) pnet.PSK {
	if secret == "" {
		return nil
	}
	h := sha256.Sum256([]byte("ha-p2p-psk-v1:" + secret))
	return h[:]
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
		// Prioridade 1: Rede local padrão (192.168.x.x ou 10.x.x.x)
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
		// Prioridade 2: Qualquer IPv4 que não seja bridge Docker (172.17 / 172.30)
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

	// Handler flexível para servir o arquivo APK
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

	// QR Code para baixar o APK no celular usando o IP real da LAN
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

	// QR Code para conectar o PeerID (com ou sem PSK)
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

	// API de estatísticas ao vivo
	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		stats.mu.Lock()
		defer stats.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"active_streams": atomic.LoadInt64(&stats.ActiveStreams),
			"total_streams":  atomic.LoadInt64(&stats.TotalStreams),
			"uptime":         time.Since(stats.StartedAt).Round(time.Second).String(),
			"history":        stats.History,
		})
	})

	// Página Web Ingress / Painel de Controle
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		pskBadge := `<span class="badge badge-off">Desativada</span>`
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
  .container { max-width: 920px; margin: 0 auto; }
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
  .btn { width: 100%%%%; background: #0284c7; color: white; border: none; padding: 12px; border-radius: 8px; font-weight: 700; font-size: 14px; cursor: pointer; text-decoration: none; text-align: center; transition: 0.2s; display: inline-block; }
  .btn:hover { background: #0369a1; }
  .btn-green { background: #16a34a; }
  .btn-green:hover { background: #15803d; }
  .link-direct { margin-top: 10px; font-size: 12px; color: #94a3b8; word-break: break-all; }
  .link-direct a { color: #38bdf8; text-decoration: underline; }
  .peer-id-box { background: #0b1120; padding: 10px; border-radius: 8px; border: 1px solid var(--border); word-break: break-all; font-family: monospace; font-size: 12px; color: #38bdf8; margin-bottom: 10px; width: 100%%%%; }
  .stat-row { display: flex; justify-content: space-between; padding: 10px 0; border-bottom: 1px solid var(--border); font-size: 14px; }
  .stat-row:last-child { border-bottom: none; }
  .stat-value { font-weight: 700; color: #fff; }
  .badge { padding: 4px 10px; border-radius: 20px; font-size: 12px; font-weight: 600; }
  .badge-on { background: #15803d; color: #dcfce7; }
  .badge-off { background: #475569; color: #f1f5f9; }
  .table-box { background: var(--card); border: 1px solid var(--border); border-radius: 14px; padding: 20px; overflow-x: auto; margin-top: 20px; }
  table { width: 100%%%%; border-collapse: collapse; font-size: 13px; text-align: left; }
  th { color: #94a3b8; padding: 10px; border-bottom: 1px solid var(--border); }
  td { padding: 10px; border-bottom: 1px solid #1e293b; color: #e2e8f0; }
  tr:last-child td { border-bottom: none; }
  .live-tag { color: var(--success); font-weight: 700; animation: blink 1.5s infinite; }
  @keyframes blink { 0%%%%,100%%%% { opacity: 1; } 50%%%% { opacity: 0.4; } }
</style>
</head>
<body>
<div class="container">
  <div class="header">
    <div class="title">
      <span class="status-dot"></span> HA P2P Tunnel 
      <span class="dev-tag">v%s (Beta) • Dev: <b>%s</b></span>
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

  <!-- Status Card -->
  <div class="card">
    <h2>Status da Conexão P2P</h2>
    <div class="stat-row"><span>Estado</span><span class="stat-value" style="color:var(--success)">● Escutando Conexões P2P</span></div>
    <div class="stat-row"><span>Streams Ativos Agora</span><span class="stat-value" id="activeStreams">0</span></div>
    <div class="stat-row"><span>Total de Conexões</span><span class="stat-value" id="totalStreams">0</span></div>
    <div class="stat-row"><span>Tempo em Execução</span><span class="stat-value" id="uptime">0s</span></div>
    <div class="stat-row"><span>IPv4 + IPv6</span><span class="stat-value" style="color:var(--success)">QUIC e TCP Habilitados</span></div>
    <div class="stat-row"><span>Hole Punching / UPnP</span><span class="stat-value" style="color:var(--success)">Ativo</span></div>
  </div>

  <!-- Registro de Conexões Recentes -->
  <div class="table-box">
    <h2 style="margin-top:0; color:#94a3b8; font-size:15px;">Registro de Conexões Recentes</h2>
    <table>
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
      <tbody id="historyBody">
        <tr><td colspan="6" style="text-align:center; color:#64748b; padding:20px;">Nenhuma conexão registrada ainda.</td></tr>
      </tbody>
    </table>
  </div>
</div>

<script>
function copyPeer() {
  const text = document.getElementById("peerText").innerText.trim();
  const btn = document.getElementById("btnCopyPeer");
  function onSuccess() {
    btn.innerText = "✓ Copiado!";
    btn.style.background = "#16a34a";
    setTimeout(() => {
      btn.innerText = " Copiar Peer ID";
      btn.style.background = "";
    }, 2500);
  }
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(text).then(onSuccess).catch(() => fallbackCopy(text, onSuccess));
  } else {
    fallbackCopy(text, onSuccess);
  }
}

function fallbackCopy(text, cb) {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.left = "-9999px";
  document.body.appendChild(ta);
  ta.focus();
  ta.select();
  try {
    const ok = document.execCommand("copy");
    if (ok) {
      cb();
    } else {
      prompt("Copie seu PeerID:", text);
    }
  } catch (e) {
    prompt("Copie seu PeerID:", text);
  }
  document.body.removeChild(ta);
}

function updateStats() {
  fetch("./api/stats")
    .then(r => r.json())
    .then(data => {
      document.getElementById("activeStreams").innerText = data.active_streams;
      document.getElementById("totalStreams").innerText = data.total_streams;
      document.getElementById("uptime").innerText = data.uptime;
      
      const tbody = document.getElementById("historyBody");
      if (data.history && data.history.length > 0) {
        tbody.innerHTML = data.history.map(ev => {
          const status = ev.active ? '<span class="live-tag">● Conectado</span>' : '<span style="color:#64748b">Encerrado</span>';
          const totalBytes = ((ev.bytes_in + ev.bytes_out) / 1024).toFixed(1) + " KB";
          const shortPeer = ev.peer_id.length > 18 ? ev.peer_id.slice(0, 10) + "..." + ev.peer_id.slice(-6) : ev.peer_id;
          const timeStr = new Date(ev.start_time).toLocaleTimeString();
          return "<tr><td>" + status + "</td><td title='" + ev.peer_id + "'><code>" + shortPeer + "</code></td><td><code>" + ev.remote + "</code></td><td>" + timeStr + "</td><td>" + ev.duration + "</td><td>" + totalBytes + "</td></tr>";
        }).join("");
      }
    }).catch(console.error);
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

	pskEnabled := false
	if secretPSK != "" {
		psk := derivePSK(secretPSK)
		opts = append(opts, libp2p.PrivateNetwork(psk))
		pskEnabled = true
	}

	host, err := libp2p.New(opts...)
	if err != nil {
		log.Fatalf("[ERRO FATAL] Falha ao iniciar libp2p: %v", err)
	}
	defer host.Close()

	peerIDStr := host.ID().String()

	// Inicia painel Web Ingress na porta 8099
	startIngressServer(*ingressPort, peerIDStr, secretPSK)

	// Conecta na DHT global
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

	fmt.Println("==========================================================================")
	fmt.Printf("        HA P2P TUNNEL v%s (Beta) - Desenvolvido por %s        \n", AppVersion, AppDeveloper)
	fmt.Println("==========================================================================")
	fmt.Printf(" Data/Hora de Início: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Printf(" PEER ID DO SEU HOME ASSISTANT:\n   >>> %s <<<\n", peerIDStr)
	fmt.Printf(" Painel Web / Ingress: Ativo na porta %s (Acesse pelo Home Assistant)\n", *ingressPort)
	if pskEnabled {
		fmt.Println(" Rede Privada (PSK): [ATIVADA] - Somente clientes com sua chave conectam.")
	} else {
		fmt.Println(" Rede Privada (PSK): [DESATIVADA] - Qualquer cliente com seu PeerID pode conectar.")
	}
	fmt.Println(" Protocolos: IPv4 + IPv6 | QUIC + TCP | UPnP NAT-PMP Ativo")
	fmt.Println("==========================================================================")

	// Stream handler
	host.SetStreamHandler(ProtocolID, func(s network.Stream) {
		defer s.Close()

		remotePeer := s.Conn().RemotePeer().String()
		remoteAddr := s.Conn().RemoteMultiaddr().String()
		activeNow := atomic.AddInt64(&stats.ActiveStreams, 1)
		atomic.AddInt64(&stats.TotalStreams, 1)

		startTime := time.Now()
		event := &ConnectionEvent{
			PeerID:    remotePeer,
			Remote:    remoteAddr,
			StartTime: startTime,
			Active:    true,
		}
		stats.addEvent(event)

		log.Printf("[+] [NOVA CONEXÃO P2P] Cliente conectado!")
		log.Printf("    PeerID: %s", remotePeer)
		log.Printf("    Origem: %s", remoteAddr)
		log.Printf("    Streams Ativos no Momento: %d", activeNow)

		haConn, err := net.Dial("tcp", "127.0.0.1:8123")
		if err != nil {
			log.Printf("[-] [ERRO] Falha ao encaminhar para Home Assistant local: %v", err)
			atomic.AddInt64(&stats.ActiveStreams, -1)
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
		remaining := atomic.AddInt64(&stats.ActiveStreams, -1)

		event.Active = false
		event.EndTime = time.Now()
		event.Duration = duration.String()
		event.BytesIn = bytesIn
		event.BytesOut = bytesOut

		log.Printf("[-] [CONEXÃO ENCERRADA] Cliente desconectado.")
		log.Printf("    PeerID: %s", remotePeer)
		log.Printf("    Duração: %v | Enviados ao HA: %.2f KB | Recebidos do HA: %.2f KB",
			duration, float64(bytesIn)/1024, float64(bytesOut)/1024)
		log.Printf("    Streams Ativos Restantes: %d", remaining)
	})

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
