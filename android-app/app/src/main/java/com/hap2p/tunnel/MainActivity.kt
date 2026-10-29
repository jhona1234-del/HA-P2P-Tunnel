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
    private lateinit var tvStatus: TextView
    private lateinit var viewStatusDot: View
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
                TunnelService.ACTION_STATUS -> {
                    val isRunning = intent.getBooleanExtra(TunnelService.EXTRA_IS_RUNNING, false)
                    updateUI(isRunning)
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
        tvStatus = findViewById(R.id.tvStatus)
        viewStatusDot = findViewById(R.id.viewStatusDot)
        tvLogs = findViewById(R.id.tvLogs)
        logScrollView = findViewById(R.id.logScrollView)
        btnCopyLogs = findViewById(R.id.btnCopyLogs)
        btnClearLogs = findViewById(R.id.btnClearLogs)

        // 100% LIMPO: Nada hardcoded. Carrega vazio ou o que o usuário salvou anteriormente
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
        updateUI(TunnelService.isRunning)
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
        updateUI(true)
    }

    private fun stopTunnel() {
        appendLog("[APP] Parando túnel...")
        val intent = Intent(this, TunnelService::class.java).apply {
            action = TunnelService.ACTION_STOP
        }
        startService(intent)
        updateUI(false)
    }

    private fun updateUI(isRunning: Boolean) {
        if (isRunning) {
            tvStatus.text = "Túnel Conectado (127.0.0.1:8123)"
            tvStatus.setTextColor(ContextCompat.getColor(this, R.color.status_green))
            viewStatusDot.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_green)
            btnToggleTunnel.text = "PARAR TÚNEL P2P"
            btnToggleTunnel.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_red)
        } else {
            tvStatus.text = "Túnel Parado"
            tvStatus.setTextColor(ContextCompat.getColor(this, R.color.text_main))
            viewStatusDot.backgroundTintList = ContextCompat.getColorStateList(this, R.color.status_red)
            btnToggleTunnel.text = "INICIAR TÚNEL P2P"
            btnToggleTunnel.backgroundTintList = ContextCompat.getColorStateList(this, R.color.primary)
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
            addAction(TunnelService.ACTION_LOG)
        }
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            registerReceiver(receiver, filter, Context.RECEIVER_NOT_EXPORTED)
        } else {
            registerReceiver(receiver, filter)
        }
        updateUI(TunnelService.isRunning)
    }

    override fun onPause() {
        super.onPause()
        try {
            unregisterReceiver(receiver)
        } catch (_: Exception) {}
    }
}
