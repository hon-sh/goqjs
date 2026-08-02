package runtime

/*
#cgo CFLAGS: -I${SRCDIR} -I${SRCDIR}/../third_party/quickjs -D_GNU_SOURCE
#cgo LDFLAGS: -lm
#cgo linux LDFLAGS: -ldl -lpthread
#cgo darwin LDFLAGS: -ldl -lpthread

#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"unsafe"
)

//export goqjs_host_write
func goqjs_host_write(callID C.int, s *C.char) {
	// Prefix with call identity so interleaved output is readable.
	fmt.Fprintf(os.Stdout, "[%d]%s\n", int(callID), C.GoString(s))
	_ = os.Stdout.Sync()
}

func buildScript(counts []int) string {
	var b strings.Builder
	b.WriteString(`globalThis.sleep = function(ms) { return os.sleepAsync(ms); };
globalThis.resp = {
  write: async function(callId, s) { respWrite(callId, s); }
};
(async () => {
  const jobs = [];
`)
	for _, c := range counts {
		fmt.Fprintf(&b, `  jobs.push((async () => {
    const c = %d;
    for (let i = 0; i < c; ++i) {
      await resp.write(c, String(i));
      await sleep(1000);
    }
  })());
`, c)
	}
	b.WriteString(`  await Promise.all(jobs);
})();
`)
	return b.String()
}

// Run starts one QuickJS runtime on the calling OS thread, launches a
// concurrent async job per count, and blocks in js_std_loop until all finish.
func Run(counts []int) error {
	if len(counts) == 0 {
		return fmt.Errorf("no job counts")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	script := buildScript(counts)
	cs := C.CString(script)
	defer C.free(unsafe.Pointer(cs))

	ret := C.goqjs_run(cs)
	if ret != 0 {
		return fmt.Errorf("quickjs run failed")
	}
	return nil
}
