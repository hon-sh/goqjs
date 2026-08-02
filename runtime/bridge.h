#ifndef GOQJS_BRIDGE_H
#define GOQJS_BRIDGE_H

#include <stdint.h>

typedef struct goqjs_rt goqjs_rt;

/* Create runtime+context, install host helpers, open wake pipe.
 * Returns NULL on failure. */
goqjs_rt *goqjs_create(void);

/* Eval script. module!=0 => JS_EVAL_TYPE_MODULE. Returns 0 on success. */
int goqjs_eval(goqjs_rt *r, const char *script, const char *filename, int module);

/* Install wake read-handler so js_std_loop stays alive until goqjs_request_stop. */
int goqjs_install_wake(goqjs_rt *r);

/* Drive js_std_loop until stop (wake handler cleared) and idle. */
void goqjs_loop(goqjs_rt *r);

/* Signal stop and wake the poll; safe from another thread. */
void goqjs_request_stop(goqjs_rt *r);

/* Call globalThis.__goqjs_invoke(id, jsonArgs) on the loop thread. */
int goqjs_invoke(goqjs_rt *r, int id, const char *json_args);

/* Wake poll so the read-handler runs (e.g. after enqueueing work). */
void goqjs_wake(goqjs_rt *r);

void goqjs_destroy(goqjs_rt *r);

#endif
