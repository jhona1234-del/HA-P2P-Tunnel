package com.hap2p.tunnel

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.net.Uri

class LaunchReceiver : BroadcastReceiver() {
    companion object {
        const val ACTION_OPEN_HA = "com.hap2p.tunnel.OPEN_HA"

        fun openHomeAssistant(context: Context) {
            val pm = context.packageManager
            val launchIntent = pm.getLaunchIntentForPackage("io.homeassistant.companion.android")
            if (launchIntent != null) {
                launchIntent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                context.startActivity(launchIntent)
            } else {
                val browserIntent = Intent(Intent.ACTION_VIEW, Uri.parse("http://127.0.0.1:8123")).apply {
                    addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                }
                context.startActivity(browserIntent)
            }
        }
    }

    override fun onReceive(context: Context, intent: Intent?) {
        if (intent?.action == ACTION_OPEN_HA) {
            openHomeAssistant(context)
        }
    }
}
