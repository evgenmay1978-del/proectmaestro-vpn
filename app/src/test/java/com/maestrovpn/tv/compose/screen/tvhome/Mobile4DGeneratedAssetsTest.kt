package com.maestrovpn.tv.compose.screen.tvhome

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class Mobile4DGeneratedAssetsTest {
    @Test
    fun manifestUsesTheMasterCanvasAndApprovedLayerOrder() {
        assertEquals(2160, Mobile4DGeneratedAssets.masterWidth)
        assertEquals(4670, Mobile4DGeneratedAssets.masterHeight)
        assertEquals(
            listOf("wood", "console", "contacts", "frame", "cartouche", "vines", "arc", "ring"),
            Mobile4DGeneratedAssets.layerZOrder,
        )
        assertEquals(
            "runtime Home compositor must draw every generated layer exactly once",
            Mobile4DGeneratedAssets.layerZOrder,
            mobile4DHomeLayerOrder,
        )
        assertEquals(
            Mobile4DGeneratedAssets.layerZOrder.toSet(),
            Mobile4DGeneratedAssets.fragments.map { it.layer }.toSet(),
        )
    }

    @Test
    fun pagesAndFragmentSourcesStayInsideTheAtlasLimit() {
        val pagesByPath = Mobile4DGeneratedAssets.pages.associateBy { it.path }

        Mobile4DGeneratedAssets.pages.forEach { page ->
            assertTrue(page.width in 1..2048)
            assertTrue(page.height in 1..2048)
        }
        Mobile4DGeneratedAssets.fragments.forEach { fragment ->
            val page = checkNotNull(pagesByPath[fragment.pagePath])
            assertEquals(fragment.light, page.light)
            assertEquals(fragment.pageIndex, page.pageIndex)
            assertTrue(fragment.sourceRect.x >= fragment.gutter)
            assertTrue(fragment.sourceRect.y >= fragment.gutter)
            assertTrue(
                fragment.sourceRect.x + fragment.sourceRect.width + fragment.gutter <= page.width,
            )
            assertTrue(
                fragment.sourceRect.y + fragment.sourceRect.height + fragment.gutter <= page.height,
            )
        }
    }

    @Test
    fun lightVariantsUseDistinctPathsWithIdenticalGeometry() {
        val pageLayouts = Mobile4DGeneratedAssets.pages
            .groupBy { it.light }
            .mapValues { (_, pages) ->
                pages.sortedBy { it.pageIndex }.map { page ->
                    Triple(page.pageIndex, page.width, page.height)
                }
            }
        val expectedPageLayout = checkNotNull(pageLayouts[Mobile4DAssetLight.Left])

        Mobile4DAssetLight.entries.forEach { light ->
            assertEquals(expectedPageLayout, pageLayouts[light])
        }
        Mobile4DGeneratedAssets.fragments.groupBy { it.id }.forEach { (id, variants) ->
            assertEquals(id, 3, variants.size)
            assertEquals(Mobile4DAssetLight.entries.toSet(), variants.map { it.light }.toSet())
            assertEquals(3, variants.map { it.pagePath }.toSet().size)
            assertEquals(1, variants.map { it.pageIndex }.toSet().size)
            assertEquals(1, variants.map { it.sourceRect }.toSet().size)
            assertEquals(1, variants.map { it.sceneRect }.toSet().size)
            assertEquals(1, variants.map { it.layer }.toSet().size)
            assertEquals(1, variants.map { it.zOrder }.toSet().size)
        }
    }

    @Test
    fun everyFragmentCarriesAnExactTwoPixelExtrudedGutter() {
        Mobile4DGeneratedAssets.fragments.forEach { fragment ->
            assertEquals(fragment.id, 2, fragment.gutter)
        }
    }

    @Test
    fun fragmentsCanReconstructEveryLayerInSceneCoordinates() {
        Mobile4DAssetLight.entries.forEach { light ->
            val fragments = Mobile4DGeneratedAssets.fragments.filter { it.light == light }

            Mobile4DGeneratedAssets.layerZOrder.forEachIndexed { zOrder, layer ->
                val layerFragments = fragments.filter { it.layer == layer }
                assertTrue("$light/$layer has no fragments", layerFragments.isNotEmpty())
                layerFragments.forEach { fragment ->
                    assertEquals(zOrder, fragment.zOrder)
                    assertEquals(fragment.sceneRect.width, fragment.sourceRect.width)
                    assertEquals(fragment.sceneRect.height, fragment.sourceRect.height)
                    assertTrue(fragment.sceneRect.x >= 0)
                    assertTrue(fragment.sceneRect.y >= 0)
                    assertTrue(
                        fragment.sceneRect.x + fragment.sceneRect.width <=
                            Mobile4DGeneratedAssets.masterWidth,
                    )
                    assertTrue(
                        fragment.sceneRect.y + fragment.sceneRect.height <=
                            Mobile4DGeneratedAssets.masterHeight,
                    )
                }
                layerFragments.forEachIndexed { index, fragment ->
                    layerFragments.drop(index + 1).forEach { other ->
                        assertTrue(
                            "$light/$layer fragments ${fragment.id} and ${other.id} overlap",
                            !overlaps(fragment.sceneRect, other.sceneRect),
                        )
                    }
                }
            }

            val woodArea = fragments
                .filter { it.layer == "wood" }
                .sumOf { it.sceneRect.width.toLong() * it.sceneRect.height }
            assertEquals(
                Mobile4DGeneratedAssets.masterWidth.toLong() * Mobile4DGeneratedAssets.masterHeight,
                woodArea,
            )
        }
    }

    private fun overlaps(first: Mobile4DAssetRect, second: Mobile4DAssetRect): Boolean =
        first.x < second.x + second.width &&
            first.x + first.width > second.x &&
            first.y < second.y + second.height &&
            first.y + first.height > second.y
}
