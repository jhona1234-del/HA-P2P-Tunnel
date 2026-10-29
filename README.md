# 🚇 HA P2P Tunnel

[![Open your Home Assistant instance and show the add add-on repository dialog with a specific repository URL pre-filled.](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fjhona1234-del%2FHA_P2P_Tunnel)
[![Home Assistant Add-on](https://img.shields.io/badge/Home%20Assistant-Add--on-blue.svg)](https://www.home-assistant.io/)
[![Version](https://img.shields.io/badge/version-0.1.0--beta-green.svg)](https://github.com/jhona1234-del/HA_P2P_Tunnel/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**Acesso remoto P2P direto ao Home Assistant pelo Android — sem abrir portas no roteador, sem IP público fixo, sem DDNS, sem Cloudflare Tunnels e sem custos mensais.**

O **HA P2P Tunnel** cria um túnel ponto-a-ponto (P2P) criptografado utilizando a pilha `go-libp2p` (QUIC/TCP + Kademlia DHT + Hole Punching + AutoRelay). Ele atravessa redes com **CGNAT** nas duas pontas (tanto na internet de casa quanto no 4G/5G do celular), conectando seu aplicativo Android diretamente ao Home Assistant (`127.0.0.1:8123`).

---

## ⚡ Como Instalar no Home Assistant (1 Clique)

### Opção 1: Pelo botão My Home Assistant
Clique no botão abaixo para adicionar este repositório diretamente ao seu Home Assistant:

[![Adicionar Repositório no Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fjhona1234-del%2FHA_P2P_Tunnel)

### Opção 2: Manualmente
1. No seu Home Assistant, vá em **Configurações > Add-ons > Loja de Add-ons** (Add-on Store).
2. No canto superior direito, clique nos **3 pontinhos verticais (⋮) > Repositórios**.
3. Adicione a URL do repositório:
   ```text
   https://github.com/jhona1234-del/HA_P2P_Tunnel
   ```
4. Clique em **Adicionar** e depois em **Fechar**.
5. Procure por **HA P2P Tunnel** na lista, clique em **Instalar** e depois em **Iniciar**.

---

## 📱 Como Usar (Fluxo Sem Complicações)

1. **Abra o Painel:** No Home Assistant, clique em **"Abrir Interface Web"** (ou no atalho **HA P2P Tunnel** na barra lateral).
2. **Passo 1 (Baixar App):** Aponte a câmera do seu celular para o **QR Code 1** para baixar o instalador Android (`HA_P2P_Tunnel.apk`) diretamente pela rede local. Instale o APK no seu aparelho.
3. **Passo 2 (Conectar):** Abra o aplicativo no celular, toque no botão **"📷 ESCANEAR QR CODE DO HA"** e aponte para o **QR Code 2** exibido no monitor.
4. **Pronto!** Toque em **"INICIAR TÚNEL P2P"** e depois em **"ABRIR HOME ASSISTANT NO NAVEGADOR"**.

---

## ✨ Funcionalidades

* 🚀 **Zero Configuração de Portas (Fura CGNAT):** Usa *UDP/QUIC Hole Punching*, *UPnP/NAT-PMP* e *IPv6* com descoberta descentralizada via Kademlia DHT global.
* 🛡️ **Rede Privada Opcional (PSK):** Suporte a senha pré-compartilhada (`secret_psk` via SHA-256) que rejeita tentativas de conexão no nível criptográfico antes mesmo de chegar ao HA.
* 📦 **APK Embutido:** O próprio Add-on hospeda o instalador Android compilado, permitindo download direto em qualquer rede.
* 📷 **Leitor de QR Code Integrado:** O app Android possui leitor de QR embutido com câmera para pareamento instantâneo.
* 📊 **Painel Web Ingress:** Monitoramento em tempo real com contador de conexões ativas, histórico de clientes, endereços remotos e tráfego de dados.
* 🔋 **Foreground Service Android:** Mantém o túnel ativo em segundo plano mesmo se a tela apagar ou se o celular alternar entre Wi-Fi e 4G/5G.

---

## 📂 Estrutura do Projeto

```text
├── repository.yaml         # Manifesto do repositório oficial do Home Assistant
├── README.md               # Esta documentação
├── LICENSE                 # Licença MIT
│
├── ha_p2p_tunnel/          # Add-on oficial do Home Assistant
│   ├── config.yaml         # Metadados, permissões, Ingress e portas do Add-on
│   ├── Dockerfile          # Compilação multi-stage em Go + Alpine 3.20
│   ├── main.go             # Motor P2P, Ingress Web UI, servidor de APK e logs
│   ├── run.sh              # Inicialização e persistência de chaves
│   ├── go.mod / go.sum     # Módulos libp2p, kad-dht e go-qrcode
│   ├── ha-p2p-tunnel.apk   # Binário do APK pronto para download
│   └── DOCS.md             # Documentação interna exibida no painel do HA
│
└── android-app/            # Código-fonte completo do App Android (Kotlin + Gradle)
    ├── app/src/main/
    │   ├── java/com/hap2p/tunnel/
    │   │   ├── MainActivity.kt     # Interface com leitor de QR e logs em tempo real
    │   │   ├── TunnelService.kt    # Foreground Service persistente com WakeLock
    │   │   └── BootReceiver.kt     # Início automático no boot
    │   └── jniLibs/arm64-v8a/
    │       └── libtunnel.so        # Motor do túnel Go compilado para ARM64
```

---

## 🔒 Segurança

* Todas as conexões utilizam criptografia de ponta a ponta nativa do protocolo **QUIC** (TLS 1.3) gerenciada pela pilha `libp2p`.
* O Add-on expõe localmente apenas a porta interna para tráfego local; nenhuma porta pública externa precisa ser aberta no roteador.
* Se configurada a chave `PSK`, nenhum nó consegue sequer realizar o handshake inicial se não possuir a senha de rede.

---

## 👨‍💻 Autor

Criado por **Jhona1234** ([@jhona1234-del](https://github.com/jhona1234-del))  
Licenciado sob a [Licença MIT](LICENSE).
