# 🏠 Jhonatan's Home Assistant Add-ons Repository

[![Adicionar Repositório no Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fjhona1234-del%2FHA-P2P-Tunnel)
[![Home Assistant Add-on](https://img.shields.io/badge/Home%20Assistant-Add--ons-blue.svg)](https://www.home-assistant.io/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Repositório oficial com complementos de conectividade e inteligência artificial para Home Assistant.

---

## ⚡ Instalação em 1 Clique

Clique no botão abaixo para adicionar este repositório diretamente ao seu Home Assistant:

[![Adicionar Repositório no Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fjhona1234-del%2FHA-P2P-Tunnel)

*(Ou adicione manualmente em **Configurações > Add-ons > Loja de Add-ons > Repositórios**: `https://github.com/jhona1234-del/HA-P2P-Tunnel`)*

---

## 📦 Complementos Inclusos

### 1. 🌐 HA P2P Tunnel (v0.4.5)
* **Acesso Remoto Ultrarrápido via libp2p (QUIC / TCP)**: Conexão direta entre o app Android e seu Home Assistant, atravessando CGNAT sem abrir portas no roteador.
* **Túnel HTTPS 443 (Anti-Firewall)**: Conexão para iOS, Windows e redes corporativas via Cloudflare.
* **Entidades Nativas no Home Assistant**: Publica automaticamente `sensor.ha_p2p_tunnel_status`, `sensor.ha_p2p_tunnel_https_url`, `sensor.ha_p2p_tunnel_peer_id` e `sensor.ha_p2p_tunnel_psk`.
* **100% Focado em Conectividade**: Código otimizado, sem IA ou Telegram, consumo mínimo de CPU e memória.

### 2. 🤖 IABoT (v1.4.2)
* **Assistente IA com Telegram Bot**: Respostas contextuais com conhecimento do estado em tempo real da casa.
* **Fila de Fallback Automática Instantânea**:
  1. `gemini-3.5-flash-lite` (Padrão / Ultrarrápido e Maior Limite)
  2. `gemini-3.1-flash-lite` (Fallback 1)
  3. `gemini-3.6-flash` (Fallback 2 / Raciocínio Avançado)
  *(Nunca utiliza versões Flash Lite inferiores a 3.1)*
* **Canal de Notificações para Automações**: Envio de mensagens personalizadas via REST Command e cards Mushroom prontos para usar.
* **Controle de Acesso por Aprovação**: Estranhos não conversam; o Administrador recebe alerta com botões `[✅ Autorizar]` e `[❌ Bloquear]`.
* **Disparo de Mensagens via HA**: Endpoint REST `POST /api/send_telegram` (porta 8099) compatível com `rest_command.herbie_telegram`.

---

## 📄 Licença
Distribuído sob licença MIT.
