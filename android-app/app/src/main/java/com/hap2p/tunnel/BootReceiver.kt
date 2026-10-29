package com.hap2p.tunnel

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Build

class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED) {
            val prefs = context.getSharedPreferences("hap2p_prefs", Context.MODE_PRIVATE)
            val autoStart = prefs.getBoolean("auto_start", false)
            val peerId = prefs.getString("peer_id", "") ?: ""
            val psk = prefs.getString("psk", "") ?: ""

            if (autoStart && peerId.isNotEmpty()) {
                val serviceIntent = Intent(context, TunnelService::class.java).apply {
                    action = TunnelService.ACTION_START
                    putExtra(TunnelService.EXTRA_PEER_ID, peerId)
                    putExtra(TunnelService.EXTRA_PSK, psk)
                }

                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    context.startForegroundService(serviceIntent)
                } else {
                    context.startService(serviceIntent)
                }
            }
        }
    }
}
