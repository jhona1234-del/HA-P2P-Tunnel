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

### 1. 🌐 HA P2P Tunnel (v0.5.0)
* **Acesso Remoto Ultrarrápido via libp2p (QUIC / TCP)**: Conexão direta entre o app Android e seu Home Assistant, atravessando CGNAT sem abrir portas no roteador.
* **Túnel HTTPS 443 (Anti-Firewall)**: Conexão para iOS, Windows e redes corporativas via Cloudflare.
* **100% Focado em Conectividade**: Código otimizado, sem IA ou Telegram, consumo mínimo de CPU e memória.

### 2. 🤖 IABoT (v1.1.0)
* **Assistente IA com Telegram Bot**: Respostas contextuais usando os modelos mais modernos do Google Gemini.
* **Fila de Fallback Automática de Alta Disponibilidade**:
  1. `gemini-3.6-flash` (Principal / Mais Inteligente)
  2. `gemini-3.5-flash-lite` (Fallback 1 / Ultrarrápido)
  3. `gemini-3.1-flash-lite` (Fallback 2 / Econômico)
  *(Nunca utiliza versões Flash Lite inferiores a 3.1)*
* **Controle de Acesso por Aprovação**: Estranhos não conversam; o Administrador recebe alerta com botões `[✅ Autorizar]` e `[❌ Bloquear]`.
* **Disparo de Mensagens via HA**: Endpoint REST `POST /api/send_telegram` (porta 8099) compatível com `rest_command.herbie_telegram`.

---

## 📄 Licença
Distribuído sob licença MIT.
