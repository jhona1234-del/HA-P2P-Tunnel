# 🤖 IABoT - Home Assistant AI Assistant

[![Adicionar Repositório no Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fjhona1234-del%2Fiabot)
[![Home Assistant Add-on](https://img.shields.io/badge/Home%20Assistant-Add--on-blue.svg)](https://www.home-assistant.io/)
[![Version](https://img.shields.io/badge/version-1.0.0-green.svg)](https://github.com/jhona1234-del/iabot)

Assistente inteligente autônomo com Bot do Telegram e Google Gemini AI para Home Assistant.

---

## ⚡ Como Instalar no Home Assistant (1 Clique)

### Opção 1: Pelo botão My Home Assistant
Clique no botão abaixo para adicionar este repositório diretamente ao seu Home Assistant:

[![Adicionar Repositório no Home Assistant](https://my.home-assistant.io/badges/supervisor_add_addon_repository.svg)](https://my.home-assistant.io/redirect/supervisor_add_addon_repository/?repository_url=https%3A%2F%2Fgithub.com%2Fjhona1234-del%2Fiabot)

### Opção 2: Manualmente
1. No seu Home Assistant, vá em **Configurações > Add-ons > Loja de Add-ons** (Add-on Store).
2. No canto superior direito, clique nos **3 pontinhos verticais (⋮) > Repositórios**.
3. Adicione a URL do repositório:
   ```text
   https://github.com/jhona1234-del/iabot
   ```
4. Clique em **Adicionar** e depois em **Fechar**.
5. Procure por **IABoT** na lista, clique em **Instalar** e depois em **Iniciar**.

---

## 🚀 Recursos
- **Telegram Bot nativo em Go**: Polling direto e ultra-leve sem bibliotecas pesadas de Python.
- **Google Gemini AI Integrado**: Suporte a `gemini-2.5-flash` com respostas rápidas e contexto da conversa.
- **Controle de Acesso por Aprovação**: Estranhos não conseguem conversar; o Administrador recebe alerta no Telegram com botões interativos `[✅ Autorizar]` e `[❌ Bloquear]`.
- **API REST de Notificações**: Envie mensagens para contatos específicos (`Jhonatan`, `Claudia`, `todos`) via `POST /api/send_telegram` (porta 8099).
- **Painel Ingress Integrado**: Passo a passo de configuração com @BotFather, teste de mensagens e gerador de YAML.

## Licença
Todos os direitos reservados.
