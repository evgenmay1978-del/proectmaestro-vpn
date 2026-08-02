package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class LivingEyeLayerGeometryTest {

    /**
     * ⛔ КОНТРАКТ ИЗМЕНЁН 31.07.2026 по решению владельца («глаз сам стал меньше»).
     *
     * Было: слой целиком вписывался в бронзовый отступ, `scale = 0.5258427`, границы лежали внутри
     * канвы. Ценой этого глаз терял 16.8% стороны и 30.8% площади, и владелец это увидел.
     *
     * Стало: масштаб снова прямой (`medallion / 822.5`), слой НАМЕРЕННО вылезает за канву на
     * 21.34 px с каждой стороны, а лишнее прячет `clipPath` по кольцу в LivingEyeMedallion.
     * Поэтому старые проверки `bounds.left > 0` / `bounds.right < 520` здесь были бы прямо
     * противоположны задуманному — их заменили на «слой перекрывает клип-круг по горизонтали».
     */
    @Test
    fun stateRectangleKeepsDirectSceneMappingAndOverlapsTheBronzeRing() {
        val fit = fitLivingEyeLayer(width = 520f, height = 520f)
        val bounds = fit.stateBounds
        val inset = livingEyeBronzeInset(520f, 520f)

        assertEquals(26f, inset, 0.001f)
        assertEquals(-21.33739f, bounds.left, 0.001f)
        assertEquals(59.27052f, bounds.top, 0.001f)
        assertEquals(541.33739f, bounds.right, 0.001f)
        assertEquals(460.72948f, bounds.bottom, 0.001f)
        assertEquals(260f, bounds.centerX, 0.001f)
        assertEquals(260f, bounds.centerY, 0.001f)
        assertEquals(-166.74772f, fit.translationX, 0.001f)
        assertEquals(-411.73252f, fit.translationY, 0.001f)
        assertEquals(fit.mapSourceLengthX(100f), fit.mapSourceLengthY(100f), 0.000001f)

        // Прямой маппинг кадра владельца — ЭТО и есть защита от повторного «глаз стал меньше».
        // Любое второе масштабирование сразу уронит эту строку.
        assertEquals(520f / 822.5f, fit.scale, 0.000001f)

        // По горизонтали слой обязан перекрывать клип-круг, иначе по краям кольца была бы дыра.
        assertTrue(bounds.left < inset)
        assertTrue(bounds.right > 520f - inset)
    }

    /**
     * Сверху и снизу слой клип-круг НЕ перекрывает (кадр 890x635 шире, чем выше), и это НЕ дефект:
     * масштаб совпадает с прежним запечённым глазом (сам `mobile_home_scene.webp` удалён в
     * c15b4e3), поэтому в непокрытых
     * сегментах круга видно ту же самую картинку — шва не возникает. Именно ради этого совпадения
     * прямой маппинг и восстановлен. ⛔ Если кто-то снова введёт второй масштаб, регистрация с
     * фоном сломается и сегменты станут заметны — тест ниже это зафиксирует.
     */
    @Test
    fun verticalGapsAreCoveredByTheBakedSceneBecauseMappingMatchesIt() {
        val fit = fitLivingEyeLayer(width = 520f, height = 520f)
        val inset = livingEyeBronzeInset(520f, 520f)

        assertTrue(fit.stateBounds.top > inset)
        assertTrue(fit.stateBounds.bottom < 520f - inset)
        assertEquals(520f / 822.5f, fit.scale, 0.000001f)
    }

    @Test
    fun transformedStateRectangleCoversEntireStaticOpenEyeAperture() {
        val fit = fitLivingEyeLayer(width = 520f, height = 520f)
        val transformedApertureBounds = fit.mapSourceBounds(
            left = 388f,
            top = 957f,
            right = 957f,
            bottom = 1174f,
        )
        assertEquals(78.55319f, transformedApertureBounds.left, 0.001f)
        assertEquals(193.30091f, transformedApertureBounds.top, 0.001f)
        assertEquals(438.28571f, transformedApertureBounds.right, 0.001f)
        assertEquals(330.49240f, transformedApertureBounds.bottom, 0.001f)

        // ⛔ ЛОВУШКА: здесь когда-то стояли литералы от давно мёртвого масштаба, и сторож был
        // зелёным всегда. Сравниваем с апертурой, выведенной из того же fit — тогда проверка
        // означает то, что называет: прямоугольник состояния накрывает прорезь, и фон-основа
        // не светится по краям.
        assertTrue(fit.stateBounds.left <= transformedApertureBounds.left)
        assertTrue(fit.stateBounds.top <= transformedApertureBounds.top)
        assertTrue(fit.stateBounds.right >= transformedApertureBounds.right)
        assertTrue(fit.stateBounds.bottom >= transformedApertureBounds.bottom)
    }

    @Test
    fun integrationProfileAddsOcclusionWithoutChangingEyeRegistration() {
        val fit = fitLivingEyeLayer(width = 520f, height = 520f)
        val profile = livingEyeIntegrationProfile(width = 520f, height = 520f)

        assertEquals(fit.scale, profile.fitScale, 0.000001f)
        assertEquals(fit.stateBounds, profile.stateBounds)
        assertEquals(3f, profile.eyelidContactShadowBlurPx, 0.001f)
        assertEquals(0.18f, profile.eyelidContactShadowAlpha, 0f)
        assertTrue(profile.eyelidContactShadowBlurPx < livingEyeBronzeInset(520f, 520f))
    }

    @Test
    fun apertureUsesOneCommonGridAndClosesToZeroHeight() {
        val fit = fitLivingEyeLayer(520f, 520f)
        val open = livingEyeApertureContour(fit, closure = 0f, seamOverlapPx = 0f)
        val half = livingEyeApertureContour(fit, closure = 0.5f, seamOverlapPx = 0f)
        val closed = livingEyeApertureContour(fit, closure = 1f, seamOverlapPx = 0f)

        assertEquals(open.upper.size, open.lower.size)
        assertEquals(open.upper.map { it.x }, open.lower.map { it.x })
        assertEquals(open.upper.map { it.x }, half.upper.map { it.x })
        assertEquals(open.upper.map { it.x }, half.lower.map { it.x })
        assertEquals(open.upper.map { it.x }, closed.upper.map { it.x })
        assertEquals(open.upper.map { it.x }, closed.lower.map { it.x })

        open.upper.indices.forEach { index ->
            val openHeight = open.lower[index].y - open.upper[index].y
            val halfHeight = half.lower[index].y - half.upper[index].y
            val closedHeight = closed.lower[index].y - closed.upper[index].y
            assertEquals(openHeight * 0.5f, halfHeight, 0.001f)
            assertEquals(0f, closedHeight, 0.001f)
        }
    }

    @Test
    fun upperLidTravelsSeventyPercentAndLowerLidThirtyPercent() {
        val fit = fitLivingEyeLayer(520f, 520f)
        val open = livingEyeApertureContour(fit, closure = 0f, seamOverlapPx = 0f)
        val closed = livingEyeApertureContour(fit, closure = 1f, seamOverlapPx = 0f)
        val sampleX = fit.mapSourceX(700f)
        val openUpper = open.upper.first { kotlin.math.abs(it.x - sampleX) < 0.001f }
        val openLower = open.lower.first { kotlin.math.abs(it.x - sampleX) < 0.001f }
        val closedUpper = closed.upper.first { kotlin.math.abs(it.x - sampleX) < 0.001f }
        val closedLower = closed.lower.first { kotlin.math.abs(it.x - sampleX) < 0.001f }
        val expectedSeamY = openUpper.y + (openLower.y - openUpper.y) * 0.70f

        assertEquals(expectedSeamY, closedUpper.y, 0.001f)
        assertEquals(expectedSeamY, closedLower.y, 0.001f)
        assertEquals(520f / 822.5f, fit.scale, 0.000001f)
    }
}
