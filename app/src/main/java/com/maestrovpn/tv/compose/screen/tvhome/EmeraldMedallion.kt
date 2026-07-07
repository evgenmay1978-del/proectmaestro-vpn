package com.maestrovpn.tv.compose.screen.tvhome

import android.graphics.BitmapFactory
import android.graphics.BitmapShader
import android.graphics.RuntimeShader
import android.graphics.Shader
import android.os.Build
import androidx.annotation.RequiresApi
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawWithCache
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ShaderBrush
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.unit.dp
import com.maestrovpn.tv.R
import com.maestrovpn.tv.compose.rememberIsLowRam

// Live jewel-grade EMERALD medallion (phone connect button). Two material states driven by the
// VPN connection: solid polished crystal (OFF) <-> thick boiling emerald magma (CONNECTED), with a
// black spider silhouette sealed in the depth. Motion comes ONLY from viscous fluid physics +
// material shading (env reflection / refraction / inner heat) — NO drawn patterns.
//
// Tier-A (API33+, non-low-RAM): a live AGSL RuntimeShader, single-pass, animated every frame.
// Tier-B (API<33 OR low-RAM): a baked still image (emerald_still) — no animation, never crashes.
// TV keeps the lightweight SpiderMedallion; this is phone-only. Nothing here touches the VPN engine.

// AGSL ported 1:1 from the S2-validated GLSL (scratchpad/emerald_analytic.frag). reflect/refract are
// hand-rolled (not reliably present in AGSL); no texture()/sampler2D (uses `uniform shader` + .eval).
private const val EMERALD_AGSL = """
uniform float2 iResolution;
uniform float  iTime;
uniform float  uMolten;
uniform float  uFreeze;
uniform float2 uFinger;
uniform float  uFingerAmt;
uniform shader uSpider;
uniform float2 uSpiderRes;

float h11(float n){ return fract(sin(n)*43758.5453); }
float2 h22(float n){ return float2(h11(n), h11(n+7.13)); }
float vn(float2 x){
  float2 p=floor(x), f=fract(x); f=f*f*(3.0-2.0*f);
  return mix(mix(h11(dot(p,float2(1.0,57.0))),        h11(dot(p+float2(1,0),float2(1.0,57.0))), f.x),
             mix(h11(dot(p+float2(0,1),float2(1.0,57.0))), h11(dot(p+float2(1,1),float2(1.0,57.0))), f.x), f.y);
}
float fbm(float2 p){ float s=0.0, a=0.5; for(int i=0;i<4;i++){ s+=a*vn(p); p=p*2.03+1.7; a*=0.5; } return s; }
float3 refl3(float3 I, float3 N){ return I-2.0*dot(N,I)*N; }
float3 refr3(float3 I, float3 N, float eta){
  float ni=dot(N,I);
  float k=1.0-eta*eta*(1.0-ni*ni);
  if(k<0.0) return float3(0.0);
  return eta*I-(eta*ni+sqrt(k))*N;
}
float heightAt(float2 uv, float t){
  float H=0.0;
  for(int i=0;i<5;i++){
    float fi=float(i);
    float period=3.2+h11(fi*1.7)*2.4;
    float k=(t + h11(fi*3.1)*period)/period;
    float ph=fract(k);
    float2 c=(h22(fi*5.3+floor(k))*2.0-1.0)*0.50;
    float rad=0.09+h11(fi*2.2)*0.07;
    float d=length(uv-c);
    float g=exp(-d*d/(rad*rad));
    float dome=smoothstep(0.0,0.62,ph)*(1.0-smoothstep(0.62,0.82,ph));
    H+=dome*0.085*g;
  }
  H+=0.018*sin(uv.x*3.0-t*0.6)*sin(uv.y*2.6+t*0.5);
  H+=0.005*(sin(uv.x*9.0+t*1.3)+sin(uv.y*8.0-t*1.1));
  float2 df=uv-uFinger; float fg=exp(-dot(df,df)/0.05);
  H+=-0.05*fg*uFingerAmt;
  return H;
}
float activityAt(float2 uv, float t){
  float A=0.0;
  for(int i=0;i<5;i++){
    float fi=float(i);
    float period=3.2+h11(fi*1.7)*2.4;
    float k=(t + h11(fi*3.1)*period)/period;
    float ph=fract(k);
    float2 c=(h22(fi*5.3+floor(k))*2.0-1.0)*0.50;
    float rad=0.09+h11(fi*2.2)*0.07;
    float d=length(uv-c);
    float g=exp(-d*d/(rad*rad));
    float dome=smoothstep(0.0,0.62,ph)*(1.0-smoothstep(0.62,0.82,ph));
    float burst=smoothstep(0.60,0.66,ph)*(1.0-smoothstep(0.66,0.80,ph));
    A+=(dome*0.5+burst*1.6)*g;
  }
  return clamp(A,0.0,1.0);
}
float3 env(float3 d){
  float up=d.y*0.5+0.5;
  float3 base=mix(float3(0.006,0.010,0.016), float3(0.030,0.052,0.070), up);
  float3 K=normalize(float3(-0.24,0.76,0.60));
  float3 F=normalize(float3( 0.60,0.10,0.79));
  float3 T=normalize(float3( 0.05,-0.60,0.80));
  float3 W=normalize(float3( 0.68,-0.22,0.70));
  base+=pow(max(dot(d,K),0.0),190.0)*float3(7.2,7.1,6.7);
  base+=pow(max(dot(d,K),0.0),22.0) *float3(0.45,0.62,0.66);
  base+=pow(max(dot(d,F),0.0),40.0) *float3(1.0,1.5,1.7);
  base+=pow(max(dot(d,T),0.0),80.0) *float3(1.2,2.2,1.5);
  base+=pow(max(dot(d,W),0.0),55.0) *float3(2.0,1.1,0.4);
  return base;
}
half4 main(float2 fragCoord){
  float2 uv=(fragCoord/iResolution)*2.0-1.0; uv.y=-uv.y;
  float r2=dot(uv,uv); if(r2>1.0){ return half4(0.0); }
  float r=sqrt(r2), h=sqrt(max(1.0-r2,0.0)); float t=iTime;
  float3 V=float3(0.0,0.0,1.0);
  float front=1.06-uFreeze*1.12;
  float lmol=clamp(uMolten,0.0,1.0)*(1.0-smoothstep(front-0.14,front+0.02,r));
  float DOME=1.35; float3 Ndome=normalize(float3(uv, max(h,1e-3)*DOME));
  float e=1.6/iResolution.x;
  float hx=heightAt(uv+float2(e,0.0),t)-heightAt(uv-float2(e,0.0),t);
  float hy=heightAt(uv+float2(0.0,e),t)-heightAt(uv-float2(0.0,e),t);
  float micro=(fbm(uv*70.0)-0.5)*0.006;
  float3 Nfluid=normalize(Ndome+float3(-hx,-hy,0.0)*130.0+float3(-micro,-micro,0.0)*5.0);
  float3 N=normalize(mix(Ndome,Nfluid,lmol));
  float NdV=clamp(dot(N,V),0.0,1.0);
  float fres=0.03+0.97*pow(1.0-NdV,5.0);
  float3 Tr=refr3(-V,N,1.0/1.55);
  float depth=1.6*h+0.28; float2 inUV=uv+Tr.xy*(0.55*depth);
  float2 spuv=1.0-(inUV*0.5+0.5); spuv=0.5+(spuv-0.5)*1.46;
  float sp = uSpider.eval(clamp(spuv,0.0,1.0)*uSpiderRes).r;
  float spB=(uSpider.eval(clamp(spuv+float2(0.010,0.0),0.0,1.0)*uSpiderRes).r
            +uSpider.eval(clamp(spuv-float2(0.010,0.0),0.0,1.0)*uSpiderRes).r
            +uSpider.eval(clamp(spuv+float2(0.0,0.010),0.0,1.0)*uSpiderRes).r
            +uSpider.eval(clamp(spuv-float2(0.0,0.010),0.0,1.0)*uSpiderRes).r)*0.25;
  float spider=clamp(max(sp,spB*0.9),0.0,1.0);
  float halo=uSpider.eval(clamp(spuv,0.0,1.0)*uSpiderRes).g;
  float3 absorb=exp(-depth*float3(3.4,0.55,2.3));
  float3 lit=env(refr3(-V,N,1.0/1.55));
  float3 body=lit*absorb*float3(0.22,1.05,0.52);
  body+=float3(0.02,0.30,0.15)*pow(clamp(1.0-r*0.92,0.0,1.0),1.6);
  body*=0.86+0.26*fbm(inUV*3.4);
  float darkMass=0.30+0.10*halo; body*=mix(1.0,darkMass,spider);
  float rim=clamp(halo-spider,0.0,1.0);
  body+=rim*float3(0.10,0.55,0.28)*0.9; body+=rim*float3(0.06,0.02,0.14)*0.5;
  float3 refl=env(refl3(-V,N));
  float3 disp=float3(env(refl3(-V,normalize(N+float3(0.03,0.0,0.0)))).r, refl.g,
                     env(refl3(-V,normalize(N-float3(0.03,0.0,0.0)))).b);
  refl=mix(refl,disp,pow(1.0-NdV,2.0)*0.7);
  float heat=activityAt(uv,t)*lmol;
  float3 hotcol=mix(float3(0.07,0.45,0.17),float3(0.72,0.86,0.20),heat);
  float3 emission=hotcol*heat*heat*(0.40+0.60*h)*0.75;
  emission+=float3(0.30,0.55,0.10)*pow(clamp(1.0-r*0.9,0.0,1.0),1.4)*lmol*0.25;
  float3 col=mix(body,refl,fres)+emission;
  float3 Ldir=normalize(float3(-0.24,0.76,0.60)); float3 Hn=normalize(Ldir+V);
  col+=pow(max(dot(N,Hn),0.0),360.0)*float3(1.0,1.0,0.97)*(1.25-0.6*lmol);
  float ray=smoothstep(0.16,0.0,abs(dot(uv,float2(cos(t*0.12),sin(t*0.12)))-0.10*sin(t*0.09)));
  col+=ray*float3(0.06,0.14,0.09)*(1.0-clamp(uMolten,0.0,1.0))*h;
  float girdle=smoothstep(0.86,0.965,r)*(1.0-smoothstep(0.965,1.0,r));
  col+=girdle*float3(0.14,0.34,0.24)*0.7;
  col+=pow(1.0-NdV,3.0)*float3(0.10,0.22,0.16)*0.5; col*=0.80+0.20*h;
  col=(col*(2.51*col+0.03))/(col*(2.43*col+0.59)+0.14);
  col=pow(clamp(col,0.0,1.0),float3(0.94));
  float a=smoothstep(1.0,0.982,r);
  return half4(half3(col*a), half(a));
}
"""

@Composable
fun EmeraldMedallion(
    connected: Boolean,
    onToggle: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val ctx = LocalContext.current
    val lowRam = rememberIsLowRam()
    val canAgsl = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU && !lowRam

    // solid crystal (0) <-> boiling magma (1); heavy, slow material transition
    val molten by animateFloatAsState(
        if (connected) 1f else 0f,
        tween(1400, easing = FastOutSlowInEasing),
        label = "molten",
    )

    // Spider silhouette (R=shape, G=halo) as a child `uniform shader`. Decoded once.
    val spider = remember {
        runCatching {
            val bmp = BitmapFactory.decodeResource(ctx.resources, R.drawable.emerald_spider)
            Pair(BitmapShader(bmp, Shader.TileMode.CLAMP, Shader.TileMode.CLAMP), bmp)
        }.getOrNull()
    }
    // Create AND fully validate the shader once: set EVERY uniform + the input shader now, so a
    // missing/typo uniform name throws HERE (caught → still fallback) instead of crashing later in
    // the per-frame draw phase. Constant uniforms (finger/freeze/spider) persist for the lifetime.
    val shader = remember(spider) {
        if (!canAgsl || spider == null) return@remember null
        runCatching {
            val s = RuntimeShader(EMERALD_AGSL)
            s.setFloatUniform("iResolution", 100f, 100f)
            s.setFloatUniform("iTime", 0f)
            s.setFloatUniform("uMolten", 0f)
            s.setFloatUniform("uFreeze", 0f)
            s.setFloatUniform("uFinger", -10f, -10f)
            s.setFloatUniform("uFingerAmt", 0f)
            s.setFloatUniform("uSpiderRes", spider.second.width.toFloat(), spider.second.height.toFloat())
            s.setInputShader("uSpider", spider.first)
            s
        }.getOrNull()
    }

    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier.size(252.dp),
    ) {
        if (shader != null) {
            EmeraldShaderDisc(shader, molten, Modifier.size(232.dp).clip(CircleShape))
        } else {
            EmeraldStillDisc(Modifier.size(232.dp).clip(CircleShape))
        }

        // transparent tap target — identical to SpiderMedallion; toggles the VPN via onToggle
        Button(
            onClick = onToggle,
            shape = CircleShape,
            contentPadding = PaddingValues(0.dp),
            colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = Color.White),
            elevation = ButtonDefaults.buttonElevation(defaultElevation = 0.dp, pressedElevation = 0.dp),
            modifier = Modifier.size(200.dp),
            content = {},
        )
    }
}

@RequiresApi(Build.VERSION_CODES.TIRAMISU)
@Composable
private fun EmeraldShaderDisc(
    shader: RuntimeShader,
    molten: Float,
    modifier: Modifier = Modifier,
) {
    // frame clock — the ONLY per-frame state; draw-phase reads it so only the disc redraws
    val time = remember { mutableStateOf(0f) }
    LaunchedEffect(Unit) {
        val t0 = withFrameNanos { it }
        while (true) {
            withFrameNanos { frame -> time.value = (frame - t0) / 1_000_000_000f }
        }
    }
    val brush = remember(shader) { ShaderBrush(shader) }
    Box(
        modifier.drawWithCache {
            shader.setFloatUniform("iResolution", size.width, size.height)
            onDrawBehind {
                // names validated at creation; guard anyway so the home screen can never crash
                // from a draw-phase shader error.
                runCatching {
                    shader.setFloatUniform("iTime", time.value)
                    shader.setFloatUniform("uMolten", molten)
                    drawRect(brush)
                }
            }
        },
    )
}

@Composable
private fun EmeraldStillDisc(modifier: Modifier = Modifier) {
    Image(
        painter = painterResource(R.drawable.emerald_still),
        contentDescription = null,
        modifier = modifier,
        contentScale = ContentScale.Fit,
    )
}
