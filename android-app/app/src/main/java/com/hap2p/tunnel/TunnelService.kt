package com.hap2p.tunnel

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.core.app.NotificationCompat
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

class TunnelService : Service() {

    enum class HAState {
        STOPPED,
        SEARCHING,
        CONNECTED
    }

    private var tunnelProcess: Process? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    @Volatile
    private var isStopping = false

    companion object {
        private const val TAG = "HAP2P"
        const val CHANNEL_ID = "ha_p2p_channel"
        const val NOTIFICATION_ID = 1001
        const val ACTION_START = "com.hap2p.tunnel.START"
        const val ACTION_STOP = "com.hap2p.tunnel.STOP"
        const val ACTION_STATUS = "com.hap2p.tunnel.STATUS_UPDATE"
        const val ACTION_HA_STATE = "com.hap2p.tunnel.HA_STATE_UPDATE"
        const val ACTION_LOG = "com.hap2p.tunnel.LOG_EVENT"
        const val EXTRA_IS_RUNNING = "is_running"
        const val EXTRA_HA_STATE = "ha_state"
        const val EXTRA_HA_MESSAGE = "ha_message"
        const val EXTRA_PEER_ID = "peer_id"
        const val EXTRA_PSK = "psk"
        const val EXTRA_ADDRS = "addrs"
        const val EXTRA_LOG_MESSAGE = "log_message"

        var isRunning = false
            private set

        var haState: HAState = HAState.STOPPED
            private set

        var haMessage: String = "Túnel Parado"
            private set

        val logHistory = mutableListOf<String>()
        private val PEER_REGEX = Regex("12D3KooW[a-zA-Z0-9]{36,}")

        fun maskSensitive(text: String): String {
            return PEER_REGEX.replace(text) { m ->
                val id = m.value
                id.take(10) + "..." + id.takeLast(6)
            }
        }

        fun addLog(msg: String, context: Context? = null) {
            val time = SimpleDateFormat("HH:mm:ss", Locale.getDefault()).format(Date())
            val masked = maskSensitive(msg)
            val formatted = "[$time] $masked"
            synchronized(logHistory) {
                logHistory.add(formatted)
                if (logHistory.size > 100) {
                    logHistory.removeAt(0)
                }
            }
            Log.i(TAG, formatted)

            context?.let { ctx ->
                val intent = Intent(ACTION_LOG).apply {
                    putExtra(EXTRA_LOG_MESSAGE, formatted)
                    setPackage(ctx.packageName)
                }
                ctx.sendBroadcast(intent)
            }
        }
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onCreate() {
        super.onCreate()
        createNotificationChannel()

        val powerManager = getSystemService(Context.POWER_SERVICE) as PowerManager
        wakeLock = powerManager.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "HAP2P:TunnelWakeLock")

        val wifiManager = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        wifiLock = wifiManager.createWifiLock(WifiManager.WIFI_MODE_FULL, "HAP2P:TunnelWifiLock")
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                addLog("Recebida solicitação para parar o túnel.", this)
                stopTunnel()
                stopSelf()
            }
            ACTION_START -> {
                val peerId = intent.getStringExtra(EXTRA_PEER_ID) ?: ""
                val psk = intent.getStringExtra(EXTRA_PSK) ?: ""
                val addrs = intent.getStringExtra(EXTRA_ADDRS) ?: ""
                if (peerId.isNotEmpty()) {
                    startTunnel(peerId, psk, addrs)
                } else {
                    addLog("[ERRO] PeerID não fornecido!", this)
                }
            }
        }
        return START_STICKY
    }

    private fun startTunnel(peerId: String, psk: String, addrs: String = "") {
        if (isRunning) {
            addLog("Túnel já está em execução.", this)
            return
        }

        isStopping = false

        // Economia de bateria: WakeLock temporário de 30 segundos apenas para a fase inicial de conexão.
        // O Android Foreground Service (DATA_SYNC) mantém o processo vivo sem precisar travar a CPU em 100% 24 horas por dia.
        try {
            if (wakeLock?.isHeld == true) wakeLock?.release()
            wakeLock?.acquire(30_000L) // 30 segundos
            wifiLock?.acquire()
        } catch (_: Exception) {}

        val notification = buildNotification("Buscando Home Assistant na rede P2P...")
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC)
        } else {
            startForeground(NOTIFICATION_ID, notification)
        }

        Thread {
            try {
                addLog("Iniciando processo do túnel P2P...", this)
                val nativeLib = File(applicationInfo.nativeLibraryDir, "libtunnel.so")

                if (!nativeLib.exists()) {
                    addLog("[ERRO FATAL] Binário nativo não encontrado em: ${nativeLib.absolutePath}", this)
                    stopTunnel()
                    return@Thread
                }

                if (!nativeLib.canExecute()) {
                    nativeLib.setExecutable(true)
                }

                // Diretório de cache persistente para identidade e endereços recentes do HA
                val cacheDir = File(filesDir, "p2p_cache")
                cacheDir.mkdirs()

                val cmd = mutableListOf<String>()
                cmd.add(nativeLib.absolutePath)
                cmd.add("-peer")
                cmd.add(peerId)
                cmd.add("-cache")
                cmd.add(cacheDir.absolutePath)

                if (psk.isNotEmpty()) {
                    cmd.add("-psk")
                    cmd.add(psk)
                    addLog("Modo de rede privada (PSK) ativado.", this)
                }

                if (addrs.isNotEmpty()) {
                    cmd.add("-addrs")
                    cmd.add(addrs)
                    addLog("Endereços diretos do HA configurados para conexão rápida.", this)
                }

                addLog("Executando: ${nativeLib.name} -peer ${peerId.take(12)}...", this)

                val processBuilder = ProcessBuilder(cmd)
                processBuilder.redirectErrorStream(true)
                tunnelProcess = processBuilder.start()
                isRunning = true
                haState = HAState.SEARCHING
                haMessage = "Buscando Home Assistant na rede P2P..."

                broadcastStatus(true)
                broadcastHAState()
                updateNotification("Buscando Home Assistant...")

                val reader = tunnelProcess?.inputStream?.bufferedReader()
                reader?.forEachLine { line ->
                    addLog(line, this@TunnelService)

                    // Analisa estado de conexão com o Home Assistant em tempo real
                    if (line.contains("[HA_STATE:CONNECTED]") ||
                        line.contains("Conectado diretamente ao Home Assistant") ||
                        line.contains("Conectado ao Home Assistant via Discovery")
                    ) {
                        haState = HAState.CONNECTED
                        haMessage = "Home Assistant Conectado!"
                        broadcastHAState()
                        updateNotification("Home Assistant Conectado (127.0.0.1:8123)")

                        // Salva histórico de logs para envio do relatório ao Home Assistant
                        try {
                            val logFile = File(cacheDir, "recent_logs.txt")
                            synchronized(logHistory) {
                                logFile.writeText(logHistory.takeLast(25).joinToString("\n"))
                            }
                        } catch (_: Exception) {}
                    } else if (line.contains("[HA_STATE:SEARCHING]") ||
                        line.contains("Conexão com Home Assistant perdida")
                    ) {
                        haState = HAState.SEARCHING
                        haMessage = "Buscando Home Assistant na rede P2P..."
                        broadcastHAState()
                        updateNotification("Buscando Home Assistant...")
                    }
                }

                val exitCode = tunnelProcess?.waitFor() ?: -1
                if (!isStopping) {
                    addLog("Processo do túnel encerrou com código: $exitCode", this@TunnelService)
                }
            } catch (e: java.io.IOException) {
                // Interrupção normal ao encerrar o túnel
                if (!isStopping && isRunning) {
                    addLog("[AVISO] Leitura interrompida: ${e.message}", this)
                }
            } catch (e: Exception) {
                if (!isStopping) {
                    addLog("[EXCEÇÃO] Falha ao rodar túnel: ${e.message}", this)
                    e.printStackTrace()
                }
            } finally {
                stopTunnel()
            }
        }.start()
    }

    private fun stopTunnel() {
        isStopping = true
        isRunning = false
        haState = HAState.STOPPED
        haMessage = "Túnel Parado"

        try {
            tunnelProcess?.destroy()
            tunnelProcess = null
        } catch (_: Exception) {}

        if (wakeLock?.isHeld == true) wakeLock?.release()
        if (wifiLock?.isHeld == true) wifiLock?.release()

        broadcastStatus(false)
        broadcastHAState()
        stopForeground(STOP_FOREGROUND_REMOVE)
    }

    private fun broadcastStatus(running: Boolean) {
        val intent = Intent(ACTION_STATUS).apply {
            putExtra(EXTRA_IS_RUNNING, running)
            setPackage(packageName)
        }
        sendBroadcast(intent)
    }

    private fun broadcastHAState() {
        val intent = Intent(ACTION_HA_STATE).apply {
            putExtra(EXTRA_HA_STATE, haState.name)
            putExtra(EXTRA_HA_MESSAGE, haMessage)
            setPackage(packageName)
        }
        sendBroadcast(intent)
    }

    private fun createNotificationChannel() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val channel = NotificationChannel(
                CHANNEL_ID,
                "Túnel Home Assistant P2P",
                NotificationManager.IMPORTANCE_LOW
            ).apply {
                description = "Mantém o túnel P2P ativo em segundo plano"
            }
            val manager = getSystemService(NotificationManager::class.java)
            manager.createNotificationChannel(channel)
        }
    }

    private fun buildNotification(contentText: String): Notification {
        val pendingIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val stopIntent = Intent(this, TunnelService::class.java).apply {
            action = ACTION_STOP
        }
        val stopPendingIntent = PendingIntent.getService(
            this,
            1,
            stopIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val openIntent = Intent(this, LaunchReceiver::class.java).apply {
            action = LaunchReceiver.ACTION_OPEN_HA
        }
        val openPendingIntent = PendingIntent.getBroadcast(
            this,
            2,
            openIntent,
            PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        )

        val builder = NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("HA P2P Tunnel")
            .setContentText(contentText)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentIntent(pendingIntent)
            .addAction(R.drawable.ic_launch, "Abrir HA", openPendingIntent)
            .addAction(R.drawable.ic_notification, "Desconectar", stopPendingIntent)
            .setOngoing(true)

        return builder.build()
    }

    private fun updateNotification(contentText: String) {
        val manager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        manager.notify(NOTIFICATION_ID, buildNotification(contentText))
    }

    override fun onDestroy() {
        stopTunnel()
        super.onDestroy()
    }
}
