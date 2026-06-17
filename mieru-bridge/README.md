# mieru-bridge

`bridge.go` is the in-process mieru client for the MaestroVPN Android app. It
replaces the old exec'd `libmieru.so` child process with a library call, so the
proxy runs inside the app process (immune to the Android 12+ phantom-process
killer; works on armv7 too).

**It is not built as a standalone module.** To keep a *single* Go runtime in the
app (a second Go runtime alongside sing-box's libbox causes signal-handler
crashes on some ROMs), `bridge.go` is **bound together with sing-box's libbox in
one `gomobile bind`**. The `.github/workflows/libbox-mieru.yml` workflow:

1. clones SagerNet/sing-box at the pinned `SINGBOX_REF`,
2. copies this `bridge.go` into `sing-box/mierubridge/`,
3. `go get github.com/enfein/mieru/v3@<ver>` + `go mod tidy` in that checkout,
4. patches `cmd/internal/build_libbox` to also bind `./mierubridge`,
5. runs the verbatim sing-box `make lib_android`.

The result is a single `libbox.aar` whose `libbox.so` contains both sing-box and
mieru, exposing `io.nekohasekai.mierubridge.Mierubridge.{start,stop,isRunning}`
next to `io.nekohasekai.libbox.*`. `MieruHelper.kt` calls it, falling back to the
bundled exec binary only if the library is unavailable.
