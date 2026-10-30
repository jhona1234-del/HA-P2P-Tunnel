# Changelog

Todas as mudanças notáveis deste Add-on serão documentadas neste arquivo.

O formato é baseado em [Keep a Changelog](https://keepachangelog.com/pt-BR/1.0.0/) e este projeto segue [Versionamento Semântico](https://semver.org/lang/pt-BR/).

---

## [0.3.2] - 2026-10-30

### 🛡️ Correção do Erro "400: Bad Request" no Home Assistant
- **Sanitização Completa de Cabeçalhos de Proxy (`cleanTransport`):** O Home Assistant bloqueia nativamente qualquer requisição HTTP ou WebSocket que contenha cabeçalhos como `X-Forwarded-For`, `X-Forwarded-Proto`, `X-Forwarded-Host` ou `CF-Connecting-IP` vindos de proxies locais não declarados no `configuration.yaml` (`trusted_proxies`).
- **Acesso Local Transparente:** O proxy reverso agora atua em nível de transporte removendo todos os cabeçalhos de proxy e reescrevendo o `Host` e `Origin` diretamente para `127.0.0.1:8123`. Dessa forma, o Home Assistant trata todas as conexões como acesso local direto sem exigir nenhuma configuração manual em arquivos YAML.
- **Normalização do Host no Cloudflared:** Adicionada a flag `--http-host-header 127.0.0.1:8124` no processo do `cloudflared` para prevenir vazamento do cabeçalho Host externo.
- **Reescrita de Redirecionamentos:** Tratamento automático de cabeçalhos `Location` em respostas 301/302 para manter a navegação no domínio seguro.

---

## [0.3.1] - 2026-10-30

### 📥 Download Direto de Logs no Add-on
- **Novo Botão "Baixar Logs do Add-on (.txt)":** Adicionado botão no cabeçalho do painel Ingress que permite baixar o histórico completo de logs do servidor (`ha_p2p_addon_logs_...txt`) com 1 clique diretamente pelo navegador, sem precisar ir nas configurações do Supervisor.
- **Buffer de Logs em Memória:** Captura de todas as saídas do servidor, eventos de conexão libp2p, inicialização do `cloudflared` e relatórios de clientes para download instantâneo.

### 🌐 Resolução Ingress e Acesso HTTPS no App
- **Resolução Dinâmica de Endpoints:** O painel Ingress agora calcula as rotas relativas (`getEndpoint`) respeitando o caminho do iframe do Home Assistant (com ou sem barra final), garantindo que o link HTTPS gerado seja atualizado na tela sem falhas.
- **Renderização Inicial no Servidor:** O link HTTPS ativo agora é injetado diretamente pelo servidor no carregamento da página, eliminando o atraso de inicialização na interface.
- **Botão HTTPS no App Android:** O QR Code agora transmite o link HTTPS completo autenticado para o app Android. Caso o usuário esteja em uma rede corporativa com bloqueio total de P2P/UDP, um botão destacado **"🌐 Acessar via HTTPS 443"** é exibido para abrir o Home Assistant diretamente pelo navegador.

---

## [0.3.0] - 2026-10-30

### 🌐 Modo Híbrido: Acesso HTTPS Porta 443 (Fura-Firewall)
- **Túnel Outbound HTTPS Porta 443 via Cloudflare:** Integração com Cloudflare Quick Tunnel (`trycloudflare.com`), permitindo acesso direto em redes corporativas e empresariais restritas onde portas UDP ou conexões P2P arbitrárias são bloqueadas por firewall ou proxy corporativo.
- **Porteiro Local com PSK e Cookie Seguro:** Proteção de acesso com tela de login web exigindo a chave `secret_psk` (ou liberação automática com link `?psk=...`), gravando sessão autenticada em cookie `HttpOnly` seguro por 30 dias.
- **Compatibilidade Nativa com Home Assistant:** Proxy reverso HTTP e WebSockets configurado para não disparar erros de `trusted_proxies` ou `400 Bad Request` no Home Assistant.
- **Painel Ingress Híbrido:** Novo card no topo exibindo o link HTTPS ativo em tempo real, botão para copiar o link autenticado e botão para abrir com 1 clique.
- **Zero Configuração de Domínio:** Funciona imediatamente sem necessidade de criar conta no Cloudflare ou configurar DNS/DuckDNS.

## [0.2.1] - 2026-10-29

### 🚀 Melhorias de Velocidade e Conexão Instantânea
- **Conexão Direta IPv6 / QUIC:** Conexão agora é praticamente instantânea (50 a 150 ms) graças ao aproveitamento nativo do IPv6 de operadoras móveis e banda larga.
- **Porta P2P Fixa `41820`:** Substituída a porta dinâmica aleatória (`0`) pela porta fixa `41820` (TCP e QUIC/UDP). Com isso, o endereço do seu Home Assistant nunca muda entre reinicializações e o cache do celular não se torna obsoleto.
- **QR Code Inteligente com Multiaddrs:** O QR Code de pareamento agora injeta automaticamente os endereços diretos do servidor (`hap2p://connect?peer=...&addrs=...`). O app Android conecta de primeira em menos de 200ms sem precisar aguardar a varredura global da DHT.
- **Busca DHT Concorrente e Paralela:** Caso os endereços mudem, a busca no Kademlia DHT e o Rendezvous Discovery agora rodam simultaneamente em paralelo com tempos de resposta de 5-6 segundos.

### 📱 Aplicativo Android
- **Fim do Horário Duplicado nos Logs:** Corrigido o carimbo de data/hora nos logs do celular através da desativação do prefixo UTC padrão do Go (`log.SetFlags(0)`).
- **Badge Visual de Versão:** Exibição clara da versão `v0.2.1` e créditos do desenvolvedor: `Por Jhonatan Garcia (jhona1234)`.
- **Prevenção de Cache de Download:** Adicionados cabeçalhos HTTP `Cache-Control: no-cache, no-store, must-revalidate` no endpoint de download do APK para evitar que o navegador baixe versões antigas em cache.
- **Launcher Integrado com Home Assistant Companion:** A notificação fixa e a tela principal detectam se o app oficial `io.homeassistant.companion.android` está instalado e o abrem automaticamente, com fallback para o navegador.

### 🖥️ Painel Ingress do Add-on
- **Agrupamento Inteligente por Dispositivo (`DeviceSession`):** Fim da duplicação de linhas! As múltiplas requisições HTTP paralelas abertas pelo Lovelace/Companion são consolidadas em uma única linha elegante por aparelho com indicador de canais simultâneos.
- **Botões de Cópia com 1 Clique:**
  - 📋 `Copiar Logs (Técnico)`
  - 📄 `Copiar Resumo (Humano)`
  - 📥 `Baixar CSV`
- **Fallback de Clipboard no Iframe:** Garantida a cópia para a área de transferência mesmo dentro do iframe não seguro do Home Assistant.

---

## [0.2.0] - 2026-10-29

### 🔒 Segurança e Autenticação
- **Stream PSK Auth:** Sistema de autenticação por chave secreta (PSK) no nível de handshake do protocolo (`/ha-p2p-tunnel/1.0.0`), impedindo acessos não autorizados sem quebrar nós de bootstrap globais.
- **Mascaramento de Peer ID:** Proteção visual do Peer ID no app Android com botão de alternância (ícone de olho) e mascaramento automático nos registros de log (`12D3KooW...xxxxxx`).
- **Assinatura de Produção Oficial:** APK assinado com keystore oficial de produção no nome do desenvolvedor **Jhonatan Garcia (jhona1234)**, com validade até o ano 2054.
- **Ações na Notificação do Android:** Botões diretos para `ABRIR HA` e `DESCONECTAR` diretamente na barra de notificações sem precisar abrir o app.

---

## [0.1.0] - 2026-10-28

### 🎉 Lançamento Inicial
- Túnel P2P descentralizado para acesso remoto ao Home Assistant baseado na stack de rede `libp2p` (Go).
- Suporte a QUIC, TCP, Kademlia DHT e Hole Punching nativo.
- Funciona 100% sem abrir portas no roteador, sem CGNAT, sem DuckDNS e sem Cloudflare.
- Painel Web Ingress no Home Assistant com visualizador de métricas em tempo real.
- QR Code dinâmico para download direto do APK na rede local.
- Proxy reverso local no celular em `http://127.0.0.1:8123`.
