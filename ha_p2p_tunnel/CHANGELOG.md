# Changelog

Todas as mudanças notáveis deste Add-on estão documentadas neste arquivo.
O formato é baseado em [Keep a Changelog](https://keepachangelog.com/pt-BR/1.0.0/) e este projeto segue [Versionamento Semântico](https://semver.org/lang/pt-BR/).

---

## [0.4.4] - 2026-11-11

### ✨ Entidades Dedicadas no HA & Download do APK via GitHub
- **Entidades Dedicadas no Home Assistant:**
  - `sensor.ha_p2p_tunnel_status`: Status principal (`Online`/`Offline`) com todos os atributos da rede.
  - `sensor.ha_p2p_tunnel_https_url`: URL HTTPS 443 ativa, ideal para automações que disparam notificações no Telegram quando a URL for gerada ou alterada.
  - `sensor.ha_p2p_tunnel_peer_id`: Peer ID puro para pareamento e integrações.
  - `sensor.ha_p2p_tunnel_psk`: Status e chave da rede privada.
- **Resolução de Rede Supervisor:** Conexão multi-endpoint (`http://supervisor/core/api`, `http://172.30.32.2/core/api` e `http://127.0.0.1:8123/api`) garantindo que as entidades publiquem mesmo sob `host_network: true`.
- **Download do APK Direto via GitHub & QR Code:** QR Code e botão de download apontando diretamente para o repositório oficial no GitHub, permitindo baixar em qualquer rede (LAN ou 4G/5G).
- **Notificação Persistente Nativa:** Gera automaticamente uma notificação no painel do Home Assistant assim que a rota HTTPS fica online.

## [0.4.2] - 2026-11-10

### 🚀 Separação de Arquitetura & Otimização
- **Foco 100% em Conectividade P2P:** A inteligência artificial e o bot do Telegram foram migrados para o seu próprio complemento dedicado (**IABoT**), tornando o `HA P2P Tunnel` ultra-leve, hiper-rápido e focado exclusivamente na superação de CGNAT e acesso remoto 443.
- **Ícones e Logo com Transparência RGBA:** Remoção dos cantos brancos do `icon.png` e `logo.png`, garantindo integração estética perfeita com o tema escuro do Home Assistant.
- **Porta Web Reorganizada (8098):** Painel Ingress e download direto do APK configurados para a porta 8098, liberando a porta 8099 para o IABoT.
- **Sincronização Direta de Sensor Supervisor:** Publicação automática de `sensor.ha_p2p_tunnel_status` com atributos `peer_id`, `https_url`, `active_streams` e `total_streams`.
- **Card Customizado Integrado:** Suporte ao card `ha-p2p-tunnel-card.js` com popup modal de links e botão de cópia.

---

## [0.3.5] - 2026-11-09

### Adicionado
- **Sincronização de Entidades no Home Assistant:**
  - `sensor.ha_p2p_tunnel_status` (estado geral do túnel).
  - `binary_sensor.ha_p2p_tunnel` (conectividade com device_class).
  - `sensor.ha_p2p_tunnel_url` (link direto seguro com PSK).
  - `sensor.ha_p2p_tunnel_peer_id` (identificador P2P persistente).
- Notificações nativas no Home Assistant via `persistent_notification` e `notify.notify`.
- QR Code e download direto do APK com redirecionamento para o repositório global.

---

## [0.3.4] - 2026-11-08

### Adicionado
- Notificações automáticas no Telegram quando o túnel inicia e quando a URL HTTPS (Cloudflare) é gerada ou alterada, enviando link direto com token PSK.
- Atualização proativa de entidades no Home Assistant via Supervisor API.
- Persistência garantida de Peer ID do servidor.

---

## [0.3.3] - 2026-11-03

### Adicionado
- Endpoint `/api/stats` expondo peer_id (token P2P), https_url e streams ativas para integração nativa no dashboard do Home Assistant.
- Suporte a monitoramento dinâmico de status e cópia rápida de tokens.

---

## [0.3.2] - 2026-10-30

### 🛡️ Correção do Erro "400: Bad Request" no Home Assistant
- **Sanitização Completa de Cabeçalhos de Proxy (`cleanTransport`):** O proxy reverso remove cabeçalhos como `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host` e `CF-Connecting-IP`, reescrevendo `Host` e `Origin` diretamente para `127.0.0.1:8123`.
- **Acesso Local Transparente:** Conexões externas tratadas como locais, eliminando a exigência de `trusted_proxies` manual no `configuration.yaml`.
- **Normalização do Host no Cloudflared:** Adicionada proteção contra vazamento do cabeçalho Host externo.

---

## [0.3.1] - 2026-10-30

### Adicionado
- Download direto do histórico de logs do servidor pelo painel Ingress.
- Resolução dinâmica de rotas e renderização prévia no servidor.
