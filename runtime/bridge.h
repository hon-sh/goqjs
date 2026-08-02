#ifndef GOQJS_BRIDGE_H
#define GOQJS_BRIDGE_H

/* Create runtime+context, eval script (starts concurrent async jobs),
 * drive js_std_loop until idle. Returns 0 on success. */
int goqjs_run(const char *script);

#endif
