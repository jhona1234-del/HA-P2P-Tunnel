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

    private var tunnelProcess: Process? = null
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null

    companion object {
        private const val TAG = "HAP2P"
        const val CHANNEL_ID = "ha_p2p_channel"
        const val NOTIFICATION_ID = 1001
        const val ACTION_START = "com.hap2p.tunnel.START"
        const val ACTION_STOP = "com.hap2p.tunnel.STOP"
        const val ACTION_STATUS = "com.hap2p.tunnel.STATUS_UPDATE"
        const val ACTION_LOG = "com.hap2p.tunnel.LOG_EVENT"
        const val EXTRA_IS_RUNNING = "is_running"
        const val EXTRA_PEER_ID = "peer_id"
        const val EXTRA_PSK = "psk"
        const val EXTRA_LOG_MESSAGE = "log_message"

        var isRunning = false
            private set

        val logHistory = mutableListOf<String>()

        fun addLog(msg: String, context: Context? = null) {
            val time = SimpleDateFormat("HH:mm:ss", Locale.getDefault()).format(Date())
            val formatted = "[$time] $msg"
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
        wifiLock = wifiManager.createWifiLock(WifiManager.WIFI_MODE_FULL_HIGH_PERF, "HAP2P:TunnelWifiLock")
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
                if (peerId.isNotEmpty()) {
                    startTunnel(peerId, psk)
                } else {
                    addLog("[ERRO] PeerID não fornecido!", this)
                }
            }
        }
        return START_STICKY
    }

    private fun startTunnel(peerId: String, psk: String) {
        if (isRunning) {
            addLog("Túnel já está em execução.", this)
            return
        }

        wakeLock?.acquire(24 * 60 * 60 * 1000L) // 24 horas
        wifiLock?.acquire()

        val notification = buildNotification("Conectando ao Home Assistant...")
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

                val cmd = mutableListOf<String>()
                cmd.add(nativeLib.absolutePath)
                cmd.add("-peer")
                cmd.add(peerId)

                if (psk.isNotEmpty()) {
                    cmd.add("-psk")
                    cmd.add(psk)
                    addLog("Modo de rede privada (PSK) ativado.", this)
                }

                addLog("Executando: ${nativeLib.name} -peer ${peerId.take(12)}...", this)

                val processBuilder = ProcessBuilder(cmd)
                processBuilder.redirectErrorStream(true)
                tunnelProcess = processBuilder.start()
                isRunning = true
                broadcastStatus(true)

                updateNotification("Túnel P2P Ativo (127.0.0.1:8123)")

                val reader = tunnelProcess?.inputStream?.bufferedReader()
                reader?.forEachLine { line ->
                    addLog(line, this@TunnelService)
                }

                val exitCode = tunnelProcess?.waitFor() ?: -1
                addLog("Processo do túnel encerrou com código: $exitCode", this@TunnelService)
            } catch (e: Exception) {
                addLog("[EXCEÇÃO] Falha ao rodar túnel: ${e.message}", this)
                e.printStackTrace()
            } finally {
                stopTunnel()
            }
        }.start()
    }

    private fun stopTunnel() {
        isRunning = false
        try {
            tunnelProcess?.destroy()
            tunnelProcess = null
        } catch (_: Exception) {}

        if (wakeLock?.isHeld == true) wakeLock?.release()
        if (wifiLock?.isHeld == true) wifiLock?.release()

        broadcastStatus(false)
        stopForeground(STOP_FOREGROUND_REMOVE)
    }

    private fun broadcastStatus(running: Boolean) {
        val intent = Intent(ACTION_STATUS).apply {
            putExtra(EXTRA_IS_RUNNING, running)
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

        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setContentTitle("HA P2P Tunnel")
            .setContentText(contentText)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentIntent(pendingIntent)
            .setOngoing(true)
            .build()
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
