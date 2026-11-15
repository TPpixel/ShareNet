package com.tppixel.sharenet;

import android.animation.ValueAnimator;
import android.content.Context;
import android.content.res.ColorStateList;
import android.content.res.Configuration;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.graphics.Path;
import android.graphics.RectF;
import android.graphics.Typeface;
import android.graphics.drawable.Drawable;
import android.graphics.drawable.GradientDrawable;
import android.graphics.drawable.RippleDrawable;
import android.os.Build;
import android.os.SystemClock;
import android.util.TypedValue;
import android.view.Choreographer;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.view.animation.DecelerateInterpolator;
import android.widget.LinearLayout;
import android.widget.TextView;

/**
 * Material 3 Expressive (MD3E) primitives without Compose dependencies:
 * - Syncs with Android Light/Dark theme + Material You (Android 12+ / 14+ / 16) dynamic colors
 * - Wallpaper-tinted tonal surfaces (Canvas -> SurfaceLow -> Surface -> SurfaceHigh -> SurfaceHighest)
 * - Semi-implicit Euler spring physics (FastSpatial zeta=0.60 k=800, FastEffects zeta=1.0 k=3800)
 * - AOSP-inspired TouchRipple wave, official Material Symbols vector icons, and Clock/Windscribe widgets
 */
final class ExpressiveUi {
    // M3 Expressive Dynamic Tonal Roles (synced with system Light/Dark & Material You palette)
    static boolean IS_DARK = true;

    static int CANVAS = 0xFF1B1916;
    static int SURFACE_LOW = 0xFF282521;
    static int SURFACE = 0xFF24221E;
    static int SURFACE_HIGH = 0xFF282521;
    static int SURFACE_HIGHEST = 0xFF2F2C27;

    static int ON_SURFACE = Color.parseColor("#FFFFFF");
    static int ON_SURFACE_VARIANT = Color.parseColor("#BDB8B1");
    static int HINT_TEXT = Color.parseColor("#8A8594");

    static int PRIMARY = 0xFFEDEEBF;
    static int ON_PRIMARY = 0xFF4D4F2C;
    static int PRIMARY_CONTAINER = 0xFF7C7465;
    static int ON_PRIMARY_CONTAINER = Color.parseColor("#FFFFFF");

    static int SECONDARY_CONTAINER = 0xFF79746D;
    static int ON_SECONDARY_CONTAINER = Color.parseColor("#FFFFFF");

    static int PILL_UNSELECTED_BG = 0xFF2F2C27;
    static int PILL_UNSELECTED_FG = 0xFFFFFFFF;
    static int PILL_SELECTED_BG = 0xFF79746D;
    static int PILL_SELECTED_FG = 0xFFFFFFFF;

    static int TERTIARY = 0xFFFCFCCD;
    static int TERTIARY_CONTAINER = 0xFFEDEEBF;
    static int ON_TERTIARY_CONTAINER = 0xFF4D4F2C;

    static int ERROR_CONTAINER = 0xFFBA573F;
    static int ON_ERROR_CONTAINER = Color.parseColor("#F9DEDC");
    static int ERROR_HERO = 0xFFF4947D;
    static int ON_ERROR_HERO = 0xFF591001;

    static int WARN_CONTAINER = Color.parseColor("#422C05");
    static int ON_WARN_CONTAINER = Color.parseColor("#FCD34D");

    private ExpressiveUi() {}

    static int syncWithTheme(Context ctx) {
        int nightMask = ctx.getResources().getConfiguration().uiMode & Configuration.UI_MODE_NIGHT_MASK;
        IS_DARK = (nightMask == Configuration.UI_MODE_NIGHT_YES);

        if (IS_DARK) {
            HINT_TEXT = Color.parseColor("#8A8594");
            WARN_CONTAINER = Color.parseColor("#422C05");
            ON_WARN_CONTAINER = Color.parseColor("#FCD34D");

            if (Build.VERSION.SDK_INT >= 34) {
                int sContainer = sysColor(ctx, android.R.color.system_surface_container_dark, 0xFF1B1916);
                int sHigh = sysColor(ctx, android.R.color.system_surface_container_high_dark, 0xFF211F1C);
                int sHighest = sysColor(ctx, android.R.color.system_surface_container_highest_dark, 0xFF282521);
                int sBright = sysColor(ctx, android.R.color.system_surface_bright_dark, 0xFF2F2C27);

                CANVAS = sContainer;
                SURFACE_LOW = sBright;
                SURFACE = lerpColor(sHigh, sHighest, 0.5f);
                SURFACE_HIGH = sHighest;
                SURFACE_HIGHEST = sBright;

                ON_SURFACE = sysColor(ctx, android.R.color.system_on_surface_dark, 0xFFFFFFFF);
                ON_SURFACE_VARIANT = sysColor(ctx, android.R.color.system_on_surface_variant_dark, 0xFFBDB8B1);

                int pr = sysColor(ctx, android.R.color.system_primary_dark, 0xFFD0C5B4);
                int onPr = sysColor(ctx, android.R.color.system_on_primary_dark, 0xFF3A3428);
                int tc = sysColor(ctx, android.R.color.system_tertiary_container_dark, 0xFFEDEEBF);
                int onTc = sysColor(ctx, android.R.color.system_on_tertiary_container_dark, 0xFF4D4F2C);
                if (isBrighterAccent(tc, pr)) {
                    PRIMARY = tc;
                    ON_PRIMARY = onTc;
                } else {
                    PRIMARY = pr;
                    ON_PRIMARY = onPr;
                }
                PRIMARY_CONTAINER = sysColor(ctx, android.R.color.system_primary_container_dark, 0xFF7C7465);
                ON_PRIMARY_CONTAINER = sysColor(ctx, android.R.color.system_on_primary_container_dark, 0xFFE5DEFF);

                SECONDARY_CONTAINER = sysColor(ctx, android.R.color.system_secondary_container_dark, 0xFF79746D);
                ON_SECONDARY_CONTAINER = sysColor(ctx, android.R.color.system_on_secondary_container_dark, 0xFFFFFFFF);

                TERTIARY = sysColor(ctx, android.R.color.system_tertiary_dark, 0xFFFCFCCD);
                TERTIARY_CONTAINER = tc;
                ON_TERTIARY_CONTAINER = onTc;

                int errDark = sysColor(ctx, android.R.color.system_error_dark, 0xFFF2B8B5);
                int errContDark = sysColor(ctx, android.R.color.system_error_container_dark, 0xFF601410);
                ERROR_CONTAINER = errContDark;
                ON_ERROR_CONTAINER = sysColor(ctx, android.R.color.system_on_error_container_dark, 0xFFF9DEDC);
                ERROR_HERO = lerpColor(errDark, errContDark, 0.16f);
                ON_ERROR_HERO = sysColor(ctx, android.R.color.system_on_error_dark, 0xFF601410);
            } else if (Build.VERSION.SDK_INT >= 31) {
                int n800 = sysColor(ctx, android.R.color.system_neutral1_800, 0xFF1B1916);
                int n700 = sysColor(ctx, android.R.color.system_neutral1_700, 0xFF282521);
                CANVAS = lerpColor(sysColor(ctx, android.R.color.system_neutral1_900, 0xFF1B1916), n800, 0.45f);
                SURFACE_LOW = lerpColor(n800, n700, 0.55f);
                SURFACE = lerpColor(CANVAS, n800, 0.55f);
                SURFACE_HIGH = lerpColor(n800, sysColor(ctx, android.R.color.system_neutral2_700, 0xFF282521), 0.55f);
                SURFACE_HIGHEST = lerpColor(n800, n700, 0.65f);

                ON_SURFACE = sysColor(ctx, android.R.color.system_neutral1_100, 0xFFE6E1E6);
                ON_SURFACE_VARIANT = sysColor(ctx, android.R.color.system_neutral2_200, 0xFFC4C0CE);

                int a1 = sysColor(ctx, android.R.color.system_accent1_200, 0xFFD0C5B4);
                int onA1 = sysColor(ctx, android.R.color.system_accent1_800, 0xFF3A3428);
                int a3 = sysColor(ctx, android.R.color.system_accent3_100, 0xFFE4E5B7);
                int onA3 = sysColor(ctx, android.R.color.system_accent3_800, 0xFF4D4E2A);
                if (isBrighterAccent(a3, a1)) {
                    PRIMARY = a3;
                    ON_PRIMARY = onA3;
                } else {
                    PRIMARY = a1;
                    ON_PRIMARY = onA1;
                }
                PRIMARY_CONTAINER = sysColor(ctx, android.R.color.system_accent1_700, 0xFF463990);
                ON_PRIMARY_CONTAINER = sysColor(ctx, android.R.color.system_accent1_100, 0xFFE5DEFF);

                SECONDARY_CONTAINER = sysColor(ctx, android.R.color.system_accent2_700, 0xFF79746D);
                ON_SECONDARY_CONTAINER = sysColor(ctx, android.R.color.system_accent2_100, 0xFFE2DEF6);

                TERTIARY = sysColor(ctx, android.R.color.system_accent3_200, 0xFF78DCB2);
                TERTIARY_CONTAINER = sysColor(ctx, android.R.color.system_accent3_700, 0xFF174B37);
                ON_TERTIARY_CONTAINER = sysColor(ctx, android.R.color.system_accent3_100, 0xFFB7F4D8);

                ERROR_CONTAINER = 0xFF601410;
                ON_ERROR_CONTAINER = 0xFFF9DEDC;
                ERROR_HERO = 0xFFF09881;
                ON_ERROR_HERO = 0xFF590E00;
            } else {
                CANVAS = 0xFF1B1916;
                SURFACE_LOW = 0xFF2F2C27;
                SURFACE = 0xFF24221E;
                SURFACE_HIGH = 0xFF282521;
                SURFACE_HIGHEST = 0xFF2F2C27;
                ON_SURFACE = 0xFFFFFFFF;
                ON_SURFACE_VARIANT = 0xFFBDB8B1;
                PRIMARY = 0xFFEDEEBF;
                ON_PRIMARY = 0xFF4D4F2C;
                PRIMARY_CONTAINER = 0xFF7C7465;
                ON_PRIMARY_CONTAINER = 0xFFE5DEFF;
                SECONDARY_CONTAINER = 0xFF79746D;
                ON_SECONDARY_CONTAINER = 0xFFFFFFFF;
                TERTIARY = 0xFFFCFCCD;
                TERTIARY_CONTAINER = 0xFFEDEEBF;
                ON_TERTIARY_CONTAINER = 0xFF4D4F2C;
                ERROR_CONTAINER = 0xFF601410;
                ON_ERROR_CONTAINER = 0xFFF9DEDC;
                ERROR_HERO = 0xFFF09881;
                ON_ERROR_HERO = 0xFF590E00;
            }
        } else {
            HINT_TEXT = Color.parseColor("#726D7C");
            WARN_CONTAINER = Color.parseColor("#FFF0C7");
            ON_WARN_CONTAINER = Color.parseColor("#6B4200");

            if (Build.VERSION.SDK_INT >= 34) {
                CANVAS = sysColor(ctx, android.R.color.system_surface_container_light, 0xFFF2EEF7);
                SURFACE_LOW = sysColor(ctx, android.R.color.system_surface_container_lowest_light, 0xFFFFFFFF);
                SURFACE = sysColor(ctx, android.R.color.system_surface_container_low_light, 0xFFF7F2FA);
                SURFACE_HIGH = sysColor(ctx, android.R.color.system_surface_container_high_light, 0xFFE7E1EE);
                SURFACE_HIGHEST = sysColor(ctx, android.R.color.system_surface_container_highest_light, 0xFFE0D9E8);

                ON_SURFACE = sysColor(ctx, android.R.color.system_on_surface_light, 0xFF1C1B20);
                ON_SURFACE_VARIANT = sysColor(ctx, android.R.color.system_on_surface_variant_light, 0xFF494552);

                PRIMARY = sysColor(ctx, android.R.color.system_primary_light, 0xFF5B4BB7);
                ON_PRIMARY = sysColor(ctx, android.R.color.system_on_primary_light, 0xFFFFFFFF);
                PRIMARY_CONTAINER = sysColor(ctx, android.R.color.system_primary_container_light, 0xFFE4DFFF);
                ON_PRIMARY_CONTAINER = sysColor(ctx, android.R.color.system_on_primary_container_light, 0xFF1A0864);

                SECONDARY_CONTAINER = sysColor(ctx, android.R.color.system_secondary_container_light, 0xFFE3DFFA);
                ON_SECONDARY_CONTAINER = sysColor(ctx, android.R.color.system_on_secondary_container_light, 0xFF1B1830);

                TERTIARY = sysColor(ctx, android.R.color.system_tertiary_light, 0xFF146C4B);
                TERTIARY_CONTAINER = sysColor(ctx, android.R.color.system_tertiary_container_light, 0xFFA4F2CE);
                ON_TERTIARY_CONTAINER = sysColor(ctx, android.R.color.system_on_tertiary_container_light, 0xFF002114);

                ERROR_CONTAINER = sysColor(ctx, android.R.color.system_error_container_light, 0xFFF9DEDC);
                ON_ERROR_CONTAINER = sysColor(ctx, android.R.color.system_on_error_container_light, 0xFF410E0B);
                ERROR_HERO = sysColor(ctx, android.R.color.system_error_light, 0xFFB3261E);
                ON_ERROR_HERO = sysColor(ctx, android.R.color.system_on_error_light, 0xFFFFFFFF);
            } else if (Build.VERSION.SDK_INT >= 31) {
                CANVAS = sysColor(ctx, android.R.color.system_neutral1_50, 0xFFF2EEF7);
                SURFACE_LOW = sysColor(ctx, android.R.color.system_neutral1_0, 0xFFFFFFFF);
                SURFACE = sysColor(ctx, android.R.color.system_neutral1_10, 0xFFF7F2FA);
                SURFACE_HIGH = sysColor(ctx, android.R.color.system_neutral2_100, 0xFFE7E1EE);
                SURFACE_HIGHEST = sysColor(ctx, android.R.color.system_neutral2_200, 0xFFDDD6E5);

                ON_SURFACE = sysColor(ctx, android.R.color.system_neutral1_900, 0xFF1C1B20);
                ON_SURFACE_VARIANT = sysColor(ctx, android.R.color.system_neutral2_700, 0xFF494552);

                PRIMARY = sysColor(ctx, android.R.color.system_accent1_600, 0xFF5B4BB7);
                ON_PRIMARY = sysColor(ctx, android.R.color.system_accent1_0, 0xFFFFFFFF);
                PRIMARY_CONTAINER = sysColor(ctx, android.R.color.system_accent1_100, 0xFFE4DFFF);
                ON_PRIMARY_CONTAINER = sysColor(ctx, android.R.color.system_accent1_900, 0xFF1A0864);

                SECONDARY_CONTAINER = sysColor(ctx, android.R.color.system_accent2_100, 0xFFE3DFFA);
                ON_SECONDARY_CONTAINER = sysColor(ctx, android.R.color.system_accent2_900, 0xFF1B1830);

                TERTIARY = sysColor(ctx, android.R.color.system_accent3_600, 0xFF146C4B);
                TERTIARY_CONTAINER = sysColor(ctx, android.R.color.system_accent3_100, 0xFFA4F2CE);
                ON_TERTIARY_CONTAINER = sysColor(ctx, android.R.color.system_accent3_900, 0xFF002114);

                ERROR_CONTAINER = 0xFFF9DEDC;
                ON_ERROR_CONTAINER = 0xFF410E0B;
                ERROR_HERO = 0xFFB3261E;
                ON_ERROR_HERO = 0xFFFFFFFF;
            } else {
                CANVAS = 0xFFF2EEF7;
                SURFACE_LOW = 0xFFFFFFFF;
                SURFACE = 0xFFF7F2FA;
                SURFACE_HIGH = 0xFFE7E1EE;
                SURFACE_HIGHEST = 0xFFDDD6E5;
                ON_SURFACE = 0xFF1C1B20;
                ON_SURFACE_VARIANT = 0xFF494552;
                PRIMARY = 0xFF5B4BB7;
                ON_PRIMARY = 0xFFFFFFFF;
                PRIMARY_CONTAINER = 0xFFE4DFFF;
                ON_PRIMARY_CONTAINER = 0xFF1A0864;
                SECONDARY_CONTAINER = 0xFFE3DFFA;
                ON_SECONDARY_CONTAINER = 0xFF1B1830;
                TERTIARY = 0xFF146C4B;
                TERTIARY_CONTAINER = 0xFFA4F2CE;
                ON_TERTIARY_CONTAINER = 0xFF002114;
                ERROR_CONTAINER = 0xFFF9DEDC;
                ON_ERROR_CONTAINER = 0xFF410E0B;
                ERROR_HERO = 0xFFB3261E;
                ON_ERROR_HERO = 0xFFFFFFFF;
            }
        }

        resolveOptionPillTokens();

        int sig = IS_DARK ? 1 : 0;
        sig = 31 * sig + CANVAS;
        sig = 31 * sig + PRIMARY;
        sig = 31 * sig + SURFACE_LOW;
        sig = 31 * sig + SECONDARY_CONTAINER;
        sig = 31 * sig + TERTIARY_CONTAINER;
        return sig;
    }

    /**
     * Resolves Google Clock pill selector tones so the selected pill stands out as a slightly
     * lighter (dark theme) or darker (light theme) tone without diverging from Material 3 style.
     */
    private static void resolveOptionPillTokens() {
        PILL_UNSELECTED_BG = SURFACE_HIGHEST;
        PILL_UNSELECTED_FG = ON_SURFACE;

        int selBg = SECONDARY_CONTAINER;
        if (IS_DARK) {
            if (contrastRatio(selBg, PILL_UNSELECTED_BG) < 2.4f) {
                for (int step = 1; step <= 100; step++) {
                    int cand = lerpColor(SECONDARY_CONTAINER, Color.WHITE, step / 100f);
                    if (contrastRatio(cand, PILL_UNSELECTED_BG) >= 2.4f) {
                        selBg = cand;
                        break;
                    }
                }
            }
        } else {
            int darkTarget = wcagLuminance(ON_SECONDARY_CONTAINER) < 0.08f
                    ? ON_SECONDARY_CONTAINER
                    : 0xFF141414;
            if (contrastRatio(selBg, PILL_UNSELECTED_BG) < 2.3f) {
                for (int step = 1; step <= 100; step++) {
                    int cand = lerpColor(SECONDARY_CONTAINER, darkTarget, step / 100f);
                    if (contrastRatio(cand, PILL_UNSELECTED_BG) >= 2.3f) {
                        selBg = cand;
                        break;
                    }
                }
            }
        }
        PILL_SELECTED_BG = selBg;

        int selFg = ON_SECONDARY_CONTAINER;
        if (contrastRatio(selFg, PILL_SELECTED_BG) < 4.5f) {
            int nearBlack = 0xFF141414;
            selFg = contrastRatio(Color.WHITE, PILL_SELECTED_BG) >= contrastRatio(nearBlack, PILL_SELECTED_BG)
                    ? Color.WHITE
                    : nearBlack;
        }
        PILL_SELECTED_FG = selFg;
    }

    private static float linearizeChannel(int c) {
        float s = c / 255f;
        return (s <= 0.03928f) ? (s / 12.92f) : (float) Math.pow((s + 0.055f) / 1.055f, 2.4f);
    }

    static float wcagLuminance(int color) {
        return 0.2126f * linearizeChannel(Color.red(color))
                + 0.7152f * linearizeChannel(Color.green(color))
                + 0.0722f * linearizeChannel(Color.blue(color));
    }

    static float contrastRatio(int a, int b) {
        float la = wcagLuminance(a);
        float lb = wcagLuminance(b);
        float hi = Math.max(la, lb);
        float lo = Math.min(la, lb);
        return (hi + 0.05f) / (lo + 0.05f);
    }

    private static float relLuminance(int color) {
        float r = Color.red(color) / 255f;
        float g = Color.green(color) / 255f;
        float b = Color.blue(color) / 255f;
        return 0.2126f * r + 0.7152f * g + 0.0722f * b;
    }

    private static float accentScore(int color) {
        float r = Color.red(color) / 255f;
        float g = Color.green(color) / 255f;
        float b = Color.blue(color) / 255f;
        float max = Math.max(r, Math.max(g, b));
        float min = Math.min(r, Math.min(g, b));
        float chroma = max - min;
        return relLuminance(color) + 0.6f * chroma;
    }

    private static boolean isBrighterAccent(int cand, int ref) {
        return relLuminance(cand) >= 0.45f && accentScore(cand) > accentScore(ref);
    }

    private static int sysColor(Context ctx, int resId, int fallback) {
        try {
            if (Build.VERSION.SDK_INT >= 23) {
                return ctx.getColor(resId);
            }
            return ctx.getResources().getColor(resId);
        } catch (Throwable ignored) {
            return fallback;
        }
    }

    static int dp(Context ctx, float value) {
        return Math.round(TypedValue.applyDimension(
                TypedValue.COMPLEX_UNIT_DIP, value, ctx.getResources().getDisplayMetrics()));
    }

    static float dpf(Context ctx, float value) {
        return TypedValue.applyDimension(
                TypedValue.COMPLEX_UNIT_DIP, value, ctx.getResources().getDisplayMetrics());
    }

    static float clamp01(float v) {
        return Math.max(0f, Math.min(1f, v));
    }

    static float lerp(float a, float b, float t) {
        return a + (b - a) * t;
    }

    static int lerpColor(int a, int b, float rawT) {
        float t = clamp01(rawT);
        int ar = Color.red(a), ag = Color.green(a), ab = Color.blue(a), aa = Color.alpha(a);
        int br = Color.red(b), bg = Color.green(b), bb = Color.blue(b), ba = Color.alpha(b);
        return Color.argb(
                Math.round(lerp(aa, ba, t)),
                Math.round(lerp(ar, br, t)),
                Math.round(lerp(ag, bg, t)),
                Math.round(lerp(ab, bb, t)));
    }

    static void setRadiiBg(View view, int color, float tlPx, float trPx, float brPx, float blPx) {
        GradientDrawable d = new GradientDrawable();
        d.setColor(color);
        d.setCornerRadii(new float[]{tlPx, tlPx, trPx, trPx, brPx, brPx, blPx, blPx});
        view.setBackground(d);
    }

    static void setUniformBg(View view, int color, float radiusPx) {
        GradientDrawable d = new GradientDrawable();
        d.setColor(color);
        d.setCornerRadius(radiusPx);
        view.setBackground(d);
    }

    static void attachRippleButton(View view, int bgColor, float radiusPx, int rippleColor, int maxAlpha) {
        GradientDrawable content = new GradientDrawable();
        content.setColor(bgColor);
        content.setCornerRadius(radiusPx);

        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
            GradientDrawable mask = new GradientDrawable();
            mask.setColor(Color.WHITE);
            mask.setCornerRadius(radiusPx);
            int rColor = Color.argb(maxAlpha, Color.red(rippleColor), Color.green(rippleColor), Color.blue(rippleColor));
            view.setBackground(new RippleDrawable(ColorStateList.valueOf(rColor), content, mask));
        } else {
            view.setBackground(content);
        }
    }

    /**
     * Builds a hardware-accelerated Material 3 RippleDrawable (matching Google Clock's
     * native AOSP RippleShader / RenderThread wave).
     */
    private static int withAlpha(int color, int alpha) {
        return Color.argb(alpha, Color.red(color), Color.green(color), Color.blue(color));
    }

    /**
     * Real Hookean spring integrator supporting Spatial (underdamped, zeta < 1)
     * and Effects (critically damped, zeta = 1) schemes with mid-flight velocity continuity.
     */
    static final class SpringDriver implements Choreographer.FrameCallback {
        interface Listener {
            void onSpringUpdate(float value);
        }

        private final float dampingRatio;
        private final float stiffness;
        private final Listener listener;

        private float value;
        private float velocity;
        private float target;
        private long lastFrameNs = 0L;
        private boolean running = false;

        SpringDriver(float initialValue, float dampingRatio, float stiffness, Listener listener) {
            this.value = initialValue;
            this.target = initialValue;
            this.dampingRatio = dampingRatio;
            this.stiffness = stiffness;
            this.listener = listener;
        }

        float getValue() {
            return value;
        }

        void setValueImmediate(float v) {
            cancel();
            this.value = v;
            this.target = v;
            this.velocity = 0f;
        }

        void setTarget(float newTarget) {
            if (Math.abs(newTarget - target) < 0.0005f && !running) {
                return;
            }
            this.target = newTarget;
            if (!running) {
                running = true;
                lastFrameNs = 0L;
                Choreographer.getInstance().postFrameCallback(this);
            }
        }

        void cancel() {
            running = false;
            Choreographer.getInstance().removeFrameCallback(this);
        }

        @Override
        public void doFrame(long frameTimeNanos) {
            if (!running) return;
            if (lastFrameNs == 0L) {
                lastFrameNs = frameTimeNanos;
                Choreographer.getInstance().postFrameCallback(this);
                return;
            }
            float dt = (frameTimeNanos - lastFrameNs) / 1_000_000_000f;
            lastFrameNs = frameTimeNanos;
            if (dt > 0.033f) dt = 0.033f;

            // Substep for numerical stability at high stiffness
            int steps = 2;
            float subDt = dt / steps;
            float c = 2f * dampingRatio * (float) Math.sqrt(stiffness);
            for (int i = 0; i < steps; i++) {
                float accel = -stiffness * (value - target) - c * velocity;
                velocity += accel * subDt;
                value += velocity * subDt;
            }

            listener.onSpringUpdate(value);

            if (Math.abs(value - target) < 0.0008f && Math.abs(velocity) < 0.004f) {
                value = target;
                velocity = 0f;
                running = false;
                listener.onSpringUpdate(value);
                return;
            }
            Choreographer.getInstance().postFrameCallback(this);
        }
    }

    /**
     * Attaches FastSpatial (zeta=0.60, k=800) tactile press-release recoil and corner morph.
     */
    static void attachSpatialButtonPhysics(View view, float restRadiusPx, float pressRadiusPx) {
        SpringDriver pressSpring = new SpringDriver(0f, 0.60f, 800f, p -> {
            float scale = lerp(1.0f, 0.965f, p);
            view.setScaleX(scale);
            view.setScaleY(scale);
            if (view.getBackground() instanceof GradientDrawable) {
                float r = Math.max(0f, lerp(restRadiusPx, pressRadiusPx, p));
                ((GradientDrawable) view.getBackground()).setCornerRadius(r);
            }
        });
        view.setTag(pressSpring);
        view.setOnTouchListener((v, event) -> {
            if (!v.isEnabled()) return false;
            int action = event.getActionMasked();
            if (action == MotionEvent.ACTION_DOWN) {
                pressSpring.setTarget(1f);
            } else if (action == MotionEvent.ACTION_UP || action == MotionEvent.ACTION_CANCEL) {
                pressSpring.setTarget(0f);
            }
            return false;
        });
    }

    /**
     * MaterialShapes.Cookie10Sided badge (10-lobed scallop) with vector emblem
     * and FastSpatial elastic spin/pop on state change or tap.
     */
    static final class Cookie10BadgeView extends View {
        private final Paint fillPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
        private final Paint glyphPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
        private final Path cookiePath = new Path();
        private final Path glyphPath = new Path();

        private float activeFrac = 0f;
        private float spinAngle = 0f;
        private final SpringDriver activeSpring;
        private final SpringDriver spinSpring;

        Cookie10BadgeView(Context ctx) {
            super(ctx);
            glyphPaint.setStyle(Paint.Style.STROKE);
            glyphPaint.setStrokeCap(Paint.Cap.ROUND);
            glyphPaint.setStrokeJoin(Paint.Join.ROUND);

            activeSpring = new SpringDriver(0f, 1.0f, 3800f, v -> {
                activeFrac = clamp01(v);
                invalidate();
            });
            spinSpring = new SpringDriver(0f, 0.60f, 800f, deg -> {
                spinAngle = deg;
                invalidate();
            });
            setOnClickListener(v -> spinSpring.setTarget(spinSpring.target + 36f));
        }

        void setActive(boolean active) {
            float target = active ? 1f : 0f;
            if (Math.abs(target - activeSpring.target) > 0.1f) {
                spinSpring.setTarget(spinSpring.target + 36f);
            }
            activeSpring.setTarget(target);
        }

        @Override
        protected void onSizeChanged(int w, int h, int oldw, int oldh) {
            super.onSizeChanged(w, h, oldw, oldh);
            float cx = w * 0.5f;
            float cy = h * 0.5f;
            float rBase = Math.min(w, h) * 0.44f;

            cookiePath.reset();
            int samples = 120;
            for (int i = 0; i <= samples; i++) {
                double theta = (2.0 * Math.PI * i) / samples;
                double r = rBase * (0.90 + 0.10 * Math.cos(10.0 * theta));
                float x = cx + (float) (r * Math.cos(theta));
                float y = cy + (float) (r * Math.sin(theta));
                if (i == 0) cookiePath.moveTo(x, y);
                else cookiePath.lineTo(x, y);
            }
            cookiePath.close();

            // Clean geometric shield/bolt emblem inside the cookie badge
            float s = rBase * 0.48f;
            glyphPath.reset();
            glyphPath.moveTo(cx + s * 0.10f, cy - s * 0.85f);
            glyphPath.lineTo(cx - s * 0.55f, cy + s * 0.08f);
            glyphPath.lineTo(cx + s * 0.02f, cy + s * 0.08f);
            glyphPath.lineTo(cx - s * 0.12f, cy + s * 0.85f);
            glyphPath.lineTo(cx + s * 0.55f, cy - s * 0.08f);
            glyphPath.lineTo(cx - s * 0.02f, cy - s * 0.08f);
            glyphPath.close();
            glyphPaint.setStrokeWidth(Math.max(2f, rBase * 0.11f));
        }

        @Override
        protected void onDraw(Canvas canvas) {
            int bg = lerpColor(PRIMARY_CONTAINER, TERTIARY_CONTAINER, activeFrac);
            int fg = lerpColor(ON_PRIMARY_CONTAINER, TERTIARY, activeFrac);
            fillPaint.setColor(bg);
            glyphPaint.setColor(fg);

            canvas.save();
            canvas.rotate(spinAngle, getWidth() * 0.5f, getHeight() * 0.5f);
            canvas.drawPath(cookiePath, fillPaint);
            canvas.restore();

            glyphPaint.setStyle(Paint.Style.FILL_AND_STROKE);
            canvas.drawPath(glyphPath, glyphPaint);
        }
    }

    /**
     * Tactile Connected ButtonGroup ([Left | Right] inside a 28dp surfaceContainer capsule).
     * Follows m3_shapes_and_geometry.md Section 2B:
     * - Rest: outer corners 24dp, inner corners 10dp
     * - Press: pressed tile expands weight (1f + stretch*0.38f), morphs inner corners 10dp -> 20dp,
     *   neighbor absorbs compression without moving outer container bounds.
     */
    static final class ConnectedButtonGroup extends LinearLayout {
        private final TextView leftTile;
        private final TextView rightTile;
        private final float outerR;
        private final float innerRestR;
        private final float innerPressR;

        private float leftPress = 0f;
        private float rightPress = 0f;
        private int selectedIndex = -1;

        private final SpringDriver leftSpring;
        private final SpringDriver rightSpring;

        ConnectedButtonGroup(Context ctx, String leftLabel, String rightLabel,
                             Runnable onLeftClick, Runnable onRightClick) {
            super(ctx);
            setOrientation(HORIZONTAL);
            int pad = dp(ctx, 4);
            setPadding(pad, pad, pad, pad);
            setUniformBg(this, SURFACE, dpf(ctx, 28));

            outerR = dpf(ctx, 24);
            innerRestR = dpf(ctx, 10);
            innerPressR = dpf(ctx, 20);

            leftTile = buildTile(ctx, leftLabel);
            rightTile = buildTile(ctx, rightLabel);

            LayoutParams lpL = new LayoutParams(0, dp(ctx, 48), 1f);
            lpL.rightMargin = dp(ctx, 4);
            LayoutParams lpR = new LayoutParams(0, dp(ctx, 48), 1f);

            addView(leftTile, lpL);
            addView(rightTile, lpR);

            leftSpring = new SpringDriver(0f, 0.60f, 800f, v -> {
                leftPress = v;
                applyLayoutAndShapes();
            });
            rightSpring = new SpringDriver(0f, 0.60f, 800f, v -> {
                rightPress = v;
                applyLayoutAndShapes();
            });

            wireTile(leftTile, 0, leftSpring, onLeftClick);
            wireTile(rightTile, 1, rightSpring, onRightClick);
            applyLayoutAndShapes();
        }

        private TextView buildTile(Context ctx, String text) {
            TextView tv = new TextView(ctx);
            tv.setText(text);
            tv.setGravity(Gravity.CENTER);
            tv.setTextSize(TypedValue.COMPLEX_UNIT_SP, 13.5f);
            tv.setTypeface(Typeface.DEFAULT_BOLD);
            tv.setSingleLine(true);
            tv.setPadding(dp(ctx, 10), 0, dp(ctx, 10), 0);
            return tv;
        }

        private void wireTile(TextView tile, int index, SpringDriver spring, Runnable onClick) {
            tile.setOnTouchListener((v, ev) -> {
                if (!isEnabled()) return false;
                int a = ev.getActionMasked();
                if (a == MotionEvent.ACTION_DOWN) {
                    spring.setTarget(1f);
                } else if (a == MotionEvent.ACTION_UP) {
                    spring.setTarget(0f);
                    setSelectedIndex(index);
                    if (onClick != null) onClick.run();
                } else if (a == MotionEvent.ACTION_CANCEL) {
                    spring.setTarget(0f);
                }
                return true;
            });
        }

        void setSelectedIndex(int idx) {
            selectedIndex = idx;
            applyLayoutAndShapes();
        }

        @Override
        public void setEnabled(boolean enabled) {
            super.setEnabled(enabled);
            leftTile.setEnabled(enabled);
            rightTile.setEnabled(enabled);
            setAlpha(enabled ? 1.0f : 0.55f);
        }

        private void applyLayoutAndShapes() {
            float delta = (leftPress - rightPress) * 0.36f;
            float wLeft = Math.max(0.55f, 1.0f + delta);
            float wRight = Math.max(0.55f, 1.0f - delta);

            LayoutParams lpL = (LayoutParams) leftTile.getLayoutParams();
            LayoutParams lpR = (LayoutParams) rightTile.getLayoutParams();
            lpL.weight = wLeft;
            lpR.weight = wRight;
            leftTile.setLayoutParams(lpL);
            rightTile.setLayoutParams(lpR);

            float inL = Math.max(0f, lerp(innerRestR, innerPressR, leftPress));
            float inR = Math.max(0f, lerp(innerRestR, innerPressR, rightPress));

            int bgL = (selectedIndex == 0) ? PILL_SELECTED_BG : PILL_UNSELECTED_BG;
            int fgL = (selectedIndex == 0) ? PILL_SELECTED_FG : PILL_UNSELECTED_FG;
            int bgR = (selectedIndex == 1) ? PILL_SELECTED_BG : PILL_UNSELECTED_BG;
            int fgR = (selectedIndex == 1) ? PILL_SELECTED_FG : PILL_UNSELECTED_FG;

            setRadiiBg(leftTile, bgL, outerR, inL, inL, outerR);
            leftTile.setTextColor(fgL);

            setRadiiBg(rightTile, bgR, inR, outerR, outerR, inR);
            rightTile.setTextColor(fgR);
        }
    }

    /**
     * Giant Clock-style hero button:
     * - Stopped: full stadium pill (height / 2 radius), PRIMARY fill, "Запустить"
     * - Running: smoothly spring-morphs into rounded rectangle (28dp), ERROR_HERO fill, "Стоп"
     */
    static final class MorphHeroButton extends View {
        private final Paint textPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
        private final GradientDrawable contentDrawable = new GradientDrawable();
        private final GradientDrawable maskDrawable = new GradientDrawable();
        private final RippleDrawable rippleDrawable;

        private boolean active = false;
        private float morphProgress = 0f; // 0 = stadium pill, 1 = rounded rect
        private float rectCornerPx;

        private String idleText = "Запустить";
        private String activeText = "Стоп";

        private final SpringDriver morphSpring;
        private Runnable onClickAction;
        private int lastPrimary = 0;
        private int lastErrorHero = 0;

        MorphHeroButton(Context ctx) {
            super(ctx);
            rectCornerPx = dpf(ctx, 28);
            textPaint.setTextAlign(Paint.Align.CENTER);
            textPaint.setTextSize(TypedValue.applyDimension(
                    TypedValue.COMPLEX_UNIT_SP, 28, ctx.getResources().getDisplayMetrics()));
            textPaint.setTypeface(Typeface.create("sans-serif", Typeface.NORMAL));

            maskDrawable.setColor(Color.WHITE);
            rippleDrawable = new RippleDrawable(
                    ColorStateList.valueOf(withAlpha(ON_PRIMARY, 28)),
                    contentDrawable,
                    maskDrawable);
            setBackground(rippleDrawable);
            updateAppearance();

            setClickable(true);
            setFocusable(true);
            setOnClickListener(v -> {
                if (onClickAction != null) {
                    onClickAction.run();
                }
            });

            morphSpring = new SpringDriver(0f, 1.0f, 7000f, v -> {
                morphProgress = v;
                updateAppearance();
                invalidate();
            });
        }

        private void updateAppearance() {
            int h = getHeight();
            float pillRadius = h > 0 ? h * 0.5f : dpf(getContext(), 64);
            float tMorph = clamp01(morphProgress);
            float radius = lerp(pillRadius, rectCornerPx, tMorph);

            int bg = lerpColor(PRIMARY, ERROR_HERO, tMorph);
            int fg = lerpColor(ON_PRIMARY, ON_ERROR_HERO, tMorph);
            contentDrawable.setColor(bg);
            contentDrawable.setCornerRadius(radius);
            maskDrawable.setCornerRadius(radius);
            rippleDrawable.setColor(ColorStateList.valueOf(withAlpha(fg, 28)));
            lastPrimary = PRIMARY;
            lastErrorHero = ERROR_HERO;
        }

        @Override
        protected void onSizeChanged(int w, int h, int oldw, int oldh) {
            super.onSizeChanged(w, h, oldw, oldh);
            updateAppearance();
        }

        void setLabels(String idle, String active) {
            this.idleText = idle;
            this.activeText = active;
            invalidate();
        }

        void setOnHeroClick(Runnable action) {
            this.onClickAction = action;
        }

        void setActive(boolean isActive, boolean animate) {
            this.active = isActive;
            float target = isActive ? 1f : 0f;
            if (!animate) {
                morphSpring.cancel();
                morphProgress = target;
                updateAppearance();
                invalidate();
            } else {
                morphSpring.setTarget(target);
            }
        }

        @Override
        protected void onDraw(Canvas canvas) {
            float w = getWidth();
            float h = getHeight();
            if (w <= 0 || h <= 0) return;

            if (lastPrimary != PRIMARY || lastErrorHero != ERROR_HERO) {
                updateAppearance();
            }

            float tMorph = clamp01(morphProgress);
            int fg = lerpColor(ON_PRIMARY, ON_ERROR_HERO, tMorph);
            textPaint.setColor(fg);
            Paint.FontMetrics fm = textPaint.getFontMetrics();
            float textY = h * 0.5f - (fm.ascent + fm.descent) * 0.5f;
            canvas.drawText(active ? activeText : idleText, w * 0.5f, textY, textPaint);
        }
    }

    /**
     * Three-dot overflow icon matching Google Clock header.
     */
    static final class MoreDotsView extends View {
        private final Paint paint = new Paint(Paint.ANTI_ALIAS_FLAG);

        MoreDotsView(Context ctx) {
            super(ctx);
        }

        @Override
        protected void onDraw(Canvas canvas) {
            float cx = getWidth() * 0.5f;
            float cy = getHeight() * 0.5f;
            float r = dpf(getContext(), 2.1f);
            float step = dpf(getContext(), 6.2f);
            paint.setColor(ON_SURFACE_VARIANT);
            canvas.drawCircle(cx, cy - step, r, paint);
            canvas.drawCircle(cx, cy, r, paint);
            canvas.drawCircle(cx, cy + step, r, paint);
        }
    }

    /**
     * Vertical stack of two full-pill buttons matching Google Clock Stopwatch secondary actions
     * ("Сбросить" / "Круг" stacked directly under the Stop rectangle).
     */
    static final class StackedPillSelector extends LinearLayout {
        private final PillOptionView topPill;
        private final PillOptionView bottomPill;
        private int selectedIndex = 0;

        StackedPillSelector(Context ctx, String topLabel, String bottomLabel,
                            Runnable onTopClick, Runnable onBottomClick) {
            super(ctx);
            setOrientation(VERTICAL);

            topPill = new PillOptionView(ctx, topLabel);
            bottomPill = new PillOptionView(ctx, bottomLabel);

            LayoutParams lpTop = new LayoutParams(LayoutParams.MATCH_PARENT, dp(ctx, 88));
            lpTop.bottomMargin = dp(ctx, 9);
            LayoutParams lpBot = new LayoutParams(LayoutParams.MATCH_PARENT, dp(ctx, 88));

            addView(topPill, lpTop);
            addView(bottomPill, lpBot);

            topPill.setOnPillClick(() -> {
                setSelectedIndex(0);
                if (onTopClick != null) onTopClick.run();
            });
            bottomPill.setOnPillClick(() -> {
                setSelectedIndex(1);
                if (onBottomClick != null) onBottomClick.run();
            });
            setSelectedIndex(0);
        }

        void setSelectedIndex(int idx) {
            selectedIndex = idx;
            topPill.setSelectedState(idx == 0);
            bottomPill.setSelectedState(idx == 1);
        }

        @Override
        public void setEnabled(boolean enabled) {
            super.setEnabled(enabled);
            topPill.setEnabled(enabled);
            bottomPill.setEnabled(enabled);
            setAlpha(enabled ? 1.0f : 0.55f);
        }

        private static final class PillOptionView extends View {
            private final Paint textPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
            private final GradientDrawable contentDrawable = new GradientDrawable();
            private final GradientDrawable maskDrawable = new GradientDrawable();
            private final RippleDrawable rippleDrawable;
            private final String label;
            private final SpringDriver pressSpring;
            private float pressProgress = 0f;
            private boolean selected = false;
            private Runnable onClick;
            private int lastSelectedBg = 0;
            private int lastUnselectedBg = 0;

            PillOptionView(Context ctx, String label) {
                super(ctx);
                this.label = label;
                textPaint.setTextAlign(Paint.Align.CENTER);
                textPaint.setTextSize(TypedValue.applyDimension(
                        TypedValue.COMPLEX_UNIT_SP, 26, ctx.getResources().getDisplayMetrics()));
                textPaint.setTypeface(Typeface.create("sans-serif", Typeface.NORMAL));

                maskDrawable.setColor(Color.WHITE);
                rippleDrawable = new RippleDrawable(
                        ColorStateList.valueOf(withAlpha(PILL_UNSELECTED_FG, 26)),
                        contentDrawable,
                        maskDrawable);
                setBackground(rippleDrawable);
                updateAppearance();

                setFocusable(true);
                setClickable(true);
                setOnClickListener(v -> {
                    if (onClick != null) {
                        onClick.run();
                    }
                });

                pressSpring = new SpringDriver(0f, 1.0f, 3800f, p -> {
                    pressProgress = p;
                    updateAppearance();
                    invalidate();
                });
            }

            private void updateAppearance() {
                float h = getHeight() > 0 ? getHeight() : dpf(getContext(), 88);
                float r = Math.max(dpf(getContext(), 22), h * 0.5f - pressProgress * dpf(getContext(), 6));
                int bg = selected ? PILL_SELECTED_BG : PILL_UNSELECTED_BG;
                int fg = selected ? PILL_SELECTED_FG : PILL_UNSELECTED_FG;
                contentDrawable.setColor(bg);
                contentDrawable.setCornerRadius(r);
                maskDrawable.setCornerRadius(r);
                rippleDrawable.setColor(ColorStateList.valueOf(withAlpha(fg, 26)));
                lastSelectedBg = PILL_SELECTED_BG;
                lastUnselectedBg = PILL_UNSELECTED_BG;
            }

            @Override
            protected void onSizeChanged(int w, int h, int oldw, int oldh) {
                super.onSizeChanged(w, h, oldw, oldh);
                updateAppearance();
            }

            @Override
            public boolean onTouchEvent(MotionEvent ev) {
                if (!isEnabled()) return false;
                int a = ev.getActionMasked();
                if (a == MotionEvent.ACTION_DOWN) {
                    pressSpring.setTarget(1f);
                } else if (a == MotionEvent.ACTION_UP || a == MotionEvent.ACTION_CANCEL) {
                    pressSpring.setTarget(0f);
                }
                return super.onTouchEvent(ev);
            }

            void setOnPillClick(Runnable r) {
                this.onClick = r;
            }

            void setSelectedState(boolean sel) {
                this.selected = sel;
                setSelected(sel);
                setContentDescription(sel ? (label + ", выбрано") : label);
                updateAppearance();
                invalidate();
            }

            @Override
            protected void onDraw(Canvas canvas) {
                float w = getWidth();
                float h = getHeight();
                if (w <= 0 || h <= 0) return;

                if (lastSelectedBg != PILL_SELECTED_BG || lastUnselectedBg != PILL_UNSELECTED_BG) {
                    updateAppearance();
                }

                int fg = selected ? PILL_SELECTED_FG : PILL_UNSELECTED_FG;
                textPaint.setColor(fg);
                Paint.FontMetrics fm = textPaint.getFontMetrics();
                float textY = h * 0.5f - (fm.ascent + fm.descent) * 0.5f;
                canvas.drawText(label, w * 0.5f, textY, textPaint);
            }
        }
    }

    /**
     * Windscribe-style vertical odometer/reel IP:Port display:
     * - Idle: "0.0.0.0:0000" in muted ON_SURFACE_VARIANT
     * - Running: each digit rolls vertically like a mechanical odometer wheel in a staggered cascade
     *   and locks smoothly onto the target IP:Port digits in ON_SURFACE.
     */
    static final class OdometerIpView extends View {
        private static final class Slot {
            boolean isSep;
            char sepChar;
            int fromDigit;
            int toDigit;
            float wStart;
            float wEnd;
            int digitOrder;
            int loops;
        }

        private final Paint textPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
        private final float maxTextSizePx;
        private final float minTextSizePx;

        private ValueAnimator animator;
        private boolean lastRunning = false;
        private String lastTarget = "0.0.0.0:0000";

        private String settledText = "0.0.0.0:0000";
        private final java.util.List<Slot> slots = new java.util.ArrayList<>();
        private float animProgress = 1f;
        private int startColor = ON_SURFACE_VARIANT;
        private int endColor = ON_SURFACE_VARIANT;
        private boolean animIsExpanding = true;

        OdometerIpView(Context ctx) {
            super(ctx);
            maxTextSizePx = TypedValue.applyDimension(
                    TypedValue.COMPLEX_UNIT_SP, 54, ctx.getResources().getDisplayMetrics());
            minTextSizePx = TypedValue.applyDimension(
                    TypedValue.COMPLEX_UNIT_SP, 20, ctx.getResources().getDisplayMetrics());

            textPaint.setTypeface(Typeface.create("sans-serif", Typeface.NORMAL));
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.LOLLIPOP) {
                textPaint.setFontFeatureSettings("tnum");
                textPaint.setLetterSpacing(-0.02f);
            }
            textPaint.setTextAlign(Paint.Align.CENTER);
            textPaint.setTextSize(maxTextSizePx);

            rebuildSlots("0.0.0.0:0000", "0.0.0.0:0000", true);
        }

        void update(boolean running, String activeAddress, boolean animate) {
            String settled = running
                    ? (activeAddress != null && !activeAddress.isEmpty() ? activeAddress : "0.0.0.0:8080")
                    : "0.0.0.0:0000";

            boolean stateChanged = (running != lastRunning);
            boolean addrChanged = !settled.equals(lastTarget);
            String prevSettled = lastTarget;

            lastRunning = running;
            lastTarget = settled;

            if (!animate) {
                if (animator != null) animator.cancel();
                settledText = settled;
                rebuildSlots(settled, settled, running);
                animProgress = 1f;
                startColor = running ? ON_SURFACE : ON_SURFACE_VARIANT;
                endColor = startColor;
                invalidate();
                return;
            }

            if (!stateChanged && !addrChanged) {
                if (animator != null && animator.isRunning()) return;
                settledText = settled;
                startColor = running ? ON_SURFACE : ON_SURFACE_VARIANT;
                endColor = startColor;
                invalidate();
                return;
            }

            if (animator != null) animator.cancel();

            settledText = settled;
            animIsExpanding = running;
            startColor = running ? ON_SURFACE_VARIANT : ON_SURFACE;
            endColor = running ? ON_SURFACE : ON_SURFACE_VARIANT;
            rebuildSlots(prevSettled, settled, running);

            animProgress = 0f;
            animator = ValueAnimator.ofFloat(0f, 1f);
            animator.setDuration(running ? 1350L : 620L);
            animator.setInterpolator(new android.view.animation.LinearInterpolator());
            animator.addUpdateListener(a -> {
                animProgress = (Float) a.getAnimatedValue();
                invalidate();
            });
            animator.start();
        }

        private void rebuildSlots(String fromAddr, String toAddr, boolean starting) {
            slots.clear();
            String[] fSegs = splitSegs(fromAddr);
            String[] tSegs = splitSegs(toAddr);

            int digitOrd = 0;
            for (int s = 0; s < 5; s++) {
                if (s > 0) {
                    Slot sep = new Slot();
                    sep.isSep = true;
                    sep.sepChar = (s == 4) ? ':' : '.';
                    sep.wStart = 1f;
                    sep.wEnd = 1f;
                    slots.add(sep);
                }
                String fStr = fSegs[s];
                String tStr = tSegs[s];
                int maxLen = Math.max(fStr.length(), tStr.length());
                for (int k = 0; k < maxLen; k++) {
                    Slot d = new Slot();
                    d.isSep = false;
                    boolean inFrom = k < fStr.length();
                    boolean inTo = k < tStr.length();
                    d.wStart = inFrom ? 1f : 0f;
                    d.wEnd = inTo ? 1f : 0f;
                    d.fromDigit = inFrom ? digitVal(fStr.charAt(k)) : 0;
                    d.toDigit = inTo ? digitVal(tStr.charAt(k)) : 0;
                    d.digitOrder = digitOrd++;
                    d.loops = starting ? (1 + (d.digitOrder % 2)) : 1;
                    slots.add(d);
                }
            }
        }

        private static int digitVal(char c) {
            return (c >= '0' && c <= '9') ? (c - '0') : 0;
        }

        private static String[] splitSegs(String addr) {
            String[] out = new String[]{"0", "0", "0", "0", "0000"};
            try {
                String clean = addr == null ? "" : addr.trim();
                String[] hp = clean.split(":");
                String[] oct = hp[0].split("\\.");
                for (int i = 0; i < Math.min(4, oct.length); i++) {
                    String s = oct[i].trim();
                    if (!s.isEmpty()) out[i] = s;
                }
                if (hp.length > 1) {
                    String p = hp[1].trim();
                    if (!p.isEmpty()) out[4] = p;
                }
            } catch (Throwable ignored) {
            }
            return out;
        }

        @Override
        protected void onDraw(Canvas canvas) {
            float w = getWidth();
            float h = getHeight();
            if (w <= 0 || h <= 0 || slots.isEmpty()) return;

            float availW = Math.max(1f, w - getPaddingLeft() - getPaddingRight());
            float p = clamp01(animProgress);

            // Compute width factors for each slot at current progress
            float[] wFactors = new float[slots.size()];
            for (int i = 0; i < slots.size(); i++) {
                Slot s = slots.get(i);
                if (s.wStart == s.wEnd) {
                    wFactors[i] = s.wEnd;
                } else if (s.wStart < s.wEnd) {
                    float tW = clamp01(p / 0.24f);
                    float eW = 1f - (1f - tW) * (1f - tW) * (1f - tW);
                    wFactors[i] = lerp(0f, 1f, eW);
                } else {
                    float tW = clamp01((p - 0.58f) / 0.42f);
                    float eW = tW * tW;
                    wFactors[i] = lerp(1f, 0f, eW);
                }
            }

            // Fit textSize smoothly so even the widest state fits cleanly without clipping
            textPaint.setTextSize(maxTextSizePx);
            float baseDigitW = textPaint.measureText("0");
            float baseDotW = textPaint.measureText(".");
            float baseColonW = textPaint.measureText(":");

            float maxPossibleW = 0f;
            float curTotalBaseW = 0f;
            for (int i = 0; i < slots.size(); i++) {
                Slot s = slots.get(i);
                float slotBaseW = s.isSep ? (s.sepChar == ':' ? baseColonW : baseDotW) : baseDigitW;
                maxPossibleW += slotBaseW * Math.max(s.wStart, s.wEnd);
                curTotalBaseW += slotBaseW * wFactors[i];
            }

            float fittedSize = maxTextSizePx;
            if (maxPossibleW > availW) {
                fittedSize = Math.max(minTextSizePx, maxTextSizePx * (availW / maxPossibleW));
            }
            textPaint.setTextSize(fittedSize);

            float digitW = textPaint.measureText("0");
            float dotW = textPaint.measureText(".");
            float colonW = textPaint.measureText(":");

            float totalW = 0f;
            float[] slotWidths = new float[slots.size()];
            for (int i = 0; i < slots.size(); i++) {
                Slot s = slots.get(i);
                float sw = (s.isSep ? (s.sepChar == ':' ? colonW : dotW) : digitW) * wFactors[i];
                slotWidths[i] = sw;
                totalW += sw;
            }

            Paint.FontMetrics fm = textPaint.getFontMetrics();
            float baselineY = h * 0.5f - (fm.ascent + fm.descent) * 0.5f;
            float glyphH = -fm.ascent;
            float rowH = glyphH * 1.18f;
            float clipTop = h * 0.5f - glyphH * 0.65f;
            float clipBot = h * 0.5f + glyphH * 0.65f;

            int baseColor = lerpColor(startColor, endColor, Math.min(1f, p * 2.4f));
            int baseAlpha = Color.alpha(baseColor);
            int rgb = baseColor & 0x00FFFFFF;

            int totalDigits = 1;
            for (Slot s : slots) {
                if (!s.isSep) totalDigits = Math.max(totalDigits, s.digitOrder + 1);
            }

            float curX = (w - totalW) * 0.5f;
            for (int i = 0; i < slots.size(); i++) {
                Slot s = slots.get(i);
                float sw = slotWidths[i];
                if (sw <= 0.5f) {
                    curX += sw;
                    continue;
                }

                float cx = curX + sw * 0.5f;
                if (s.isSep) {
                    textPaint.setColor(baseColor);
                    canvas.drawText(String.valueOf(s.sepChar), cx, baselineY, textPaint);
                } else {
                    float delay = (totalDigits <= 1) ? 0f : (0.24f * s.digitOrder / Math.max(1, totalDigits - 1));
                    float span = 0.76f;
                    float localT = (p >= 1f) ? 1f : clamp01((p - delay) / span);
                    // Cubic ease-out for smooth Windscribe mechanical braking
                    float inv = 1f - localT;
                    float eased = 1f - inv * inv * inv;

                    int delta = (s.toDigit - s.fromDigit + 10) % 10;
                    if (delta == 0 && animIsExpanding && s.wStart > 0f && s.wEnd > 0f) {
                        delta = 0; // still does full s.loops turns!
                    }
                    float totalSteps = s.loops * 10f + delta;
                    float travel = (localT >= 1f) ? totalSteps : (totalSteps * eased);

                    int stepFloor = (int) Math.floor(travel);
                    float frac = travel - stepFloor;
                    int curDig = (s.fromDigit + stepFloor) % 10;
                    int nextDig = (curDig + 1) % 10;

                    float visAlpha = wFactors[i];
                    canvas.save();
                    canvas.clipRect(curX - digitW * 0.15f, clipTop, curX + sw + digitW * 0.15f, clipBot);

                    if (frac < 0.002f || localT >= 1f) {
                        int a = Math.round(baseAlpha * visAlpha);
                        textPaint.setColor((a << 24) | rgb);
                        canvas.drawText(String.valueOf(s.toDigit), cx, baselineY, textPaint);
                    } else {
                        int aCur = Math.round(baseAlpha * visAlpha * (1f - frac * 0.78f));
                        int aNext = Math.round(baseAlpha * visAlpha * (0.22f + frac * 0.78f));

                        textPaint.setColor((aCur << 24) | rgb);
                        canvas.drawText(String.valueOf(curDig), cx, baselineY - frac * rowH, textPaint);

                        textPaint.setColor((aNext << 24) | rgb);
                        canvas.drawText(String.valueOf(nextDig), cx, baselineY + (1f - frac) * rowH, textPaint);
                    }
                    canvas.restore();
                }
                curX += sw;
            }
        }
    }

    /**
     * Material 3 Navigation Bar matching Google Clock bottom tabs.
     * 3 tabs: 0 = Раздача, 1 = Приём, 2 = Утилиты
     */
    static final class NavTabBar extends LinearLayout {
        interface OnTabSelected {
            void onSelect(int index);
        }

        private final TabItemView[] items = new TabItemView[3];
        private int selected = 0;

        NavTabBar(Context ctx, OnTabSelected listener) {
            super(ctx);
            setOrientation(HORIZONTAL);
            setBackgroundColor(SURFACE);
            setPadding(dp(ctx, 8), dp(ctx, 8), dp(ctx, 8), dp(ctx, 10));

            String[] labels = {"Раздача", "Приём", "Утилиты"};
            for (int i = 0; i < 3; i++) {
                final int idx = i;
                TabItemView item = new TabItemView(ctx, idx, labels[i]);
                item.setOnClickListener(v -> {
                    setSelectedTab(idx);
                    if (listener != null) listener.onSelect(idx);
                });
                items[i] = item;
                addView(item, new LayoutParams(0, LayoutParams.WRAP_CONTENT, 1f));
            }
            setSelectedTab(0, false);
        }

        void setSelectedTab(int idx) {
            setSelectedTab(idx, true);
        }

        void setSelectedTab(int idx, boolean animate) {
            selected = idx;
            for (int i = 0; i < items.length; i++) {
                items[i].setSelectedState(i == idx, animate);
            }
        }

        void refreshTheme() {
            setBackgroundColor(SURFACE);
            for (TabItemView item : items) {
                item.invalidate();
            }
        }

        private static final class TabItemView extends View {
            private final int kind;
            private final String label;
            private final Drawable iconDrawable;
            private final Paint pillPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
            private final Paint textPaint = new Paint(Paint.ANTI_ALIAS_FLAG);
            private final RectF pillRect = new RectF();

            private float selectFrac = 0f;
            private final SpringDriver selectSpring;

            TabItemView(Context ctx, int kind, String label) {
                super(ctx);
                this.kind = kind;
                this.label = label;
                int resId;
                if (kind == 0) resId = R.drawable.ic_nav_hotspot;
                else if (kind == 1) resId = R.drawable.ic_nav_receive;
                else resId = R.drawable.ic_nav_reset;
                Drawable d = ctx.getResources().getDrawable(resId, ctx.getTheme());
                this.iconDrawable = (d != null) ? d.mutate() : null;

                textPaint.setTextAlign(Paint.Align.CENTER);
                textPaint.setTextSize(TypedValue.applyDimension(
                        TypedValue.COMPLEX_UNIT_SP, 12f, ctx.getResources().getDisplayMetrics()));
                selectSpring = new SpringDriver(0f, 0.85f, 700f, v -> {
                    selectFrac = v;
                    invalidate();
                });
            }

            void setSelectedState(boolean sel, boolean animate) {
                if (!animate) {
                    selectSpring.setValueImmediate(sel ? 1f : 0f);
                    selectFrac = sel ? 1f : 0f;
                    textPaint.setTypeface(sel ? Typeface.DEFAULT_BOLD : Typeface.DEFAULT);
                    invalidate();
                    return;
                }
                selectSpring.setTarget(sel ? 1f : 0f);
                textPaint.setTypeface(sel ? Typeface.DEFAULT_BOLD : Typeface.DEFAULT);
            }

            @Override
            protected void onMeasure(int widthMeasureSpec, int heightMeasureSpec) {
                int w = MeasureSpec.getSize(widthMeasureSpec);
                int h = dp(getContext(), 60);
                setMeasuredDimension(w, h);
            }

            @Override
            protected void onDraw(Canvas canvas) {
                float w = getWidth();
                float h = getHeight();
                if (w <= 0 || h <= 0) return;

                float t = clamp01(selectFrac);
                float cx = w * 0.5f;
                float pillCy = dp(getContext(), 19);
                float halfW = lerp(dpf(getContext(), 14), dpf(getContext(), 31), t);
                float halfH = dpf(getContext(), 15.5f);

                if (t > 0.02f) {
                    int pillCol = Color.argb(
                            Math.round(255 * t),
                            Color.red(SECONDARY_CONTAINER),
                            Color.green(SECONDARY_CONTAINER),
                            Color.blue(SECONDARY_CONTAINER));
                    pillPaint.setColor(pillCol);
                    pillRect.set(cx - halfW, pillCy - halfH, cx + halfW, pillCy + halfH);
                    canvas.drawRoundRect(pillRect, halfH, halfH, pillPaint);
                }

                int fg = lerpColor(ON_SURFACE_VARIANT, ON_SECONDARY_CONTAINER, t);
                if (iconDrawable != null) {
                    int halfIcon = dp(getContext(), 12);
                    int icx = Math.round(cx);
                    int icy = Math.round(pillCy);
                    iconDrawable.setBounds(icx - halfIcon, icy - halfIcon, icx + halfIcon, icy + halfIcon);
                    iconDrawable.setTint(fg);
                    iconDrawable.draw(canvas);
                }

                textPaint.setColor(lerpColor(ON_SURFACE_VARIANT, ON_SURFACE, t));
                canvas.drawText(label, cx, dp(getContext(), 50), textPaint);
            }
        }
    }
}
