#ifndef HON_BRIDGE_H
#define HON_BRIDGE_H

#include <stdint.h>

typedef struct hon_rt hon_rt;

/* Create runtime+context, wake pipe, fixed host stubs. No JS boot here. */
hon_rt *hon_create(int32_t go_id);

/* Eval script. module!=0 => JS_EVAL_TYPE_MODULE. Returns 0 on success. */
int hon_eval(hon_rt *r, const char *script, const char *filename, int module);

int hon_install_wake(hon_rt *r);
void hon_loop(hon_rt *r);
void hon_request_stop(hon_rt *r);
int hon_invoke(hon_rt *r, int id, const char *json_args);
void hon_wake(hon_rt *r);

/* Eval settle helper on the loop thread: __hon_async_settle(id, ok, payload). */
int hon_async_settle(hon_rt *r, int id, int ok, const char *payload);

void hon_destroy(hon_rt *r);

#endif
