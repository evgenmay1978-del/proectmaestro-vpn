package com.maestrovpn.tv.qrs

import android.util.Base64
import java.io.ByteArrayOutputStream
import java.util.zip.Inflater

class QRSDecoder {
    private companion object {
        // Потолок для распаковки одного набора QR-кадров. Реальный конфиг sing-box — десятки
        // килобайт; 8 МБ с огромным запасом, но не даёт битому/зловредному потоку раздуть буфер.
        const val MAX_DECOMPRESSED_BYTES = 8 * 1024 * 1024
    }

    private var codec: LubyCodec? = null
    private var state: LubyCodec.DecodingState? = null
    private val processedHashes = mutableSetOf<Int>()

    private val inflater = Inflater()
    private val decompressBuffer = ByteArray(8192)
    private val outputBuffer = ByteArrayOutputStream(32768)

    data class DecodeProgress(
        val decodedBlocks: Int,
        val totalBlocks: Int,
        val isComplete: Boolean,
        val data: ByteArray? = null,
        val error: String? = null,
    ) {
        override fun equals(other: Any?): Boolean {
            if (this === other) return true
            if (javaClass != other?.javaClass) return false
            other as DecodeProgress
            return decodedBlocks == other.decodedBlocks &&
                totalBlocks == other.totalBlocks &&
                isComplete == other.isComplete &&
                data.contentEquals(other.data) &&
                error == other.error
        }

        override fun hashCode(): Int {
            var result = decodedBlocks
            result = 31 * result + totalBlocks
            result = 31 * result + isComplete.hashCode()
            result = 31 * result + (data?.contentHashCode() ?: 0)
            result = 31 * result + (error?.hashCode() ?: 0)
            return result
        }
    }

    @Synchronized
    fun processFrame(base64Content: String): DecodeProgress? {
        val payload = try {
            Base64.decode(base64Content, Base64.NO_WRAP)
        } catch (e: Exception) {
            return null
        }
        return processFrame(payload)
    }

    @Synchronized
    fun processFrame(payload: ByteArray): DecodeProgress? {
        val hash = payload.contentHashCode()
        if (hash in processedHashes) {
            return state?.let {
                DecodeProgress(it.decodedCount, it.totalBlocks, it.decodedCount == it.totalBlocks)
            }
        }
        processedHashes.add(hash)

        val block = parsePayload(payload) ?: return null

        // Auto-detect dataset switch: if checksum changes, reset decoder
        if (state != null && state!!.checksum != block.checksum) {
            reset()
        }

        if (codec == null) {
            codec = LubyCodec(sliceSize = block.data.size)
            state = codec!!.createDecodingState(block)
        }

        val currentState = state!!
        val complete = codec!!.processBlock(currentState, block)

        return if (complete) {
            val assembledData = codec!!.assembleData(currentState)
            // compressedSize приходит СЫРЫМ из отсканированного кадра (parsePayload) и до этой
            // проверки нигде не ограничивался — в отличие от соседнего degree. Подделанный или
            // просто битый QR с полем 2 ГБ приводил к OutOfMemoryError на copyOf, а отрицательное
            // значение — к NegativeArraySizeException. Ни то ни другое не ловилось: copyOf стоит
            // СНАРУЖИ try ниже, а OOM в Kotlin вообще не Exception. Итог — смерть процесса на
            // потоке анализа камеры, то есть достаточно навести сканер на чужой QR.
            // Размер не может превышать собранные данные: больше просто неоткуда взяться.
            val declared = currentState.compressedSize
            if (declared <= 0 || declared > assembledData.size) {
                reset()
                return null
            }
            val compressedData = assembledData.copyOf(declared)

            val decompressedData = try {
                decompress(compressedData)
            } catch (e: Exception) {
                null
            }

            if (decompressedData != null) {
                val checksumValid = codec!!.verifyChecksum(
                    decompressedData,
                    currentState.checksum,
                    currentState.totalBlocks,
                )
                if (checksumValid) {
                    return DecodeProgress(
                        currentState.decodedCount,
                        currentState.totalBlocks,
                        true,
                        decompressedData,
                    )
                }
            }

            val rawChecksumValid = codec!!.verifyChecksum(
                compressedData,
                currentState.checksum,
                currentState.totalBlocks,
            )
            if (rawChecksumValid) {
                DecodeProgress(currentState.decodedCount, currentState.totalBlocks, true, compressedData)
            } else {
                DecodeProgress(
                    currentState.decodedCount,
                    currentState.totalBlocks,
                    true,
                    error = "Checksum verification failed",
                )
            }
        } else {
            DecodeProgress(currentState.decodedCount, currentState.totalBlocks, false)
        }
    }

    @Synchronized
    fun reset() {
        codec = null
        state = null
        processedHashes.clear()
    }

    val progress: DecodeProgress?
        @Synchronized get() = state?.let {
            DecodeProgress(it.decodedCount, it.totalBlocks, it.decodedCount == it.totalBlocks)
        }

    private fun parsePayload(payload: ByteArray): LubyCodec.EncodedBlock? {
        if (payload.size < 16) return null

        var offset = 0
        val degree = payload.readIntLE(offset)
        offset += 4

        if (degree <= 0 || payload.size < 4 + 4 * degree + 12) return null

        val indices = IntArray(degree) {
            val idx = payload.readIntLE(offset)
            offset += 4
            idx
        }

        val totalBlocks = payload.readIntLE(offset)
        offset += 4

        val compressedSize = payload.readIntLE(offset)
        offset += 4

        val checksum = payload.readIntLE(offset).toLong() and 0xFFFFFFFFL
        offset += 4

        if (offset > payload.size) return null

        val data = payload.copyOfRange(offset, payload.size)

        return LubyCodec.EncodedBlock(degree, indices, totalBlocks, compressedSize, checksum, data)
    }

    private fun decompress(data: ByteArray): ByteArray? {
        inflater.reset()
        inflater.setInput(data)
        outputBuffer.reset()

        // Выход был только по finished() либо (count==0 && needsInput()). Если zlib-поток объявляет
        // флаг FDICT (нужен предустановленный словарь), то inflate() возвращает 0, needsDictionary()
        // истинно, а needsInput() ЛОЖНО и finished() ложно — цикл крутился ВЕЧНО на 100% CPU.
        // Исключения при этом нет, поэтому catch у вызывающего бесполезен, а сам цикл выполняется
        // внутри synchronized(qrsLock) на потоке анализа камеры: сканер умирал насовсем до
        // перезапуска приложения. Достаточно навести камеру на подделанный QR.
        // Плюс жёсткий предел на объём: испорченный поток не должен раздувать буфер без границ.
        while (!inflater.finished()) {
            if (inflater.needsDictionary()) return null
            val count = inflater.inflate(decompressBuffer)
            if (count == 0 && (inflater.needsInput() || inflater.needsDictionary())) break
            outputBuffer.write(decompressBuffer, 0, count)
            if (outputBuffer.size() > MAX_DECOMPRESSED_BYTES) return null
        }

        return outputBuffer.toByteArray()
    }
}
