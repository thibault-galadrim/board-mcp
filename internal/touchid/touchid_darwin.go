// Package touchid asks the owner to authenticate with LocalAuthentication.
// No agent can produce this proof: it needs a finger on the sensor (or the
// owner's password when allowed).
package touchid

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework LocalAuthentication -framework Foundation
#import <LocalAuthentication/LocalAuthentication.h>
#include <stdlib.h>

// Returns 1 when authenticated, 0 otherwise; *code holds the LAError code
// (0 on success), and *msg a malloc'd description when there is one.
static int authenticate(const char *reason, int biometricsOnly, int timeoutSec, long *code, char **msg) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		LAPolicy policy = biometricsOnly
			? LAPolicyDeviceOwnerAuthenticationWithBiometrics
			: LAPolicyDeviceOwnerAuthentication;
		NSError *err = nil;
		*code = 0;
		if (![ctx canEvaluatePolicy:policy error:&err]) {
			*code = err.code;
			*msg = strdup(err.localizedDescription.UTF8String);
			return 0;
		}
		__block int ok = 0;
		__block long errCode = 0;
		__block NSString *errMsg = nil;
		dispatch_semaphore_t sem = dispatch_semaphore_create(0);
		[ctx evaluatePolicy:policy
		    localizedReason:[NSString stringWithUTF8String:reason]
		              reply:^(BOOL success, NSError *e) {
			ok = success ? 1 : 0;
			if (!success && e) {
				errCode = e.code;
				errMsg = e.localizedDescription;
			}
			dispatch_semaphore_signal(sem);
		}];
		dispatch_time_t deadline = dispatch_time(DISPATCH_TIME_NOW, (int64_t)timeoutSec * NSEC_PER_SEC);
		if (dispatch_semaphore_wait(sem, deadline) != 0) {
			[ctx invalidate];
			dispatch_semaphore_wait(sem, DISPATCH_TIME_FOREVER);
		}
		*code = errCode;
		if (errMsg) *msg = strdup(errMsg.UTF8String);
		return ok;
	}
}
*/
import "C"

import (
	"context"
	"fmt"
	"time"
	"unsafe"
)

type Decision int

const (
	// Undecided: the prompt timed out or the system dismissed it. Nothing
	// was decided; a request stays pending.
	Undecided Decision = iota
	Approved
	Refused
)

// LAError codes (LocalAuthentication/LAError.h).
const (
	errUserCancel   = -2
	errUserFallback = -3
	errSystemCancel = -4
	errAppCancel    = -9
)

type Authenticator struct {
	// BiometricsOnly refuses the password fallback.
	BiometricsOnly bool
	slot           chan struct{}
}

func New(biometricsOnly bool) *Authenticator {
	return &Authenticator{BiometricsOnly: biometricsOnly, slot: make(chan struct{}, 1)}
}

// Proof names what an approval rests on, for the journal.
func (a *Authenticator) Proof() string {
	if a.BiometricsOnly {
		return "touchid"
	}
	return "touchid_or_password"
}

// Confirm shows one prompt at a time and waits until the owner answers or
// ctx expires. An error means the prompt could not be shown at all.
func (a *Authenticator) Confirm(ctx context.Context, reason string) (Decision, error) {
	select {
	case a.slot <- struct{}{}:
	case <-ctx.Done():
		return Undecided, nil
	}
	defer func() { <-a.slot }()

	timeout := 60 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		timeout = time.Until(deadline)
	}
	if timeout < time.Second {
		return Undecided, nil
	}
	cReason := C.CString(reason)
	defer C.free(unsafe.Pointer(cReason))
	bio := C.int(0)
	if a.BiometricsOnly {
		bio = 1
	}
	var code C.long
	var msg *C.char
	ok := C.authenticate(cReason, bio, C.int(timeout/time.Second), &code, &msg)
	text := ""
	if msg != nil {
		text = C.GoString(msg)
		C.free(unsafe.Pointer(msg))
	}
	if ok == 1 {
		return Approved, nil
	}
	switch code {
	case errUserCancel, errUserFallback:
		return Refused, nil
	case errSystemCancel, errAppCancel:
		return Undecided, nil
	}
	return Undecided, fmt.Errorf("local authentication unavailable (%d): %s", int(code), text)
}
