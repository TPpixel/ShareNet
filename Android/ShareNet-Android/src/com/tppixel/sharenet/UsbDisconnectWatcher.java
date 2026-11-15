package com.tppixel.sharenet;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.os.BatteryManager;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.util.Log;

/**
 * Watches USB data + wired power state via USB_STATE, BATTERY_CHANGED, and ACTION_POWER_DISCONNECTED.
 * On Pixel/modern Android devices, USB_STATE(connected=false) may be delayed several seconds or skipped
 * during quick cable pulls, whereas ACTION_POWER_DISCONNECTED and BATTERY_CHANGED(plugged=0) fire
 * within ~50ms. Triggers callback once after a short debounce to absorb initial attach/reconfigure glitches.
 */
final class UsbDisconnectWatcher {
    private static final String TAG = "ShareNetUsb";
    private static final String ACTION_USB_STATE = "android.hardware.usb.action.USB_STATE";
    private static final long DISCONNECT_DEBOUNCE_MS = 350L;

    private final Context appContext;
    private final Handler handler = new Handler(Looper.getMainLooper());
    private final Runnable onDisconnected;
    private final Runnable fireTask;

    private volatile boolean registered = false;
    private volatile boolean assumeUsbMode = false;
    private volatile boolean usbDataConnected = false;
    private volatile boolean wiredPlugged = false;
    private volatile boolean hadUsbSession = false;
    private volatile boolean hadWiredPower = false;

    private final BroadcastReceiver receiver = new BroadcastReceiver() {
        @Override
        public void onReceive(Context context, Intent intent) {
            if (intent == null || intent.getAction() == null) {
                return;
            }
            String action = intent.getAction();
            if (ACTION_USB_STATE.equals(action)) {
                boolean connected = intent.getBooleanExtra("connected", false);
                usbDataConnected = connected;
                if (connected) {
                    hadUsbSession = true;
                    if (wiredPlugged) {
                        hadWiredPower = true;
                    }
                    handler.removeCallbacks(fireTask);
                } else if (hadUsbSession) {
                    scheduleDisconnectCheck();
                }
            } else if (Intent.ACTION_BATTERY_CHANGED.equals(action)) {
                int plugged = intent.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0);
                boolean usbPwr = (plugged & BatteryManager.BATTERY_PLUGGED_USB) != 0;
                boolean wired = (plugged & (BatteryManager.BATTERY_PLUGGED_USB | BatteryManager.BATTERY_PLUGGED_AC)) != 0;
                wiredPlugged = wired;
                if (usbPwr) {
                    hadUsbSession = true;
                }
                if (wired && (hadUsbSession || usbDataConnected || assumeUsbMode)) {
                    hadWiredPower = true;
                    if (usbDataConnected || assumeUsbMode) {
                        handler.removeCallbacks(fireTask);
                    }
                } else if (!wired && (hadUsbSession || assumeUsbMode) && hadWiredPower) {
                    scheduleDisconnectCheck();
                }
            } else if (Intent.ACTION_POWER_DISCONNECTED.equals(action)) {
                wiredPlugged = false;
                if ((hadUsbSession || assumeUsbMode) && hadWiredPower) {
                    scheduleDisconnectCheck();
                }
            }
        }
    };

    UsbDisconnectWatcher(Context context, Runnable onDisconnected) {
        this.appContext = context.getApplicationContext();
        this.onDisconnected = onDisconnected;
        this.fireTask = () -> {
            if (!registered) {
                return;
            }
            refreshStickies();
            boolean usbLost = hadUsbSession && !usbDataConnected;
            boolean powerLost = (hadUsbSession || assumeUsbMode) && hadWiredPower && !wiredPlugged;
            if ((usbLost || powerLost) && this.onDisconnected != null) {
                Log.i(TAG, "USB cable disconnect confirmed (usbLost=" + usbLost + ", powerLost=" + powerLost + ")");
                hadUsbSession = false;
                hadWiredPower = false;
                usbDataConnected = false;
                this.onDisconnected.run();
            }
        };
    }

    private void scheduleDisconnectCheck() {
        handler.removeCallbacks(fireTask);
        handler.postDelayed(fireTask, DISCONNECT_DEBOUNCE_MS);
    }

    private void refreshStickies() {
        Intent usbSticky = appContext.registerReceiver(null, new IntentFilter(ACTION_USB_STATE));
        if (usbSticky != null) {
            usbDataConnected = usbSticky.getBooleanExtra("connected", false);
            if (usbDataConnected) {
                hadUsbSession = true;
            }
        }
        Intent battSticky = appContext.registerReceiver(null, new IntentFilter(Intent.ACTION_BATTERY_CHANGED));
        if (battSticky != null) {
            int plugged = battSticky.getIntExtra(BatteryManager.EXTRA_PLUGGED, 0);
            boolean usbPwr = (plugged & BatteryManager.BATTERY_PLUGGED_USB) != 0;
            boolean wired = (plugged & (BatteryManager.BATTERY_PLUGGED_USB | BatteryManager.BATTERY_PLUGGED_AC)) != 0;
            wiredPlugged = wired;
            if (usbPwr) {
                hadUsbSession = true;
            }
            if (wired && (hadUsbSession || assumeUsbMode)) {
                hadWiredPower = true;
            }
        }
    }

    void refreshSnapshot(boolean assumeUsb) {
        this.assumeUsbMode = assumeUsb;
        handler.removeCallbacks(fireTask);
        refreshStickies();
    }

    void register() {
        register(false);
    }

    void register(boolean assumeUsb) {
        this.assumeUsbMode = assumeUsb;
        if (!registered) {
            registered = true;
            IntentFilter filter = new IntentFilter();
            filter.addAction(ACTION_USB_STATE);
            filter.addAction(Intent.ACTION_BATTERY_CHANGED);
            filter.addAction(Intent.ACTION_POWER_DISCONNECTED);
            if (Build.VERSION.SDK_INT >= 33) {
                appContext.registerReceiver(receiver, filter, Context.RECEIVER_EXPORTED);
            } else {
                appContext.registerReceiver(receiver, filter);
            }
        }
        refreshStickies();
    }

    void unregister() {
        registered = false;
        handler.removeCallbacks(fireTask);
        try {
            appContext.unregisterReceiver(receiver);
        } catch (IllegalArgumentException ignored) {
        }
    }
}
