#include "goqjs_config.h"
#include "bridge.h"

#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "../third_party/quickjs/quickjs-libc.h"

/* Implemented in Go (cgo //export). */
extern void goqjs_host_write(int call_id, char *s);
extern void goqjs_host_done(int id, int ok, char *err);
extern void goqjs_host_wake_process(void);

struct goqjs_rt {
    JSRuntime *rt;
    JSContext *ctx;
    int wake_r;
    int wake_w;
    volatile int stop;
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

static JSValue js_resp_write(JSContext *ctx, JSValueConst this_val,
                             int argc, JSValueConst *argv)
{
    int call_id;
    const char *s;

    (void)this_val;
    if (argc < 2)
        return JS_ThrowTypeError(ctx, "respWrite(callId, s) expects 2 args");
    if (JS_ToInt32(ctx, &call_id, argv[0]))
        return JS_EXCEPTION;
    s = JS_ToCString(ctx, argv[1]);
    if (!s)
        return JS_EXCEPTION;
    goqjs_host_write(call_id, (char *)s);
    JS_FreeCString(ctx, s);
    return JS_UNDEFINED;
}

static JSValue js_run_done(JSContext *ctx, JSValueConst this_val,
                           int argc, JSValueConst *argv)
{
    int id, ok;
    const char *err = NULL;

    (void)this_val;
    if (argc < 2)
        return JS_ThrowTypeError(ctx, "__goqjs_done(id, ok, err?) expects >=2 args");
    if (JS_ToInt32(ctx, &id, argv[0]))
        return JS_EXCEPTION;
    ok = JS_ToBool(ctx, argv[1]);
    if (argc >= 3 && !JS_IsNull(argv[2]) && !JS_IsUndefined(argv[2])) {
        err = JS_ToCString(ctx, argv[2]);
        if (!err)
            return JS_EXCEPTION;
    }
    goqjs_host_done(id, ok, (char *)err);
    if (err)
        JS_FreeCString(ctx, err);
    return JS_UNDEFINED;
}

/* Drain wake pipe and ask Go to process the invoke queue.
 * If stop was requested, clear the read handler so js_std_loop can exit. */
static JSValue js_wake_drain(JSContext *ctx, JSValueConst this_val,
                             int argc, JSValueConst *argv)
{
    goqjs_rt *r;
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
            break; /* EAGAIN / empty */
        }
        if (n == 0)
            break;
    }

    if (r->stop) {
        char clear[128];
        snprintf(clear, sizeof(clear),
                 "os.setReadHandler(%d, null);", r->wake_r);
        JS_Eval(ctx, clear, strlen(clear), "<goqjs-stop>", JS_EVAL_TYPE_GLOBAL);
        return JS_UNDEFINED;
    }

    goqjs_host_wake_process();
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

goqjs_rt *goqjs_create(void)
{
    goqjs_rt *r;
    JSValue global_obj;
    int fds[2];

    r = calloc(1, sizeof(*r));
    if (!r)
        return NULL;
    r->wake_r = -1;
    r->wake_w = -1;

    if (pipe(fds) != 0) {
        fprintf(stderr, "goqjs: pipe failed\n");
        free(r);
        return NULL;
    }
    r->wake_r = fds[0];
    r->wake_w = fds[1];
    if (set_nonblock(r->wake_r) != 0 || set_nonblock(r->wake_w) != 0) {
        fprintf(stderr, "goqjs: set_nonblock failed\n");
        goto fail;
    }

    r->rt = JS_NewRuntime();
    if (!r->rt) {
        fprintf(stderr, "goqjs: cannot allocate JS runtime\n");
        goto fail;
    }
    js_std_set_worker_new_context_func(JS_NewCustomContext);
    js_std_init_handlers(r->rt);
    r->ctx = JS_NewCustomContext(r->rt);
    if (!r->ctx) {
        fprintf(stderr, "goqjs: cannot allocate JS context\n");
        goto fail;
    }
    JS_SetContextOpaque(r->ctx, r);

    JS_SetModuleLoaderFunc2(r->rt, NULL, js_module_loader, js_module_check_attributes, NULL);
    js_std_add_helpers(r->ctx, -1, NULL);

    global_obj = JS_GetGlobalObject(r->ctx);
    JS_SetPropertyStr(r->ctx, global_obj, "respWrite",
                      JS_NewCFunction(r->ctx, js_resp_write, "respWrite", 2));
    JS_SetPropertyStr(r->ctx, global_obj, "__goqjs_done",
                      JS_NewCFunction(r->ctx, js_run_done, "__goqjs_done", 3));
    JS_SetPropertyStr(r->ctx, global_obj, "__goqjs_wake_drain",
                      JS_NewCFunction(r->ctx, js_wake_drain, "__goqjs_wake_drain", 0));
    JS_FreeValue(r->ctx, global_obj);

    /* std/os globals + host helpers used by every job. */
    {
        const char *boot =
            "import * as std from 'std';\n"
            "import * as os from 'os';\n"
            "globalThis.std = std;\n"
            "globalThis.os = os;\n"
            "globalThis.sleep = function(ms) { return os.sleepAsync(ms); };\n"
            "globalThis.resp = {\n"
            "  write: async function(callId, s) { respWrite(callId, s); }\n"
            "};\n"
            "globalThis.__goqjs_invoke = function(id, jsonArgs) {\n"
            "  let args;\n"
            "  try { args = JSON.parse(jsonArgs); }\n"
            "  catch (e) { __goqjs_done(id, false, String(e)); return; }\n"
            "  if (typeof globalThis.__goqjs_run !== 'function') {\n"
            "    __goqjs_done(id, false, '__goqjs_run is not defined');\n"
            "    return;\n"
            "  }\n"
            "  Promise.resolve(__goqjs_run.apply(undefined, args)).then(\n"
            "    function() { __goqjs_done(id, true, null); },\n"
            "    function(e) {\n"
            "      var msg = (e && e.stack) ? String(e.stack) : String(e);\n"
            "      __goqjs_done(id, false, msg);\n"
            "    }\n"
            "  );\n"
            "};\n";
        if (eval_buf(r->ctx, boot, "<boot>", JS_EVAL_TYPE_MODULE))
            goto fail;
    }

    return r;

fail:
    goqjs_destroy(r);
    return NULL;
}

int goqjs_eval(goqjs_rt *r, const char *script, const char *filename, int module)
{
    int flags = module ? JS_EVAL_TYPE_MODULE : JS_EVAL_TYPE_GLOBAL;
    if (!r || !r->ctx)
        return -1;
    return eval_buf(r->ctx, script, filename ? filename : "<eval>", flags);
}

int goqjs_install_wake(goqjs_rt *r)
{
    char buf[160];
    if (!r || !r->ctx)
        return -1;
    snprintf(buf, sizeof(buf),
             "os.setReadHandler(%d, function() { __goqjs_wake_drain(); });",
             r->wake_r);
    return eval_buf(r->ctx, buf, "<goqjs-wake>", JS_EVAL_TYPE_GLOBAL);
}

void goqjs_loop(goqjs_rt *r)
{
    if (!r || !r->ctx)
        return;
    js_std_loop(r->ctx);
}

void goqjs_wake(goqjs_rt *r)
{
    char b = 1;
    ssize_t n;
    if (!r || r->wake_w < 0)
        return;
    do {
        n = write(r->wake_w, &b, 1);
    } while (n < 0 && errno == EINTR);
}

void goqjs_request_stop(goqjs_rt *r)
{
    if (!r)
        return;
    r->stop = 1;
    goqjs_wake(r);
}

int goqjs_invoke(goqjs_rt *r, int id, const char *json_args)
{
    JSValue global_obj, fn, args[2], ret;
    if (!r || !r->ctx)
        return -1;

    global_obj = JS_GetGlobalObject(r->ctx);
    fn = JS_GetPropertyStr(r->ctx, global_obj, "__goqjs_invoke");
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

void goqjs_destroy(goqjs_rt *r)
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
