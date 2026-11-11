# 🤖 IABoT - Home Assistant AI Assistant (v1.3.0)

[![Adicionar Repositório no Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fjhona1234-del%2FHA-P2P-Tunnel)
[![Home Assistant Add-on](https://img.shields.io/badge/Home%20Assistant-Add--on-blue.svg)](https://www.home-assistant.io/)
[![Version](https://img.shields.io/badge/version-1.3.0-green.svg)](https://github.com/jhona1234-del/HA-P2P-Tunnel)

Assistente inteligente autônomo com Bot do Telegram, Google Gemini AI (3.5-flash-lite / 3.1-flash-lite), mensagens de voz (áudio TTS), controle de dispositivos e notificações para Home Assistant.

---

## ⚡ Como Instalar no Home Assistant (1 Clique)

### Opção 1: Pelo botão My Home Assistant
Clique no botão abaixo para adicionar o repositório multi-addon diretamente ao seu Home Assistant:

[![Adicionar Repositório no Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fjhona1234-del%2FHA-P2P-Tunnel)

### Opção 2: Manualmente
1. No seu Home Assistant, vá em **Configurações > Add-ons > Loja de Add-ons** (Add-on Store).
2. No canto superior direito, clique nos **3 pontinhos verticais (⋮) > Repositórios**.
3. Adicione a URL do repositório:
   ```text
   https://github.com/jhona1234-del/HA-P2P-Tunnel
   ```
4. Clique em **Adicionar** e depois em **Fechar**.
5. Procure por **IABoT** na lista, clique em **Instalar** (ou **Atualizar** para v1.3.0) e depois em **Iniciar**.

---

## 🚀 Recursos na v1.3.0

1. **Google Gemini AI com Fila de Fallback Silenciosa**:
   - Padrão primário: **`gemini-3.5-flash-lite`** (500 requisições/dia).
   - Fallback secundário: **`gemini-3.1-flash-lite`** (500 requisições/dia).
   - Opcional terciário: `gemini-3.6-flash`.
   - Se um modelo atingir cota momentânea, o bot chaveia instantaneamente para o próximo sem mensagens de erro na conversa.

2. **Mensagens de Áudio e Voz**:
   - **Entrada**: Ouça e compreenda notas de voz do Telegram enviando diretamente o áudio para a IA do Gemini.
   - **Saída**: Respostas em áudio de voz natural em Português do Brasil (via Google TTS nativo).
   - Três modos configuráveis no painel: `auto` (responde em áudio apenas quando recebe áudio), `always` (sempre áudio) ou `never` (apenas texto).

3. **Controle de Dispositivos e Status do HA**:
   - Comande diretamente pelo Telegram: *"Ligar o astronauta"*, *"Apagar ledthor"*, *"Ligar ar da sala"*.
   - Digite `/status` para listar todos os interruptores e lâmpadas acesos na casa.

4. **Controle de Acesso Rigoroso**:
   - Apenas o Administrador e usuários aprovados interagem.
   - Estranhos geram alertas com botões interativos `[✅ Autorizar]` e `[❌ Bloquear]`.

5. **Notificações REST para Automações**:
   - Endpoint `POST /api/send_telegram` (porta 8099). Compatível com `rest_command.herbie_telegram` e `rest_command.iabot_telegram`.

---

## Licença
Todos os direitos reservados.
