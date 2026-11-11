# 🤖 IABoT - Assistente IA Autônomo para Home Assistant

O **IABoT** é um complemento dedicado para Home Assistant que integra um Bot do Telegram com Inteligência Artificial (Google Gemini), controle de acesso restrito e integração nativa com automações do Home Assistant.

---

## 🚀 Funcionalidades Principais

1. **Bot do Telegram com IA Google Gemini**:
   - Respostas inteligentes, rápidas e com memória contextual da conversa.
   - Suporte aos modelos mais recentes do Gemini (ex: `gemini-2.5-flash`).
   - Prompt do sistema configurável para personalizar o comportamento do assistente (ex: Herbie).

2. **Segurança & Controle de Acesso (Zero Acesso Indevido)**:
   - **Administrador**: O primeiro usuário a interagir se torna o Administrador, ou pode ser definido pelo painel.
   - **Aprovação de Novos Usuários**: Se um estranho enviar mensagem, o bot NÃO executa comandos. Ele envia imediatamente um alerta ao Administrador no Telegram com botões interativos `[✅ Autorizar]` e `[❌ Bloquear]`, além de criar uma notificação no painel do Home Assistant.
   - **Gerenciamento Completo**: Autorize familiares (ex: Claudia) e defina apelidos para envio direto de mensagens.

3. **Notificações e Comandos via Home Assistant**:
   - Endpoint REST nativo em `http://127.0.0.1:8099/api/send_telegram`.
   - Compatível diretamente com `rest_command.herbie_telegram` e `rest_command.iabot_telegram`.
   - Envie alertas para um destinatário específico (`target: "Claudia"`, `target: "Jhonatan"`) ou para todos (`target: "todos"`).

4. **Painel Ingress Integrado**:
   - Guia visual passo a passo para criar o Bot com o `@BotFather` no Telegram.
   - Teste de notificação com 1 clique.
   - Gerador de snippets YAML para automações e cards do Lovelace.

---

## ⚙️ Configuração Inicial

### 1. Criar o Bot no Telegram via @BotFather
1. No Telegram, converse com [@BotFather](https://t.me/BotFather).
2. Envie o comando `/newbot`.
3. Escolha o nome de exibição (ex: `Herbie`).
4. Escolha o nome de usuário (ex: `meu_herbie_casa_bot`).
5. Copie o **HTTP API Token** gerado.
6. Cole no painel do **IABoT** na aba **Configurações** e clique em **Salvar**.

### 2. Obter a Chave do Google Gemini (Gratuita)
1. Acesse o [Google AI Studio](https://aistudio.google.com/).
2. Clique em **Get API Key** e crie uma nova chave.
3. Cole no campo **Chave API do Gemini** no painel do IABoT.

---

## 🏠 Integração com o Home Assistant

Adicione ao seu `configuration.yaml` (se ainda não tiver):

```yaml
rest_command:
  herbie_telegram:
    url: "http://127.0.0.1:8099/api/send_telegram"
    method: POST
    headers:
      content-type: "application/json"
    payload: '{"message": "{{ message }}", "target": "{{ target | default(''todos'') }}"}'

  iabot_telegram:
    url: "http://127.0.0.1:8099/api/send_telegram"
    method: POST
    headers:
      content-type: "application/json"
    payload: '{"message": "{{ message }}", "target": "{{ target | default(''todos'') }}"}'
```

### Exemplo de Automação (Luz do Thor):
```yaml
alias: 'Luz do Thor: Ligar e Desligar (Sincronizado)'
description: 'Obedece ao período da casa (Clima e Sol) e notifica no Telegram'
triggers:
  - trigger: state
    entity_id: input_select.periodo_cozinha
    to: dia
    id: ficou_claro
  - trigger: state
    entity_id: input_select.periodo_cozinha
    from: dia
    id: ficou_escuro
conditions: []
actions:
  - choose:
      - conditions:
          - condition: trigger
            id: ficou_claro
        sequence:
          - action: switch.turn_off
            target:
              entity_id: switch.ledthor
      - conditions:
          - condition: trigger
            id: ficou_escuro
        sequence:
          - action: switch.turn_on
            target:
              entity_id: switch.ledthor
          - action: notify.mobile_app_m2012k11ag
            data:
              message: Luz do Thor deve estar ligada agora
          - action: rest_command.herbie_telegram
            data:
              target: Jhonatan
              message: 💡 Luz do Thor foi ligada automaticamente (ficou escuro).
          - action: rest_command.herbie_telegram
            data:
              target: Claudia
              message: 🐶 A luz do cantinho do Thor acabou de acender!
            enabled: false
mode: single
```
