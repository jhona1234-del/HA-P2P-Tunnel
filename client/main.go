package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	ipfslog "github.com/ipfs/go-log/v2"
	"github.com/libp2p/go-libp2p"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/p2p/discovery/routing"
	"github.com/multiformats/go-multiaddr"
)

const (
	ProtocolID       = protocol.ID("/ha-p2p-tunnel/1.0.0")
	ProtocolIDReport = protocol.ID("/ha-p2p-tunnel-report/1.0.0")
)

func computePSKToken(secret string) [32]byte {
	return sha256.Sum256([]byte("ha-p2p-psk-auth-v2:" + secret))
}

func loadOrCreateIdentity(cacheDir string) (crypto.PrivKey, error) {
	if cacheDir == "" {
		priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
		return priv, err
	}
	keyPath := filepath.Join(cacheDir, "client_identity.key")
	if data, err := os.ReadFile(keyPath); err == nil {
		if priv, err := crypto.UnmarshalPrivateKey(data); err == nil {
			return priv, nil
		}
	}
	priv, _, err := crypto.GenerateEd25519Key(rand.Reader)
	if err != nil {
		return nil, err
	}
	if data, err := crypto.MarshalPrivateKey(priv); err == nil {
		_ = os.WriteFile(keyPath, data, 0600)
	}
	return priv, nil
}

func loadCachedAddrs(cacheDir string) []multiaddr.Multiaddr {
	if cacheDir == "" {
		return nil
	}
	cachePath := filepath.Join(cacheDir, "ha_addrs.json")
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil
	}
	var addrStrings []string
	if err := json.Unmarshal(data, &addrStrings); err != nil {
		return nil
	}
	var res []multiaddr.Multiaddr
	for _, s := range addrStrings {
		if ma, err := multiaddr.NewMultiaddr(s); err == nil {
			res = append(res, ma)
		}
	}
	return res
}

func saveCachedAddrs(cacheDir string, addrs []multiaddr.Multiaddr) {
	if cacheDir == "" || len(addrs) == 0 {
		return
	}
	cachePath := filepath.Join(cacheDir, "ha_addrs.json")
	var addrStrings []string
	for _, a := range addrs {
		addrStrings = append(addrStrings, a.String())
	}
	data, err := json.Marshal(addrStrings)
	if err == nil {
		_ = os.WriteFile(cachePath, data, 0600)
	}
}

func sendReport(h host.Host, targetPeerID peer.ID, cacheDir string, usedCache bool, durationMs int64) {
	if cacheDir == "" {
		return
	}
	reportFile := filepath.Join(cacheDir, "recent_logs.txt")
	var logs []string
	if data, err := os.ReadFile(reportFile); err == nil {
		lines := strings.Split(string(data), "\n")
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				logs = append(logs, l)
			}
		}
	}

	report := map[string]interface{}{
		"peer_id":             h.ID().String(),
		"device":              "Android",
		"connected_at":        time.Now().Format("2006-01-02 15:04:05"),
		"connect_duration_ms": durationMs,
		"used_cache":          usedCache,
		"logs":                logs,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	s, err := h.NewStream(ctx, targetPeerID, ProtocolIDReport)
	if err != nil {
		return
	}
	defer s.Close()
	_ = json.NewEncoder(s).Encode(report)
}

func main() {
	// Silencia mensagens de erro de rota do netlink no Android (SELinux)
	_ = ipfslog.SetLogLevel("basichost", "fatal")
	_ = ipfslog.SetLogLevel("autonat", "fatal")

	peerIDStr := flag.String("peer", "", "PeerID do Add-on do Home Assistant")
	pskStr := flag.String("psk", "", "Chave secreta PSK (opcional)")
	localPort := flag.String("port", "8123", "Porta local para abrir")
	cacheDir := flag.String("cache", "", "Diretório de cache para identidade e endereços")
	flag.Parse()

	if *peerIDStr == "" {
		log.Fatal("Uso: ./ha-client -peer <PEER_ID_DO_HOME_ASSISTANT> [-psk <SENHA>] [-cache <DIR>]")
	}

	targetPeerID, err := peer.Decode(*peerIDStr)
	if err != nil {
		log.Fatalf("PeerID inválido: %v", err)
	}

	ctx := context.Background()

	var privKey crypto.PrivKey
	if *cacheDir != "" {
		_ = os.MkdirAll(*cacheDir, 0755)
		privKey, _ = loadOrCreateIdentity(*cacheDir)
	}

	opts := []libp2p.Option{
		libp2p.ListenAddrStrings(
			"/ip4/0.0.0.0/tcp/0",
			"/ip4/0.0.0.0/udp/0/quic-v1",
			"/ip6/::/tcp/0",
			"/ip6/::/udp/0/quic-v1",
		),
		libp2p.NATPortMap(),
		libp2p.EnableHolePunching(),
		libp2p.EnableAutoRelayWithStaticRelays(dht.GetDefaultBootstrapPeerAddrInfos()),
	}

	if privKey != nil {
		opts = append(opts, libp2p.Identity(privKey))
	}

	if *pskStr != "" {
		log.Println("[P2P] Chave de Proteção (PSK) configurada para autenticação de Stream.")
	}

	host, err := libp2p.New(opts...)
	if err != nil {
		log.Fatalf("Erro ao iniciar libp2p: %v", err)
	}
	defer host.Close()

	log.Printf("[P2P] Nó local iniciado: %s", host.ID())

	kademliaDHT, err := dht.New(host, dht.Mode(dht.ModeClient))
	if err != nil {
		log.Fatalf("Erro ao criar DHT: %v", err)
	}
	_ = kademliaDHT.Bootstrap(ctx)

	connectBootstrap := func() {
		for _, addr := range dht.DefaultBootstrapPeers {
			pi, err := peer.AddrInfoFromP2pAddr(addr)
			if err == nil {
				go host.Connect(ctx, *pi)
			}
		}
	}
	connectBootstrap()

	listenAddr := "127.0.0.1:" + *localPort
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalf("Erro ao abrir porta %s: %v", listenAddr, err)
	}
	defer listener.Close()

	log.Println("==========================================================")
	log.Printf("Proxy local ativo em: http://%s", listenAddr)
	log.Printf("Buscando Home Assistant: %s", targetPeerID)
	log.Println("==========================================================")
	log.Printf("[HA_STATE:SEARCHING] Buscando nó do Home Assistant na rede P2P DHT...")

	connectStartTime := time.Now()
	usedCacheForConnection := false

	// 0. Reconexão rápida via Cache
	if *cacheDir != "" {
		cachedAddrs := loadCachedAddrs(*cacheDir)
		if len(cachedAddrs) > 0 {
			log.Printf("[P2P] Tentando reconexão imediata com %d endereços recentes em cache...", len(cachedAddrs))
			fastCtx, fastCancel := context.WithTimeout(ctx, 4*time.Second)
			if err := host.Connect(fastCtx, peer.AddrInfo{ID: targetPeerID, Addrs: cachedAddrs}); err == nil {
				usedCacheForConnection = true
				log.Printf("[HA_STATE:CONNECTED] Conectado instantaneamente ao Home Assistant via cache!")
			}
			fastCancel()
		}
	}

	// Monitor de conectividade contínuo
	go func() {
		var wasConnected bool = false
		for {
			time.Sleep(2 * time.Second)
			isConnected := host.Network().Connectedness(targetPeerID) == network.Connected
			if isConnected && !wasConnected {
				wasConnected = true
				durationMs := time.Since(connectStartTime).Milliseconds()
				log.Printf("[HA_STATE:CONNECTED] Home Assistant conectado e pronto!")

				if *cacheDir != "" {
					addrs := host.Peerstore().Addrs(targetPeerID)
					if len(addrs) > 0 {
						saveCachedAddrs(*cacheDir, addrs)
					}
				}

				// Envia relatório de diagnóstico para o Home Assistant
				go func(dur int64, c bool) {
					time.Sleep(1 * time.Second)
					sendReport(host, targetPeerID, *cacheDir, c, dur)
				}(durationMs, usedCacheForConnection)

			} else if !isConnected && wasConnected {
				wasConnected = false
				connectStartTime = time.Now()
				usedCacheForConnection = false
				log.Printf("[HA_STATE:SEARCHING] Conexão com Home Assistant perdida, buscando novamente...")
			}
		}
	}()

	// Loop de busca DHT resiliente
	routingDiscovery := routing.NewRoutingDiscovery(kademliaDHT)
	go func() {
		attempt := 0
		for {
			if host.Network().Connectedness(targetPeerID) == network.Connected {
				attempt = 0
				time.Sleep(6 * time.Second)
				continue
			}

			attempt++
			if len(host.Network().Peers()) < 3 {
				connectBootstrap()
			}

			// 1. Busca Kademlia DHT (timeout 10s)
			findCtx, findCancel := context.WithTimeout(ctx, 10*time.Second)
			peerInfo, err := kademliaDHT.FindPeer(findCtx, targetPeerID)
			findCancel()

			if err == nil && len(peerInfo.Addrs) > 0 {
				connectCtx, cCancel := context.WithTimeout(ctx, 5*time.Second)
				if err := host.Connect(connectCtx, peerInfo); err == nil {
					log.Printf("[HA_STATE:CONNECTED] Conectado diretamente ao Home Assistant!")
					if *cacheDir != "" {
						saveCachedAddrs(*cacheDir, peerInfo.Addrs)
					}
					cCancel()
					continue
				}
				cCancel()
			}

			// 2. Busca Rendezvous Discovery (timeout 8s)
			discCtx, discCancel := context.WithTimeout(ctx, 8*time.Second)
			peerChan, _ := routingDiscovery.FindPeers(discCtx, *peerIDStr)
			foundViaDisc := false
			for p := range peerChan {
				if p.ID == targetPeerID && len(p.Addrs) > 0 {
					connectCtx, cCancel := context.WithTimeout(ctx, 5*time.Second)
					if err := host.Connect(connectCtx, p); err == nil {
						log.Printf("[HA_STATE:CONNECTED] Conectado ao Home Assistant via Discovery!")
						if *cacheDir != "" {
							saveCachedAddrs(*cacheDir, p.Addrs)
						}
						foundViaDisc = true
						cCancel()
						break
					}
					cCancel()
				}
			}
			discCancel()

			if foundViaDisc {
				continue
			}

			if attempt < 2 {
				time.Sleep(2 * time.Second)
			} else {
				log.Printf("[HA_STATE:SEARCHING] Tentativa %d sem resposta. Atualizando rede e tentando novamente...", attempt)
				time.Sleep(6 * time.Second)
			}
		}
	}()

	for {
		clientConn, err := listener.Accept()
		if err != nil {
			continue
		}

		go func(conn net.Conn) {
			defer conn.Close()

			stream, err := host.NewStream(network.WithUseTransient(ctx, "relay"), targetPeerID, ProtocolID)
			if err != nil {
				log.Printf("[P2P] Erro ao abrir stream com HA: %v", err)
				return
			}
			defer stream.Close()

			// Autenticação por Chave PSK com o Add-on
			if *pskStr != "" {
				token := computePSKToken(*pskStr)
				_ = stream.SetWriteDeadline(time.Now().Add(5 * time.Second))
				if _, err := stream.Write(token[:]); err != nil {
					log.Printf("[ERRO PSK] Falha ao enviar chave PSK: %v", err)
					return
				}
				_ = stream.SetWriteDeadline(time.Time{})

				authResp := make([]byte, 1)
				_ = stream.SetReadDeadline(time.Now().Add(5 * time.Second))
				if _, err := io.ReadFull(stream, authResp); err != nil || authResp[0] != 0x01 {
					log.Printf("[SEGURANÇA] Conexão REJEITADA pelo Home Assistant: Chave PSK incorreta!")
					return
				}
				_ = stream.SetReadDeadline(time.Time{})
			}

			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); io.Copy(stream, conn) }()
			go func() { defer wg.Done(); io.Copy(conn, stream) }()
			wg.Wait()
		}(clientConn)
	}
}
