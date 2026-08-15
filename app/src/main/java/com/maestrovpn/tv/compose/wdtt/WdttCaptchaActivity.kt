package com.maestrovpn.tv.compose.wdtt

import android.annotation.SuppressLint
import android.app.Activity
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.webkit.JavascriptInterface
import android.webkit.SafeBrowsingResponse
import android.webkit.SslErrorHandler
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import android.net.http.SslError
import com.maestrovpn.tv.bg.WdttManager
import com.maestrovpn.tv.utils.DeviceFormFactor

internal class WdttCaptchaActivity : Activity() {
    private var requestId = -1L
    private var delivered = false
    private var webView: WebView? = null
    private val timeoutHandler = Handler(Looper.getMainLooper())
    private val timeout = Runnable { finishWith(WdttCaptchaResult.Timeout) }

    @SuppressLint("SetJavaScriptEnabled")
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P || DeviceFormFactor.isTelevision(this)) {
            finishWith(WdttCaptchaResult.Cancelled)
            return
        }
        requestId = intent.getLongExtra(EXTRA_REQUEST_ID, -1L)
        val redirect = intent.getStringExtra(EXTRA_REDIRECT_URI).orEmpty()
        if (requestId < 0L || !WdttCaptchaPolicy.isAllowedTopLevel(redirect)) {
            finishWith(WdttCaptchaResult.Cancelled)
            return
        }

        WebView.setWebContentsDebuggingEnabled(false)
        val view = WebView(this)
        webView = view
        setContentView(view)
        view.settings.apply {
            javaScriptEnabled = true
            allowFileAccess = false
            allowContentAccess = false
            databaseEnabled = false
            domStorageEnabled = true
            javaScriptCanOpenWindowsAutomatically = false
            setSupportMultipleWindows(false)
            mixedContentMode = android.webkit.WebSettings.MIXED_CONTENT_NEVER_ALLOW
            safeBrowsingEnabled = true
        }
        view.setDownloadListener { _, _, _, _, _ -> finishWith(WdttCaptchaResult.Cancelled) }
        view.webChromeClient = object : WebChromeClient() {
            override fun onCreateWindow(
                view: WebView?,
                isDialog: Boolean,
                isUserGesture: Boolean,
                resultMsg: android.os.Message?,
            ): Boolean = false
        }
        view.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView?, request: WebResourceRequest?): Boolean =
                rejectUnlessAllowed(request?.url?.toString().orEmpty())

            @Deprecated("Deprecated in Android")
            override fun shouldOverrideUrlLoading(view: WebView?, url: String?): Boolean =
                rejectUnlessAllowed(url.orEmpty())

            override fun onPageStarted(view: WebView?, url: String?, favicon: android.graphics.Bitmap?) {
                if (rejectUnlessAllowed(url.orEmpty())) return
                super.onPageStarted(view, url, favicon)
            }

            override fun onPageFinished(view: WebView?, url: String?) {
                if (url != null && WdttCaptchaPolicy.isAllowedTopLevel(url)) {
                    view?.evaluateJavascript(CAPTCHA_INTERCEPTOR, null)
                }
            }

            override fun onReceivedSslError(view: WebView?, handler: SslErrorHandler?, error: SslError?) {
                handler?.cancel()
                finishWith(WdttCaptchaResult.Cancelled)
            }

            override fun onReceivedError(
                view: WebView?,
                request: WebResourceRequest?,
                error: WebResourceError?,
            ) {
                if (request?.isForMainFrame == true) finishWith(WdttCaptchaResult.Cancelled)
            }

            override fun onSafeBrowsingHit(
                view: WebView?,
                request: WebResourceRequest?,
                threatType: Int,
                callback: SafeBrowsingResponse?,
            ) {
                callback?.backToSafety(true)
                finishWith(WdttCaptchaResult.Cancelled)
            }
        }
        view.addJavascriptInterface(CaptchaBridge(), BRIDGE_NAME)
        timeoutHandler.postDelayed(timeout, CAPTCHA_TIMEOUT_MS)
        view.loadUrl(redirect)
    }

    override fun onBackPressed() {
        finishWith(WdttCaptchaResult.Cancelled)
    }

    override fun onDestroy() {
        timeoutHandler.removeCallbacks(timeout)
        if (!delivered && requestId >= 0L) {
            delivered = true
            WdttManager.submitCaptchaResult(requestId, WdttCaptchaResult.Cancelled)
        }
        webView?.let { view ->
            view.removeJavascriptInterface(BRIDGE_NAME)
            view.stopLoading()
            view.loadUrl("about:blank")
            view.clearHistory()
            view.removeAllViews()
            view.destroy()
        }
        webView = null
        super.onDestroy()
    }

    private fun rejectUnlessAllowed(url: String): Boolean {
        if (WdttCaptchaPolicy.isAllowedTopLevel(url)) return false
        finishWith(WdttCaptchaResult.Cancelled)
        return true
    }

    private fun finishWith(result: WdttCaptchaResult) {
        if (delivered) return
        delivered = true
        timeoutHandler.removeCallbacks(timeout)
        if (requestId >= 0L) WdttManager.submitCaptchaResult(requestId, result)
        finish()
    }

    private inner class CaptchaBridge {
        @JavascriptInterface
        fun successToken(value: String) {
            val safe = WdttCaptchaPolicy.sanitizeSuccessToken(value) ?: return
            runOnUiThread { finishWith(WdttCaptchaResult.Success(safe)) }
        }
    }

    companion object {
        internal const val EXTRA_REQUEST_ID = "wdtt.captcha.request_id"
        internal const val EXTRA_REDIRECT_URI = "wdtt.captcha.redirect_uri"
        private const val BRIDGE_NAME = "MaestroWdttCaptcha"
        private const val CAPTCHA_TIMEOUT_MS = 120_000L
        private val CAPTCHA_INTERCEPTOR = """
            (function() {
              if (window.__maestroWdttCaptchaInstalled) return;
              window.__maestroWdttCaptchaInstalled = true;
              function inspect(value) {
                if (!value || typeof value !== 'object') return;
                if (typeof value.success_token === 'string') {
                  MaestroWdttCaptcha.successToken(value.success_token);
                }
              }
              var originalFetch = window.fetch;
              if (originalFetch) {
                window.fetch = function() {
                  return originalFetch.apply(this, arguments).then(function(response) {
                    try { response.clone().json().then(inspect).catch(function() {}); } catch (_) {}
                    return response;
                  });
                };
              }
              var originalOpen = XMLHttpRequest.prototype.open;
              var originalSend = XMLHttpRequest.prototype.send;
              XMLHttpRequest.prototype.open = function() {
                return originalOpen.apply(this, arguments);
              };
              XMLHttpRequest.prototype.send = function() {
                this.addEventListener('load', function() {
                  try { inspect(JSON.parse(this.responseText)); } catch (_) {}
                });
                return originalSend.apply(this, arguments);
              };
            })();
        """.trimIndent()
    }
}
