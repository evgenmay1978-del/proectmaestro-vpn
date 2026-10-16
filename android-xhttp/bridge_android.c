#include <jni.h>
#include <pthread.h>
#include <stdint.h>
#include <stdlib.h>
#include "_cgo_export.h"

#define STATUS_OK 0
#define STATUS_INVALID_INPUT 1
#define STATUS_BUSY 2
#define STATUS_JNI_ERROR 5
#define MAX_PAYLOAD_BYTES (16 * 1024)

static JavaVM *java_vm;
static pthread_mutex_t operation_lock = PTHREAD_MUTEX_INITIALIZER;
static pthread_mutex_t callback_lock = PTHREAD_MUTEX_INITIALIZER;
static jobject callback;
static jmethodID protect_method;
static int64_t callback_session;

/* Never hold operation_lock in a callback: Go may be in Start/Close while a
 * transport goroutine calls back. Kotlin must not re-enter nativeStart/Stop. */
JNIEXPORT jint JNICALL JNI_OnLoad(JavaVM *vm, void *reserved) {
    (void)reserved;
    java_vm = vm;
    return JNI_VERSION_1_6;
}

static void clear_exception(JNIEnv *env) {
    if ((*env)->ExceptionCheck(env)) {
        (*env)->ExceptionClear(env);
    }
}

static void clear_callback(JNIEnv *env, int64_t id) {
    pthread_mutex_lock(&callback_lock);
    if (callback_session == id) {
        callback_session = 0;
        protect_method = NULL;
        if (callback != NULL) {
            (*env)->DeleteGlobalRef(env, callback);
            callback = NULL;
        }
    }
    pthread_mutex_unlock(&callback_lock);
}

int MaestroXhttpProtect(int64_t id, int fd) {
    if (java_vm == NULL || id <= 0 || fd < 0) {
        return 0;
    }
    JNIEnv *env = NULL;
    int attached_here = 0;
    jint state = (*java_vm)->GetEnv(java_vm, (void **)&env, JNI_VERSION_1_6);
    if (state == JNI_EDETACHED) {
        if ((*java_vm)->AttachCurrentThread(java_vm, &env, NULL) != JNI_OK) {
            return 0;
        }
        attached_here = 1;
    } else if (state != JNI_OK) {
        return 0;
    }

    /* A local reference survives concurrent Stop's DeleteGlobalRef. No C pointer
     * to a freed session or jobject is retained in Go. */
    pthread_mutex_lock(&callback_lock);
    jobject local = NULL;
    jmethodID method = NULL;
    if (callback_session == id && callback != NULL) {
        local = (*env)->NewLocalRef(env, callback);
        method = protect_method;
    }
    pthread_mutex_unlock(&callback_lock);

    int allowed = 0;
    if (local != NULL && method != NULL && !(*env)->ExceptionCheck(env)) {
        jboolean result = (*env)->CallBooleanMethod(env, local, method, (jlong)id, (jint)fd);
        if (!(*env)->ExceptionCheck(env) && result == JNI_TRUE) {
            pthread_mutex_lock(&callback_lock);
            allowed = callback_session == id && callback != NULL;
            pthread_mutex_unlock(&callback_lock);
        }
    }
    clear_exception(env);
    if (local != NULL) {
        (*env)->DeleteLocalRef(env, local);
    }
    if (attached_here) {
        (*java_vm)->DetachCurrentThread(java_vm);
    }
    return allowed;
}

JNIEXPORT jint JNICALL
Java_com_maestrovpn_tv_whitelist_XhttpNative_nativeStart(
    JNIEnv *env, jclass klass, jlong id, jbyteArray payload, jobject protector) {
    (void)klass;
    if (id <= 0 || id > INT32_MAX || payload == NULL || protector == NULL) {
        return STATUS_INVALID_INPUT;
    }
    jsize size = (*env)->GetArrayLength(env, payload);
    if (size <= 0 || size > MAX_PAYLOAD_BYTES) {
        return STATUS_INVALID_INPUT;
    }

    pthread_mutex_lock(&operation_lock);
    pthread_mutex_lock(&callback_lock);
    int busy = callback_session != 0;
    pthread_mutex_unlock(&callback_lock);
    if (busy) {
        pthread_mutex_unlock(&operation_lock);
        return STATUS_BUSY;
    }

    jclass protector_class = (*env)->GetObjectClass(env, protector);
    jmethodID method = protector_class == NULL ? NULL :
        (*env)->GetMethodID(env, protector_class, "protectSocket", "(JI)Z");
    if (protector_class != NULL) {
        (*env)->DeleteLocalRef(env, protector_class);
    }
    if (method == NULL || (*env)->ExceptionCheck(env)) {
        clear_exception(env);
        pthread_mutex_unlock(&operation_lock);
        return STATUS_JNI_ERROR;
    }
    jobject global = (*env)->NewGlobalRef(env, protector);
    char *bytes = malloc((size_t)size);
    if (global == NULL || bytes == NULL || (*env)->ExceptionCheck(env)) {
        clear_exception(env);
        if (global != NULL) {
            (*env)->DeleteGlobalRef(env, global);
        }
        free(bytes);
        pthread_mutex_unlock(&operation_lock);
        return STATUS_JNI_ERROR;
    }
    (*env)->GetByteArrayRegion(env, payload, 0, size, (jbyte *)bytes);
    if ((*env)->ExceptionCheck(env)) {
        clear_exception(env);
        (*env)->DeleteGlobalRef(env, global);
        free(bytes);
        pthread_mutex_unlock(&operation_lock);
        return STATUS_JNI_ERROR;
    }

    pthread_mutex_lock(&callback_lock);
    callback = global;
    protect_method = method;
    callback_session = id;
    pthread_mutex_unlock(&callback_lock);
    int result = MaestroXhttpStart((int64_t)id, bytes, (int)size);
    /* Avoid retaining an extra native copy of credentials. */
    volatile char *wipe = bytes;
    for (jsize i = 0; i < size; i++) {
        wipe[i] = 0;
    }
    free(bytes);
    if (result != STATUS_OK) {
        clear_callback(env, id);
    }
    pthread_mutex_unlock(&operation_lock);
    return result;
}

JNIEXPORT jint JNICALL
Java_com_maestrovpn_tv_whitelist_XhttpNative_nativeStop(
    JNIEnv *env, jclass klass, jlong id) {
    (void)klass;
    pthread_mutex_lock(&operation_lock);
    /* Invalidate before Close; racing callbacks are denied even if their Java
     * method has already returned true. Stale Stop cannot clear a newer ref. */
    clear_callback(env, id);
    int result = MaestroXhttpStop((int64_t)id);
    pthread_mutex_unlock(&operation_lock);
    return result;
}
