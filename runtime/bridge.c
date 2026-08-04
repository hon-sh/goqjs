#include "hon_config.h"
#include "bridge.h"

#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "../third_party/quickjs/quickjs-libc.h"

/* Implemented in Go (cgo //export). */
extern void hon_host_done(int go_id, int id, int ok, char *err);
extern void hon_host_wake_process(int go_id);
/* Returns malloc'd result string (caller frees); on error sets *err (malloc'd). */
extern char *hon_host_call(int go_id, char *name, char *payload, char **err);
/* Returns async id (>0), or 0 and *err set. */
extern int hon_host_async_start(int go_id, char *name, char *payload, char **err);

struct hon_rt {
    JSRuntime *rt;
    JSContext *ctx;
    int wake_r;
    int wake_w;
    volatile int stop;
    int32_t go_id;
};

static JSContext *JS_NewCustomContext(JSRuntime *rt)
{
    JSContext *ctx = JS_NewContext(rt);
    if (!ctx)
        return NULL;
    js_init_module_std(ctx, "std");
    js_init_module_os(ctx, "os");
    return ctx;
}

static JSValue js_run_done(JSContext *ctx, JSValueConst this_val,
                           int argc, JSValueConst *argv)
{
    hon_rt *r;
    int id, ok;
    const char *err = NULL;

    (void)this_val;
    r = JS_GetContextOpaque(ctx);
    if (!r)
        return JS_ThrowTypeError(ctx, "missing hon runtime");
    if (argc < 2)
        return JS_ThrowTypeError(ctx, "__hon_done(id, ok, err?) expects >=2 args");
    if (JS_ToInt32(ctx, &id, argv[0]))
        return JS_EXCEPTION;
    ok = JS_ToBool(ctx, argv[1]);
    if (argc >= 3 && !JS_IsNull(argv[2]) && !JS_IsUndefined(argv[2])) {
        err = JS_ToCString(ctx, argv[2]);
        if (!err)
            return JS_EXCEPTION;
    }
    hon_host_done(r->go_id, id, ok, (char *)err);
    if (err)
        JS_FreeCString(ctx, err);
    return JS_UNDEFINED;
}

static JSValue js_host_call(JSContext *ctx, JSValueConst this_val,
                            int argc, JSValueConst *argv)
{
    hon_rt *r;
    const char *name, *payload;
    char *err = NULL, *result;
    JSValue ret;

    (void)this_val;
    r = JS_GetContextOpaque(ctx);
    if (!r)
        return JS_ThrowTypeError(ctx, "missing hon runtime");
    if (argc < 2)
        return JS_ThrowTypeError(ctx, "__hon_host(name, payload) expects 2 args");
    name = JS_ToCString(ctx, argv[0]);
    if (!name)
        return JS_EXCEPTION;
    payload = JS_ToCString(ctx, argv[1]);
    if (!payload) {
        JS_FreeCString(ctx, name);
        return JS_EXCEPTION;
    }
    result = hon_host_call(r->go_id, (char *)name, (char *)payload, &err);
    JS_FreeCString(ctx, name);
    JS_FreeCString(ctx, payload);
    if (err) {
        ret = JS_ThrowInternalError(ctx, "%s", err);
        free(err);
        free(result);
        return ret;
    }
    if (!result)
        return JS_UNDEFINED;
    ret = JS_NewString(ctx, result);
    free(result);
    return ret;
}

static JSValue js_async_start(JSContext *ctx, JSValueConst this_val,
                              int argc, JSValueConst *argv)
{
    hon_rt *r;
    const char *name, *payload;
    char *err = NULL;
    int id;

    (void)this_val;
    r = JS_GetContextOpaque(ctx);
    if (!r)
        return JS_ThrowTypeError(ctx, "missing hon runtime");
    if (argc < 2)
        return JS_ThrowTypeError(ctx, "__hon_async_start(name, payload) expects 2 args");
    name = JS_ToCString(ctx, argv[0]);
    if (!name)
        return JS_EXCEPTION;
    payload = JS_ToCString(ctx, argv[1]);
    if (!payload) {
        JS_FreeCString(ctx, name);
        return JS_EXCEPTION;
    }
    id = hon_host_async_start(r->go_id, (char *)name, (char *)payload, &err);
    JS_FreeCString(ctx, name);
    JS_FreeCString(ctx, payload);
    if (id <= 0) {
        JSValue ret = JS_ThrowInternalError(ctx, "%s", err ? err : "async_start failed");
        free(err);
        return ret;
    }
    return JS_NewInt32(ctx, id);
}

static JSValue js_wake_drain(JSContext *ctx, JSValueConst this_val,
                             int argc, JSValueConst *argv)
{
    hon_rt *r;
    char buf[64];
    ssize_t n;

    (void)this_val;
    (void)argc;
    (void)argv;

    r = JS_GetContextOpaque(ctx);
    if (!r)
        return JS_UNDEFINED;

    for (;;) {
        n = read(r->wake_r, buf, sizeof(buf));
        if (n < 0) {
            if (errno == EINTR)
                continue;
            break;
        }
        if (n == 0)
            break;
    }

    if (r->stop) {
        char clear[128];
        snprintf(clear, sizeof(clear),
                 "os.setReadHandler(%d, null);", r->wake_r);
        JS_Eval(ctx, clear, strlen(clear), "<hon-stop>", JS_EVAL_TYPE_GLOBAL);
        return JS_UNDEFINED;
    }

    hon_host_wake_process(r->go_id);
    return JS_UNDEFINED;
}

static int eval_buf(JSContext *ctx, const char *buf, const char *filename, int eval_flags)
{
    JSValue val;
    int ret;

    if ((eval_flags & JS_EVAL_TYPE_MASK) == JS_EVAL_TYPE_MODULE) {
        val = JS_Eval(ctx, buf, strlen(buf), filename,
                      eval_flags | JS_EVAL_FLAG_COMPILE_ONLY);
        if (!JS_IsException(val)) {
            js_module_set_import_meta(ctx, val, 1, 1);
            val = JS_EvalFunction(ctx, val);
        }
        val = js_std_await(ctx, val);
    } else {
        val = JS_Eval(ctx, buf, strlen(buf), filename, eval_flags);
    }
    if (JS_IsException(val)) {
        js_std_dump_error(ctx);
        ret = -1;
    } else {
        ret = 0;
    }
    JS_FreeValue(ctx, val);
    return ret;
}

static int set_nonblock(int fd)
{
    int flags = fcntl(fd, F_GETFL, 0);
    if (flags < 0)
        return -1;
    return fcntl(fd, F_SETFL, flags | O_NONBLOCK);
}

hon_rt *hon_create(int32_t go_id)
{
    hon_rt *r;
    JSValue global_obj;
    int fds[2];

    r = calloc(1, sizeof(*r));
    if (!r)
        return NULL;
    r->wake_r = -1;
    r->wake_w = -1;
    r->go_id = go_id;

    if (pipe(fds) != 0) {
        fprintf(stderr, "hon: pipe failed\n");
        free(r);
        return NULL;
    }
    r->wake_r = fds[0];
    r->wake_w = fds[1];
    if (set_nonblock(r->wake_r) != 0 || set_nonblock(r->wake_w) != 0) {
        fprintf(stderr, "hon: set_nonblock failed\n");
        goto fail;
    }

    r->rt = JS_NewRuntime();
    if (!r->rt) {
        fprintf(stderr, "hon: cannot allocate JS runtime\n");
        goto fail;
    }
    js_std_set_worker_new_context_func(JS_NewCustomContext);
    js_std_init_handlers(r->rt);
    r->ctx = JS_NewCustomContext(r->rt);
    if (!r->ctx) {
        fprintf(stderr, "hon: cannot allocate JS context\n");
        goto fail;
    }
    JS_SetContextOpaque(r->ctx, r);

    JS_SetModuleLoaderFunc2(r->rt, NULL, js_module_loader, js_module_check_attributes, NULL);
    js_std_add_helpers(r->ctx, -1, NULL);

    global_obj = JS_GetGlobalObject(r->ctx);
    JS_SetPropertyStr(r->ctx, global_obj, "__hon_done",
                      JS_NewCFunction(r->ctx, js_run_done, "__hon_done", 3));
    JS_SetPropertyStr(r->ctx, global_obj, "__hon_host",
                      JS_NewCFunction(r->ctx, js_host_call, "__hon_host", 2));
    JS_SetPropertyStr(r->ctx, global_obj, "__hon_async_start",
                      JS_NewCFunction(r->ctx, js_async_start, "__hon_async_start", 2));
    JS_SetPropertyStr(r->ctx, global_obj, "__hon_wake_drain",
                      JS_NewCFunction(r->ctx, js_wake_drain, "__hon_wake_drain", 0));
    JS_FreeValue(r->ctx, global_obj);

    return r;

fail:
    hon_destroy(r);
    return NULL;
}

int hon_eval(hon_rt *r, const char *script, const char *filename, int module)
{
    int flags = module ? JS_EVAL_TYPE_MODULE : JS_EVAL_TYPE_GLOBAL;
    if (!r || !r->ctx)
        return -1;
    return eval_buf(r->ctx, script, filename ? filename : "<eval>", flags);
}

int hon_install_wake(hon_rt *r)
{
    char buf[160];
    if (!r || !r->ctx)
        return -1;
    snprintf(buf, sizeof(buf),
             "os.setReadHandler(%d, function() { __hon_wake_drain(); });",
             r->wake_r);
    return eval_buf(r->ctx, buf, "<hon-wake>", JS_EVAL_TYPE_GLOBAL);
}

void hon_loop(hon_rt *r)
{
    if (!r || !r->ctx)
        return;
    js_std_loop(r->ctx);
}

void hon_wake(hon_rt *r)
{
    char b = 1;
    ssize_t n;
    if (!r || r->wake_w < 0)
        return;
    do {
        n = write(r->wake_w, &b, 1);
    } while (n < 0 && errno == EINTR);
}

void hon_request_stop(hon_rt *r)
{
    if (!r)
        return;
    r->stop = 1;
    hon_wake(r);
}

int hon_invoke(hon_rt *r, int id, const char *json_args)
{
    JSValue global_obj, fn, args[2], ret;
    if (!r || !r->ctx)
        return -1;

    global_obj = JS_GetGlobalObject(r->ctx);
    fn = JS_GetPropertyStr(r->ctx, global_obj, "__hon_invoke");
    JS_FreeValue(r->ctx, global_obj);
    if (!JS_IsFunction(r->ctx, fn)) {
        JS_FreeValue(r->ctx, fn);
        return -1;
    }
    args[0] = JS_NewInt32(r->ctx, id);
    args[1] = JS_NewString(r->ctx, json_args ? json_args : "[]");
    ret = JS_Call(r->ctx, fn, JS_UNDEFINED, 2, args);
    JS_FreeValue(r->ctx, args[0]);
    JS_FreeValue(r->ctx, args[1]);
    JS_FreeValue(r->ctx, fn);
    if (JS_IsException(ret)) {
        js_std_dump_error(r->ctx);
        JS_FreeValue(r->ctx, ret);
        return -1;
    }
    JS_FreeValue(r->ctx, ret);
    return 0;
}

int hon_async_settle(hon_rt *r, int id, int ok, const char *payload)
{
    char *script;
    int ret, n;

    if (!r || !r->ctx)
        return -1;
    if (!payload)
        payload = "";
    /* Escape payload as JSON string via Go side — payload must already be a JS
     * string literal content safe for embedding, or a JSON text used as arg.
     * We pass payload as JSON.parse argument: settle(id, ok, payloadJSON). */
    n = snprintf(NULL, 0,
                 "__hon_async_settle(%d, %s, %s);",
                 id, ok ? "true" : "false", payload);
    if (n < 0)
        return -1;
    script = malloc((size_t)n + 1);
    if (!script)
        return -1;
    snprintf(script, (size_t)n + 1,
             "__hon_async_settle(%d, %s, %s);",
             id, ok ? "true" : "false", payload);
    ret = eval_buf(r->ctx, script, "<async-settle>", JS_EVAL_TYPE_GLOBAL);
    free(script);
    return ret;
}

void hon_destroy(hon_rt *r)
{
    if (!r)
        return;
    if (r->ctx) {
        JS_SetContextOpaque(r->ctx, NULL);
        JS_FreeContext(r->ctx);
        r->ctx = NULL;
    }
    if (r->rt) {
        js_std_free_handlers(r->rt);
        JS_FreeRuntime(r->rt);
        r->rt = NULL;
    }
    if (r->wake_r >= 0)
        close(r->wake_r);
    if (r->wake_w >= 0)
        close(r->wake_w);
    free(r);
}
