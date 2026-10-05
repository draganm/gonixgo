#include "add.h"
#include "_cgo_export.h"

int add(int a, int b) { return a + b + BONUS; }
int call_twice(int x) { return goTwice(x); }

/* EXTRA comes from $CGO_CFLAGS, which only a packageOverrides entry sets. */
#ifndef EXTRA
#define EXTRA 0
#endif
int extra(void) { return EXTRA; }
