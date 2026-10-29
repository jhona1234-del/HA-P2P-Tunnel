package com.hap2p.tunnel

import android.Manifest
import android.content.BroadcastReceiver
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.content.IntentFilter
import android.content.SharedPreferences
import android.content.pm.PackageManager
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.view.View
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import androidx.appcompat.app.AppCompatActivity
import androidx.core.app.ActivityCompat
import androidx.core.content.ContextCompat
import com.google.android.material.button.MaterialButton
import com.google.android.material.switchmaterial.SwitchMaterial
import com.google.android.material.textfield.TextInputEditText
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions

class MainActivity : AppCompatActivity() {

    private lateinit var etPeerId: TextInputEditText
    private lateinit var etPsk: TextInputEditText
    private lateinit var switchBoot: SwitchMaterial
    private lateinit var btnScanQr: MaterialButton
    private lateinit var btnToggleTunnel: MaterialButton
    private lateinit var btnOpenHA: MaterialButton
    private lateinit var badgeStatus: TextView
    private lateinit var viewStatusDot: View
    private lateinit var tvStatusTitle: TextView
    private lateinit var tvStatusSubtitle: TextView
    private lateinit var progressBarConnecting: ProgressBar
    private lateinit var tvLocalAddr: TextView
    private lateinit var tvConnectedHA: TextView
    private lateinit var tvLogs: TextView
    private lateinit var logScrollView: ScrollView
    private lateinit var btnCopyLogs: TextView
    private lateinit var btnClearLogs: TextView
    private lateinit var prefs: SharedPreferences

    // Scanner de QR Code nativo integrado
    private val qrScannerLauncher = registerForActivityResult(ScanContract()) { result ->
        if (result.contents != null) {
            handleScannedQr(result.contents)
        }
    }

    private val receiver = object : BroadcastReceiver() {
        override fun onReceive(context: Context?, intent: Intent?) {
            when (intent?.action) {
                TunnelService.ACTION_HA_STATE -> {
                    val stateName = intent.getStringExtra(TunnelService.EXTRA_HA_STATE) ?: TunnelService.HAState.STOPPED.name
                    val message = intent.getStringExtra(TunnelService.EXTRA_HA_MESSAGE) ?: ""
                    val state = try {
                        TunnelService.HAState.valueOf(stateName)
                    } catch (_: Exception) {
                        TunnelService.HAState.STOPPED
                    }
                    updateStatusUI(state, message)
                }
                TunnelService.ACTION_STATUS -> {
                    val isRunning = intent.getBooleanExtra(TunnelService.EXTRA_IS_RUNNING, false)
                    if (!isRunning) {
                        updateStatusUI(TunnelService.HAState.STOPPED, "Túnel Parado")
                    }
                }
                TunnelService.ACTION_LOG -> {
                    val line = intent.getStringExtra(TunnelService.EXTRA_LOG_MESSAGE) ?: return
                    appendLog(line)
                }
            }
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        setContentView(R.layout.activity_main)

        prefs = getSharedPreferences("hap2p_prefs", Context.MODE_PRIVATE)

        btnScanQr = findViewById(R.id.btnScanQr)
        etPeerId = findViewById(R.id.etPeerId)
        etPsk = findViewById(R.id.etPsk)
        switchBoot = findViewById(R.id.switchBoot)
        btnToggleTunnel = findViewById(R.id.btnToggleTunnel)
        btnOpenHA = findViewById(R.id.btnOpenHA)
        badgeStatus = findViewById(R.id.badgeStatus)
        viewStatusDot = findViewById(R.id.viewStatusDot)
        tvStatusTitle = findViewById(R.id.tvStatusTitle)
        tvStatusSubtitle = findViewById(R.id.tvStatusSubtitle)
        progressBarConnecting = findViewById(R.id.progressBarConnecting)
        tvLocalAddr = findViewById(R.id.tvLocalAddr)
        tvConnectedHA = findViewById(R.id.tvConnectedHA)
        tvLogs = findViewById(R.id.tvLogs)
        logScrollView = findViewById(R.id.logScrollView)
        btnCopyLogs = findViewById(R.id.btnCopyLogs)
        btnClearLogs = findViewById(R.id.btnClearLogs)

        // 100% LIMPO: Zero valores hardcoded
        etPeerId.setText(prefs.getString("peer_id", ""))
        etPsk.setText(prefs.getString("psk", ""))
        switchBoot.isChecked = prefs.getBoolean("auto_start", false)

        switchBoot.setOnCheckedChangeListener { _, isChecked ->
            prefs.edit().putBoolean("auto_start", isChecked).apply()
        }

        btnScanQr.setOnClickListener {
            val options = ScanOptions().apply {
                setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                setPrompt("Aponte a câmera para o QR Code no Home Assistant")
                setCameraId(0)
                setBeepEnabled(true)
                setBarcodeImageEnabled(false)
                setOrientationLocked(false)
            }
            qrScannerLauncher.launch(options)
        }

        btnToggleTunnel.setOnClickListener {
            if (TunnelService.isRunning) {
                stopTunnel()
            } else {
                startTunnel()
            }
        }

        btnOpenHA.setOnClickListener {
            val browserIntent = Intent(Intent.ACTION_VIEW, Uri.parse("http://127.0.0.1:8123"))
            startActivity(browserIntent)
        }

        btnCopyLogs.setOnClickListener {
            val clipboard = getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
            val clip = ClipData.newPlainText("HA P2P Logs", tvLogs.text.toString())
            clipboard.setPrimaryClip(clip)
            Toast.makeText(this, "Logs copiados para a área de transferência!", Toast.LENGTH_SHORT).show()
        }

        btnClearLogs.setOnClickListener {
            synchronized(TunnelService.logHistory) {
                TunnelService.logHistory.clear()
            }
            tvLogs.text = "Logs limpos.\n"
        }

        loadExistingLogs()
        requestNotificationPermission()
        updateStatusUI(TunnelService.haState, TunnelService.haMessage)
    }

    private fun handleScannedQr(scanned: String) {
        val trimmed = scanned.trim()
        if (trimmed.startsWith("hap2p://connect")) {
            try {
                val uri = Uri.parse(trimmed)
                val peer = uri.getQueryParameter("peer") ?: ""
                val psk = uri.getQueryParameter("psk") ?: ""
                if (peer.isNotEmpty()) {
                    etPeerId.setText(peer)
                    if (psk.isNotEmpty()) etPsk.setText(psk)
                    Toast.makeText(this, "Configuração do Home Assistant lida com sucesso!", Toast.LENGTH_SHORT).show()
                }
            } catch (_: Exception) {}
        } else {
            // Raw PeerID (ex: 12D3KooW...)
            etPeerId.setText(trimmed)
            Toast.makeText(this, "PeerID lido com sucesso!", Toast.LENGTH_SHORT).show()
        }
    }

    private fun loadExistingLogs() {
        synchronized(TunnelService.logHistory) {
            if (TunnelService.logHistory.isNotEmpty()) {
                tvLogs.text = TunnelService.logHistory.joinToString("\n")
                scrollToBottom()
            }
        }
    }

    private fun appendLog(line: String) {
        if (tvLogs.text.toString().startsWith("Pronto para")) {
            tvLogs.text = line + "\n"
        } else {
            tvLogs.append(line + "\n")
        }
        scrollToBottom()
    }

    private fun scrollToBottom() {
        logScrollView.post {
            logScrollView.fullScroll(ScrollView.FOCUS_DOWN)
        }
    }

    private fun startTunnel() {
        val peerId = etPeerId.text.toString().trim()
        val psk = etPsk.text.toString().trim()

        if (peerId.isEmpty()) {
            Toast.makeText(this, "Por favor insira ou escaneie o PeerID do Home Assistant", Toast.LENGTH_SHORT).show()
            return
        }

        prefs.edit()
            .putString("peer_id", peerId)
            .putString("psk", psk)
            .apply()

        appendLog("[APP] Solicitando inicialização do túnel...")

        val intent = Intent(this, TunnelService::class.java).apply {
            action = TunnelService.ACTION_START
            putExtra(TunnelService.EXTRA_PEER_ID, peerId)
            putExtra(TunnelService.EXTRA_PSK, psk)
        }

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            startForegroundService(intent)
        } else {
            startService(intent)
        }
        updateStatusUI(TunnelService.HAState.SEARCHING, "Iniciando túnel e buscando Home Assistant...")
    }

    private fun stopTunnel() {
        appendLog("[APP] Parando túnel...")
        val intent = Intent(this, TunnelService::class.java).apply {
            action = TunnelService.ACTION_STOP
        }
        startService(intent)
        updateStatusUI(TunnelService.HAState.STOPPED, "Túnel Parado")
    }

    private fun updateStatusUI(state: TunnelService.HAState, message: String) {
        when (state) {
            TunnelService.HAState.STOPPED -> {
                badgeStatus.text = "DESCONECTADO"
                badgeStatus.backgroundTintList = ContextCompat.getColorStateList(this, R.color.badge_stopped_bg)
                badgeStatus.setTextColor(ContextCompat.getColor(this, R.color.badge_stopped_text))

                viewStatusDot.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_red)
                tvStatusTitle.text = "Túnel Parado"
                tvStatusTitle.setTextColor(ContextCompat.getColor(this, R.color.text_main))
                tvStatusSubtitle.text = "Inicie o túnel para conectar ao Home Assistant"

                progressBarConnecting.visibility = View.GONE
                tvConnectedHA.text = "Desconectado"
                tvConnectedHA.setTextColor(ContextCompat.getColor(this, R.color.text_secondary))

                btnToggleTunnel.text = "INICIAR TÚNEL P2P"
                btnToggleTunnel.backgroundTintList = ContextCompat.getColorStateList(this, R.color.primary)

                btnOpenHA.isEnabled = false
                btnOpenHA.alpha = 0.5f
                btnOpenHA.backgroundTintList = ContextCompat.getColorStateList(this, R.color.card_bg)
            }
            TunnelService.HAState.SEARCHING -> {
                badgeStatus.text = "BUSCANDO NÓ"
                badgeStatus.backgroundTintList = ContextCompat.getColorStateList(this, R.color.badge_searching_bg)
                badgeStatus.setTextColor(ContextCompat.getColor(this, R.color.badge_searching_text))

                viewStatusDot.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_yellow)
                tvStatusTitle.text = "Buscando Home Assistant..."
                tvStatusTitle.setTextColor(ContextCompat.getColor(this, R.color.status_yellow))
                tvStatusSubtitle.text = "Localizando nó na rede P2P DHT (IPv6/QUIC)..."

                progressBarConnecting.visibility = View.VISIBLE
                tvConnectedHA.text = "Buscando na rede DHT..."
                tvConnectedHA.setTextColor(ContextCompat.getColor(this, R.color.status_yellow))

                btnToggleTunnel.text = "PARAR TÚNEL P2P"
                btnToggleTunnel.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_red)

                btnOpenHA.isEnabled = true
                btnOpenHA.alpha = 0.8f
                btnOpenHA.backgroundTintList = ContextCompat.getColorStateList(this, R.color.primary)
            }
            TunnelService.HAState.CONNECTED -> {
                badgeStatus.text = "ONLINE"
                badgeStatus.backgroundTintList = ContextCompat.getColorStateList(this, R.color.badge_connected_bg)
                badgeStatus.setTextColor(ContextCompat.getColor(this, R.color.badge_connected_text))

                viewStatusDot.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_green)
                tvStatusTitle.text = "Home Assistant Conectado!"
                tvStatusTitle.setTextColor(ContextCompat.getColor(this, R.color.status_green))
                tvStatusSubtitle.text = "Túnel P2P direto ativo e criptografado"

                progressBarConnecting.visibility = View.GONE
                tvConnectedHA.text = "Conectado via IPv6/QUIC"
                tvConnectedHA.setTextColor(ContextCompat.getColor(this, R.color.status_green))

                btnToggleTunnel.text = "PARAR TÚNEL P2P"
                btnToggleTunnel.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_red)

                btnOpenHA.isEnabled = true
                btnOpenHA.alpha = 1.0f
                btnOpenHA.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_green)
            }
        }
    }

    private fun requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            if (ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
                ActivityCompat.requestPermissions(this, arrayOf(Manifest.permission.POST_NOTIFICATIONS), 101)
            }
        }
    }

    override fun onResume() {
        super.onResume()
        val filter = IntentFilter().apply {
            addAction(TunnelService.ACTION_STATUS)
            addAction(TunnelService.ACTION_HA_STATE)
            addAction(TunnelService.ACTION_LOG)
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            registerReceiver(receiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            registerReceiver(receiver, filter)
        }
        updateStatusUI(TunnelService.haState, TunnelService.haMessage)
    }

    override fun onPause() {
        super.onPause()
        try {
            unregisterReceiver(receiver)
        } catch (_: Exception) {}
    }
}
