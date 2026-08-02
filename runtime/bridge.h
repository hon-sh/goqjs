#ifndef GOQJS_BRIDGE_H
#define GOQJS_BRIDGE_H

#include <stdint.h>

typedef struct goqjs_rt goqjs_rt;

/* Create runtime+context, wake pipe, fixed host stubs. No JS boot here. */
goqjs_rt *goqjs_create(int32_t go_id);

/* Eval script. module!=0 => JS_EVAL_TYPE_MODULE. Returns 0 on success. */
int goqjs_eval(goqjs_rt *r, const char *script, const char *filename, int module);

int goqjs_install_wake(goqjs_rt *r);
void goqjs_loop(goqjs_rt *r);
void goqjs_request_stop(goqjs_rt *r);
int goqjs_invoke(goqjs_rt *r, int id, const char *json_args);
void goqjs_wake(goqjs_rt *r);

/* Eval settle helper on the loop thread: __goqjs_async_settle(id, ok, payload). */
int goqjs_async_settle(goqjs_rt *r, int id, int ok, const char *payload);

void goqjs_destroy(goqjs_rt *r);

#endif
