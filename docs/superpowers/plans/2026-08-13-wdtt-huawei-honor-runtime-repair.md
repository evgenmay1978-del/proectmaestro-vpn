# WDTT Huawei/Honor Runtime Repair Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Package the reviewed Huawei/Honor networking fix in the mobile WDTT client and prove it on the real production phone before OTA.

**Architecture:** Keep the production WDTT server and subscription schema unchanged. Move the single reviewed upstream pin, make CI enforce it, build checksum-bound native artifacts in GitHub, then build a normal monotonic mobile release candidate. Promotion is blocked until structured READY, a server-side device/handshake, and real egress are observed.

**Tech Stack:** GitHub Actions, Python unittest, Go cross-build, Android/Kotlin/Gradle.

## Global Constraints

- Exact upstream commit: `1ff024899a577cb5db4691e526614619bf5a06a3`.
- Production server stays on `8b26530dfe90ff9b6aa3880ba2c1f070e21e2d3a` during client validation.
- TV code paths, resources and assets are read-only.
- No customer identifiers, passwords, keys, subscription URLs or authenticated URLs in Git or logs.
- No OTA until a real phone proves READY, egress, reconnect, sleep/wake and network switching.

---

### Task 1: Fail-closed upstream pin policy

**Files:**
- Create: `ops/test_wdtt_upstream_pin.py`
- Modify: `.github/workflows/wdtt-bin.yml`
- Test: `ops/test_wdtt_upstream_pin.py`

**Interfaces:**
- Consumes: `version.properties`, `.github/workflows/wdtt-bin.yml`, `.github/workflows/android-test.yml`, `.github/workflows/android.yml`.
- Produces: a zero-exit policy check only when the exact reviewed pin is selected and all packaging workflows verify `WDTT_UPSTREAM_COMMIT`.

- [ ] **Step 1: Write the failing policy test**

Create a unittest which parses `version.properties` and asserts:

```python
EXPECTED = "1ff024899a577cb5db4691e526614619bf5a06a3"
self.assertEqual(props["WDTT_REF"], EXPECTED)
for path in WORKFLOWS:
    text = path.read_text(encoding="utf-8")
    self.assertIn("WDTT_UPSTREAM_COMMIT", text)
    self.assertIn('test "$(cat', text)
```

The test must also assert `.github/workflows/wdtt-bin.yml` invokes `python -m unittest -v ops/test_wdtt_upstream_pin.py`.

- [ ] **Step 2: Run RED**

Run: `python -m unittest -v ops/test_wdtt_upstream_pin.py`  
Expected: FAIL because `WDTT_REF` is still `8b26530d...`.

- [ ] **Step 3: Commit and push RED**

Stage only the new test and workflow invocation. Commit: `test(wdtt): require Huawei Honor network fix`.

- [ ] **Step 4: Prove RED on the exact SHA**

Use the GitHub workflow run for that SHA. Expected: the pin policy step fails before native artifact publication; no APK or OTA is published.

### Task 2: Move the reviewed client pin

**Files:**
- Modify: `version.properties`
- Test: `ops/test_wdtt_upstream_pin.py`

**Interfaces:**
- Consumes: exact upstream commit from Task 1.
- Produces: `WDTT_REF=1ff024899a577cb5db4691e526614619bf5a06a3`.

- [ ] **Step 1: Change only the pin**

Replace the existing `WDTT_REF` value with the exact reviewed commit. Do not alter `WDTT_GO_VERSION`, server deployment, subscription JSON, or TV code.

- [ ] **Step 2: Run GREEN policy test**

Run: `python -m unittest -v ops/test_wdtt_upstream_pin.py`  
Expected: PASS.

- [ ] **Step 3: Commit and push GREEN**

Commit: `fix(wdtt): include Huawei Honor network fix`.

- [ ] **Step 4: Verify exact-SHA native build**

The exact SHA must pass checkout-at-pin, Android ABI builds, Linux canary build, checksum manifest, upstream commit marker, relay tests and offline image checks. Downloaded artifact metadata must report the exact new pin.

### Task 3: Mobile release-candidate provenance

**Files:**
- Modify: `.github/workflows/android-test.yml`
- Test: `ops/test_wdtt_upstream_pin.py`

**Interfaces:**
- Consumes: a successful exact-pin `wdtt-bin` artifact.
- Produces: a release-signed, normal monotonic mobile candidate; it is not an OTA release.

- [ ] **Step 1: Add a failing workflow-policy assertion**

Extend the test to require Android test packaging to reject a WDTT artifact unless both its checksum manifest and `WDTT_UPSTREAM_COMMIT` match the repository pin. Require both `arm64-v8a` and `armeabi-v7a`.

- [ ] **Step 2: Run RED and inspect the expected missing assertion**

Run the focused unittest and verify the failure names the missing release-candidate provenance rule.

- [ ] **Step 3: Implement the minimal workflow gate**

Use an exact successful `wdtt-bin` artifact, run `sha256sum -c`, compare the commit marker, then copy both native binaries. Keep TV behavior unchanged and do not use a `90xxx` versionCode for the phone candidate.

- [ ] **Step 4: Run GREEN and exact-SHA Android checks**

Required: focused policy unittest, Android unit tests, lint/compile checks, APK signature verification, packaged ABI inspection, and a diff audit proving no TV source/resource change.

- [ ] **Step 5: Commit and push**

Commit: `ci(android): bind WDTT candidate to reviewed artifact`.

### Task 4: Real-phone validation and promotion gate

**Files:**
- Modify after validation: `CURRENT_PRODUCTION_HANDOFF.md`
- No app source change.

**Interfaces:**
- Consumes: release-signed candidate from Task 3 and live version-156 subscription.
- Produces: an anonymized pass/fail record that authorizes or blocks OTA.

- [ ] **Step 1: Install the candidate without deleting application data**

Confirm the installed app reports the intended normal monotonic version and the same package/signing identity.

- [ ] **Step 2: Prove WDTT remains advertised**

Refresh subscription repeatedly, restart the app, and confirm the WDTT chip remains present for the allowed mobile account. Confirm TV receives no WDTT payload.

- [ ] **Step 3: Prove the runtime**

Select WDTT. Required evidence:

```text
client structured event: READY
server: one device/handshake count increase
tunnel interface: inbound and outbound byte counters increase
public HTTPS request: success through the tunnel
```

Store only counts and pass/fail results; never record identifiers or addresses.

- [ ] **Step 4: Prove lifecycle stability**

Disconnect/reconnect, app restart, sleep/wake, Wi-Fi→mobile and mobile→Wi-Fi. Each transition must return to READY and pass egress.

- [ ] **Step 5: Promote or roll back**

If every check passes, build the production mobile OTA with the same reviewed pin and signing identity. If any check fails, do not publish OTA; retain version 156 and document the exact failing boundary.

- [ ] **Step 6: Update durable memory**

Update `CURRENT_PRODUCTION_HANDOFF.md` with exact source/release SHA, workflow IDs, mobile result and rollback state, using only anonymized facts. Commit: `docs: record WDTT phone validation`.
