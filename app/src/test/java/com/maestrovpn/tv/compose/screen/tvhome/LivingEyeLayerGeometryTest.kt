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
        assertEquals(18.2f, profile.innerOcclusionWidthPx, 0.001f)
        assertEquals(0.36f, profile.innerOcclusionAlpha, 0f)
        assertEquals(7.8f, profile.eyelidContactShadowBlurPx, 0.001f)
        assertEquals(0.24f, profile.eyelidContactShadowAlpha, 0f)
        assertTrue(profile.innerOcclusionWidthPx < livingEyeBronzeInset(520f, 520f))
        assertTrue(profile.eyelidContactShadowBlurPx < profile.innerOcclusionWidthPx)
    }
}
