class HAP2PTunnelCard extends HTMLElement {
  set hass(hass) {
    this._hass = hass;
    if (!this._built) {
      this.buildDOM();
    }
    this.updateData();
  }

  setConfig(config) {
    this._config = config;
  }

  buildDOM() {
    this._built = true;
    if (!this.shadowRoot) {
      this.attachShadow({ mode: 'open' });
    }

    this.shadowRoot.innerHTML = `
      <style>
        :host {
          display: block;
        }
        .mushroom-card {
          background: var(--ha-card-background, var(--card-background-color, #1e293b));
          border-radius: var(--ha-card-border-radius, 12px);
          box-shadow: var(--ha-card-box-shadow, none);
          padding: 12px;
          display: flex;
          align-items: center;
          gap: 12px;
          cursor: pointer;
          user-select: none;
          transition: transform 0.1s, background 0.2s;
        }
        .mushroom-card:active {
          transform: scale(0.98);
        }
        .icon-box {
          width: 40px;
          height: 40px;
          border-radius: 50%;
          background: rgba(107, 114, 128, 0.15);
          display: flex;
          align-items: center;
          justify-content: center;
          color: #6b7280;
          transition: color 0.3s, background 0.3s;
        }
        .info {
          flex: 1;
          display: flex;
          flex-direction: column;
        }
        .primary {
          font-weight: 600;
          font-size: 14px;
          color: var(--primary-text-color, #f8fafc);
        }
        .secondary {
          font-size: 12px;
          color: var(--secondary-text-color, #94a3b8);
          margin-top: 2px;
        }
        /* MODAL */
        .modal-overlay {
          position: fixed;
          inset: 0;
          background: rgba(0,0,0,0.75);
          backdrop-filter: blur(4px);
          display: none;
          align-items: center;
          justify-content: center;
          z-index: 999999;
          padding: 16px;
        }
        .modal-card {
          background: var(--ha-card-background, #1e293b);
          border: 1px solid var(--divider-color, #334155);
          border-radius: 16px;
          max-width: 440px;
          width: 100%;
          padding: 22px;
          box-shadow: 0 20px 25px -5px rgba(0,0,0,0.5);
          font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
          color: var(--primary-text-color, #f8fafc);
        }
        .modal-header {
          display: flex;
          justify-content: space-between;
          align-items: center;
          margin-bottom: 18px;
        }
        .modal-title {
          font-size: 16px;
          font-weight: 700;
          display: flex;
          align-items: center;
          gap: 8px;
        }
        .close-btn {
          background: none;
          border: none;
          font-size: 22px;
          color: #94a3b8;
          cursor: pointer;
        }
        .field-group {
          margin-bottom: 14px;
        }
        .field-label {
          font-size: 11px;
          font-weight: 600;
          text-transform: uppercase;
          letter-spacing: 0.5px;
          color: #94a3b8;
          margin-bottom: 6px;
          display: block;
        }
        .field-input-box {
          display: flex;
          gap: 8px;
        }
        .field-input {
          flex: 1;
          background: var(--secondary-background-color, #0f172a);
          border: 1px solid var(--divider-color, #334155);
          border-radius: 8px;
          padding: 9px 12px;
          font-family: monospace;
          font-size: 11px;
          color: #38bdf8;
          outline: none;
        }
        .btn-copy {
          background: #3b82f6;
          color: #fff;
          border: none;
          padding: 0 14px;
          border-radius: 8px;
          font-size: 12px;
          font-weight: 600;
          cursor: pointer;
          white-space: nowrap;
          transition: background 0.2s;
        }
        .btn-copy:hover {
          background: #2563eb;
        }
        .btn-copy.copied {
          background: #10b981;
        }
      </style>

      <div class="mushroom-card" id="cardTrigger">
        <div class="icon-box" id="iconBox">
          <ha-icon icon="mdi:lan-connect"></ha-icon>
        </div>
        <div class="info">
          <span class="primary">HA P2P Tunnel</span>
          <span class="secondary" id="secondaryText">Carregando...</span>
        </div>
      </div>

      <div class="modal-overlay" id="modalOverlay">
        <div class="modal-card">
          <div class="modal-header">
            <div class="modal-title">
              <ha-icon icon="mdi:tunnel" style="color:#3b82f6;"></ha-icon>
              <span>Conexão HA P2P Tunnel</span>
            </div>
            <button class="close-btn" id="modalClose">&times;</button>
          </div>

          <div class="field-group">
            <span class="field-label">🌐 Link Direto HTTPS (Porta 443 - Fura Firewall):</span>
            <div class="field-input-box">
              <input type="text" readonly class="field-input" id="httpsInput" value="" />
              <button class="btn-copy" id="btnCopyHttps">Copiar</button>
            </div>
          </div>

          <div class="field-group">
            <span class="field-label">🔑 Peer ID (App Android P2P):</span>
            <div class="field-input-box">
              <input type="text" readonly class="field-input" id="peerInput" value="" />
              <button class="btn-copy" id="btnCopyPeer">Copiar</button>
            </div>
          </div>

          <div class="field-group" id="pskGroup">
            <span class="field-label">🔐 Chave PSK (Rede Privada):</span>
            <div class="field-input-box">
              <input type="text" readonly class="field-input" id="pskInput" value="" />
              <button class="btn-copy" id="btnCopyPsk">Copiar</button>
            </div>
          </div>

          <div style="margin-top:18px; display:flex; justify-content:space-between; align-items:center;">
            <span style="font-size:11px; color:#64748b;">Toque em Copiar para transferir</span>
            <button class="btn-copy" style="background:#475569;" id="btnDone">Fechar</button>
          </div>
        </div>
      </div>
    `;

    const cardTrigger = this.shadowRoot.getElementById('cardTrigger');
    const modalOverlay = this.shadowRoot.getElementById('modalOverlay');
    const modalClose = this.shadowRoot.getElementById('modalClose');
    const btnDone = this.shadowRoot.getElementById('btnDone');
    const btnCopyHttps = this.shadowRoot.getElementById('btnCopyHttps');
    const btnCopyPeer = this.shadowRoot.getElementById('btnCopyPeer');
    const btnCopyPsk = this.shadowRoot.getElementById('btnCopyPsk');
    const httpsInput = this.shadowRoot.getElementById('httpsInput');
    const peerInput = this.shadowRoot.getElementById('peerInput');
    const pskInput = this.shadowRoot.getElementById('pskInput');

    cardTrigger.onclick = () => {
      modalOverlay.style.display = 'flex';
    };

    const closeModal = () => {
      modalOverlay.style.display = 'none';
    };
    modalClose.onclick = closeModal;
    btnDone.onclick = closeModal;
    modalOverlay.onclick = (e) => {
      if (e.target === modalOverlay) closeModal();
    };

    btnCopyHttps.onclick = (e) => {
      e.stopPropagation();
      navigator.clipboard.writeText(httpsInput.value);
      btnCopyHttps.innerText = 'Copiado!';
      btnCopyHttps.classList.add('copied');
      setTimeout(() => {
        btnCopyHttps.innerText = 'Copiar';
        btnCopyHttps.classList.remove('copied');
      }, 2000);
    };

    btnCopyPeer.onclick = (e) => {
      e.stopPropagation();
      navigator.clipboard.writeText(peerInput.value);
      btnCopyPeer.innerText = 'Copiado!';
      btnCopyPeer.classList.add('copied');
      setTimeout(() => {
        btnCopyPeer.innerText = 'Copiar';
        btnCopyPeer.classList.remove('copied');
      }, 2000);
    };

    if (btnCopyPsk) {
      btnCopyPsk.onclick = (e) => {
        e.stopPropagation();
        navigator.clipboard.writeText(pskInput.value);
        btnCopyPsk.innerText = 'Copiado!';
        btnCopyPsk.classList.add('copied');
        setTimeout(() => {
          btnCopyPsk.innerText = 'Copiar';
          btnCopyPsk.classList.remove('copied');
        }, 2000);
      };
    }
  }

  updateData() {
    if (!this._hass || !this._built) return;

    const entity = this._hass.states['sensor.ha_p2p_tunnel_status'];
    const urlEntity = this._hass.states['sensor.ha_p2p_tunnel_https_url'];
    const peerEntity = this._hass.states['sensor.ha_p2p_tunnel_peer_id'];

    const hasActiveUrl = urlEntity && urlEntity.state && urlEntity.state.startsWith('http');
    const hasPeer = peerEntity && peerEntity.state && peerEntity.state.startsWith('12D3');
    const isOnline = (entity && (entity.state === 'Online' || entity.state === 'online')) || hasActiveUrl || hasPeer;

    const activeStreams = (entity && entity.attributes && entity.attributes.active_streams) || 0;
    
    // Resolve HTTPS URL
    let httpsUrl = (entity && entity.attributes && (entity.attributes.https_auth_url || entity.attributes.https_url)) || '';
    if (!httpsUrl && urlEntity) {
      const s = urlEntity.state;
      if (s && s !== 'unavailable' && s !== 'unknown' && s !== 'Iniciando...') {
        httpsUrl = s;
      }
    }

    // Resolve Peer ID
    let peerId = (entity && entity.attributes && entity.attributes.peer_id) || '';
    if (!peerId && peerEntity) {
      const s = peerEntity.state;
      if (s && s !== 'unavailable' && s !== 'unknown') {
        peerId = s;
      }
    }

    // Resolve PSK
    let psk = (entity && entity.attributes && entity.attributes.psk) || '';
    if (!psk && this._hass.states['sensor.ha_p2p_tunnel_psk']) {
      const s = this._hass.states['sensor.ha_p2p_tunnel_psk'].state;
      if (s && s !== 'unavailable' && s !== 'unknown' && s !== 'Desativada') {
        psk = s;
      }
    }

    const secondary = isOnline ? `🟢 Online · ${activeStreams} streams` : '🛡️ libp2p / QUIC (CGNAT)';
    const iconColor = isOnline ? '#10b981' : '#6b7280';
    const bgIcon = isOnline ? 'rgba(16, 185, 129, 0.15)' : 'rgba(107, 114, 128, 0.15)';

    const iconBox = this.shadowRoot.getElementById('iconBox');
    const secondaryText = this.shadowRoot.getElementById('secondaryText');
    const httpsInput = this.shadowRoot.getElementById('httpsInput');
    const peerInput = this.shadowRoot.getElementById('peerInput');
    const pskInput = this.shadowRoot.getElementById('pskInput');

    if (iconBox) {
      iconBox.style.background = bgIcon;
      iconBox.style.color = iconColor;
    }
    if (secondaryText) {
      secondaryText.textContent = secondary;
    }
    if (httpsInput) {
      httpsInput.value = httpsUrl || 'Aguardando inicialização do túnel...';
    }
    if (peerInput) {
      peerInput.value = peerId || 'Aguardando inicialização...';
    }
    if (pskInput) {
      pskInput.value = psk || 'Desativada (Acesso Livre)';
    }
  }

  getCardSize() {
    return 1;
  }
}

customElements.define('ha-p2p-tunnel-card', HAP2PTunnelCard);
window.customCards = window.customCards || [];
window.customCards.push({
  type: 'ha-p2p-tunnel-card',
  name: 'HA P2P Tunnel Card',
  description: 'Card Mushroom do HA P2P Tunnel com janela modal e botões de cópia direta',
});
