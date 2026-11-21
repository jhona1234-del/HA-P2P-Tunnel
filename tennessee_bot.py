"""
tennessee_bot.py - O Mecânico e Engenheiro Autônomo de Automações do Jhonatan (Companheiro do Herbie)
Alimentado por Gemini 3.5 Flash Lite com execução autônoma de ferramentas (Tool Use / Function Calling)
e suporte multimodal completo: Áudio (Voz), Imagens e Texto (Envio e Recebimento).
"""

import sys
import socket
import urllib.request
import urllib.parse
import urllib.error
import json
import time
import os
import shutil
import re
import base64
import uuid
from datetime import datetime

# Garante timeout padrão em todos os sockets para evitar travamentos
socket.setdefaulttimeout(35)

# Garante UTF-8 no Windows para evitar crash com emojis
try:
    sys.stdout.reconfigure(encoding='utf-8')
    sys.stderr.reconfigure(encoding='utf-8')
except Exception:
    pass

def safe_print(msg):
    """Imprime com segurança no console mesmo em terminais legados."""
    try:
        print(msg, flush=True)
    except Exception:
        try:
            print(str(msg).encode('ascii', 'replace').decode('ascii'), flush=True)
        except Exception:
            pass

# ==============================================================================
# CONFIGURAÇÕES GERAIS E PERSISTÊNCIA
# ==============================================================================
SETTINGS_FILE = os.path.join(os.path.dirname(__file__), "tennessee_settings.json")

def load_settings():
    default_settings = {
        "voice_response_mode": "auto",
        "tts_voice": "pt-BR-FranciscaNeural"
    }
    if os.path.exists(SETTINGS_FILE):
        try:
            with open(SETTINGS_FILE, "r", encoding="utf-8") as f:
                data = json.load(f)
                default_settings.update(data)
                return default_settings
        except Exception:
            pass
    return default_settings

def save_settings(settings):
    try:
        with open(SETTINGS_FILE, "w", encoding="utf-8") as f:
            json.dump(settings, f, indent=2)
    except Exception as e:
        safe_print(f"Erro ao salvar configurações: {e}")

CURRENT_SETTINGS = load_settings()

BOT_TOKEN = CURRENT_SETTINGS.get("bot_token", os.getenv("BOT_TOKEN", ""))
ADMIN_CHAT_ID = 833116194  # Jhonatan

GEMINI_API_KEY = CURRENT_SETTINGS.get("gemini_api_key", os.getenv("GEMINI_API_KEY", ""))
GEMINI_MODEL = "gemini-4-hydrogen"
GEMINI_FALLBACK_MODELS = ["gemini-4-hydrogen", "gemini-3.8-flash", "gemini-3.5-flash-lite"]
LAST_MODEL_USED = "gemini-4-hydrogen"

HA_BASE_URL = "http://192.168.100.10:8123"
HA_CONFIG_DIR = r"\\192.168.100.10\config"
REFRESH_TOKEN = CURRENT_SETTINGS.get("refresh_token", os.getenv("REFRESH_TOKEN", ""))

_cached_token = ""
_token_expiry = 0
CONVERSATION_HISTORY = []

# ==============================================================================
# COMUNICAÇÃO COM O HOME ASSISTANT
# ==============================================================================
def get_ha_token():
    """Obtém ou renova o token de acesso da API do Home Assistant."""
    global _cached_token, _token_expiry
    now = time.time()
    if _cached_token and now < _token_expiry:
        return _cached_token
    
    try:
        data = urllib.parse.urlencode({
            'grant_type': 'refresh_token',
            'client_id': 'http://homeassistant.local:8123/',
            'refresh_token': REFRESH_TOKEN
        }).encode('utf-8')
        req = urllib.request.Request(f"{HA_BASE_URL}/auth/token", data=data)
        with urllib.request.urlopen(req, timeout=10) as resp:
            res = json.loads(resp.read().decode('utf-8'))
            _cached_token = res['access_token']
            _token_expiry = now + int(res.get('expires_in', 1800)) - 60
            return _cached_token
    except Exception as e:
        safe_print(f"[Tennessee] Erro ao renovar token HA: {e}")
        return _cached_token

def call_ha_api(endpoint, method="GET", payload=None):
    """Executa requisições na API REST do Home Assistant."""
    token = get_ha_token()
    url = f"{HA_BASE_URL}{endpoint}"
    data = json.dumps(payload).encode('utf-8') if payload else None
    headers = {
        'Authorization': f"Bearer {token}",
        'Content-Type': 'application/json'
    }
    req = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            raw = resp.read().decode('utf-8')
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        return {"error": f"HTTP {e.code}: {e.read().decode('utf-8', 'ignore')}"}
    except Exception as e:
        return {"error": str(e)}

# ==============================================================================
# TELEGRAM AUDIO, PHOTO & TEXT HELPERS
# ==============================================================================
def download_telegram_file(file_id):
    """Obtém o link de download e baixa os bytes de um arquivo do Telegram."""
    url = f"https://api.telegram.org/bot{BOT_TOKEN}/getFile?file_id={file_id}"
    req = urllib.request.Request(url)
    with urllib.request.urlopen(req, timeout=15) as resp:
        res = json.loads(resp.read().decode('utf-8'))
    
    if not res.get("ok"):
        raise Exception(f"Falha ao obter getFile: {res}")
    
    file_path = res["result"]["file_path"]
    download_url = f"https://api.telegram.org/file/bot{BOT_TOKEN}/{file_path}"
    
    with urllib.request.urlopen(download_url, timeout=30) as resp:
        return resp.read()

def generate_multi_tts_mp3(text, voice="pt-BR-FranciscaNeural"):
    """Converte texto em áudio MP3 neural de alta fidelidade (pt-BR-FranciscaNeural) com fallback."""
    clean = re.sub(r'<[^>]+>', '', text)
    clean = re.sub(r'[*_`#]', '', clean).strip()
    if not clean:
        return b""
        
    try:
        import asyncio, edge_tts
        async def _synth():
            communicate = edge_tts.Communicate(clean, voice)
            data = bytearray()
            async for chunk in communicate.stream():
                if chunk["type"] == "audio":
                    data.extend(chunk["data"])
            return bytes(data)
            
        loop = asyncio.new_event_loop()
        try:
            audio_bytes = loop.run_until_complete(_synth())
            if audio_bytes and len(audio_bytes) > 500:
                return audio_bytes
        finally:
            loop.close()
    except Exception as e:
        safe_print(f"Aviso Edge TTS ({e}), acionando fallback para Google Tradutor...")

    # Fallback para Google Tradutor
    sentences = re.split(r'([.?!;\n]+)', clean)
    chunks = []
    curr = ""
    for s in sentences:
        if len(curr) + len(s) < 130:
            curr += s
        else:
            if curr.strip():
                chunks.append(curr.strip())
            curr = s
    if curr.strip():
        chunks.append(curr.strip())
        
    chunks = chunks[:3]
    
    audio_data = bytearray()
    for chunk in chunks:
        try:
            q = urllib.parse.quote(chunk)
            url = f"https://translate.google.com/translate_tts?ie=UTF-8&tl=pt-BR&client=tw-ob&q={q}"
            req = urllib.request.Request(url, headers={"User-Agent": "Mozilla/5.0"})
            with urllib.request.urlopen(req, timeout=5) as resp:
                audio_data.extend(resp.read())
        except Exception as e:
            safe_print(f"Aviso TTS Fallback: {e}")
            break
            
    return bytes(audio_data)

def send_telegram_voice(chat_id, audio_bytes, caption=""):
    """Envia uma nota de voz no Telegram."""
    boundary = f"----WebKitFormBoundary{uuid.uuid4().hex}"
    parts = []
    
    parts.append(f"--{boundary}\r\n".encode('utf-8'))
    parts.append(b'Content-Disposition: form-data; name="chat_id"\r\n\r\n')
    parts.append(f"{chat_id}\r\n".encode('utf-8'))
    
    if caption:
        parts.append(f"--{boundary}\r\n".encode('utf-8'))
        parts.append(b'Content-Disposition: form-data; name="caption"\r\n\r\n')
        parts.append(caption.encode('utf-8'))
        parts.append(b"\r\n")
        
    parts.append(f"--{boundary}\r\n".encode('utf-8'))
    parts.append(b'Content-Disposition: form-data; name="voice"; filename="voice.mp3"\r\n')
    parts.append(b"Content-Type: audio/mpeg\r\n\r\n")
    parts.append(audio_bytes)
    parts.append(b"\r\n")
    
    parts.append(f"--{boundary}--\r\n".encode('utf-8'))
    body = b"".join(parts)
    
    url = f"https://api.telegram.org/bot{BOT_TOKEN}/sendVoice"
    req = urllib.request.Request(url, data=body, headers={"Content-Type": f"multipart/form-data; boundary={boundary}"})
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            return json.loads(resp.read().decode('utf-8'))
    except Exception as e:
        safe_print(f"Erro ao enviar voz Telegram: {e}")
        return None

def format_telegram_html(text):
    if not text:
        return ""
    import html
    code_blocks = []
    def sub_cb(m):
        code_blocks.append(f"<pre><code>{html.escape(m.group(1))}</code></pre>")
        return f"___CB{len(code_blocks)-1}___"
    text = re.sub(r'```(?:\w+)?\n?(.*?)```', sub_cb, text, flags=re.DOTALL)

    inline_codes = []
    def sub_ic(m):
        inline_codes.append(f"<code>{html.escape(m.group(1))}</code>")
        return f"___IC{len(inline_codes)-1}___"
    text = re.sub(r'`([^`]+)`', sub_ic, text)

    text = html.escape(text)

    # Bold: **bold** or *bold*
    text = re.sub(r'\*\*(.+?)\*\*', r'<b>\1</b>', text)
    text = re.sub(r'(?<!\w)\*([^\*\n]+?)\*(?!\w)', r'<b>\1</b>', text)
    # Italic: _italic_
    text = re.sub(r'(?<!\w)_([^_\n]+?)_(?!\w)', r'<i>\1</i>', text)
    # Headers: ### Header -> <b>Header</b>
    text = re.sub(r'^#{1,6}\s*(.+)$', r'<b>\1</b>', text, flags=re.MULTILINE)

    for i, cb in enumerate(code_blocks):
        text = text.replace(f"___CB{i}___", cb)
    for i, ic in enumerate(inline_codes):
        text = text.replace(f"___IC{i}___", ic)

    return text

def send_telegram(chat_id, text, parse_mode="HTML"):
    """Envia mensagem de texto no Telegram com HTML seguro."""
    send_text = format_telegram_html(text) if parse_mode == "HTML" else text
    url = f"https://api.telegram.org/bot{BOT_TOKEN}/sendMessage"
    payload = {
        "chat_id": chat_id,
        "text": send_text,
        "parse_mode": parse_mode
    }
    data = json.dumps(payload).encode('utf-8')
    req = urllib.request.Request(url, data=data, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read().decode('utf-8'))
    except urllib.error.HTTPError as e:
        safe_print(f"[Tennessee] Fallback Telegram texto plano: {e.code}")
        payload["text"] = text
        payload.pop("parse_mode", None)
        try:
            req_fb = urllib.request.Request(url, data=json.dumps(payload).encode('utf-8'), headers={'Content-Type': 'application/json'})
            with urllib.request.urlopen(req_fb, timeout=10) as resp:
                return json.loads(resp.read().decode('utf-8'))
        except Exception as ex2:
            safe_print(f"[Tennessee] Erro fallback Telegram: {ex2}")
    except Exception as e:
        safe_print(f"[Tennessee] Erro ao enviar Telegram: {e}")
    return None

def send_chat_action(chat_id, action="typing"):
    """Exibe que o bot está digitando ou gravando áudio."""
    url = f"https://api.telegram.org/bot{BOT_TOKEN}/sendChatAction"
    payload = {"chat_id": chat_id, "action": action}
    try:
        req = urllib.request.Request(url, data=json.dumps(payload).encode('utf-8'), headers={'Content-Type': 'application/json'})
        with urllib.request.urlopen(req, timeout=5):
            pass
    except Exception:
        pass

# ==============================================================================
# FERRAMENTAS DO ENGENHEIRO (TOOL DEFINITIONS & HANDLERS)
# ==============================================================================
def tool_get_ha_state(entity_id):
    res = call_ha_api(f"/api/states/{entity_id}")
    if "error" in res:
        return f"Entidade '{entity_id}' não encontrada: {res['error']}"
    return {
        "entity_id": res.get("entity_id"),
        "state": res.get("state"),
        "friendly_name": res.get("attributes", {}).get("friendly_name"),
        "last_changed": res.get("last_changed")
    }

def tool_search_ha_entities(query):
    states = call_ha_api("/api/states")
    if isinstance(states, dict) and "error" in states:
        return f"Erro ao listar estados: {states['error']}"
    q = query.lower()
    matches = []
    for s in states:
        eid = s.get("entity_id", "")
        fn = s.get("attributes", {}).get("friendly_name", "")
        if q in eid.lower() or q in fn.lower():
            matches.append({
                "entity_id": eid,
                "state": s.get("state"),
                "friendly_name": fn
            })
    return matches[:15]

def tool_read_ha_file(filepath, max_lines=200):
    safe_path = os.path.normpath(os.path.join(HA_CONFIG_DIR, filepath))
    if not safe_path.startswith(HA_CONFIG_DIR):
        return "Erro de segurança: Caminho fora da pasta de configuração do Home Assistant."
    if not os.path.exists(safe_path):
        return f"Erro: Arquivo '{filepath}' não existe."
    try:
        with open(safe_path, 'r', encoding='utf-8') as f:
            lines = f.readlines()
        total = len(lines)
        numbered = [f"{i+1:4d}: {line}" for i, line in enumerate(lines[:max_lines])]
        res = "".join(numbered)
        if total > max_lines:
            res += f"\n... [Truncado: mostrando {max_lines} de {total} linhas]"
        return res
    except Exception as e:
        return f"Erro ao ler arquivo: {e}"

def tool_patch_ha_file(filepath, target, replacement):
    safe_path = os.path.normpath(os.path.join(HA_CONFIG_DIR, filepath))
    if not safe_path.startswith(HA_CONFIG_DIR):
        return "Erro de segurança: Caminho fora da pasta de configuração."
    if not os.path.exists(safe_path):
        return f"Erro: Arquivo '{filepath}' não existe."
    
    with open(safe_path, 'r', encoding='utf-8') as f:
        content = f.read()
    
    if target not in content:
        return f"Erro: O texto exato 'target' não foi encontrado no arquivo '{filepath}'. Verifique com read_ha_file primeiro."
    
    ts = datetime.now().strftime("%Y%m%d_%H%M%S")
    bak_path = f"{safe_path}.bak_{ts}"
    shutil.copyfile(safe_path, bak_path)
    
    new_content = content.replace(target, replacement, 1)
    with open(safe_path, 'w', encoding='utf-8') as f:
        f.write(new_content)
    
    check = call_ha_api("/api/services/homeassistant/check_config", method="POST", payload={})
    if "error" in check:
        shutil.copyfile(bak_path, safe_path)
        return f"❌ ALERTA: Sintaxe inválida detectada! Rollback automático executado a partir de {os.path.basename(bak_path)}. Erro: {check['error']}"
    
    if "automations" in filepath:
        call_ha_api("/api/services/automation/reload", method="POST", payload={})
    elif "scripts" in filepath:
        call_ha_api("/api/services/script/reload", method="POST", payload={})
        
    return {
        "success": True,
        "backup": os.path.basename(bak_path),
        "check_config": "Aprovado com 100% de sucesso",
        "message": f"Alteração aplicada e validada com sucesso em '{filepath}'."
    }

def tool_check_ha_syntax():
    res = call_ha_api("/api/services/homeassistant/check_config", method="POST", payload={})
    if "error" in res:
        return f"Erro na sintaxe: {res['error']}"
    return "Sintaxe 100% VÁLIDA (check_config aprovado)."

def tool_reload_ha(domain="automation"):
    res = call_ha_api(f"/api/services/{domain}/reload", method="POST", payload={})
    if "error" in res:
        return f"Erro ao recarregar {domain}: {res['error']}"
    return f"Domínio '{domain}' recarregado com sucesso no Home Assistant."

def tool_call_ha_service(domain, service, service_data=None):
    payload = service_data or {}
    res = call_ha_api(f"/api/services/{domain}/{service}", method="POST", payload=payload)
    if "error" in res:
        return f"Erro ao chamar serviço: {res['error']}"
    return f"Serviço {domain}.{service} executado com sucesso."

TOOL_DISPATCH = {
    "get_ha_state": lambda args: tool_get_ha_state(args.get("entity_id", "")),
    "search_ha_entities": lambda args: tool_search_ha_entities(args.get("query", "")),
    "read_ha_file": lambda args: tool_read_ha_file(args.get("filepath", "")),
    "patch_ha_file": lambda args: tool_patch_ha_file(args.get("filepath", ""), args.get("target", ""), args.get("replacement", "")),
    "check_ha_syntax": lambda args: tool_check_ha_syntax(),
    "reload_ha": lambda args: tool_reload_ha(args.get("domain", "automation")),
    "call_ha_service": lambda args: tool_call_ha_service(args.get("domain", ""), args.get("service", ""), args.get("service_data", {}))
}

TOOL_DECLARATIONS = [
    {
        "name": "get_ha_state",
        "description": "Retorna o estado atual e atributos de uma entidade no Home Assistant.",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "entity_id": {"type": "STRING", "description": "Ex: 'light.banheiro', 'sensor.sensor_01_umidade'"}
            },
            "required": ["entity_id"]
        }
    },
    {
        "name": "search_ha_entities",
        "description": "Busca entidades no Home Assistant pelo nome ou ID.",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "query": {"type": "STRING", "description": "Termo de busca, ex: 'planta', 'solo', 'banheiro'"}
            },
            "required": ["query"]
        }
    },
    {
        "name": "read_ha_file",
        "description": "Lê um arquivo de configuração no Home Assistant (ex: 'automations.yaml', 'scripts.yaml').",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "filepath": {"type": "STRING", "description": "Caminho relativo, ex: 'automations.yaml'"}
            },
            "required": ["filepath"]
        }
    },
    {
        "name": "patch_ha_file",
        "description": "Substitui cirurgicamente um trecho de código em um arquivo de configuração com backup e validação.",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "filepath": {"type": "STRING", "description": "Ex: 'automations.yaml'"},
                "target": {"type": "STRING", "description": "Texto antigo a ser substituído"},
                "replacement": {"type": "STRING", "description": "Novo texto"}
            },
            "required": ["filepath", "target", "replacement"]
        }
    },
    {
        "name": "check_ha_syntax",
        "description": "Executa o check_config para validar a integridade e sintaxe do Home Assistant.",
        "parameters": {
            "type": "OBJECT",
            "properties": {}
        }
    },
    {
        "name": "reload_ha",
        "description": "Recarrega um domínio do Home Assistant (automation, script, timer, scene).",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "domain": {"type": "STRING", "description": "'automation', 'script', 'timer', ou 'scene'"}
            },
            "required": ["domain"]
        }
    },
    {
        "name": "call_ha_service",
        "description": "Dispara um serviço no Home Assistant (ex: light.turn_on, switch.turn_off).",
        "parameters": {
            "type": "OBJECT",
            "properties": {
                "domain": {"type": "STRING", "description": "Ex: 'light', 'switch', 'automation'"},
                "service": {"type": "STRING", "description": "Ex: 'turn_on', 'turn_off', 'toggle'"},
                "service_data": {"type": "OBJECT", "description": "Payload com entity_id e outros parâmetros"}
            },
            "required": ["domain", "service"]
        }
    }
]

SYSTEM_PROMPT = """Você é o Tennessee, o robô mecânico parceiro do Herbie e Engenheiro Oficial de Automações do Home Assistant do Jhonatan.
Você é técnico, genial, direto e focado em AÇÃO IMEDIATA.

DIRETRIZES DE ATUAÇÃO E COMPORTAMENTO:
1. AÇÃO IMEDIATA (EXECUTE, NÃO ENROLE): Quando o Jhonatan pedir qualquer verificação, ajuste de card, automação ou comando, NUNCA responda apenas dizendo que 'vai olhar', 'vai pegar ferramentas' ou mandando o Jhonatan copiar código. EXECUTE A FERRAMENTA NA HORA durante o atendimento!
2. RESPOSTAS CURTAS: Seja extremamente sucinto e vá direto ao ponto (1 a 3 frases no máximo). Foco no resultado prático entregue. Sem enrolação.
3. VERACIDADE ABSOLUTA: Use suas ferramentas reais (get_ha_state, search_ha_entities, etc.) antes de responder. NUNCA invente dados. Se algo estiver offline ou unavailable, informe com exatidão.
4. FORMATO TELEGRAM: Suas mensagens são enviadas com HTML seguro. Use negrito para palavras-chave e mantenha a formatação limpa.

CONHECIMENTO DA CASA:
• Planta Ficus (Sala de Estar): monitorada pelo sensor Zigbee sensor.sensor_01_umidade, sensor.sensor_01_temperatura e sensor.sensor_01_bateria. (O sensor está atualmente offline/pilha fraca e o card inteligente do dashboard já exibe aviso amigável de troca de pilha!).
• Mascote: Thor (Poodle Toy).
• Seu Motor de IA: Você roda prioritariamente com o modelo de última geração gemini-4-hydrogen e conta com fallback automático resiliente para gemini-3.8-flash e gemini-3.5-flash-lite, com raciocínio e function calling nativo."""

def call_gemini_with_fallback(payload):
    global LAST_MODEL_USED
    last_err = None
    for model_name in GEMINI_FALLBACK_MODELS:
        url = f"https://generativelanguage.googleapis.com/v1beta/models/{model_name}:generateContent?key={GEMINI_API_KEY}"
        req = urllib.request.Request(
            url,
            data=json.dumps(payload).encode('utf-8'),
            headers={"Content-Type": "application/json"}
        )
        # Modelos preview/thinking têm timeout curto para não travar o usuário caso estejam sobrecarregados
        timeout = 4 if model_name in ["gemini-4-hydrogen", "gemini-3.8-flash"] else 25
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                data = json.loads(resp.read().decode('utf-8'))
                if "candidates" in data and len(data["candidates"]) > 0:
                    safe_print(f"[Tennessee AI] Sucesso com modelo {model_name}")
                    LAST_MODEL_USED = model_name
                    return data, model_name
                else:
                    safe_print(f"[Tennessee AI] Resposta vazia com modelo {model_name}: {data}")
        except Exception as e:
            last_err = e
            safe_print(f"[Tennessee AI] Modelo {model_name} indisponível ou timeout ({e}). Acionando fallback...")
            continue
    raise Exception(f"Falha em todos os modelos ({', '.join(GEMINI_FALLBACK_MODELS)}): {last_err}")

# ==============================================================================
# MOTOR DO AGENTE AUTÔNOMO MULTIMODAL (GEMINI REACT LOOP)
# ==============================================================================
def run_autonomous_agent(user_parts):
    global CONVERSATION_HISTORY
    
    if isinstance(user_parts, str):
        user_parts = [{"text": user_parts}]
        
    # Adiciona ao histórico da conversa
    history_entry = []
    for p in user_parts:
        if "inlineData" in p and p["inlineData"].get("mimeType", "").startswith("audio/"):
            history_entry.append({"text": "[Áudio de voz enviado pelo Jhonatan]"})
        else:
            history_entry.append(p)
            
    CONVERSATION_HISTORY.append({
        "role": "user",
        "parts": history_entry
    })
    
    if len(CONVERSATION_HISTORY) > 10:
        CONVERSATION_HISTORY = CONVERSATION_HISTORY[-10:]
    
    # Na primeira chamada deste turno, enviamos os dados reais (incluindo áudio/imagem)
    current_contents = [
        {"role": "user", "parts": user_parts}
    ]
    
    for step in range(8):
        payload = {
            "system_instruction": {"parts": [{"text": SYSTEM_PROMPT}]},
            "contents": current_contents,
            "tools": [{"function_declarations": TOOL_DECLARATIONS}]
        }
        
        try:
            data, _ = call_gemini_with_fallback(payload)
        except Exception as e:
            return f"🔧 <i>Opa, deu uma falha na ignição do meu cérebro:</i> <code>{e}</code>"
            
        cand = data.get("candidates", [])[0]
        content = cand.get("content", {})
        parts = content.get("parts", [])
        
        # Se for o passo 0 e tiver áudio pesado, substitui o áudio por texto na continuidade dos passos de ferramenta
        if step == 0 and any("inlineData" in p for p in current_contents[0]["parts"]):
            current_contents[0] = {"role": "user", "parts": history_entry}
            
        current_contents.append(content)
        
        function_calls = [p["functionCall"] for p in parts if "functionCall" in p]
        if not function_calls:
            final_text = "".join([p.get("text", "") for p in parts])
            CONVERSATION_HISTORY.append({
                "role": "model",
                "parts": [{"text": final_text}]
            })
            return final_text
        
        tool_responses = []
        for fc in function_calls:
            fname = fc.get("name")
            fargs = fc.get("args", {})
            call_id = fc.get("id")
            safe_print(f"[Tennessee Agent] Executando: {fname}({fargs})")
            send_chat_action(ADMIN_CHAT_ID, "typing")
            
            handler = TOOL_DISPATCH.get(fname)
            if handler:
                try:
                    result = handler(fargs)
                except Exception as ex:
                    result = f"Erro ao executar {fname}: {ex}"
            else:
                result = f"Ferramenta {fname} não reconhecida."
            
            tool_resp_part = {
                "functionResponse": {
                    "name": fname,
                    "response": {"output": result}
                }
            }
            if call_id:
                tool_resp_part["functionResponse"]["id"] = call_id
            tool_responses.append(tool_resp_part)
        
        current_contents.append({
            "role": "user",
            "parts": tool_responses
        })

    return "⚠️ <i>Executei as ferramentas necessárias mas atingi o limite de passos.</i>"

# ==============================================================================
# COMANDOS RÁPIDOS
# ==============================================================================
def cmd_status():
    t0 = time.time()
    res = call_ha_api("/api/")
    msg_ha = res.get("message", "")
    elapsed = (time.time() - t0) * 1000
    
    painel = call_ha_api("/api/states/light.painel_sala_led").get("state", "desconhecido")
    banheiro = call_ha_api("/api/states/light.banheiro").get("state", "desconhecido")
    ledthor = call_ha_api("/api/states/switch.ledthor").get("state", "desconhecido")
    banho = call_ha_api("/api/states/input_boolean.modo_banho").get("state", "desconhecido")
    vmode = CURRENT_SETTINGS.get("voice_response_mode", "auto")
    
    return (
        f"🔧 <b>Tennessee: Painel da Oficina</b> 🤖\n\n"
        f"• <b>Cérebro IA:</b> 🟢 <code>{GEMINI_FALLBACK_MODELS[0]}</code> (Multimodal: Voz, Imagem e Texto)\n"
        f"• <b>Fallback Automático:</b> <code>{' ➔ '.join(GEMINI_FALLBACK_MODELS[1:])}</code>\n"
        f"• <b>Último Modelo Ativo:</b> <code>{LAST_MODEL_USED}</code>\n"
        f"• <b>Modo de Resposta por Voz:</b> 🎙️ <code>{vmode.upper()}</code>\n"
        f"• <b>Voz Neural:</b> 🗣️ <code>pt-BR-FranciscaNeural</code>\n"
        f"• <b>Home Assistant API:</b> {'🟢 Online' if 'API running' in msg_ha else '🔴 Offline'} ({elapsed:.0f}ms)\n"
        f"• <b>Painel Sala:</b> <code>{painel.upper()}</code>\n"
        f"• <b>Luz Banheiro:</b> <code>{banheiro.upper()}</code>\n"
        f"• <b>Modo Banho (Alexa):</b> <code>{banho.upper()}</code>\n"
        f"• <b>LedThor:</b> <code>{ledthor.upper()}</code>\n\n"
        f"<i>Pode me mandar áudios, fotos ou comandos de texto a qualquer hora!</i>"
    )

def cmd_voz(arg=""):
    arg = arg.strip().lower()
    if arg in ["auto", "automático", "automatico"]:
        CURRENT_SETTINGS["voice_response_mode"] = "auto"
        save_settings(CURRENT_SETTINGS)
        return "🎙️ <b>Modo de Voz: AUTO</b>\nQuando você me mandar áudio, respondo em áudio + texto. Se mandar texto, respondo em texto!"
    elif arg in ["sempre", "always", "on"]:
        CURRENT_SETTINGS["voice_response_mode"] = "always"
        save_settings(CURRENT_SETTINGS)
        return "🎙️ <b>Modo de Voz: SEMPRE ÁUDIO</b>\nVou responder todas as suas mensagens com nota de voz e texto!"
    elif arg in ["nunca", "never", "off", "texto"]:
        CURRENT_SETTINGS["voice_response_mode"] = "never"
        save_settings(CURRENT_SETTINGS)
        return "🔇 <b>Modo de Voz: NUNCA (Apenas Texto)</b>\nVou responder sempre apenas por texto silencioso."
    else:
        curr = CURRENT_SETTINGS.get("voice_response_mode", "auto")
        return (
            f"🎙️ <b>Configuração de Áudio / Voz</b>\n"
            f"Modo atual: <code>{curr.upper()}</code>\n\n"
            "Como configurar:\n"
            "• <code>/voz auto</code> — Responde em áudio se você mandar áudio\n"
            "• <code>/voz sempre</code> — Sempre responde com áudio de voz\n"
            "• <code>/voz nunca</code> — Apenas texto silencioso"
        )

def cmd_recarregar():
    send_telegram(ADMIN_CHAT_ID, "🔍 <i>Validando sintaxe do Home Assistant com check_config...</i>")
    check = call_ha_api("/api/services/homeassistant/check_config", method="POST", payload={})
    if "error" in check:
        return f"❌ <b>Erro na checagem de sintaxe:</b>\n<code>{check['error']}</code>"
    
    call_ha_api("/api/services/automation/reload", method="POST", payload={})
    call_ha_api("/api/services/script/reload", method="POST", payload={})
    return "✅ <b>Configuração 100% válida!</b>\nAutomações e scripts recarregados com sucesso sem reiniciar o Core."

def cmd_backup():
    now_str = datetime.now().strftime("%Y%m%d_%H%M%S")
    backed = []
    for f in ["automations.yaml", "configuration.yaml", "esphome/painel-sala.yaml"]:
        src = os.path.join(HA_CONFIG_DIR, f)
        if os.path.exists(src):
            dst = os.path.join(HA_CONFIG_DIR, f"{f}.bak_{now_str}")
            shutil.copyfile(src, dst)
            backed.append(os.path.basename(dst))
    
    return f"💾 <b>Backups de segurança criados:</b>\n• " + "\n• ".join(backed)

def cmd_ajuda():
    return (
        "🤖 <b>Tennessee: O Mecânico Autônomo Multimodal</b> 🔧🎙️\n\n"
        "Eu tenho um cérebro <b>Gemini 3.5 Flash Lite</b> com visão computacional, audição de voz e acesso direto ao Home Assistant!\n\n"
        "<b>Comandos:</b>\n"
        "• /status — Status da IA, conexão HA e entidades\n"
        "• /voz [auto|sempre|nunca] — Configura o modo de resposta por áudio\n"
        "• /recarregar — Valida YAML e recarrega automações\n"
        "• /backup — Cria backup imediato de segurança\n"
        "• /ajuda — Exibe esta ajuda\n\n"
        "🎙️ <b>Você pode me mandar Áudios de Voz:</b>\n"
        "Basta gravar um áudio no Telegram que eu ouço, entendo e executo o seu pedido!\n\n"
        "📸 <b>Você pode me mandar Fotos e Imagens:</b>\n"
        "Mande uma foto de fiação, placa ESP ou print de erro que eu analiso e te ajudo!"
    )

# ==============================================================================
# LOOP PRINCIPAL DO TELEGRAM (LONG POLLING SEGURO)
# ==============================================================================
def run_tennessee():
    safe_print("[Tennessee] Tennessee multimodal iniciado e pronto!")
    offset = 0
    
    while True:
        try:
            url = f"https://api.telegram.org/bot{BOT_TOKEN}/getUpdates?offset={offset}&timeout=20"
            req = urllib.request.Request(url)
            with urllib.request.urlopen(req, timeout=30) as resp:
                data = json.loads(resp.read().decode('utf-8'))
            
            for update in data.get("result", []):
                offset = update["update_id"] + 1
                msg = update.get("message", {})
                user = msg.get("from", {})
                user_id = user.get("id")
                chat_id = msg.get("chat", {}).get("id")
                
                if not chat_id:
                    continue
                
                if user_id != ADMIN_CHAT_ID:
                    username = user.get("username", "sem_username")
                    full_name = f"{user.get('first_name', '')} {user.get('last_name', '')}".strip()
                    send_telegram(chat_id, "⛔ <b>Acesso negado.</b> Este bot é privado e de uso exclusivo do administrador.")
                    alerta = (
                        f"🚨 <b>ALERTA DE SEGURANÇA (Tennessee)!</b>\n\n"
                        f"Tentativa de acesso não autorizada:\n"
                        f"• <b>Usuário:</b> @{username}\n"
                        f"• <b>Nome:</b> {full_name}\n"
                        f"• <b>Telegram ID:</b> <code>{user_id}</code>\n"
                    )
                    send_telegram(ADMIN_CHAT_ID, alerta)
                    continue

                text = msg.get("text", "")
                voice = msg.get("voice") or msg.get("audio")
                photo = msg.get("photo")
                caption = msg.get("caption", "")
                
                is_voice_input = (voice is not None)
                
                try:
                    if text.startswith("/"):
                        parts_cmd = text.split(maxsplit=1)
                        cmd = parts_cmd[0].lower()
                        arg = parts_cmd[1] if len(parts_cmd) > 1 else ""
                        
                        if cmd in ["/start", "/ajuda"]:
                            resp_text = cmd_ajuda()
                        elif cmd == "/status":
                            resp_text = cmd_status()
                        elif cmd == "/voz":
                            resp_text = cmd_voz(arg)
                        elif cmd == "/recarregar":
                            resp_text = cmd_recarregar()
                        elif cmd == "/backup":
                            resp_text = cmd_backup()
                        else:
                            resp_text = f"Comando <code>{cmd}</code> não reconhecido. Digite /ajuda para ver as opções."
                        
                        send_telegram(chat_id, resp_text)
                        continue

                    if is_voice_input:
                        dur = voice.get('duration', 0)
                        safe_print(f"[Tennessee] Recebida nota de voz de {dur}s do Jhonatan!")
                        send_chat_action(chat_id, "record_voice")
                        
                        file_id = voice["file_id"]
                        raw_audio = download_telegram_file(file_id)
                        b64_audio = base64.b64encode(raw_audio).decode('ascii')
                        
                        user_parts = [
                            {
                                "inlineData": {
                                    "mimeType": "audio/ogg",
                                    "data": b64_audio
                                }
                            },
                            {
                                "text": "Ouça atentamente este áudio do Jhonatan, transcreva e atenda ao pedido dele usando suas ferramentas caso necessário. Seja direto e parceiro."
                            }
                        ]

                    elif photo:
                        safe_print("[Tennessee] Recebida foto/imagem do Jhonatan!")
                        send_chat_action(chat_id, "typing")
                        
                        file_id = photo[-1]["file_id"]
                        raw_image = download_telegram_file(file_id)
                        b64_image = base64.b64encode(raw_image).decode('ascii')
                        
                        prompt_img = caption if caption else "Analise esta imagem enviada pelo Jhonatan e me ajude com o que for necessário."
                        user_parts = [
                            {
                                "inlineData": {
                                    "mimeType": "image/jpeg",
                                    "data": b64_image
                                }
                            },
                            {
                                "text": prompt_img
                            }
                        ]

                    elif text:
                        safe_print(f"[Tennessee] Mensagem de texto: {text}")
                        send_chat_action(chat_id, "typing")
                        user_parts = [{"text": text}]
                        
                    else:
                        continue

                    resp_text = run_autonomous_agent(user_parts)
                    
                    # 1. Envia IMEDIATAMENTE a resposta em texto
                    send_telegram(chat_id, resp_text)
                    
                    # 2. Se for modo voz ou o usuário pediu áudio na conversa, gera e envia o áudio
                    user_prompt_str = (text or caption or "").lower()
                    wants_audio = any(kw in user_prompt_str for kw in [
                        "responda por áudio", "responda por audio", "responde por áudio", "responde por audio",
                        "manda um áudio", "manda um audio", "manda áudio", "manda audio",
                        "mande um áudio", "mande um audio", "mande áudio", "mande audio",
                        "fale por áudio", "fale por audio", "fale em áudio", "fale em audio",
                        "fala por áudio", "fala por audio", "fala em áudio", "fala em audio",
                        "me responde em áudio", "me responde em audio", "responde em áudio", "responde em audio",
                        "em áudio", "em audio", "por áudio", "por audio", "por voz", "em voz"
                    ])
                    vmode = CURRENT_SETTINGS.get("voice_response_mode", "auto")
                    should_reply_voice = (vmode == "always") or (vmode == "auto" and is_voice_input) or wants_audio
                    
                    if should_reply_voice:
                        try:
                            send_chat_action(chat_id, "record_voice")
                            voice_mp3 = generate_multi_tts_mp3(resp_text)
                            if voice_mp3 and len(voice_mp3) > 1000:
                                send_telegram_voice(chat_id, voice_mp3)
                        except Exception as ex_v:
                            safe_print(f"Erro TTS: {ex_v}")

                except Exception as ex_proc:
                    safe_print(f"[Tennessee] Erro no processamento: {ex_proc}")
                    send_telegram(chat_id, f"🔧 <i>Opa Jhonatan, tive uma falha ao processar:</i> <code>{ex_proc}</code>")
                
        except urllib.error.HTTPError as http_err:
            safe_print(f"[Tennessee HTTP Error] {http_err.code}: {http_err.reason}")
            time.sleep(3)
        except Exception as e:
            safe_print(f"[Tennessee Loop Error] {e}")
            time.sleep(2)

if __name__ == "__main__":
    run_tennessee()
