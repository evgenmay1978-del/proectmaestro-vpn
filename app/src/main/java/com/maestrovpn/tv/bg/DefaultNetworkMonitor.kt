package com.maestrovpn.tv.bg

import android.net.Network
import android.os.Build
import io.nekohasekai.libbox.InterfaceUpdateListener
import com.maestrovpn.tv.Application
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.net.NetworkInterface
import java.util.concurrent.atomic.AtomicLong

object DefaultNetworkMonitor {

    // Фоновая очередь для ПОВТОРОВ разрешения интерфейса: единственная задача — не держать
    // вызывающий (главный) поток. Живёт столько же, сколько процесс, задач максимум единицы.
    private val retryScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    // Номер последнего события сети: фоновый повтор от устаревшего события себя отменяет.
    private val updateGeneration = AtomicLong(0)

    @Volatile
    var defaultNetwork: Network? = null

    @Volatile
    private var listener: InterfaceUpdateListener? = null

    suspend fun start() {
        DefaultNetworkListener.start(this) {
            defaultNetwork = it
            checkDefaultInterfaceUpdate(it)
        }
        defaultNetwork = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.M) {
            Application.connectivity.activeNetwork
        } else {
            DefaultNetworkListener.get()
        }
    }

    suspend fun stop() {
        DefaultNetworkListener.stop(this)
    }

    suspend fun require(): Network {
        val network = defaultNetwork
        if (network != null) {
            return network
        }
        return DefaultNetworkListener.get()
    }

    fun setListener(listener: InterfaceUpdateListener?) {
        this.listener = listener
        checkDefaultInterfaceUpdate(defaultNetwork)
    }

    /**
     * Одна попытка разрешить интерфейс сети. true — интерфейс найден и отдан libbox.
     * Вынесена отдельно, чтобы быстрый путь (обычный случай) остался синхронным, как раньше.
     */
    private fun tryPushInterface(listener: InterfaceUpdateListener, network: Network): Boolean {
        val linkProperties = Application.connectivity.getLinkProperties(network) ?: return false
        val interfaceIndex = try {
            NetworkInterface.getByName(linkProperties.interfaceName).index
        } catch (e: Exception) {
            return false
        }
        listener.updateDefaultInterface(linkProperties.interfaceName, interfaceIndex, false, false)
        return true
    }

    private fun checkDefaultInterfaceUpdate(newNetwork: Network?) {
        val current = listener ?: return
        if (newNetwork == null) {
            current.updateDefaultInterface("", -1, false, false)
            return
        }
        // Первая попытка — синхронно: в обычном случае linkProperties уже доступны, интерфейс
        // отдаётся немедленно, поведение и тайминг ровно как раньше.
        if (tryPushInterface(current, newNetwork)) return

        // Не получилось — нужны повторы с паузами. Раньше здесь стоял runBlocking(Dispatchers.IO),
        // и комментарий уверял, что вынос в IO спасает вызывающий поток. Не спасает: runBlocking
        // БЛОКИРУЕТ вызывающий поток независимо от диспетчера внутри. А вызывающий здесь —
        // ГЛАВНЫЙ: сетевые колбэки регистрируются с mainHandler (DefaultNetworkListener), а актор
        // доставки объявлен на Dispatchers.Unconfined, то есть исполняется на потоке отправителя.
        // Итог: до 10×100 мс заморозки UI при каждом переключении Wi-Fi ↔ сота с живым туннелем.
        // Теперь повторы уходят в фон и вызывающий поток не ждёт.
        //
        // updateGeneration защищает от гонки: пока фоновые повторы идут, может прийти НОВОЕ
        // событие сети. Устаревшая корутина обязана замолчать, иначе она отдала бы libbox
        // интерфейс уже неактуальной сети поверх свежего.
        val generation = updateGeneration.incrementAndGet()
        retryScope.launch {
            for (times in 0 until 9) {
                delay(100)
                if (updateGeneration.get() != generation) return@launch
                // Поле перечитывается на каждой итерации: слушатель мог сняться,
                // пока шли паузы (остановка туннеля).
                val live = DefaultNetworkMonitor.listener ?: return@launch
                if (tryPushInterface(live, newNetwork)) return@launch
            }
        }
    }
}
