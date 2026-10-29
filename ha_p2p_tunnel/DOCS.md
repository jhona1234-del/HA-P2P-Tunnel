# Como Usar o HA P2P Tunnel

## 1. Passo a Passo Inicial

1. Inicie o Add-on no Home Assistant.
2. Clique no botão **"Abrir Interface Web"** (ou no menu lateral **HA P2P Tunnel**).
3. Na tela que abrir:
   - **Passo 1:** Aponte a câmera do seu celular para o QR Code da esquerda para baixar o aplicativo Android.
   - **Passo 2:** Abra o app no celular, clique em **"📷 Escanear QR Code do HA"** e aponte para o QR Code da direita.
4. Toque em **INICIAR TÚNEL P2P**.
5. Toque em **ABRIR HOME ASSISTANT NO NAVEGADOR**.

---

## 2. Rede Privada (PSK) - Segurança Opcional

Por padrão, qualquer pessoa que souber o seu `PeerID` poderia tentar negociar conexão com o Add-on. 

Para habilitar proteção com senha de rede:
1. Na aba **Configuração** do Add-on, preencha o campo `secret_psk` com uma senha forte.
2. Salve e reinicie o Add-on.
3. No aplicativo Android, insira a mesma senha no campo **Chave PSK / Senha** (ou escaneie o QR Code novamente, que já incluirá a chave automaticamente).
