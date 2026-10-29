# 🚇 HA P2P Tunnel

**Acesso remoto P2P direto ao Home Assistant pelo Android — sem abrir portas, sem IP público fixo, sem domínio e sem mensalidades.**

O **HA P2P Tunnel** cria um túnel ponto-a-ponto (P2P) criptografado utilizando a pilha `go-libp2p` (QUIC/TCP + Kademlia DHT + Hole Punching + AutoRelay). Ele permite atravessar redes com **CGNAT** nas duas pontas (tanto na internet residencial quanto no 4G/5G do celular), conectando o aplicativo Android diretamente à instância local do Home Assistant (`127.0.0.1:8123`).

---

## ✨ Funcionalidades Principais

* **Zero Configuração de Rede (Fura CGNAT):** Utiliza *UDP/QUIC Hole Punching*, *UPnP/NAT-PMP* e *AutoRelay* com descoberta via DHT global.
* **Painel Web Ingress Nativo (Porta 8099):** Interface integrada ao menu lateral do Home Assistant (`mdi:tunnel`).
* **Download Direto do APK:** O aplicativo Android (`ha-p2p-tunnel.apk`) fica hospedado dentro do próprio Add-on e pode ser baixado na rede local via botão ou QR Code.
* **Pareamento Instantâneo via QR Code:** Gera automaticamente o QR Code contendo o `PeerID` e a chave `PSK` (`hap2p://connect?peer=...&psk=...`) para leitura direta no aplicativo.
* **Segurança de Rede Privada (PSK):** Suporte opcional a chave pré-compartilhada (`secret_psk` derivada com SHA-256), garantindo que apenas clientes com o seu segredo consigam estabelecer *streams* com o seu nó.
* **Monitoramento em Tempo Real:** Painel com *status* do túnel, *uptime*, conexões ativas no momento, histórico das últimas 30 conexões e contagem de *bytes* trafegados (enviados/recebidos).
* **Multi-arquitetura:** Compatível com `aarch64`, `armv7` e `amd64`.

---

## 📦 Estrutura do Repositório

```text
├── config.yaml         # Manifesto do Add-on (Ingress, permissões e schema)
├── Dockerfile          # Build multi-stage em Go + Alpine 3.20 e inclusão do APK
├── go.mod / go.sum     # Dependências Go (go-libp2p, kad-dht, go-qrcode)
├── main.go             # Servidor P2P, Proxy TCP 8123 e Servidor Web Ingress
├── run.sh              # Script de inicialização e persistência da chave (/data/peer.key)
└── ha-p2p-tunnel.apk   # Aplicativo Android cliente embutido para download
