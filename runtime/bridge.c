#include "goqjs_config.h"
#include "bridge.h"

#include <stdio.h>
#include <string.h>

#include "../third_party/quickjs/quickjs-libc.h"

/* Implemented in Go (cgo //export). Signature must match cgo export. */
extern void goqjs_host_write(int call_id, char *s);

static JSContext *JS_NewCustomContext(JSRuntime *rt)
{
    JSContext *ctx = JS_NewContext(rt);
    if (!ctx)
        return NULL;
    js_init_module_std(ctx, "std");
    js_init_module_os(ctx, "os");
    return ctx;
}

/* respWrite(callId, s) — flushes to Go stdout immediately. */
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

int goqjs_run(const char *script)
{
    JSRuntime *rt;
    JSContext *ctx;
    JSValue global_obj;
    int ret = -1;

    rt = JS_NewRuntime();
    if (!rt) {
        fprintf(stderr, "goqjs: cannot allocate JS runtime\n");
        return -1;
    }
    js_std_set_worker_new_context_func(JS_NewCustomContext);
    js_std_init_handlers(rt);
    ctx = JS_NewCustomContext(rt);
    if (!ctx) {
        fprintf(stderr, "goqjs: cannot allocate JS context\n");
        js_std_free_handlers(rt);
        JS_FreeRuntime(rt);
        return -1;
    }

    JS_SetModuleLoaderFunc2(rt, NULL, js_module_loader, js_module_check_attributes, NULL);
    js_std_add_helpers(ctx, -1, NULL);

    global_obj = JS_GetGlobalObject(ctx);
    JS_SetPropertyStr(ctx, global_obj, "respWrite",
                      JS_NewCFunction(ctx, js_resp_write, "respWrite", 2));
    JS_FreeValue(ctx, global_obj);

    /* Expose std/os globals (needed for os.sleepAsync). */
    {
        const char *boot =
            "import * as std from 'std';\n"
            "import * as os from 'os';\n"
            "globalThis.std = std;\n"
            "globalThis.os = os;\n";
        if (eval_buf(ctx, boot, "<boot>", JS_EVAL_TYPE_MODULE))
            goto fail;
    }

    if (eval_buf(ctx, script, "<jobs>", JS_EVAL_TYPE_GLOBAL))
        goto fail;

    /* Drive pending jobs + os timers (sleepAsync) until idle. */
    js_std_loop(ctx);
    ret = 0;

fail:
    js_std_free_handlers(rt);
    JS_FreeContext(ctx);
    JS_FreeRuntime(rt);
    return ret;
}
