package com.maestrovpn.tv.compose.wdtt

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class WdttCaptchaPolicyTest {
    @Test fun allowsOnlyExactHttpsVkIdentityHosts() {
        listOf(
            "https://id.vk.com/captcha?state=abc",
            "https://oauth.vk.com/authorize",
            "https://login.vk.com/?act=login",
            "https://vk.com/captcha.php",
            "https://m.vk.com/captcha.php",
            "https://id.vk.ru/captcha",
        ).forEach { uri -> assertTrue(uri, WdttCaptchaPolicy.isAllowedTopLevel(uri)) }
    }

    @Test fun rejectsUnsafeNavigationShapes() {
        listOf(
            "http://id.vk.com/captcha",
            "https://user@id.vk.com/captcha",
            "https://id.vk.com:443/captcha",
            "https://id.vk.com/captcha#fragment",
            "https://id.vk.com.evil.example/captcha",
            "https://evil.example/?next=id.vk.com",
            "https://%69d.vk.com/captcha",
            "https://id%2evk.com/captcha",
            "https://id.vk.com\\@evil.example/captcha",
            "https://id.vk.com/captcha\nhttps://evil.example/",
            "https:///missing-host",
        ).forEach { uri -> assertFalse(uri, WdttCaptchaPolicy.isAllowedTopLevel(uri)) }
    }

    @Test fun sanitizesOnlyBoundedControlFreeSuccessTokens() {
        assertEquals("opaque-token_123", WdttCaptchaPolicy.sanitizeSuccessToken("opaque-token_123"))
        assertNull(WdttCaptchaPolicy.sanitizeSuccessToken(""))
        assertNull(WdttCaptchaPolicy.sanitizeSuccessToken("   "))
        assertNull(WdttCaptchaPolicy.sanitizeSuccessToken("token\nSTOP"))
        assertNull(WdttCaptchaPolicy.sanitizeSuccessToken("token\u0000tail"))
        assertNull(WdttCaptchaPolicy.sanitizeSuccessToken("x".repeat(4_097)))
    }
}
