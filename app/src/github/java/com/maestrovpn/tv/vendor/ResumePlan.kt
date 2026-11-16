package com.maestrovpn.tv.vendor

/**
 * Чистая (без Android-зависимостей) логика возобновления загрузки APK — вынесена из
 * [ApkDownloader], чтобы её можно было покрыть обычным JVM-тестом.
 *
 * Повод (16.11.2026): приставка SEI Robotics Dongle G 4K застряла в петле HTTP 416.
 * update.apk.part уже дорос до полного размера (обрыв на самом последнем байте), а следующий
 * заход просил Range с позиции, равной длине файла; сервер честно отвечал 416 InvalidRange,
 * клиент считал это ошибкой и повторял 8 раз подряд — .part оставался полным навсегда.
 */
internal object ResumePlan {

    /** Что делать с уже лежащим .part перед запросом. */
    enum class Step {
        /** .part полного размера — ничего не качать: отдать файл на проверку размера/sha256. */
        READY,

        /** .part больше ожидаемого (мусор или чужой релиз) — удалить и начать с нуля. */
        RESTART,

        /** .part неполный (или отсутствует) — докачивать с позиции have. */
        RESUME,
    }

    /** Как трактовать код ответа сервера. */
    enum class Reply {
        /** 206 — сервер продолжил с позиции have. */
        APPEND,

        /** 200 — сервер отдаёт файл целиком, .part надо перезаписать. */
        FULL,

        /** 416 при уже полном .part — файл скачан; это не ошибка, а «серверу нечего отдавать». */
        DONE,

        /** Любой другой код (и 416 при неполном .part) — ошибка. */
        FAIL,
    }

    fun step(expectedSize: Long, have: Long): Step = when {
        expectedSize > 0 && have == expectedSize -> Step.READY
        expectedSize in 1 until have -> Step.RESTART
        else -> Step.RESUME
    }

    fun reply(code: Int, expectedSize: Long, have: Long): Reply = when (code) {
        206 -> Reply.APPEND
        200 -> Reply.FULL
        416 -> if (have > 0 && (expectedSize <= 0 || have >= expectedSize)) Reply.DONE else Reply.FAIL
        else -> Reply.FAIL
    }
}
