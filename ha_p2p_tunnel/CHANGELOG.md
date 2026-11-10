# Changelog

Todas as mudanças notáveis deste Add-on estão documentadas neste arquivo.
O formato é baseado em [Keep a Changelog](https://keepachangelog.com/pt-BR/1.0.0/) e este projeto segue [Versionamento Semântico](https://semver.org/lang/pt-BR/).

---

## [0.4.0] - 2026-11-10

### 🚀 Novas Funcionalidades e Grande Atualização
- **Assistente IA e Bot Telegram Integrados:** O add-on agora incorpora nativamente um bot completo do Telegram com integração ao Google Gemini (usando fila de prioridade com fallback automático: `gemini-3.6-flash`, `gemini-3.5-flash-lite`, `gemini-3.1-flash-lite`, `gemini-2.5-flash`).
- **Painel de Configurações Dinâmico (Modal ⚙️):** Gerenciador de configurações diretamente no Ingress para ativação/desativação do Telegram e IA, configuração de tokens, senhas e nomes sem necessidade de reiniciar o container.
- **Assistente de Primeira Viagem (Wizard):** Ao abrir o Ingress pela primeira vez, o assistente pergunta se o usuário deseja ativar Telegram e IA, guiando a configuração do bot e sugerindo os modelos Gemini padrão.
- **Notificações do Home Assistant Sem Edição de YAML:**
  - **Via Evento Nativo (Zero YAML):** Qualquer automação do Home Assistant pode disparar avisos no Telegram adicionando a ação `Disparar evento` com o tipo `p2p_telegram` e os dados `message: "Sua mensagem"`. O add-on ouve o evento via WebSocket oficial do Supervisor e encaminha imediatamente.
  - **Via Endpoint REST (Compatibilidade Total):** Endpoints `POST /api/send_telegram` e `POST /api/notify` na porta 8099 disponíveis para uso por `rest_command` e webhooks.
- **Registro Automático de Administrador:** Ao enviar `/start` para o bot pela primeira vez, o Chat ID do usuário é automaticamente reconhecido e salvo como administrador oficial.
- **Comandos Interativos do Bot:**
  - `/status` — Consulta dispositivos da casa (luzes/interruptores ligados) e status do túnel em tempo real.
  - `/tunnel` — Exibe links diretos Cloudflare HTTPS e Peer ID persistente.
  - `/ajuda` — Orientações de interação e controle por IA.
  - Conversação em linguagem natural com controle de luzes e atuadores via chamada de serviço ao Home Assistant.
- **Segurança Reforçada (Zero-Leak Architecture):** Chaves de API, senhas PSK e tokens de Telegram residem estritamente no armazenamento persistente local (`/data/settings.json`), sem nenhum dado sensível em código ou histórico.

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
